<script lang="ts">
  import { message as m, t, number, LocalizedError, errorText, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import {
    validateTrafficPolicy,
    validateTrafficSnapshot,
    type TrafficSnapshot,
    type TrafficPolicy,
  } from "$lib/traffic-policy";
  import TrafficBudgetUsage from "./TrafficBudgetUsage.svelte";
  let snapshot = $state<TrafficSnapshot | null>(null),
    draft = $state<TrafficPolicy | null>(null);
  let serverGiB = $state<number | undefined>(),
    accountGiB = $state<number | undefined>(),
    uploadMiB = $state<number | undefined>(),
    downloadMiB = $state<number | undefined>();
  let busy = $state(false),
    error = $state<DisplayText>(""),
    notice = $state<DisplayText>("");
  let disposed = false;
  function accept(value: unknown) {
    const next = validateTrafficSnapshot(value);
    if (disposed) return;
    snapshot = next;
    draft = { ...next.policy };
    serverGiB = next.policy.server_budget_bytes / 1024 ** 3;
    accountGiB = next.policy.default_account_budget_bytes / 1024 ** 3;
    uploadMiB = next.policy.upload_bytes_per_second / 1024 ** 2;
    downloadMiB = next.policy.download_bytes_per_second / 1024 ** 2;
  }
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    try {
      accept(await accountRequest("/admin/traffic-policy"));
    } catch (cause) {
      if (!disposed)
        error =
          cause instanceof Error ? errorText(cause) : m("couldNotLoadTrafficEnforcementPolicy");
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !draft) return;
    error = "";
    notice = "";
    try {
      const policy = validateTrafficPolicy({
        ...draft,
        server_budget_bytes: (serverGiB ?? NaN) * 1024 ** 3,
        default_account_budget_bytes: (accountGiB ?? NaN) * 1024 ** 3,
        upload_bytes_per_second: (uploadMiB ?? NaN) * 1024 ** 2,
        download_bytes_per_second: (downloadMiB ?? NaN) * 1024 ** 2,
      });
      busy = true;
      const next = validateTrafficSnapshot(
        await accountRequest("/admin/traffic-policy", "PATCH", policy),
      );
      if (
        Object.entries(policy).some(
          ([key, value]) => next.policy[key as keyof TrafficPolicy] !== value,
        )
      )
        throw new LocalizedError(m("theServerDidNotAcceptTheRequestedTrafficPolicy"));
      accept(next);
      if (!disposed)
        notice = m("transferTrafficPolicySavedMonitoringAllowanceSettingsAreUnchanged");
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? errorText(cause) : m("couldNotSaveTrafficPolicy");
    } finally {
      if (!disposed) busy = false;
    }
  }
  const streams = [
    ["max_active_streams", m("activeServerStreams")],
    ["max_streams_per_account", m("activeAccountStreams")],
    ["max_streams_per_ip", m("activeIPStreams")],
    ["max_streams_per_transfer", m("activeTransferStreams")],
    ["max_streams_per_slot", m("activeSlotStreams")],
  ] as const;
  onMount(() => {
    void load();
    return () => {
      disposed = true;
    };
  });
</script>

<section aria-labelledby="traffic-budget-heading">
  <h2 id="traffic-budget-heading">{$t(m("transferTrafficEnforcement"))}</h2>
  <p class="muted">{$t(m("enforcementIsOffByDefaultAndSeparateFromThe"))}</p>
  {#if error}<p class="error" role="alert">
      {$t(error)}
      {$t(
        snapshot
          ? m("displayedUsageMayBeStaleUnsavedValuesRemainIn")
          : m("otherAdministratorAndRecoveryControlsRemainAvailable"),
      )}
    </p>{/if}
  {#if notice}<p class="success" role="status">{$t(notice)}</p>{/if}
  <button disabled={busy} onclick={load}>{$t(m("refreshTrafficEnforcement"))}</button>
  {#if snapshot && draft}
    <TrafficBudgetUsage {snapshot} enabled={snapshot.policy.enforcement_enabled} />
    <form onsubmit={save}>
      <fieldset disabled={busy}>
        <legend>{$t(m("cycleBudgets"))}</legend>
        <label class="check"
          ><input type="checkbox" bind:checked={draft.enforcement_enabled} />{$t(
            m("enforceTransferTrafficBudget"),
          )}</label
        >
        <div class="aligned-fields">
          <label
            >{$t(m("serverTrafficBudgetGiB"))}<input
              type="number"
              min={1 / 1024 ** 3}
              step="any"
              required
              bind:value={serverGiB}
            /></label
          >
          <label
            >{$t(m("defaultAccountTrafficBudgetGiB"))}<input
              type="number"
              min={1 / 1024 ** 3}
              step="any"
              required
              bind:value={accountGiB}
            /></label
          >
          <label
            >{$t(m("enforcedCycleStartsOnDayUTC"))}<input
              type="number"
              min="1"
              max="31"
              step="1"
              required
              bind:value={draft.cycle_start_day}
            /></label
          >
          <label
            >{$t(m("countTowardEnforcedBudget"))}<select bind:value={draft.basis}
              ><option value="outbound">{$t(m("outboundDownloadsOnly"))}</option><option
                value="combined">{$t(m("uploadsAndDownloads"))}</option
              ></select
            ></label
          >
        </div>
        <p class="muted small">{$t(m("aMissingDayStartsOnTheLastDayOf"))}</p>
      </fieldset>
      <fieldset disabled={busy}>
        <legend>{$t(m("bandwidthAndConcurrentStreams"))}</legend>
        <div class="aligned-fields">
          <label
            >{$t(m("uploadBandwidthMiBS"))}<input
              type="number"
              min={1 / 1024 ** 2}
              max="10240"
              step="any"
              required
              bind:value={uploadMiB}
            /></label
          >
          <label
            >{$t(m("downloadBandwidthMiBS"))}<input
              type="number"
              min={1 / 1024 ** 2}
              max="10240"
              step="any"
              required
              bind:value={downloadMiB}
            /></label
          >
          {#each streams as [key, label]}<label
              >{$t(label)}<input
                type="number"
                min="1"
                max="4096"
                step="1"
                required
                bind:value={draft[key]}
              /></label
            >{/each}
        </div>
        <p class="muted small">
          {$t(m("theServerReservesPayloadBytesIn"))}
          {$t(number(snapshot.lease_bytes))}{$t(
            m("byteBlocksBandwidthAndStreamLimitsApplyEvenWhen"),
          )}
        </p>
      </fieldset>
      <button class="primary" disabled={busy}>{$t(m("saveTransferTrafficPolicy"))}</button>
    </form>
  {:else if busy}<p role="status">{$t(m("loadingTrafficEnforcement"))}</p>{/if}
  <p class="muted small">
    {$t(m("theseLimitsAccountForEncryptedApplicationPayloadIncludingManifests"))}
  </p>
</section>

<style>
  section {
    margin: 1.5rem 0;
    padding: 1rem;
    border: 1px solid var(--divider);
    border-radius: 0.75rem;
  }
  h2 {
    margin-top: 0;
  }
  fieldset {
    margin: 1rem 0;
    min-width: 0;
    border: 1px solid var(--divider);
    border-radius: 0.5rem;
  }
  label {
    font-size: 0.9rem;
  }
  input,
  select {
    width: 100%;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    margin-bottom: 1rem;
  }
  .check input {
    width: auto;
  }
</style>
