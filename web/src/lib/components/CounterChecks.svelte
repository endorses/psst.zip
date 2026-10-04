<script lang="ts">
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

<section aria-label="Counter checks" class="counter-checks">
  <div class="heading">
    <h2>Counter checks</h2>
    <button disabled={busy} onclick={refresh}
      >{busy ? "Refreshing counters…" : "Refresh counter checks"}</button
    >
  </div>
  <p class="muted small">Checks and repairs storage summaries derived from database records.</p>
  {#if failed}
    <p role="alert" class="error">
      Counter-check status could not be loaded.{snapshot
        ? " The previous snapshot remains below and may be stale."
        : " Retry with Refresh counter checks."}
    </p>
  {/if}
  {#if snapshot}
    <p class="summary">
      {snapshot.scan_error_code || snapshot.state === "degraded"
        ? "Counter checks need retry"
        : snapshot.state === "pending"
          ? "Counter checks pending"
          : "Last counter pass completed"}
    </p>
    {#if snapshot.scan_error_code}
      <p class="error" role="alert">The counter scan could not complete. It will retry.</p>
    {/if}
    <dl>
      <div>
        <dt>Queued checks, including retries</dt>
        <dd>{snapshot.pending_count}</dd>
      </div>
      <div>
        <dt>Queued failures</dt>
        <dd>{snapshot.failed_count}</dd>
      </div>
      <div>
        <dt>Queued checks awaiting stable data</dt>
        <dd>{snapshot.busy_count}</dd>
      </div>
    </dl>
    <p class="muted small">
      {snapshot.last_scan_completed_at
        ? `Last completed counter pass: ${utcTime(snapshot.last_scan_completed_at)}.`
        : "No completed counter pass recorded yet."}
      {snapshot.scan_pending
        ? "Additional work may await discovery or retry."
        : "The last traversal reached its end."}
    </p>
  {:else if !failed}<p role="status">Loading counter-check status…</p>{/if}
  <p class="muted small">
    Repairs run automatically. Upload quotas use live database records. These checks do not verify
    stored file contents, orphan files, disk health or backups.
  </p>
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
