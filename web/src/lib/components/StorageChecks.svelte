<script lang="ts">
  import { message as m, t } from "$lib/i18n";

  import { onMount } from "svelte";
  import { utcTime } from "$lib/admin";
  import { loadStorageChecks, type StorageChecks } from "$lib/admin-storage-checks";
  let snapshot = $state<StorageChecks | null>(null);
  let busy = $state(false),
    failed = $state(false);
  let disposed = false;
  const controller = new AbortController();
  async function refresh() {
    if (busy) return;
    busy = true;
    failed = false;
    try {
      const next = await loadStorageChecks(controller.signal);
      if (!disposed) snapshot = next;
    } catch {
      if (!disposed) failed = true;
    } finally {
      if (!disposed) busy = false;
    }
  }
  onMount(() => {
    void refresh();
    return () => {
      disposed = true;
      controller.abort();
    };
  });
</script>

<section aria-label={$t(m("storedFileChecks"))} class="storage-checks">
  <div class="heading">
    <h2>{$t(m("storedFileChecks"))}</h2>
    <button disabled={busy} onclick={refresh}
      >{$t(busy ? m("refreshingChecks") : m("refreshFileChecks"))}</button
    >
  </div>
  <p class="muted small">{$t(m("checksPayloadFilesReferencedByTheDatabase"))}</p>
  {#if failed}
    <p role="alert" class="error">
      {$t(m("fileCheckStatusCouldNotBeLoaded"))}{$t(
        snapshot
          ? m("thePreviousSnapshotRemainsBelowAndMayBeStale")
          : m("retryWithRefreshFileChecks"),
      )}
    </p>
  {/if}
  {#if snapshot}
    <p class="summary">
      {$t(
        snapshot.scan_error_code
          ? m("fileChecksIncomplete")
          : snapshot.state === "pending"
            ? snapshot.last_scan_completed_at
              ? m("fileChecksPending")
              : m("initialChecksPending")
            : snapshot.issue_count > 0
              ? m("unresolvedFileChecks")
              : m("noRecordedIssues"),
      )}
    </p>
    {#if snapshot.scan_error_code}
      <p class="error" role="alert">{$t(m("theDatabaseFileScanCouldNotCompleteItWill"))}</p>
    {/if}
    <dl>
      <div>
        <dt>{$t(m("publishedPayloadsUnavailable"))}</dt>
        <dd>{$t(snapshot.unavailable_count)}</dd>
      </div>
      <div>
        <dt>{$t(m("inspectionOrRepairFailures"))}</dt>
        <dd>{$t(snapshot.failed_count)}</dd>
      </div>
      <div>
        <dt>{$t(m("waitingForActiveFileOperations"))}</dt>
        <dd>{$t(snapshot.busy_count)}</dd>
      </div>
    </dl>
    <p class="muted small">
      {$t(
        snapshot.last_scan_completed_at
          ? m("lastCompletedDatabaseFilePassValue", {
              arg0: utcTime(snapshot.last_scan_completed_at),
            })
          : m("noCompletedDatabaseFilePassRecordedYet"),
      )}
      {$t(
        snapshot.scan_pending
          ? m("filesAreStillAwaitingChecksOrRetry")
          : m("theLastTraversalReachedItsEnd"),
      )}
    </p>
  {:else if !failed}<p role="status">{$t(m("loadingFileCheckStatus"))}</p>{/if}
  <p class="muted small">{$t(m("pendingUploadsCanBeRepairedBeforePublicationPublishedPayload"))}</p>
  <p class="muted small">{$t(m("aCompletedPassMayIncludeUnresolvedChecksThisIs"))}</p>
</section>

<style>
  .storage-checks {
    padding: 1rem;
    margin: 1.2rem 0;
    background: var(--elevated);
    border-radius: 12px;
  }
  .heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.75rem;
  }
  h2 {
    margin: 0;
  }
  .summary {
    font-weight: 600;
  }
  dl {
    margin: 1rem 0;
  }
  dl > div {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.25rem 0;
  }
  dt {
    overflow-wrap: anywhere;
  }
  dd {
    margin: 0;
    font-variant-numeric: tabular-nums;
  }
</style>
