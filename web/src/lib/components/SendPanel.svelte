<script module lang="ts">
  import { message as m, t, translate } from "$lib/i18n";

  export type SendDraft = {
    files: File[];
    maxDownloads: number;
    policyLocked: boolean;
    title?: string;
  };
</script>

<script lang="ts">
  import { onMount, onDestroy, untrack } from "svelte";
  import { beforeNavigate } from "$app/navigation";
  import { UploadJob, formatSize } from "$lib/upload-job.svelte";
  import Icon from "./Icon.svelte";
  import LinkCard from "./LinkCard.svelte";
  import { fileLimitLabel } from "$lib/limits";
  import OptionalLimit from "./OptionalLimit.svelte";
  import { getTransferInfo, type TransferInfo } from "$lib/api";
  import { downloadLinkExhausted } from "$lib/link-title";
  let {
    accountId,
    slotId,
    keyString,
    destinationTitle,
    visible = true,
    oncreated = () => {},
    onactive = () => {},
    initialDraft = { files: [], maxDownloads: 0, policyLocked: false },
    onselection = () => {},
  }: {
    accountId?: string;
    slotId?: string;
    keyString?: string;
    destinationTitle?: string | null;
    visible?: boolean;
    oncreated?: (id: string, url: string, title: string, size: number) => void;
    onactive?: (active: boolean) => void;
    initialDraft?: SendDraft;
    onselection?: (draft: SendDraft) => void;
  } = $props();
  const job = new UploadJob(untrack(() => (slotId ? { slotId, key: keyString ?? "" } : undefined)));
  let maxDownloads = $state(untrack(() => initialDraft.maxDownloads));
  let title = $state(untrack(() => initialDraft.title ?? ""));
  let policyLocked = $state(untrack(() => initialDraft.policyLocked));
  let settingsOpen = $state(untrack(() => initialDraft.maxDownloads !== 0 || !!initialDraft.title));
  let sharedState = $state<TransferInfo | null>(null);
  $effect(() => {
    if (slotId || !visible || job.state !== "done" || !job.transferId) return;
    const id = job.transferId;
    const controller = new AbortController();
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      if (stopped) return;
      if (!document.hidden) {
        try {
          const value = await getTransferInfo(id, controller.signal);
          if (!stopped) sharedState = value;
        } catch {
          /* A temporary refresh failure must not discard a share result. */
        }
      }
      if (!stopped) timer = setTimeout(refresh, 10000);
    }
    void refresh();
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller.abort();
    };
  });
  onMount(() => {
    void job.refreshLimit();
  });
  // Initial selection belongs to the account-keyed component; never react to another account.
  job.files = untrack(() => [...initialDraft.files]);
  $effect(() => {
    if (!slotId && job.transferId) policyLocked = true;
  });
  $effect(() => onselection({ files: job.files, maxDownloads, policyLocked, title }));
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
      !confirm(translate(m("stopUploadAndLeaveSelectedFilesWillBeCleared")))
    )
      cancel();
  });
  onDestroy(() => {
    job.dispose();
  });
  function start() {
    void job.start({ accountId, slotId, key: keyString, maxDownloads, title, oncreated });
  }
</script>

<svelte:window onbeforeunload={unload} />
{#if job.state === "done"}
  <h1>
    {$t(
      slotId
        ? m("filesSent")
        : sharedState && downloadLinkExhausted(sharedState)
          ? m("downloadLimitReached")
          : m("readyToShare"),
    )}
  </h1>
  {#if slotId}<p class="success" role="status">
      {$t(m("yourEncryptedFilesWereSentToTheInboxOwner"))}
    </p>{:else if !sharedState || !downloadLinkExhausted(sharedState)}<LinkCard
      url={job.url}
      label={sharedState?.title || title || m("shareYourFiles")}
    />{/if}
  {#if !slotId && maxDownloads}<p class="muted small">
      {$t(m("downloadAttemptsPerFileIncludingInterruptedDownloads", { count: maxDownloads }))}
    </p>{/if}
  <button
    onclick={() => {
      job.files = [];
      job.state = "idle";
      job.url = "";
      maxDownloads = 0;
      title = "";
      policyLocked = false;
      settingsOpen = false;
      sharedState = null;
      if (slotId) void job.refreshLimit();
    }}>{$t(m("sendMoreFiles"))}</button
  >
{:else if job.active}
  <h1>
    {$t(
      job.state === "stopping"
        ? m("stoppingUpload")
        : job.state === "preparing"
          ? m("preparingFiles")
          : m("sendingFiles"),
    )}
  </h1>
  <p class="file-name">{$t(job.current)}</p>
  {#if job.state === "preparing"}<p role="status">
      {$t(m("encryptingOnThisDevice"))}
    </p>{:else}<progress aria-label={$t(m("uploadProgress"))} max={job.total} value={job.sent}
    ></progress>
    <p>
      {$t(Math.floor((job.sent / job.total) * 100))}% · {$t(formatSize(job.sent))}
      {$t(m("of"))}
      {$t(formatSize(job.total))}
    </p>{/if}
  <button
    onclick={() => {
      if (confirm(translate(m("stopPartialUpload")))) void job.cancel();
    }}>{$t(m("cancelUpload"))}</button
  >
{:else}
  <h1>{$t(m("sendFiles"))}</h1>
  <p class="muted">
    {$t(
      slotId
        ? destinationTitle || job.availability?.title || location.host
        : m("aPrivateLinkForAnythingYouNeedToShare"),
    )}
  </p>
  {#if slotId && (job.availabilityStale || job.availability?.upload_capacity.state !== "ready")}
    <div class="guest-capacity" aria-label={$t(m("uploadAvailability"))}>
      <p role="status">
        {$t(
          job.checking
            ? m("checkingAvailability")
            : job.availability?.upload_capacity.state === "blocked"
              ? m("thisLinkCannotAcceptFilesRightNow")
              : m("couldNotCheckAvailability"),
        )}
      </p>
      <button disabled={job.checking} onclick={() => job.refreshLimit()}
        ><Icon name="Refresh" size={16} />{$t(m("refresh"))}</button
      >
    </div>
  {:else if slotId && job.availability?.remaining_files != null}
    <p class="muted small">
      {$t(m("remainingFilesCount", { count: job.availability.remaining_files }))}
    </p>
  {/if}
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
      >{$t(job.files.length ? m("addMoreFiles") : m("dropYourFilesHere"))}</strong
    >
    <span class="muted small">{$t(m("orChooseThemFromYourDevice"))}</span>
    <span class="choose-files" aria-hidden="true"
      >{$t(m("chooseFiles"))} <Icon name="Arrow" size={17} /></span
    >
    <span class="muted small file-limit"
      >{$t(
        job.limit === null
          ? m("checkingServerFileLimit")
          : m("upToValuePerFile", { arg0: fileLimitLabel(job.limit) }),
      )}</span
    ><input
      class="file-input"
      aria-label={$t(m("chooseFiles"))}
      type="file"
      multiple
      onchange={(e) => {
        job.add(Array.from(e.currentTarget.files ?? []));
        e.currentTarget.value = "";
      }}
    /></label
  >
  {#if job.files.length}<p class="selection-summary">
      {$t(m("fileCount", { count: job.files.length }))} · {$t(
        formatSize(job.files.reduce((n, f) => n + f.size, 0)),
      )}
    </p>
    <ul class="file-list">
      {#each job.files as file, i}<li>
          <span class="file-name">{$t(file.name)}</span><span class="file-size"
            >{$t(formatSize(file.size))}</span
          ><button
            aria-label={$t(m("removeValue", { arg0: file.name }))}
            onclick={() => job.remove(i)}><Icon name="Close" size={18} /></button
          >
        </li>{/each}
    </ul>
    {#if !slotId}
      <details class="send-options" bind:open={settingsOpen}>
        <summary
          >{$t(m("linkSettings"))} <span class="muted small">{$t(m("optional"))}</span></summary
        >
        <label
          >{$t(m("linkTitle"))}<input
            bind:value={title}
            maxlength="400"
            disabled={policyLocked}
            placeholder={$t(m("forExampleWeddingPhotos"))}
          /></label
        >
        <p class="muted small">{$t(m("shownToPeopleUsingThisLink"))}</p>
        <OptionalLimit
          bind:value={maxDownloads}
          disabled={policyLocked}
          label={$t(m("limitDownloadsPerFile"))}
          description={$t(m("eachFileAllowsThisManyDownloadAttemptsInterruptedDownloads"))}
        />
      </details>{/if}
    <button class="primary" disabled={job.checking || !job.guestReady} onclick={start}
      ><Icon name={job.state === "error" ? "Refresh" : "Send"} size={18} />{$t(
        job.state === "error" ? m("retryUpload") : m("sendFiles"),
      )}</button
    >{:else}<p class="encryption-note">
      <Icon name="Lock" size={16} />{$t(
        slotId
          ? m("encryptedForTheInboxOwnerOtherUploadersCannotRead")
          : m("encryptedOnYourDeviceOnlyPeopleWithTheLink"),
      )}
    </p>{/if}
{/if}
{#if job.error}<p class="error" role="alert">{$t(job.error)}</p>
  {#if job.transferId && !job.active}<button onclick={() => job.retryCleanup()}
      >{$t(m("retryCleanup"))}</button
    >{/if}{/if}

<style>
  .send-options {
    margin: 1rem 0;
  }
  .send-options summary {
    cursor: pointer;
    padding: 0.5rem 0;
  }
  .send-options label {
    margin-top: 0.75rem;
  }
  .guest-capacity {
    display: flex;
    gap: 0.75rem;
    align-items: center;
  }
  .guest-capacity p {
    margin: 0;
  }

  .dropzone {
    min-height: 220px;
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
      min-height: 200px;
      padding: 2rem 1rem;
    }
  }
</style>
