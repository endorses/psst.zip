<script lang="ts">
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

<section aria-label="Orphan file checks" class="orphan-checks">
  <div class="heading">
    <h2>Orphan file checks</h2>
    <button disabled={busy} onclick={refresh}
      >{busy ? "Refreshing orphan checks…" : "Refresh orphan checks"}</button
    >
  </div>
  <p class="muted small">Looks for regular payload files no longer referenced by the database.</p>
  {#if failed}
    <p role="alert" class="error">
      Orphan-check status could not be loaded.{snapshot
        ? " The previous snapshot remains below and may be stale."
        : " Retry with Refresh orphan checks."}
    </p>
  {/if}
  {#if snapshot}
    <p class="summary">
      {snapshot.scan_error_code ||
      snapshot.saturated ||
      snapshot.unstable ||
      snapshot.state === "degraded"
        ? "Orphan checks incomplete"
        : snapshot.state === "pending"
          ? "Orphan checks pending"
          : "Last orphan pass completed"}
    </p>
    {#if snapshot.scan_error_code}
      <p class="error" role="alert">The orphan scan could not complete. It will retry.</p>
    {/if}
    {#if snapshot.saturated}
      <p class="muted small">
        The work queue reached its limit. Additional entries may still need checking.
      </p>
    {/if}
    {#if snapshot.unstable}
      <p class="muted small">Storage changed during scanning. Another pass is needed.</p>
    {/if}
    <dl>
      <div>
        <dt>Queued directories</dt>
        <dd>{snapshot.pending_directories}</dd>
      </div>
      <div>
        <dt>Queued candidates</dt>
        <dd>{snapshot.pending_candidates}</dd>
      </div>
      <div>
        <dt>Queued checks awaiting safe access</dt>
        <dd>{snapshot.busy_count}</dd>
      </div>
      <div>
        <dt>Queued failures</dt>
        <dd>{snapshot.failed_count}</dd>
      </div>
      <div>
        <dt>Recorded unsupported entries</dt>
        <dd>{snapshot.unsupported_count}</dd>
      </div>
    </dl>
    {#if snapshot.unsupported_count > 0}
      <p class="muted small">
        Unknown or suspicious entries are retained for operator review. Nested directories are not
        scanned recursively.
      </p>
    {/if}
    {#if snapshot.oldest_pending_at}
      <p class="muted small">Oldest queued observation: {utcTime(snapshot.oldest_pending_at)}.</p>
    {/if}
    <p class="muted small">
      {snapshot.last_scan_completed_at
        ? `Last completed orphan pass: ${utcTime(snapshot.last_scan_completed_at)}.`
        : "No completed orphan pass recorded yet."}
      {snapshot.scan_pending
        ? "Additional work may await discovery or retry."
        : "The last traversal reached its end."}
    </p>
  {:else if !failed}<p role="status">Loading orphan-check status…</p>{/if}
  <p class="muted small">
    Candidate payloads are observed for at least one hour before automatic removal, with database,
    file identity and active-reader checks repeated first. Queued candidates are not confirmed
    orphans.
  </p>
  <p class="muted small">
    Counts cover bounded queued work, not a complete disk inventory. These checks do not verify
    encryption, file contents, disk health or backups.
  </p>
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
