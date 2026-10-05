<script lang="ts">
  import { message as m, t, errorText, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { utcTime, type Overview } from "$lib/admin";
  import { formatSize } from "$lib/upload-job.svelte";
  import PublicTransferControl from "./PublicTransferControl.svelte";
  let data = $state<Overview | null>(null),
    busy = $state(false),
    error = $state<DisplayText>("");
  let disposed = false;
  async function load() {
    busy = true;
    error = "";
    try {
      const result = await accountRequest<Overview>("/admin/overview");
      if (!disposed) data = result;
    } catch (e) {
      if (!disposed) error = e instanceof Error ? errorText(e) : m("overviewIsUnavailable");
    } finally {
      if (!disposed) busy = false;
    }
  }
  onMount(() => {
    void load();
    return () => {
      disposed = true;
    };
  });
</script>

<div class="heading">
  <h1>{$t(m("overview"))}</h1>
  <button disabled={busy} onclick={load}>{$t(m("refresh"))}</button>
</div>
<p class="muted">{$t(m("manageYourPrivateTransferServer"))}</p>
<PublicTransferControl />
{#if error}<p class="error" role="alert">
    {$t(error)}
    {$t(data ? m("previousMetricsMayBeStale") : "")}
  </p>{/if}
{#if data}
  {#if data.enabled_users === 0}<section class="first-user">
      <h2>{$t(m("createYourFirstUser"))}</h2>
      <p>{$t(m("administratorAccountsManageTheServiceCreateARegularAccount"))}</p>
      <a class="button primary" href="/?view=users">{$t(m("createYourFirstUser"))}</a>
    </section>{/if}
  <div class="metrics">
    <article>
      <span>{$t(m("enabledRegularUsers"))}</span><strong>{$t(data.enabled_users)}</strong><a
        href="/?view=users">{$t(m("manageUsers"))}</a
      >
    </article>
    <article>
      <span>{$t(m("activeTransfers"))}</span><strong>{$t(data.active_transfers)}</strong><small
        >{$t(m("currentResources"))}</small
      >
    </article>
    <article>
      <span>{$t(m("activeReceiveLinks"))}</span><strong>{$t(data.active_receive_links)}</strong
      ><small>{$t(m("currentResources"))}</small>
    </article>
    <article>
      <span>{$t(m("storedEncryptedData"))}</span><strong>{$t(formatSize(data.stored_bytes))}</strong
      ><small>{$t(m("currentStoredBytes"))}</small>
    </article>
    <article>
      <span>{$t(m("filesUploaded"))}</span><strong>{$t(data.files_uploaded)}</strong><small
        >{$t(m("countedOnceWhenATransferFinalizes"))}</small
      >
    </article>
    <article>
      <span>{$t(m("filesWithConfirmedDelivery"))}</span><strong>{$t(data.files_delivered)}</strong
      ><small>{$t(m("recipientReportedSaveConfirmationsCountedOnce"))}</small>
    </article>
  </div>
  <p class="muted small">
    {$t(m("activityCountedSince"))}
    {$t(utcTime(data.recording_started_at))}{$t(m("uploadedFiles"))}
    {$t(data.standalone_files_uploaded)}
    {$t(m("standaloneSends"))}
    {$t(data.received_files_uploaded)}
    {$t(m("throughReceiveLinksHistoricalCountsRemainAfterDeletionCurrent"))}
  </p>
  <section>
    <h2>{$t(m("currentCycleTraffic"))}</h2>
    {#if data.traffic.status !== "ok"}<p class="error" role="alert">
        {$t(m("trafficAccountingIsDegradedTotalsMayBeIncomplete"))}
      </p>{/if}
    <p>
      <strong>{$t(formatSize(data.traffic.cycle.counted_bytes))}</strong> · {$t(
        data.traffic.settings.basis === "outbound" ? m("outbound") : m("combined"),
      )} · {$t(utcTime(data.traffic.cycle.start))}
      {$t(m("to"))}
      {$t(utcTime(data.traffic.cycle.end))}
      {$t(m("exclusiveUTC"))}
    </p>
    <p class="muted small">
      {$t(m("measuredSince"))}
      {$t(utcTime(data.traffic.recording_started_at))}{$t(
        m("theInitialCycleMayBePartialApplicationPayloadMeasurements"),
      )}
    </p>
    <a href="/?view=traffic">{$t(m("viewTrafficBudgetsAndAllowance"))}</a>
  </section>
{:else}<p role="status">
    {$t(busy ? m("loadingOverview") : m("overviewIsUnavailableRetryWithRefresh"))}
  </p>{/if}
<div class="shortcuts">
  <a class="button" href="/?view=users">{$t(m("manageUsers"))}</a><a
    class="button"
    href="/?view=server">{$t(m("serverSettings"))}</a
  >
</div>
<h2>{$t(m("operationalResources"))}</h2>
<p class="muted">{$t(m("inspectOwnershipStatusAndStorageOrRevokeALink"))}</p>
<a class="button" href="/?view=resources">{$t(m("viewResources"))}</a>

<style>
  .heading {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
  }
  .metrics {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(165px, 1fr));
    gap: 1rem;
    margin: 1.5rem 0;
  }
  .metrics article,
  .first-user {
    background: var(--elevated);
    padding: 1.1rem;
    border-radius: 12px;
  }
  .metrics article {
    display: grid;
    gap: 0.5rem;
  }
  .metrics strong {
    font-size: 1.7rem;
  }
  .metrics small {
    color: var(--muted);
  }
  .shortcuts {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    margin: 1.5rem 0;
  }
</style>
