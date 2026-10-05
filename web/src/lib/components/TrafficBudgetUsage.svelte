<script lang="ts">
  import { message as m, t } from "$lib/i18n";

  import type { TrafficStatus } from "$lib/traffic-policy";
  import { formatSize } from "$lib/upload-job.svelte";
  import { utcTime } from "$lib/admin";
  let { snapshot, enabled }: { snapshot: TrafficStatus; enabled?: boolean } = $props();
</script>

<div class="budget-usage">
  {#if snapshot.state === "unavailable"}<p class="error" role="alert">
      {$t(m("trafficAccountingIsUnavailableEnforcedTransfersStopUntilAccounting"))}
    </p>
  {:else if snapshot.state === "exhausted"}<p class="notice" role="status">
      {$t(m("aServerOrAccountTrafficBudgetIsExhaustedAccount"))}
      {$t(utcTime(snapshot.cycle.end))}
      {$t(m("orAfterAnAdministratorChangesTheBudget"))}
    </p>
  {:else}<p>
      <strong
        >{$t(enabled === false ? m("budgetEnforcementIsOff") : m("trafficBudgetAvailable"))}</strong
      >
    </p>{/if}
  <p>
    <strong>{$t(formatSize(snapshot.usage.charged_bytes))}</strong>
    {$t(m("charged"))} <strong>{$t(formatSize(snapshot.usage.remaining_bytes))}</strong>
    {$t(m("remainingOf"))}
    {$t(formatSize(snapshot.usage.budget_bytes))}
  </p>
  <progress
    aria-label={$t(m("transferTrafficBudgetCharged"))}
    max={snapshot.usage.budget_bytes}
    value={Math.min(snapshot.usage.charged_bytes, snapshot.usage.budget_bytes)}
  ></progress>
  <p class="muted small">
    {$t(m("cycle"))}
    {$t(utcTime(snapshot.cycle.start))}
    {$t(m("to"))}
    {$t(utcTime(snapshot.cycle.end))}
    {$t(m("endExclusiveMeasuredSince"))}
    {$t(utcTime(snapshot.recording_started_at))}{$t(m("theInitialCycleMayBePartial"))}
  </p>
  <details>
    <summary>{$t(m("whatIsCharged"))}</summary>
    <dl>
      <dt>{$t(m("observedUploadDownload"))}</dt>
      <dd>
        {$t(formatSize(snapshot.usage.observed_uploaded_bytes))} / {$t(
          formatSize(snapshot.usage.observed_downloaded_bytes),
        )}
      </dd>
      <dt>{$t(m("reservedUploadDownload"))}</dt>
      <dd>
        {$t(formatSize(snapshot.usage.reserved_uploaded_bytes))} / {$t(
          formatSize(snapshot.usage.reserved_downloaded_bytes),
        )}
      </dd>
      <dt>{$t(m("conservativeUploadDownload"))}</dt>
      <dd>
        {$t(formatSize(snapshot.usage.conservative_uploaded_bytes))} / {$t(
          formatSize(snapshot.usage.conservative_downloaded_bytes),
        )}
      </dd>
    </dl>
    <p class="muted small">
      {$t(m("chargedTrafficIncludesObservedBytesActiveReservationsAndConservative"))}
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
