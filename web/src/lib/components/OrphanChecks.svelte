<script lang="ts">
  import { message as m, t } from "$lib/i18n";

  import { onMount } from "svelte";
  import { utcTime } from "$lib/admin";
  import { loadOrphanChecks, type OrphanChecks } from "$lib/admin-orphan-checks";

  let snapshot = $state<OrphanChecks | null>(null);
  let busy = $state(false);
  let failed = $state(false);
  let disposed = false;
  const controller = new AbortController();

  async function refresh() {
    if (busy) return;
    busy = true;
    failed = false;
    try {
      const next = await loadOrphanChecks(controller.signal);
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

<section aria-label={$t(m("orphanFileChecks"))} class="orphan-checks">
  <div class="heading">
    <h2>{$t(m("orphanFileChecks"))}</h2>
    <button disabled={busy} onclick={refresh}
      >{$t(busy ? m("refreshingOrphanChecks") : m("refreshOrphanChecks"))}</button
    >
  </div>
  <p class="muted small">{$t(m("looksForRegularPayloadFilesNoLongerReferencedBy"))}</p>
  {#if failed}
    <p role="alert" class="error">
      {$t(m("orphanCheckStatusCouldNotBeLoaded"))}{$t(
        snapshot
          ? m("thePreviousSnapshotRemainsBelowAndMayBeStale")
          : m("retryWithRefreshOrphanChecks"),
      )}
    </p>
  {/if}
  {#if snapshot}
    <p class="summary">
      {$t(
        snapshot.scan_error_code ||
          snapshot.saturated ||
          snapshot.unstable ||
          snapshot.state === "degraded"
          ? m("orphanChecksIncomplete")
          : snapshot.state === "pending"
            ? m("orphanChecksPending")
            : m("lastOrphanPassCompleted"),
      )}
    </p>
    {#if snapshot.scan_error_code}
      <p class="error" role="alert">{$t(m("theOrphanScanCouldNotCompleteItWillRetry"))}</p>
    {/if}
    {#if snapshot.saturated}
      <p class="muted small">{$t(m("theWorkQueueReachedItsLimitAdditionalEntriesMay"))}</p>
    {/if}
    {#if snapshot.unstable}
      <p class="muted small">{$t(m("storageChangedDuringScanningAnotherPassIsNeeded"))}</p>
    {/if}
    <dl>
      <div>
        <dt>{$t(m("queuedDirectories"))}</dt>
        <dd>{$t(snapshot.pending_directories)}</dd>
      </div>
      <div>
        <dt>{$t(m("queuedCandidates"))}</dt>
        <dd>{$t(snapshot.pending_candidates)}</dd>
      </div>
      <div>
        <dt>{$t(m("queuedChecksAwaitingSafeAccess"))}</dt>
        <dd>{$t(snapshot.busy_count)}</dd>
      </div>
      <div>
        <dt>{$t(m("queuedFailures"))}</dt>
        <dd>{$t(snapshot.failed_count)}</dd>
      </div>
      <div>
        <dt>{$t(m("recordedUnsupportedEntries"))}</dt>
        <dd>{$t(snapshot.unsupported_count)}</dd>
      </div>
    </dl>
    {#if snapshot.unsupported_count > 0}
      <p class="muted small">{$t(m("unknownOrSuspiciousEntriesAreRetainedForOperatorReview"))}</p>
    {/if}
    {#if snapshot.oldest_pending_at}
      <p class="muted small">
        {$t(m("oldestQueuedObservation"))}
        {$t(utcTime(snapshot.oldest_pending_at))}.
      </p>
    {/if}
    <p class="muted small">
      {$t(
        snapshot.last_scan_completed_at
          ? m("lastCompletedOrphanPassValue", { arg0: utcTime(snapshot.last_scan_completed_at) })
          : m("noCompletedOrphanPassRecordedYet"),
      )}
      {$t(
        snapshot.scan_pending
          ? m("additionalWorkMayAwaitDiscoveryOrRetry")
          : m("theLastTraversalReachedItsEnd"),
      )}
    </p>
  {:else if !failed}<p role="status">{$t(m("loadingOrphanCheckStatus"))}</p>{/if}
  <p class="muted small">{$t(m("candidatePayloadsAreObservedForAtLeastOneHour"))}</p>
  <p class="muted small">{$t(m("countsCoverBoundedQueuedWorkNotACompleteDisk"))}</p>
</section>

<style>
  .orphan-checks {
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
