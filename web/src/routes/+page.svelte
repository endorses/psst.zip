<script lang="ts">
  import { onMount } from "svelte";

  let mounted = $state(false);
  onMount(() => {
    mounted = true;
  });
  import { generateKey, exportKey, encrypt, encryptManifest } from "$lib/crypto";
  import { createTransfer, uploadManifest, completeTransfer, tusEndpoint } from "$lib/api";
  import type { FileManifestEntry, Manifest } from "$lib/crypto";
  import * as tus from "tus-js-client";
  import QRCode from "qrcode";

  import { assertFileSize, FILE_SIZE_NOTICE } from "$lib/limits";

  type Status = "idle" | "uploading" | "done" | "error";

  let status = $state<Status>("idle");
  let errorMessage = $state("");
  let selectedFiles = $state<File[]>([]);
  let uploadProgress = $state(0);
  let shareUrl = $state("");
  let qrDataUrl = $state("");
  let copied = $state(false);
  let copyMessage = $state("");
  let linkInput = $state<HTMLInputElement>();
  let dragOver = $state(false);

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

  async function uploadFile(endpoint: string, encryptedData: ArrayBuffer): Promise<string> {
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
      selectedFiles.forEach((file) => assertFileSize(file.size));
      const key = await generateKey();
      const keyStr = await exportKey(key);

      const { id: transferId } = await createTransfer();
      const endpoint = tusEndpoint(transferId);

      const manifestEntries: FileManifestEntry[] = [];
      const totalFiles = selectedFiles.length;

      for (let i = 0; i < totalFiles; i++) {
        const file = selectedFiles[i];
        const plaintext = await file.arrayBuffer();
        const encrypted = await encrypt(key, plaintext);
        const fileId = await uploadFile(endpoint, encrypted);

        manifestEntries.push({
          name: file.name,
          size: file.size,
          mime_type: file.type || "application/octet-stream",
          blob_id: fileId,
        });

        uploadProgress = Math.round(((i + 1) / totalFiles) * 100);
      }

      const manifest: Manifest = { files: manifestEntries };
      const encryptedManifestData = await encryptManifest(key, manifest);
      await uploadManifest(transferId, encryptedManifestData);
      await completeTransfer(transferId);

      const origin = window.location.origin;
      shareUrl = `${origin}/d/${transferId}#${keyStr}`;

      qrDataUrl = await QRCode.toDataURL(shareUrl, {
        width: 256,
        margin: 2,
        color: { dark: "#1a1a1a", light: "#ffffff" },
      });

      status = "done";
    } catch (err) {
      status = "error";
      errorMessage = err instanceof Error ? err.message : "Upload failed";
    }
  }

  async function copyLink() {
    copied = false;
    copyMessage = "";
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(shareUrl);
        copied = true;
      } catch {
        // Clipboard permission can be denied even on HTTPS. Try the selected
        // input below, which also works on LAN HTTP without navigator.clipboard.
      }
    }
    if (!copied && linkInput) {
      linkInput.focus();
      linkInput.select();
      try {
        copied = document.execCommand("copy");
      } catch {
        // Leave the link selected so the browser's Copy command remains usable.
      }
    }
    if (copied) setTimeout(() => (copied = false), 2000);
    else copyMessage = "Link selected. Use your browser’s Copy command to copy it.";
  }

  function reset() {
    status = "idle";
    selectedFiles = [];
    uploadProgress = 0;
    shareUrl = "";
    qrDataUrl = "";
    errorMessage = "";
    copied = false;
    copyMessage = "";
  }
</script>

<svelte:head>
  <title>Share Files</title>
</svelte:head>

{#if status === "idle" || status === "error"}
  <section class="upload-section">
    <h1>Share Files Securely</h1>
    <p class="subtitle">Files are encrypted in your browser before upload.</p>

    <div
      class="dropzone"
      class:drag-over={dragOver}
      role="button"
      tabindex="0"
      ondrop={handleDrop}
      ondragover={handleDragOver}
      ondragleave={handleDragLeave}
    >
      <p>{FILE_SIZE_NOTICE}</p>
      <p class="dropzone-text">Drop files here or click to browse</p>
      <input
        type="file"
        multiple
        disabled={!mounted}
        onchange={handleFileSelect}
        class="file-input"
      />
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

    {#if status === "error"}
      <p class="error">{errorMessage}</p>
    {/if}
  </section>
{:else if status === "uploading"}
  <section class="progress-section">
    <h1>Encrypting &amp; Uploading...</h1>
    <div class="progress-bar">
      <div class="progress-fill" style="width: {uploadProgress}%"></div>
    </div>
    <p class="progress-text">{uploadProgress}%</p>
  </section>
{:else if status === "done"}
  <section class="done-section">
    <h1>Ready to Share</h1>
    <p class="subtitle">Anyone with this link can download your files.</p>

    {#if qrDataUrl}
      <div class="qr-container">
        <img src={qrDataUrl} alt="QR code for download link" class="qr-code" />
      </div>
    {/if}

    <div class="link-box">
      <input bind:this={linkInput} type="text" readonly value={shareUrl} class="link-input" />
      <button class="btn" onclick={copyLink}>
        {copied ? "Copied!" : "Copy"}
      </button>
    </div>

    {#if copyMessage}
      <p role="status">{copyMessage}</p>
    {/if}

    <button class="btn secondary" onclick={reset}>Share more files</button>
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

  .btn.secondary {
    width: 100%;
    margin-top: 1rem;
  }

  .progress-section {
    text-align: center;
    padding-top: 3rem;
  }

  .progress-bar {
    width: 100%;
    height: 8px;
    background: #e5e5e5;
    border-radius: 4px;
    overflow: hidden;
    margin: 1.5rem 0 0.75rem;
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

  .done-section {
    text-align: center;
  }

  .qr-container {
    margin: 1.5rem 0;
  }

  .qr-code {
    width: 200px;
    height: 200px;
    border-radius: 8px;
  }

  .link-box {
    display: flex;
    gap: 0.5rem;
    margin: 1rem 0;
  }

  .link-input {
    flex: 1;
    padding: 0.5rem 0.75rem;
    border: 1px solid #ccc;
    border-radius: 8px;
    font-size: 0.8125rem;
    color: #333;
    background: #f9f9f9;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .error {
    color: #d33;
    margin-top: 1rem;
    font-size: 0.875rem;
  }
</style>
