<script lang="ts">
  import { message as m, t, LocalizedError, errorText, type DisplayText } from "$lib/i18n";

  import { accountRequest } from "$lib/account";
  import {
    validateAccountTrafficSnapshot,
    validateTrafficSnapshot,
    type AccountTrafficSnapshot,
    type TrafficSnapshot,
  } from "$lib/traffic-policy";
  import TrafficBudgetUsage from "./TrafficBudgetUsage.svelte";
  import { formatSize } from "$lib/upload-job.svelte";
  let { accountId, username }: { accountId?: string; username?: string } = $props();
  let snapshot = $state<TrafficSnapshot | AccountTrafficSnapshot | null>(null),
    busy = $state(false),
    error = $state<DisplayText>(""),
    notice = $state<DisplayText>("");
  let inherit = $state(true),
    budgetGiB = $state<number | undefined>(),
    open = $state(false);
  let generation = 0;
  $effect(() => {
    accountId;
    generation++;
    snapshot = null;
    error = "";
    notice = "";
    open = false;
    busy = false;
  });
  function accept(next: TrafficSnapshot | AccountTrafficSnapshot) {
    snapshot = next;
    if ("account_budget_bytes" in next) {
      inherit = next.account_budget_bytes === null;
      budgetGiB =
        (next.account_budget_bytes ?? next.policy.default_account_budget_bytes) / 1024 ** 3;
    }
  }
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    const run = generation;
    try {
      const next = accountId
        ? validateAccountTrafficSnapshot(
            await accountRequest(`/admin/users/${accountId}/traffic-policy`),
          )
        : validateTrafficSnapshot(await accountRequest("/auth/traffic-usage"));
      if (run === generation) accept(next);
    } catch (cause) {
      if (run === generation)
        error = cause instanceof Error ? errorText(cause) : m("couldNotLoadAccountTraffic");
    } finally {
      if (run === generation) busy = false;
    }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !accountId) return;
    error = "";
    notice = "";
    const bytes = inherit ? null : (budgetGiB ?? NaN) * 1024 ** 3;
    if (
      bytes !== null &&
      (!Number.isSafeInteger(bytes) ||
        bytes <= 0 ||
        !snapshot ||
        bytes > snapshot.policy.server_budget_bytes)
    ) {
      error = m("enterAPositiveWholeByteBudgetNoLargerThan");
      return;
    }
    busy = true;
    const run = generation;
    try {
      const next = validateAccountTrafficSnapshot(
        await accountRequest(`/admin/users/${accountId}/traffic-policy`, "PATCH", {
          account_budget_bytes: bytes,
        }),
      );
      if (next.account_budget_bytes !== bytes)
        throw new LocalizedError(m("theServerDidNotAcceptThisAccountBudgetRefresh"));
      if (run === generation) {
        accept(next);
        notice = m("accountTrafficBudgetSavedExistingTrafficChargesRemain");
      }
    } catch (cause) {
      if (run === generation)
        error = cause instanceof Error ? errorText(cause) : m("couldNotSaveAccountTrafficBudget");
    } finally {
      if (run === generation) busy = false;
    }
  }
</script>

<details
  bind:open
  ontoggle={(event) => {
    if (event.currentTarget.open && !snapshot && !busy) void load();
  }}
  class="account-traffic"
>
  <summary
    >{$t(
      accountId
        ? m("trafficBudgetForValue", { arg0: username ?? "account" })
        : m("accountTrafficBudgetAndUsage"),
    )}</summary
  >
  {#if error}<p class="error" role="alert">
      {$t(error)}
      {$t(m("previousUsageMayBeStaleUnsavedValuesRemain"))}
    </p>{/if}
  {#if notice}<p class="success" role="status">{$t(notice)}</p>{/if}
  {#if snapshot}
    <p class="muted small">
      {$t(
        snapshot.policy.enforcement_enabled
          ? m("budgetEnforcementIsOn")
          : m("budgetEnforcementIsOff_ef543"),
      )}
      {$t(
        snapshot.policy.basis === "outbound"
          ? m("onlyOutboundDownloadsCount")
          : m("uploadsAndDownloadsCount"),
      )}
      {$t(m("serverAndAccountBudgetsApplyTogetherReceiveLinkSubmissions"))}
    </p>
    <TrafficBudgetUsage {snapshot} enabled={snapshot.policy.enforcement_enabled} />
    {#if accountId && "account_budget_bytes" in snapshot}<form onsubmit={save}>
        <label class="check"
          ><input type="checkbox" disabled={busy} bind:checked={inherit} />{$t(
            m("useTheServerSDefaultAccountBudget"),
          )}{$t(formatSize(snapshot.policy.default_account_budget_bytes))})</label
        >
        <label
          >{$t(m("accountTrafficBudgetGiB"))}<input
            type="number"
            min={1 / 1024 ** 3}
            step="any"
            required={!inherit}
            disabled={busy || inherit}
            bind:value={budgetGiB}
          /></label
        >
        <p class="muted small">
          {$t(m("effectiveAccountBudget"))}
          {$t(formatSize(snapshot.effective_budget_bytes))}{$t(
            m("theServerBudgetAlsoLimitsThisAccountLoweringThe"),
          )}
        </p>
        <button class="primary" disabled={busy}>{$t(m("saveAccountTrafficBudget"))}</button>
      </form>{/if}
  {/if}
  <button disabled={busy} onclick={load}
    >{$t(busy ? m("loadingAccountTraffic") : m("refreshAccountTraffic"))}</button
  >
</details>

<style>
  .account-traffic {
    margin: 1rem 0;
    width: 100%;
    min-width: 0;
  }
  summary {
    cursor: pointer;
  }
  label {
    display: block;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    margin: 1rem 0;
  }
  .check input {
    width: auto;
  }
  input {
    width: 100%;
  }
</style>
