<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { beforeNavigate } from "$app/navigation";
  import { BRAND } from "$lib/brand";
  import { page } from "$app/stores";
  import { onMount, onDestroy } from "svelte";
  import { importKey, decryptManifest } from "$lib/crypto";
  import { getTransferInfo, downloadManifest, acknowledgeDownload } from "$lib/api";
  import type { Manifest, FileManifestEntry } from "$lib/crypto";
  import { zipSync } from "fflate";

  import { assertFileSize, MAX_ZIP_BYTES, MAX_BUFFERED_BYTES } from "$lib/limits";

  import { decryptFileStream } from "$lib/chunked-files";
  import { createSaveSink, cleanAbandonedDownloads, LARGE_SAVE_MESSAGE } from "$lib/file-save";
  import { safeFilename, zipEntryName } from "$lib/filenames";

  type Status = "loading" | "ready" | "downloading" | "error";

  let controller: AbortController | null = null,
    disposed = false;
  let currentFile = $state(""),
    downloadBytes = $state(0);
  onDestroy(() => {
    disposed = true;
    controller?.abort();
  });
  let status = $state<Status>("loading");
  let errorMessage = $state("");
  let manifest = $state<Manifest | null>(null);
  let transferId = $state("");
  let keyStr = $state("");
  let downloadProgress = $state<Record<string, number>>({});
  let downloadedFileIds = $state<string[]>([]);
  let confirmation = $state<"idle" | "sending" | "confirmed" | "failed">("idle");
  const saving = $derived(
    status === "downloading" || Object.values(downloadProgress).some((value) => value < 100),
  );
  function unload(event: BeforeUnloadEvent) {
    if (saving) {
      event.preventDefault();
      event.returnValue = "";
    }
  }
  beforeNavigate(({ willUnload, cancel }) => {
    if (
      !willUnload &&
      saving &&
      !confirm("Stop saving and leave? Files already saved will remain.")
    )
      cancel();
  });
  const allFilesDownloaded = $derived(
    !!manifest?.files.length &&
      manifest.files.every((file) => downloadedFileIds.includes(file.blob_id)),
  );

  async function load() {
    transferId = $page.params.transferId ?? "";
    keyStr = window.location.hash.slice(1);

    if (!keyStr) {
      status = "error";
      errorMessage =
        "This link is incomplete. Ask the sender for the full link, including the part after #.";
      return;
    }

    try {
      await getTransferInfo(transferId);

      const key = await importKey(keyStr);
      const encryptedManifestData = await downloadManifest(transferId);
      manifest = await decryptManifest(key, encryptedManifestData);
      status = "ready";
    } catch (err) {
      status = "error";
      if (err instanceof Error && (err.message.includes("404") || err.message.includes("410"))) {
        errorMessage = "This transfer has expired or was revoked. Ask the sender for a new link.";
      } else {
        errorMessage =
          "Could not open these files. Check your connection and retry, or ask the sender for a new link.";
      }
    }
  }
  onMount(() => {
    void load();
    void cleanAbandonedDownloads();
  });

  function formatSize(bytes: number): string {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return `${(bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
  }

  function triggerDownload(data: ArrayBuffer, filename: string, mimeType: string) {
    const blob = new Blob([data], { type: mimeType });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = safeFilename(filename);
    try {
      a.click();
    } finally {
      URL.revokeObjectURL(url);
    }
  }

  async function confirmDownload() {
    if (!allFilesDownloaded || confirmation === "sending" || confirmation === "confirmed") return;
    confirmation = "sending";
    try {
      await acknowledgeDownload(transferId);
      confirmation = "confirmed";
    } catch {
      // Files are already decrypted and handed to the browser. Retry only the
      // acknowledgment; never ask the recipient to download those files again.
      confirmation = "failed";
    }
  }

  function recordDownloadedFiles(ids: string[]) {
    downloadedFileIds = [...new Set([...downloadedFileIds, ...ids])];
    if (allFilesDownloaded && confirmation === "idle") void confirmDownload();
  }

  async function* downloadChunks(entry: FileManifestEntry, signal: AbortSignal) {
    const key = await importKey(keyStr);
    const response = await fetch(`/api/v1/transfers/${transferId}/files/${entry.blob_id}`, {
      signal,
      credentials: "omit",
    });
    if (!response.ok || !response.body) throw new Error("Could not download file");
    yield* decryptFileStream(key, entry, response.body, signal);
  }

  async function downloadSingleFile(entry: FileManifestEntry) {
    errorMessage = "";
    controller = new AbortController();
    const signal = controller.signal;
    let sink: Awaited<ReturnType<typeof createSaveSink>> | undefined;
    try {
      assertFileSize(entry.size);
      downloadProgress = { ...downloadProgress, [entry.blob_id]: 0 };
      sink = await createSaveSink(entry);
      signal.throwIfAborted();
      let saved = 0;
      for await (const chunk of downloadChunks(entry, signal)) {
        await sink.write(chunk);
        saved += chunk.length;
        downloadProgress = {
          ...downloadProgress,
          [entry.blob_id]: Math.min(99, Math.floor((saved / Math.max(1, entry.size)) * 100)),
        };
      }
      signal.throwIfAborted();
      await sink.close();
      sink = undefined;
      downloadProgress = { ...downloadProgress, [entry.blob_id]: 100 };
      recordDownloadedFiles([entry.blob_id]);

      setTimeout(() => {
        const { [entry.blob_id]: _, ...rest } = downloadProgress;
        downloadProgress = rest;
      }, 1000);
    } catch (err) {
      await sink?.abort().catch(() => {});
      errorMessage =
        err instanceof Error && err.message === LARGE_SAVE_MESSAGE
          ? LARGE_SAVE_MESSAGE
          : err instanceof DOMException && err.name === "AbortError"
            ? "Saving stopped. You can retry the same files."
            : "Could not save files. Check your connection and try Save files again.";
      const { [entry.blob_id]: _, ...rest } = downloadProgress;
      downloadProgress = rest;
    }
  }

  async function downloadAllAsZip() {
    if (!manifest) return;

    controller = new AbortController();
    const signal = controller.signal;
    status = "downloading";
    errorMessage = "";
    try {
      if (manifest.files.reduce((sum, file) => sum + file.size, 0) > MAX_ZIP_BYTES) {
        throw new Error("ZIP downloads are limited to 25 MiB total. Download files individually.");
      }
      const zipData: Record<string, Uint8Array> = Object.create(null);

      for (let i = 0; i < manifest.files.length; i++) {
        const entry = manifest.files[i];
        assertFileSize(entry.size);
        currentFile = entry.name;
        downloadBytes = 0;
        const plaintext = new Uint8Array(entry.size);
        let offset = 0;
        for await (const chunk of downloadChunks(entry, signal)) {
          plaintext.set(chunk, offset);
          offset += chunk.length;
          downloadBytes = offset;
        }
        signal.throwIfAborted();
        zipData[zipEntryName(entry.name, i)] = plaintext;
      }

      const zipped = zipSync(zipData);
      triggerDownload(new Uint8Array(zipped).buffer, "files.zip", "application/zip");
      recordDownloadedFiles(manifest.files.map((entry) => entry.blob_id));
      status = "ready";
    } catch (err) {
      status = "ready";
      errorMessage =
        err instanceof DOMException && err.name === "AbortError"
          ? "Saving stopped. You can retry the same files."
          : "Could not save files. Check your connection and try Save files again.";
    }
  }
</script>

<svelte:window onbeforeunload={unload} />
<svelte:head>
  <title>Save files · {BRAND}</title>
</svelte:head>

{#if status === "loading"}
  <section class="center">
    <div class="spinner"></div>
    <p>Loading transfer...</p>
  </section>
{:else if status === "error"}
  <section class="center">
    <h1>Cannot open files</h1>
    <p class="error" role="alert">{errorMessage}</p>
    <button
      onclick={() => {
        status = "loading";
        void load();
      }}><Icon name="Refresh" size={18} />Reconnect</button
    >
  </section>
{:else if status === "downloading"}
  <section class="center">
    <div class="spinner"></div>
    <p>Saving {currentFile} · {formatSize(downloadBytes)} received</p>
    <button onclick={() => controller?.abort()}>Cancel saving</button>
  </section>
{:else if manifest}
  <section>
    <h1>Save files</h1>
    <p class="subtitle">
      {manifest.files.length} file{manifest.files.length !== 1 ? "s" : ""} &middot;
      {formatSize(manifest.files.reduce((sum, f) => sum + f.size, 0))} total
    </p>

    {#if manifest.files.some((entry) => entry.size > MAX_BUFFERED_BYTES)}<p class="muted small">
        Large files save directly to disk where your browser supports it. HTTPS may be required; the
        mobile app can also save them.
      </p>{/if}
    <ul class="file-list">
      {#each manifest.files as entry}
        <li>
          <div class="file-info">
            <span class="file-name">{entry.name}</span>
            <span class="file-size">{formatSize(entry.size)}</span>
          </div>
          <button
            class="btn"
            onclick={() => downloadSingleFile(entry)}
            disabled={Object.keys(downloadProgress).length > 0}
          >
            <Icon name="Download" size={18} />
            {#if entry.blob_id in downloadProgress}
              {downloadProgress[entry.blob_id]}%
            {:else}
              {downloadedFileIds.includes(entry.blob_id) ? "Save again" : "Save file"}
            {/if}
          </button>
        </li>
      {/each}
    </ul>

    {#if manifest.files.length > 1}
      <p class="muted small">
        ZIP downloads support up to 25 MiB total. Larger transfers can be saved individually.
      </p>
      <button
        class="primary"
        disabled={Object.keys(downloadProgress).length > 0 ||
          manifest.files.reduce((n, f) => n + f.size, 0) > MAX_ZIP_BYTES}
        onclick={downloadAllAsZip}><Icon name="Download" size={18} />Save all as ZIP</button
      >
    {/if}

    {#if Object.keys(downloadProgress).length}<button onclick={() => controller?.abort()}
        >Cancel saving</button
      >{/if}
    {#if errorMessage}
      <p class="error" role="alert">{errorMessage}</p>
    {/if}

    {#if allFilesDownloaded}
      <div class="download-confirmation" role="status">
        <p>All files handed to your browser. Check its Downloads list for saved files.</p>
        {#if confirmation === "sending"}
          <p>Notifying the sender...</p>
        {:else if confirmation === "confirmed"}
          <p>Sender notified.</p>
        {:else if confirmation === "failed"}
          <p>Files downloaded, but the sender could not be notified.</p>
          <button class="btn" onclick={confirmDownload}
            ><Icon name="Refresh" size={18} />Retry confirmation</button
          >
        {/if}
      </div>
    {/if}
  </section>
{/if}

<style>
  .download-confirmation {
    margin-top: 1rem;
    padding: 1rem;
    background: var(--accent);
    border-radius: 8px;
  }
  .file-name {
    word-break: break-word;
  }
</style>
