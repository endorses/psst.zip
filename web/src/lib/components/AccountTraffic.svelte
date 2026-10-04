<script lang="ts">
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
    error = $state(""),
    notice = $state("");
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
        error = cause instanceof Error ? cause.message : "Could not load account traffic.";
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
      error =
        "Enter a positive whole-byte budget no larger than the server budget, or use the server default.";
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
        throw new Error("The server did not accept this account budget. Refresh before retrying.");
      if (run === generation) {
        accept(next);
        notice = "Account traffic budget saved. Existing traffic charges remain.";
      }
    } catch (cause) {
      if (run === generation)
        error = cause instanceof Error ? cause.message : "Could not save account traffic budget.";
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
    >{accountId
      ? `Traffic budget for ${username ?? "account"}`
      : "Account traffic budget and usage"}</summary
  >
  {#if error}<p class="error" role="alert">
      {error} Previous usage may be stale; unsaved values remain.
    </p>{/if}
  {#if notice}<p class="success" role="status">{notice}</p>{/if}
  {#if snapshot}
    <p class="muted small">
      {snapshot.policy.enforcement_enabled
        ? "Budget enforcement is on."
        : "Budget enforcement is off."}
      {snapshot.policy.basis === "outbound"
        ? "Only outbound downloads count."
        : "Uploads and downloads count."} Server and account budgets apply together. Receive-link submissions
      are charged to the owner.
    </p>
    <TrafficBudgetUsage {snapshot} enabled={snapshot.policy.enforcement_enabled} />
    {#if accountId && "account_budget_bytes" in snapshot}<form onsubmit={save}>
        <label class="check"
          ><input type="checkbox" disabled={busy} bind:checked={inherit} />Use the server's default
          account budget ({formatSize(snapshot.policy.default_account_budget_bytes)})</label
        >
        <label
          >Account traffic budget (GiB)<input
            type="number"
            min={1 / 1024 ** 3}
            step="any"
            required={!inherit}
            disabled={busy || inherit}
            bind:value={budgetGiB}
          /></label
        >
        <p class="muted small">
          Effective account budget: {formatSize(snapshot.effective_budget_bytes)}. The server budget
          also limits this account. Lowering the budget may stop transfers; deleting files never
          refunds traffic.
        </p>
        <button class="primary" disabled={busy}>Save account traffic budget</button>
      </form>{/if}
  {/if}
  <button disabled={busy} onclick={load}
    >{busy ? "Loading account traffic…" : "Refresh account traffic"}</button
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
