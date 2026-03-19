<script lang="ts">
  import { page } from "$app/stores";
  import { onMount } from "svelte";
  import { importKey, encrypt, encryptManifest } from "$lib/crypto";
  import { getSlotInfo, uploadManifest, tusSlotEndpoint } from "$lib/api";
  import type { FileManifestEntry, Manifest } from "$lib/crypto";
  import * as tus from "tus-js-client";

  type Status = "loading" | "ready" | "uploading" | "done" | "error";

  let status = $state<Status>("loading");
  let errorMessage = $state("");
  let slotId = $state("");
  let keyStr = $state("");
  let selectedFiles = $state<File[]>([]);
  let uploadProgress = $state(0);
  let dragOver = $state(false);

  onMount(async () => {
    slotId = $page.params.slotId;
    keyStr = window.location.hash.slice(1);

    if (!keyStr) {
      status = "error";
      errorMessage = "No encryption key found. The link may be incomplete.";
      return;
    }

    try {
      await getSlotInfo(slotId);
      status = "ready";
    } catch (err) {
      status = "error";
      if (err instanceof Error && err.message.includes("404")) {
        errorMessage = "This upload slot has expired or does not exist.";
      } else {
        errorMessage = err instanceof Error ? err.message : "Failed to load slot";
      }
    }
  });

  function handleFileSelect(e: Event) {
    const input = e.target as HTMLInputElement;
    if (input.files) {
      selectedFiles = [...selectedFiles, ...Array.from(input.files)];
    }
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault();
    dragOver = false;
    if (e.dataTransfer?.files) {
      selectedFiles = [...selectedFiles, ...Array.from(e.dataTransfer.files)];
    }
  }

  function handleDragOver(e: DragEvent) {
    e.preventDefault();
    dragOver = true;
  }

  function handleDragLeave() {
    dragOver = false;
  }

  function removeFile(index: number) {
    selectedFiles = selectedFiles.filter((_, i) => i !== index);
  }

  function formatSize(bytes: number): string {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return `${(bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
  }

  async function uploadFileViatTus(
    endpoint: string,
    encryptedData: ArrayBuffer,
  ): Promise<string> {
    return new Promise((resolve, reject) => {
      const blob = new Blob([encryptedData]);
      const upload = new tus.Upload(blob, {
        endpoint,
        retryDelays: [0, 1000, 3000, 5000],
        chunkSize: 5 * 1024 * 1024,
        metadata: {
          contentType: "application/octet-stream",
        },
        onError: (err) => reject(err),
        onSuccess: () => {
          const url = upload.url;
          if (!url) return reject(new Error("No upload URL returned"));
          const fileId = url.split("/").pop() ?? "";
          resolve(fileId);
        },
      });
      upload.start();
    });
  }

  async function startUpload() {
    if (selectedFiles.length === 0) return;

    status = "uploading";
    errorMessage = "";
    uploadProgress = 0;

    try {
      const key = await importKey(keyStr);
      const endpoint = tusSlotEndpoint(slotId);

      const manifestEntries: FileManifestEntry[] = [];
      const totalFiles = selectedFiles.length;

      for (let i = 0; i < totalFiles; i++) {
        const file = selectedFiles[i];
        const plaintext = await file.arrayBuffer();
        const encrypted = await encrypt(key, plaintext);
        const fileId = await uploadFileViatTus(endpoint, encrypted);

        manifestEntries.push({
          name: file.name,
          size: file.size,
          type: file.type || "application/octet-stream",
          fileId,
        });

        uploadProgress = Math.round(((i + 1) / totalFiles) * 100);
      }

      const manifest: Manifest = { files: manifestEntries };
      const encryptedManifestData = await encryptManifest(key, manifest);
      // For slots, we upload the manifest to the slot's transfer
      await uploadManifest(slotId, encryptedManifestData);

      status = "done";
    } catch (err) {
      status = "error";
      errorMessage = err instanceof Error ? err.message : "Upload failed";
    }
  }
</script>

<svelte:head>
  <title>Upload Files</title>
</svelte:head>

{#if status === "loading"}
  <section class="center">
    <div class="spinner"></div>
    <p>Loading upload slot...</p>
  </section>
{:else if status === "error"}
  <section class="center">
    <h1>Something went wrong</h1>
    <p class="error">{errorMessage}</p>
  </section>
{:else if status === "ready"}
  <section>
    <h1>Upload Files</h1>
    <p class="subtitle">Your files will be encrypted before upload.</p>

    <div
      class="dropzone"
      class:drag-over={dragOver}
      role="button"
      tabindex="0"
      ondrop={handleDrop}
      ondragover={handleDragOver}
      ondragleave={handleDragLeave}
    >
      <p class="dropzone-text">Drop files here or click to browse</p>
      <input type="file" multiple onchange={handleFileSelect} class="file-input" />
    </div>

    {#if selectedFiles.length > 0}
      <ul class="file-list">
        {#each selectedFiles as file, i}
          <li>
            <span class="file-name">{file.name}</span>
            <span class="file-size">{formatSize(file.size)}</span>
            <button class="remove-btn" onclick={() => removeFile(i)} aria-label="Remove file">
              &times;
            </button>
          </li>
        {/each}
      </ul>

      <button class="btn primary" onclick={startUpload}>
        Encrypt &amp; Upload {selectedFiles.length} file{selectedFiles.length > 1 ? "s" : ""}
      </button>
    {/if}
  </section>
{:else if status === "uploading"}
  <section class="center">
    <h1>Encrypting &amp; Uploading...</h1>
    <div class="progress-bar">
      <div class="progress-fill" style="width: {uploadProgress}%"></div>
    </div>
    <p class="progress-text">{uploadProgress}%</p>
  </section>
{:else if status === "done"}
  <section class="center">
    <h1>Upload Complete</h1>
    <p class="subtitle">Your files have been encrypted and uploaded successfully.</p>
    <p class="hint">You can close this page now.</p>
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

  .dropzone {
    position: relative;
    border: 2px dashed #ccc;
    border-radius: 12px;
    padding: 3rem 1.5rem;
    text-align: center;
    cursor: pointer;
    transition:
      border-color 0.2s,
      background 0.2s;
  }

  .dropzone:hover,
  .dropzone.drag-over {
    border-color: #4a90d9;
    background: #f0f6ff;
  }

  .dropzone-text {
    color: #888;
    font-size: 0.9375rem;
  }

  .file-input {
    position: absolute;
    inset: 0;
    opacity: 0;
    cursor: pointer;
  }

  .file-list {
    list-style: none;
    margin: 1rem 0;
  }

  .file-list li {
    display: flex;
    align-items: center;
    padding: 0.5rem 0;
    border-bottom: 1px solid #eee;
    gap: 0.75rem;
  }

  .file-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.875rem;
  }

  .file-size {
    color: #888;
    font-size: 0.8125rem;
    white-space: nowrap;
  }

  .remove-btn {
    background: none;
    border: none;
    font-size: 1.25rem;
    color: #999;
    cursor: pointer;
    padding: 0 0.25rem;
    line-height: 1;
  }

  .remove-btn:hover {
    color: #e44;
  }

  .btn {
    display: inline-block;
    padding: 0.625rem 1.25rem;
    border: 1px solid #ccc;
    border-radius: 8px;
    background: #fff;
    font-size: 0.875rem;
    font-weight: 500;
    cursor: pointer;
    transition:
      background 0.15s,
      border-color 0.15s;
  }

  .btn:hover {
    background: #f5f5f5;
  }

  .btn.primary {
    background: #1a1a1a;
    color: #fff;
    border-color: #1a1a1a;
    width: 100%;
    margin-top: 0.5rem;
  }

  .btn.primary:hover {
    background: #333;
  }

  .progress-bar {
    width: 100%;
    max-width: 400px;
    height: 8px;
    background: #e5e5e5;
    border-radius: 4px;
    overflow: hidden;
    margin: 1.5rem auto 0.75rem;
  }

  .progress-fill {
    height: 100%;
    background: #1a1a1a;
    border-radius: 4px;
    transition: width 0.3s ease;
  }

  .progress-text {
    color: #666;
    font-size: 0.875rem;
  }

  .error {
    color: #d33;
    margin-top: 0.75rem;
    font-size: 0.9375rem;
  }

  .hint {
    color: #888;
    font-size: 0.875rem;
    margin-top: 0.5rem;
  }
</style>
