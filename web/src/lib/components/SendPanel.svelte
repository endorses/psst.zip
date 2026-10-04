<script lang="ts">
  import { onMount, onDestroy, untrack } from "svelte";
  import { beforeNavigate } from "$app/navigation";
  import { UploadJob, formatSize } from "$lib/upload-job.svelte";
  import Icon from "./Icon.svelte";
  import LinkCard from "./LinkCard.svelte";
  import { fileLimitLabel } from "$lib/limits";
  import OptionalLimit from "./OptionalLimit.svelte";
  import { capacityLabel, durationLabel } from "$lib/resource-policy";
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
  let maxDownloads = $state(0);
  onMount(() => {
    void job.refreshLimit();
  });
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
    void job.start({ accountId, slotId, key: keyString, maxDownloads, oncreated });
  }
</script>

<svelte:window onbeforeunload={unload} />
{#if job.state === "done"}
  <h1>{slotId ? "Files sent" : "Ready to share"}</h1>
  {#if slotId}<p class="success" role="status">
      Your encrypted files were sent to the inbox owner. You can close this page.
    </p>{:else}<LinkCard url={job.url} label="Anyone with this link can save your files." />{/if}
  {#if !slotId && maxDownloads}<p class="muted small">
      {maxDownloads} download attempts per file, including interrupted downloads.
    </p>{/if}
  <button
    onclick={() => {
      job.files = [];
      job.state = "idle";
      job.url = "";
      maxDownloads = 0;
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
  <p class="muted">A private link for anything you need to share.</p>
  <label
    class="dropzone"
    ondragover={(e) => e.preventDefault()}
    ondrop={(e) => {
      e.preventDefault();
      job.add(Array.from(e.dataTransfer?.files ?? []));
    }}
  >
    <span class="upload-icon"><Icon name="Upload" size={28} /></span>
    <strong class="drop-title"
      >{job.files.length ? "Add more files" : "Drop your files here"}</strong
    >
    <span class="muted small">or choose them from your device</span>
    <span class="choose-files" aria-hidden="true">Choose files <Icon name="Arrow" size={17} /></span
    >
    <span class="muted small file-limit"
      >{job.limit === null
        ? "Checking server file limit…"
        : `Up to ${fileLimitLabel(job.limit)} per file.`}</span
    ><input
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
  {#if job.resourcePolicy}<p class="muted small">
      Server policy: {capacityLabel(job.resourcePolicy.account_storage_bytes)} reserved storage per account,
      {job.resourcePolicy.account_files} file allocations, links up to {durationLabel(
        job.resourcePolicy.max_retention_seconds,
      )}. Unfinished uploads expire after {durationLabel(
        job.resourcePolicy.pending_upload_seconds,
      )}. Remaining account/server capacity and disk reserves can further restrict uploads.
    </p>{/if}
  {#if job.trafficPolicy?.enforcement_enabled}<p class="muted small">
      Transfer traffic budgets are enforced by this server. {job.trafficPolicy.basis === "outbound"
        ? "Downloads count toward the budget."
        : "Uploads and downloads count toward the budget."} Available owner and server budgets can stop
      transfers; retries also count. Ask the owner or server administrator if a budget is exhausted.
    </p>{/if}
  {#if job.files.length}<p class="selection-summary">
      {job.files.length} file{job.files.length === 1 ? "" : "s"} · {formatSize(
        job.files.reduce((n, f) => n + f.size, 0),
      )}
    </p>
    <ul class="file-list">
      {#each job.files as file, i}<li>
          <span class="file-name">{file.name}</span><span class="file-size"
            >{formatSize(file.size)}</span
          ><button
            aria-label={`Remove ${file.name}`}
            onclick={() => (job.files = job.files.filter((_, index) => index !== i))}
            ><Icon name="Close" size={18} /></button
          >
        </li>{/each}
    </ul>
    {#if !slotId}<OptionalLimit
        bind:value={maxDownloads}
        label="Limit downloads per file"
        description="Each file allows this many download attempts. Interrupted downloads and retries count. This limit is fixed when you send."
      />{/if}
    <button class="primary" onclick={start}
      ><Icon name={job.state === "error" ? "Refresh" : "Send"} size={18} />{job.state === "error"
        ? "Retry upload"
        : "Send files"}</button
    >{:else}<p class="encryption-note">
      <Icon name="Lock" size={16} />{slotId
        ? "Encrypted for the inbox owner. Other uploaders cannot read your files."
        : "Encrypted on your device. Only people with the link can open your files."}
    </p>{/if}
{/if}
{#if job.error}<p class="error" role="alert">{job.error}</p>
  {#if job.transferId && !job.active}<button onclick={() => job.retryCleanup()}
      >Retry cleanup</button
    >{/if}{/if}

<style>
  .dropzone {
    min-height: 320px;
    align-content: center;
    justify-items: center;
    gap: 0.6rem;
    margin: 2rem 0 1.25rem;
  }
  .upload-icon {
    display: grid;
    place-items: center;
    width: 60px;
    height: 60px;
    background: var(--accent);
    color: var(--primary);
    border-radius: 18px;
    margin-bottom: 0.65rem;
  }
  .drop-title {
    font-size: 1.2rem;
    font-weight: 600;
    letter-spacing: -0.02em;
  }
  .choose-files {
    display: inline-flex;
    align-items: center;
    gap: 0.6rem;
    background: var(--primary);
    color: var(--on-primary);
    padding: 0.65rem 1rem;
    border-radius: 10px;
    font-weight: 550;
    margin-top: 0.4rem;
  }
  .file-limit {
    margin-top: 0.35rem;
    font-size: 0.75rem;
  }
  .encryption-note {
    display: flex;
    justify-content: center;
    gap: 0.5rem;
    align-items: baseline;
    text-align: center;
    font-size: 0.78rem;
    color: var(--muted);
  }
  .encryption-note :global(svg) {
    flex-shrink: 0;
    position: relative;
    top: 3px;
  }
  .selection-summary {
    color: var(--muted);
    font-size: 0.9rem;
    font-weight: 550;
    margin-top: 1.5rem;
  }
  @media (max-width: 540px) {
    .dropzone {
      min-height: 290px;
      padding: 2rem 1rem;
    }
  }
</style>
