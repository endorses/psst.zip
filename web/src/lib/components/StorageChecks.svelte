<script lang="ts">
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

<section aria-label="Stored file checks" class="storage-checks">
  <div class="heading">
    <h2>Stored file checks</h2>
    <button disabled={busy} onclick={refresh}
      >{busy ? "Refreshing checks…" : "Refresh file checks"}</button
    >
  </div>
  <p class="muted small">Checks payload files referenced by the database.</p>
  {#if failed}
    <p role="alert" class="error">
      File-check status could not be loaded.{snapshot
        ? " The previous snapshot remains below and may be stale."
        : " Retry with Refresh file checks."}
    </p>
  {/if}
  {#if snapshot}
    <p class="summary">
      {snapshot.scan_error_code
        ? "File checks incomplete"
        : snapshot.state === "pending"
          ? snapshot.last_scan_completed_at
            ? "File checks pending"
            : "Initial checks pending"
          : snapshot.issue_count > 0
            ? "Unresolved file checks"
            : "No recorded issues"}
    </p>
    {#if snapshot.scan_error_code}
      <p class="error" role="alert">The database-file scan could not complete. It will retry.</p>
    {/if}
    <dl>
      <div>
        <dt>Published payloads unavailable</dt>
        <dd>{snapshot.unavailable_count}</dd>
      </div>
      <div>
        <dt>Inspection or repair failures</dt>
        <dd>{snapshot.failed_count}</dd>
      </div>
      <div>
        <dt>Waiting for active file operations</dt>
        <dd>{snapshot.busy_count}</dd>
      </div>
    </dl>
    <p class="muted small">
      {snapshot.last_scan_completed_at
        ? `Last completed database-file pass: ${utcTime(snapshot.last_scan_completed_at)}.`
        : "No completed database-file pass recorded yet."}
      {snapshot.scan_pending
        ? "Files are still awaiting checks or retry."
        : "The last traversal reached its end."}
    </p>
  {:else if !failed}<p role="status">Loading file-check status…</p>{/if}
  <p class="muted small">
    Pending uploads can be repaired before publication. Published payload mismatches block downloads
    until storage is restored or the resource is revoked.
  </p>
  <p class="muted small">
    A completed pass may include unresolved checks. This is not an orphan-file scan, a complete
    disk-health check, or a measurement of physical disk usage.
  </p>
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
