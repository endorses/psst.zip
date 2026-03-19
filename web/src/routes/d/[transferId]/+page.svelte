<script lang="ts">
  import { page } from "$app/stores";
  import { onMount } from "svelte";
  import { importKey, decrypt, decryptManifest } from "$lib/crypto";
  import { getTransferInfo, downloadManifest, downloadFile } from "$lib/api";
  import type { Manifest, FileManifestEntry } from "$lib/crypto";
  import { zipSync, strToU8 } from "fflate";

  type Status = "loading" | "ready" | "downloading" | "error";

  let status = $state<Status>("loading");
  let errorMessage = $state("");
  let manifest = $state<Manifest | null>(null);
  let transferId = $state("");
  let keyStr = $state("");
  let downloadProgress = $state<Record<string, number>>({});

  onMount(async () => {
    transferId = $page.params.transferId;
    keyStr = window.location.hash.slice(1);

    if (!keyStr) {
      status = "error";
      errorMessage = "No decryption key found. The link may be incomplete.";
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
      if (err instanceof Error && err.message.includes("404")) {
        errorMessage = "This transfer has expired or does not exist.";
      } else {
        errorMessage = err instanceof Error ? err.message : "Failed to load transfer";
      }
    }
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
    a.click();
    URL.revokeObjectURL(url);
  }

  async function downloadSingleFile(entry: FileManifestEntry) {
    try {
      downloadProgress = { ...downloadProgress, [entry.fileId]: 0 };
      const key = await importKey(keyStr);

      downloadProgress = { ...downloadProgress, [entry.fileId]: 50 };
      const encrypted = await downloadFile(transferId, entry.fileId);

      downloadProgress = { ...downloadProgress, [entry.fileId]: 80 };
      const plaintext = await decrypt(key, encrypted);

      downloadProgress = { ...downloadProgress, [entry.fileId]: 100 };
      triggerDownload(plaintext, entry.name, entry.type);

      setTimeout(() => {
        const { [entry.fileId]: _, ...rest } = downloadProgress;
        downloadProgress = rest;
      }, 1000);
    } catch (err) {
      status = "error";
      errorMessage = err instanceof Error ? err.message : "Download failed";
    }
  }

  async function downloadAllAsZip() {
    if (!manifest) return;

    status = "downloading";
    try {
      const key = await importKey(keyStr);
      const zipData: Record<string, Uint8Array> = {};

      for (let i = 0; i < manifest.files.length; i++) {
        const entry = manifest.files[i];
        const encrypted = await downloadFile(transferId, entry.fileId);
        const plaintext = await decrypt(key, encrypted);
        zipData[entry.name] = new Uint8Array(plaintext);
      }

      const zipped = zipSync(zipData);
      triggerDownload(zipped.buffer, "files.zip", "application/zip");
      status = "ready";
    } catch (err) {
      status = "error";
      errorMessage = err instanceof Error ? err.message : "Download failed";
    }
  }
</script>

<svelte:head>
  <title>Download Files</title>
</svelte:head>

{#if status === "loading"}
  <section class="center">
    <div class="spinner"></div>
    <p>Loading transfer...</p>
  </section>
{:else if status === "error"}
  <section class="center">
    <h1>Something went wrong</h1>
    <p class="error">{errorMessage}</p>
  </section>
{:else if status === "downloading"}
  <section class="center">
    <div class="spinner"></div>
    <p>Downloading and decrypting files...</p>
  </section>
{:else if manifest}
  <section>
    <h1>Your Files</h1>
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
            disabled={entry.fileId in downloadProgress}
          >
            {#if entry.fileId in downloadProgress}
              {downloadProgress[entry.fileId]}%
            {:else}
              Download
            {/if}
          </button>
        </li>
      {/each}
    </ul>

    {#if manifest.files.length > 1}
      <button class="btn primary" onclick={downloadAllAsZip}>Download All as ZIP</button>
    {/if}
  </section>
{/if}

<style>
  h1 {
    font-size: 1.5rem;
    font-weight: 600;
    margin-bottom: 0.5rem;
  }

  .subtitle {
    color: #666;
    margin-bottom: 1.5rem;
  }

  .center {
    text-align: center;
    padding-top: 4rem;
  }

  .spinner {
    width: 32px;
    height: 32px;
    border: 3px solid #e5e5e5;
    border-top-color: #1a1a1a;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
    margin: 0 auto 1rem;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .file-list {
    list-style: none;
    margin: 1rem 0;
  }

  .file-list li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.75rem 0;
    border-bottom: 1px solid #eee;
    gap: 0.75rem;
  }

  .file-info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.125rem;
  }

  .file-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.9375rem;
  }

  .file-size {
    color: #888;
    font-size: 0.8125rem;
  }

  .btn {
    display: inline-block;
    padding: 0.5rem 1rem;
    border: 1px solid #ccc;
    border-radius: 8px;
    background: #fff;
    font-size: 0.8125rem;
    font-weight: 500;
    cursor: pointer;
    white-space: nowrap;
    transition:
      background 0.15s,
      border-color 0.15s;
  }

  .btn:hover:not(:disabled) {
    background: #f5f5f5;
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .btn.primary {
    background: #1a1a1a;
    color: #fff;
    border-color: #1a1a1a;
    width: 100%;
    padding: 0.625rem;
    font-size: 0.875rem;
    margin-top: 0.5rem;
  }

  .btn.primary:hover {
    background: #333;
  }

  .error {
    color: #d33;
    margin-top: 0.75rem;
    font-size: 0.9375rem;
  }
</style>
