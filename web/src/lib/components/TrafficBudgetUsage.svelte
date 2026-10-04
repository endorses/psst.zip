<script lang="ts">
  import type { TrafficStatus } from "$lib/traffic-policy";
  import { formatSize } from "$lib/upload-job.svelte";
  import { utcTime } from "$lib/admin";
  let { snapshot, enabled }: { snapshot: TrafficStatus; enabled?: boolean } = $props();
</script>

<div class="budget-usage">
  {#if snapshot.state === "unavailable"}<p class="error" role="alert">
      Traffic accounting is unavailable. Enforced transfers stop until accounting recovers.
      Administrative and recovery controls remain available.
    </p>
  {:else if snapshot.state === "exhausted"}<p class="notice" role="status">
      A server or account traffic budget is exhausted. Account headroom alone cannot override the
      server budget. Retry manually after {utcTime(snapshot.cycle.end)} or after an administrator changes
      the budget.
    </p>
  {:else}<p>
      <strong>{enabled === false ? "Budget enforcement is off" : "Traffic budget available"}</strong
      >
    </p>{/if}
  <p>
    <strong>{formatSize(snapshot.usage.charged_bytes)}</strong> charged ·
    <strong>{formatSize(snapshot.usage.remaining_bytes)}</strong>
    remaining of {formatSize(snapshot.usage.budget_bytes)}
  </p>
  <progress
    aria-label="Transfer traffic budget charged"
    max={snapshot.usage.budget_bytes}
    value={Math.min(snapshot.usage.charged_bytes, snapshot.usage.budget_bytes)}
  ></progress>
  <p class="muted small">
    Cycle {utcTime(snapshot.cycle.start)} to {utcTime(snapshot.cycle.end)} (end exclusive). Measured since
    {utcTime(snapshot.recording_started_at)}; the initial cycle may be partial.
  </p>
  <details>
    <summary>What is charged</summary>
    <dl>
      <dt>Observed upload / download</dt>
      <dd>
        {formatSize(snapshot.usage.observed_uploaded_bytes)} / {formatSize(
          snapshot.usage.observed_downloaded_bytes,
        )}
      </dd>
      <dt>Reserved upload / download</dt>
      <dd>
        {formatSize(snapshot.usage.reserved_uploaded_bytes)} / {formatSize(
          snapshot.usage.reserved_downloaded_bytes,
        )}
      </dd>
      <dt>Conservative upload / download</dt>
      <dd>
        {formatSize(snapshot.usage.conservative_uploaded_bytes)} / {formatSize(
          snapshot.usage.conservative_downloaded_bytes,
        )}
      </dd>
    </dl>
    <p class="muted small">
      Charged traffic includes observed bytes, active reservations and conservative charges retained
      after interruptions. Only the selected traffic basis counts toward the budget. Deleting links
      or files does not refund traffic. A new UTC cycle starts a new budget.
    </p>
  </details>
</div>

<style>
  progress {
    width: 100%;
  }
  dl {
    display: grid;
    gap: 0.4rem;
  }
  dd {
    margin: 0 0 0.5rem;
    overflow-wrap: anywhere;
  }
  summary {
    cursor: pointer;
  }
</style>
