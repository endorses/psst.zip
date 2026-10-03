<script lang="ts">
  import { beforeNavigate } from "$app/navigation";
  import { BRAND } from "$lib/brand";
  import { page } from "$app/stores";
  import { onMount, onDestroy } from "svelte";
  import { importKey, decrypt, decryptManifest } from "$lib/crypto";
  import { getTransferInfo, downloadManifest, downloadFile, acknowledgeDownload } from "$lib/api";
  import type { Manifest, FileManifestEntry } from "$lib/crypto";
  import { zipSync } from "fflate";

  import { assertFileSize, MAX_BUFFERED_BYTES } from "$lib/limits";

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
    a.download = filename;
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

  function validateDownloadedSize(plaintext: ArrayBuffer, entry: FileManifestEntry) {
    if (plaintext.byteLength !== entry.size) {
      throw new Error(`Downloaded file size does not match the manifest: ${entry.name}`);
    }
  }

  async function downloadSingleFile(entry: FileManifestEntry) {
    errorMessage = "";
    controller = new AbortController();
    const signal = controller.signal;
    try {
      assertFileSize(entry.size);
      downloadProgress = { ...downloadProgress, [entry.blob_id]: 0 };
      const key = await importKey(keyStr);

      const encrypted = await downloadFile(
        transferId,
        entry.blob_id,
        (bytes) =>
          (downloadProgress = {
            ...downloadProgress,
            [entry.blob_id]: Math.min(99, Math.floor((bytes / (entry.size + 28)) * 100)),
          }),
        signal,
      );
      if (signal.aborted || disposed) return;
      const plaintext = await decrypt(key, encrypted);
      if (signal.aborted || disposed) return;
      validateDownloadedSize(plaintext, entry);

      downloadProgress = { ...downloadProgress, [entry.blob_id]: 100 };
      triggerDownload(plaintext, entry.name, entry.mime_type);
      recordDownloadedFiles([entry.blob_id]);

      setTimeout(() => {
        const { [entry.blob_id]: _, ...rest } = downloadProgress;
        downloadProgress = rest;
      }, 1000);
    } catch (err) {
      errorMessage =
        err instanceof DOMException && err.name === "AbortError"
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
      const key = await importKey(keyStr);
      if (manifest.files.reduce((sum, file) => sum + file.size, 0) > MAX_BUFFERED_BYTES) {
        throw new Error("ZIP downloads are limited to 25 MiB total. Download files individually.");
      }
      const zipData: Record<string, Uint8Array> = Object.create(null);

      for (let i = 0; i < manifest.files.length; i++) {
        const entry = manifest.files[i];
        assertFileSize(entry.size);
        currentFile = entry.name;
        downloadBytes = 0;
        const encrypted = await downloadFile(
          transferId,
          entry.blob_id,
          (bytes) => (downloadBytes = bytes),
          signal,
        );
        if (signal.aborted || disposed) return;
        const plaintext = await decrypt(key, encrypted);
        if (signal.aborted || disposed) return;
        validateDownloadedSize(plaintext, entry);
        const name = entry.name.split(/[\\/]/).pop() || "file";
        zipData[`${i + 1}-${name}`] = new Uint8Array(plaintext);
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
      }}>Reconnect</button
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
            {#if entry.blob_id in downloadProgress}
              {downloadProgress[entry.blob_id]}%
            {:else}
              {downloadedFileIds.includes(entry.blob_id) ? "Save again" : "Save files"}
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
          manifest.files.reduce((n, f) => n + f.size, 0) > MAX_BUFFERED_BYTES}
        onclick={downloadAllAsZip}>Save all as ZIP</button
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
          <button class="btn" onclick={confirmDownload}>Retry confirmation</button>
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
