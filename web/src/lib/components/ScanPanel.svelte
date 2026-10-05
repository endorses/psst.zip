<script lang="ts">
  import { message as m, t, translate, type DisplayText } from "$lib/i18n";

  import { onMount, tick } from "svelte";
  import { beforeNavigate } from "$app/navigation";
  import ReceiveUploadPanel from "./ReceiveUploadPanel.svelte";
  import QrScanner from "qr-scanner";
  import Icon from "./Icon.svelte";
  import { classifyScanInput, qrImageDimensions, type ScanInput } from "$lib/scan-input";
  let { authorize }: { authorize: () => Promise<boolean> } = $props();
  let video = $state<HTMLVideoElement>();
  let paste = $state(""),
    error = $state<DisplayText>(""),
    status = $state<DisplayText>(""),
    running = $state(false),
    busy = $state(false);
  let cameras = $state<MediaDeviceInfo[]>([]),
    camera = $state("");
  let target = $state<ScanInput | null>(null),
    secure = $state(false);
  let uploadActive = $state(false);
  const invitation = $derived(
    target?.kind === "upload" && target.origin === location.origin ? new URL(target.url) : null,
  );
  beforeNavigate(({ to, willUnload, cancel }) => {
    if (
      !willUnload &&
      uploadActive &&
      to?.url.pathname === "/" &&
      to.url.searchParams.get("view") !== "scan" &&
      !confirm(translate(m("stopUploadAndLeaveSelectedFilesWillBeCleared")))
    )
      cancel();
  });
  async function scanAgain() {
    if (uploadActive && !confirm(translate(m("stopUploadAndScanAgainSelectedFilesWillBe")))) return;
    await start();
  }
  let stream: MediaStream | undefined,
    engine: Awaited<ReturnType<typeof QrScanner.createQrEngine>> | undefined;
  let generation = 0,
    disposed = false,
    timer: ReturnType<typeof setTimeout> | undefined;
  const canvas = typeof document === "undefined" ? undefined : document.createElement("canvas");
  function stop() {
    generation++;
    clearTimeout(timer);
    stream?.getTracks().forEach((track) => track.stop());
    stream = undefined;
    if (video) video.srcObject = null;
    if (engine instanceof Worker) engine.terminate();
    engine = undefined;
    if (canvas) {
      canvas.width = 0;
      canvas.height = 0;
    }
    running = false;
    busy = false;
  }
  async function accept(raw: string) {
    const parsed = classifyScanInput(raw);
    if (!parsed) {
      target = null;
      error = m("thisIsNotASupportedPsstZipLinkOr");
      return;
    }
    stop();
    const current = generation;
    if (!(await authorize()) || disposed || current !== generation) return;
    paste = "";
    error = "";
    target = parsed;
    status =
      parsed.kind === "pairing"
        ? m("thisServerLoginCodeBelongsInTheMobileApp")
        : m("linkReadyReviewTheDestinationBeforeOpeningIt");
  }
  async function start() {
    stop();
    target = null;
    error = "";
    await tick();
    if (!secure || disposed) return;
    const current = generation;
    busy = true;
    status = m("requestingCameraAccess");
    try {
      if (!(await authorize()) || disposed || current !== generation) return;
      const acquired = await navigator.mediaDevices.getUserMedia({
        audio: false,
        video: camera ? { deviceId: { exact: camera } } : { facingMode: "environment" },
      });
      if (disposed || current !== generation || !video) {
        acquired.getTracks().forEach((track) => track.stop());
        return;
      }
      stream = acquired;
      video.srcObject = acquired;
      await video.play();
      const decoder = await QrScanner.createQrEngine();
      if (disposed || current !== generation) {
        if (decoder instanceof Worker) decoder.terminate();
        return;
      }
      engine = decoder;
      cameras = (await navigator.mediaDevices.enumerateDevices()).filter(
        (device) => device.kind === "videoinput",
      );
      if (disposed || current !== generation) return;
      camera = stream?.getVideoTracks()[0]?.getSettings().deviceId ?? camera;
      running = true;
      busy = false;
      status = m("pointTheCameraAtAPsstZipQRCode");
      void frame(current);
    } catch (cause) {
      if (disposed || current !== generation) return;
      stop();
      status = "";
      error =
        cause instanceof DOMException && cause.name === "NotAllowedError"
          ? m("cameraAccessWasDeniedAllowItInYourBrowser")
          : m("theCameraIsUnavailableOrBusyTryAnotherCamera");
    } finally {
      if (current === generation) busy = false;
    }
  }
  async function frame(current: number) {
    if (!running || current !== generation || !canvas || !engine || !video) return;
    try {
      const result = await QrScanner.scanImage(video, {
        qrEngine: engine,
        canvas,
        scanRegion: {
          downScaledWidth: Math.max(
            1,
            Math.round((640 * video.videoWidth) / Math.max(video.videoWidth, video.videoHeight)),
          ),
          downScaledHeight: Math.max(
            1,
            Math.round((640 * video.videoHeight) / Math.max(video.videoWidth, video.videoHeight)),
          ),
        },
        returnDetailedScanResult: true,
      });
      if (current === generation) await accept(result.data);
    } catch {
      /* Ordinary frames without a QR code are expected. Never log decoded content. */
    }
    if (current === generation && running) timer = setTimeout(() => void frame(current), 250);
  }
  async function image(event: Event) {
    const input = event.target as HTMLInputElement,
      file = input.files?.[0];
    input.value = "";
    if (!file) return;
    stop();
    target = null;
    error = "";
    busy = true;
    status = m("readingQRImage");
    const current = generation;
    let bitmap: ImageBitmap | undefined;
    try {
      if (!(await authorize()) || disposed || current !== generation) return;
      if (file.size > 10 * 1024 * 1024 || !["image/png", "image/jpeg"].includes(file.type))
        throw new Error();
      const dimensions = qrImageDimensions(new Uint8Array(await file.arrayBuffer()));
      if (!dimensions) throw new Error();
      const scale = Math.min(1, 1024 / Math.max(dimensions.width, dimensions.height));
      bitmap = await createImageBitmap(file, {
        resizeWidth: Math.max(1, Math.round(dimensions.width * scale)),
        resizeHeight: Math.max(1, Math.round(dimensions.height * scale)),
      });
      if (disposed || current !== generation) return;
      const decoder = await QrScanner.createQrEngine();
      if (disposed || current !== generation) {
        if (decoder instanceof Worker) decoder.terminate();
        return;
      }
      engine = decoder;
      const result = await QrScanner.scanImage(bitmap, {
        qrEngine: decoder,
        returnDetailedScanResult: true,
      });
      if (current === generation) await accept(result.data);
    } catch {
      if (!disposed && current === generation) {
        error = m("couldNotReadAQRCodeChooseAClear");
        status = "";
      }
    } finally {
      bitmap?.close();
      if (current === generation) stop();
    }
  }
  async function inspectPaste() {
    if (!disposed) await accept(paste);
  }
  async function open() {
    const result = target;
    if (!result || result.kind === "pairing") return;
    if (await authorize()) {
      if (!disposed && target === result) {
        stop();
        window.location.assign(result.url);
      }
    }
  }
  onMount(() => {
    secure = window.isSecureContext && !!navigator.mediaDevices?.getUserMedia;
    if (secure) void start();
    function hide() {
      if (document.hidden) {
        stop();
        status = m("cameraPausedChooseScanAgainWhenYouReturn");
      }
    }
    function leave() {
      stop();
    }
    document.addEventListener("visibilitychange", hide);
    window.addEventListener("pagehide", leave);
    return () => {
      disposed = true;
      stop();
      document.removeEventListener("visibilitychange", hide);
      window.removeEventListener("pagehide", leave);
    };
  });
</script>

{#if invitation}
  <ReceiveUploadPanel
    slotId={invitation.pathname.split("/").pop()!}
    keyString={invitation.hash.slice(1)}
    onactive={(active) => (uploadActive = active)}
  />
  <button class="scan-again" onclick={scanAgain}><Icon name="QRCode" />{$t(m("scanAgain"))}</button>
{:else if target}
  <div class="destination">
    {#if target.kind === "pairing"}
      <h1>{$t(m("serverLoginCode"))}</h1>
      <p role="status">{$t(status)}</p>
    {:else}
      <h1>{$t(target.kind === "download" ? m("downloadLink") : m("receiveLink"))}</h1>
      <p class="server">{$t(target.origin)}</p>
      {#if target.origin !== location.origin}<p class="muted">
          {$t(m("thisLinkOpensADifferentServerYourAccountOn"))}
        </p>{/if}
      <button class="primary" onclick={open}
        >{$t(target.kind === "download" ? m("openDownloadLink") : m("openReceiveLink"))}<Icon
          name="Arrow"
        /></button
      >
    {/if}
    <button class="scan-again" onclick={scanAgain}
      ><Icon name="QRCode" />{$t(m("scanAgain"))}</button
    >
  </div>
{:else}
  <h1>{$t(m("scanQRCode"))}</h1>
  <p class="muted">{$t(m("openAFileLinkFromAnotherDeviceQRImages"))}</p>
  <div class="camera-panel">
    <video bind:this={video} muted playsinline aria-label={$t(m("qrCameraPreview"))}
      ><track kind="captions" /></video
    >
    {#if !running}<div class="camera-placeholder">
        <Icon name="QRCode" size={48} /><span
          >{$t(busy ? m("preparingScanner") : m("cameraPreview"))}</span
        >
      </div>{/if}
  </div>
  {#if !secure}<p class="notice">{$t(m("cameraScanningNeedsHTTPSOrLocalhostOnAPlain"))}</p>{/if}
  {#if cameras.length > 1}<label for="scan-camera">{$t(m("camera"))}</label><select
      id="scan-camera"
      bind:value={camera}
      onchange={() => void start()}
      disabled={busy}
      >{#each cameras as device, index}<option value={device.deviceId}
          >{$t(device.label || m("cameraValue", { arg0: index + 1 }))}</option
        >{/each}</select
    >{/if}
  <div class="scan-actions">
    {#if secure}<button onclick={() => (running ? stop() : void start())} disabled={busy}
        ><Icon name="QRCode" />{$t(running ? m("stopCamera") : m("scanAgain"))}</button
      >{/if}
    <label class="image-picker"
      ><Icon name="Upload" />{$t(m("chooseQRImage"))}<input
        type="file"
        accept="image/png,image/jpeg"
        onchange={image}
        disabled={busy}
        aria-label={$t(m("chooseQRImage"))}
      /></label
    >
  </div>
  {#if status}<p role="status">{$t(status)}</p>{/if}
  {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}

  <form
    onsubmit={(event) => {
      event.preventDefault();
      void inspectPaste();
    }}
  >
    <label for="scan-link">{$t(m("pasteLink"))}</label><textarea
      id="scan-link"
      bind:value={paste}
      maxlength="4096"
      rows="3"
      autocomplete="off"
      spellcheck="false"
      placeholder={$t(m("pasteAPsstZipLink"))}
    ></textarea>
    <button type="submit" disabled={!paste.trim() || busy}
      >{$t(m("reviewLink"))}<Icon name="Arrow" /></button
    >
  </form>
{/if}

<style>
  .camera-panel {
    position: relative;
    background: var(--background);
    border: 1px solid var(--divider);
    border-radius: 1rem;
    overflow: hidden;
    aspect-ratio: 4 / 3;
    max-height: 360px;
    margin: 1.25rem 0;
  }
  video {
    width: 100%;
    height: 100%;
    object-fit: contain;
  }
  .camera-placeholder {
    position: absolute;
    inset: 0;
    display: grid;
    place-content: center;
    justify-items: center;
    gap: 0.75rem;
    color: var(--muted);
  }
  .scan-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin: 1rem 0;
  }
  button,
  .image-picker {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.5rem;
  }
  .image-picker {
    position: relative;
    border: 1px solid var(--border);
    border-radius: 0.75rem;
    padding: 0.65rem 1rem;
    overflow: hidden;
    cursor: pointer;
  }
  .image-picker:focus-within {
    outline: 3px solid var(--primary);
    outline-offset: 3px;
  }
  .image-picker input {
    position: absolute;
    inset: 0;
    width: 100%;
    opacity: 0;
    cursor: pointer;
  }
  label {
    display: block;
    margin-bottom: 0.5rem;
  }
  textarea {
    width: 100%;
    resize: vertical;
    border: 1px solid var(--border);
    border-radius: 0.75rem;
    background: var(--surface);
    color: var(--text);
    padding: 0.75rem;
    font: inherit;
  }
  form {
    margin-top: 1.5rem;
  }
  form button {
    margin-top: 0.75rem;
  }
  .scan-again {
    margin-top: 1rem;
  }
  .destination {
    padding: 1rem;
    border: 1px solid var(--divider);
    border-radius: 0.75rem;
  }
  .server {
    overflow-wrap: anywhere;
  }
</style>
