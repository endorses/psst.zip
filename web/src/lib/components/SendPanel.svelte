<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { beforeNavigate } from "$app/navigation";
  import { UploadJob, formatSize } from "$lib/upload-job.svelte";
  import LinkCard from "./LinkCard.svelte";
  import { FILE_SIZE_NOTICE } from "$lib/limits";
  let {
    accountId,
    slotId,
    keyString,
    oncreated = () => {},
    onactive = () => {},
    initialFiles = [],
    onselection = () => {},
  }: {
    accountId?: string;
    slotId?: string;
    keyString?: string;
    oncreated?: (id: string, url: string, title: string, size: number) => void;
    onactive?: (active: boolean) => void;
    initialFiles?: File[];
    onselection?: (files: File[]) => void;
  } = $props();
  const job = new UploadJob();
  // Initial selection belongs to the account-keyed component; never react to another account.
  job.files = untrack(() => [...initialFiles]);
  $effect(() => onselection(job.files));
  $effect(() => onactive(job.active || (job.files.length > 0 && job.state !== "done")));
  function unload(e: BeforeUnloadEvent) {
    if (job.active || (job.files.length && job.state !== "done")) {
      e.preventDefault();
      e.returnValue = "";
    }
  }
  beforeNavigate(({ willUnload, cancel, to }) => {
    if (
      !willUnload &&
      to?.url.pathname !== "/" &&
      (job.active || (job.files.length && job.state !== "done")) &&
      !confirm("Stop upload and leave? Selected files will be cleared.")
    )
      cancel();
  });
  onDestroy(() => {
    void job.cancel();
  });
  function start() {
    void job.start({ accountId, slotId, key: keyString, oncreated });
  }
</script>

<svelte:window onbeforeunload={unload} />
{#if job.state === "done"}
  <h1>{slotId ? "Files sent" : "Ready to share"}</h1>
  {#if slotId}<p class="success" role="status">
      Your files were sent securely. You can close this page.
    </p>{:else}<LinkCard url={job.url} label="Anyone with this link can save your files." />{/if}
  <button
    onclick={() => {
      job.files = [];
      job.state = "idle";
      job.url = "";
    }}>Send more files</button
  >
{:else if job.active}
  <h1>
    {job.state === "stopping"
      ? "Stopping upload"
      : job.state === "preparing"
        ? "Preparing files"
        : "Sending files"}
  </h1>
  <p class="file-name">{job.current}</p>
  {#if job.state === "preparing"}<p role="status">Encrypting on this device…</p>{:else}<progress
      aria-label="Upload progress"
      max={job.total}
      value={job.sent}
    ></progress>
    <p>
      {Math.floor((job.sent / job.total) * 100)}% · {formatSize(job.sent)} of {formatSize(
        job.total,
      )}
    </p>{/if}
  <button
    onclick={() => {
      if (confirm("Stop upload? Partial files will be removed when possible.")) void job.cancel();
    }}>Cancel upload</button
  >
{:else}
  <h1>Send files</h1>
  <p class="muted">
    Files are encrypted automatically. Share a link or QR code, nearby or anywhere.
  </p>
  <label
    class="dropzone"
    ondragover={(e) => e.preventDefault()}
    ondrop={(e) => {
      e.preventDefault();
      job.add(Array.from(e.dataTransfer?.files ?? []));
    }}
    ><span>{FILE_SIZE_NOTICE}</span><span>Drop files here or choose files</span><input
      class="file-input"
      aria-label="Choose files"
      type="file"
      multiple
      onchange={(e) => {
        job.add(Array.from(e.currentTarget.files ?? []));
        e.currentTarget.value = "";
      }}
    /></label
  >
  {#if job.files.length}<p>
      {job.files.length} files · {formatSize(job.files.reduce((n, f) => n + f.size, 0))}
    </p>
    <ul class="file-list">
      {#each job.files as file, i}<li>
          <span class="file-name">{file.name}</span><span class="file-size"
            >{formatSize(file.size)}</span
          ><button
            aria-label={`Remove ${file.name}`}
            onclick={() => (job.files = job.files.filter((_, index) => index !== i))}>×</button
          >
        </li>{/each}
    </ul>
    <button class="primary" onclick={start}
      >{job.state === "error" ? "Retry upload" : "Send files"}</button
    >{/if}
{/if}
{#if job.error}<p class="error" role="alert">{job.error}</p>
  {#if job.transferId && !job.active}<button onclick={() => job.retryCleanup()}
      >Retry cleanup</button
    >{/if}{/if}
