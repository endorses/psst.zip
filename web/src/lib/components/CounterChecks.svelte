<script lang="ts">
  import { message as m, t } from "$lib/i18n";

  import { onMount } from "svelte";
  import { utcTime } from "$lib/admin";
  import { loadCounterChecks, type CounterChecks } from "$lib/admin-counter-checks";

  let snapshot = $state<CounterChecks | null>(null);
  let busy = $state(false);
  let failed = $state(false);
  let disposed = false;
  const controller = new AbortController();

  async function refresh() {
    if (busy) return;
    busy = true;
    failed = false;
    try {
      const next = await loadCounterChecks(controller.signal);
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

<section aria-label={$t(m("counterChecks"))} class="counter-checks">
  <div class="heading">
    <h2>{$t(m("counterChecks"))}</h2>
    <button disabled={busy} onclick={refresh}
      >{$t(busy ? m("refreshingCounters") : m("refreshCounterChecks"))}</button
    >
  </div>
  <p class="muted small">{$t(m("checksAndRepairsStorageSummariesDerivedFromDatabaseRecords"))}</p>
  {#if failed}
    <p role="alert" class="error">
      {$t(m("counterCheckStatusCouldNotBeLoaded"))}{$t(
        snapshot
          ? m("thePreviousSnapshotRemainsBelowAndMayBeStale")
          : m("retryWithRefreshCounterChecks"),
      )}
    </p>
  {/if}
  {#if snapshot}
    <p class="summary">
      {$t(
        snapshot.scan_error_code || snapshot.state === "degraded"
          ? m("counterChecksNeedRetry")
          : snapshot.state === "pending"
            ? m("counterChecksPending")
            : m("lastCounterPassCompleted"),
      )}
    </p>
    {#if snapshot.scan_error_code}
      <p class="error" role="alert">{$t(m("theCounterScanCouldNotCompleteItWillRetry"))}</p>
    {/if}
    <dl>
      <div>
        <dt>{$t(m("queuedChecksIncludingRetries"))}</dt>
        <dd>{$t(snapshot.pending_count)}</dd>
      </div>
      <div>
        <dt>{$t(m("queuedFailures"))}</dt>
        <dd>{$t(snapshot.failed_count)}</dd>
      </div>
      <div>
        <dt>{$t(m("queuedChecksAwaitingStableData"))}</dt>
        <dd>{$t(snapshot.busy_count)}</dd>
      </div>
    </dl>
    <p class="muted small">
      {$t(
        snapshot.last_scan_completed_at
          ? m("lastCompletedCounterPassValue", { arg0: utcTime(snapshot.last_scan_completed_at) })
          : m("noCompletedCounterPassRecordedYet"),
      )}
      {$t(
        snapshot.scan_pending
          ? m("additionalWorkMayAwaitDiscoveryOrRetry")
          : m("theLastTraversalReachedItsEnd"),
      )}
    </p>
  {:else if !failed}<p role="status">{$t(m("loadingCounterCheckStatus"))}</p>{/if}
  <p class="muted small">{$t(m("repairsRunAutomaticallyUploadQuotasUseLiveDatabaseRecords"))}</p>
</section>

<style>
  .counter-checks {
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
