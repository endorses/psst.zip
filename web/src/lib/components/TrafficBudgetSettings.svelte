<script lang="ts">
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
    error = $state(""),
    notice = $state("");
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
          cause instanceof Error ? cause.message : "Could not load traffic enforcement policy.";
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
        throw new Error(
          "The server did not accept the requested traffic policy. Refresh before retrying.",
        );
      accept(next);
      if (!disposed)
        notice = "Transfer traffic policy saved. Monitoring allowance settings are unchanged.";
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? cause.message : "Could not save traffic policy.";
    } finally {
      if (!disposed) busy = false;
    }
  }
  const streams = [
    ["max_active_streams", "Server-wide active streams"],
    ["max_streams_per_account", "Active streams per account"],
    ["max_streams_per_ip", "Active streams per IP address"],
    ["max_streams_per_transfer", "Active streams per transfer"],
    ["max_streams_per_slot", "Active streams per receive link"],
  ] as const;
  onMount(() => {
    void load();
    return () => {
      disposed = true;
    };
  });
</script>

<section aria-labelledby="traffic-budget-heading">
  <h2 id="traffic-budget-heading">Transfer traffic enforcement</h2>
  <p class="muted">
    Enforcement is off by default and separate from the monitoring allowance below. Server and
    account budgets apply together, including invited uploads charged to the receive-link owner.
  </p>
  {#if error}<p class="error" role="alert">
      {error}
      {snapshot
        ? "Displayed usage may be stale. Unsaved values remain in the form."
        : "Other administrator and recovery controls remain available."}
    </p>{/if}
  {#if notice}<p class="success" role="status">{notice}</p>{/if}
  <button disabled={busy} onclick={load}>Refresh traffic enforcement</button>
  {#if snapshot && draft}
    <TrafficBudgetUsage {snapshot} enabled={snapshot.policy.enforcement_enabled} />
    <form onsubmit={save}>
      <fieldset disabled={busy}>
        <legend>Cycle budgets</legend>
        <label class="check"
          ><input type="checkbox" bind:checked={draft.enforcement_enabled} />Enforce transfer
          traffic budget</label
        >
        <div class="fields">
          <label
            >Server traffic budget (GiB)<input
              type="number"
              min={1 / 1024 ** 3}
              step="any"
              required
              bind:value={serverGiB}
            /></label
          >
          <label
            >Default account traffic budget (GiB)<input
              type="number"
              min={1 / 1024 ** 3}
              step="any"
              required
              bind:value={accountGiB}
            /></label
          >
          <label
            >Enforced cycle starts on day (UTC)<input
              type="number"
              min="1"
              max="31"
              step="1"
              required
              bind:value={draft.cycle_start_day}
            /></label
          >
          <label
            >Count toward enforced budget<select bind:value={draft.basis}
              ><option value="outbound">Outbound downloads only</option><option value="combined"
                >Uploads and downloads</option
              ></select
            ></label
          >
        </div>
        <p class="muted small">
          A missing day starts on the last day of that month. Set individual account overrides in
          Users. The default account budget cannot exceed the server budget. Lowering a budget can
          stop transfers immediately; existing data and administrative access remain available.
          Changing the cycle or traffic basis recalculates retained usage; it does not reset
          charges.
        </p>
      </fieldset>
      <fieldset disabled={busy}>
        <legend>Bandwidth and concurrent streams</legend>
        <div class="fields">
          <label
            >Upload bandwidth (MiB/s)<input
              type="number"
              min={1 / 1024 ** 2}
              max="10240"
              step="any"
              required
              bind:value={uploadMiB}
            /></label
          >
          <label
            >Download bandwidth (MiB/s)<input
              type="number"
              min={1 / 1024 ** 2}
              max="10240"
              step="any"
              required
              bind:value={downloadMiB}
            /></label
          >
          {#each streams as [key, label]}<label
              >{label}<input
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
          The server reserves payload bytes in {snapshot.lease_bytes.toLocaleString()}-byte blocks.
          Bandwidth and stream limits apply even when budget enforcement is off. Concurrent streams
          share server capacity.
        </p>
      </fieldset>
      <button class="primary" disabled={busy}>Save transfer traffic policy</button>
    </form>
  {:else if busy}<p role="status">Loading traffic enforcement…</p>{/if}
  <p class="muted small">
    These limits account for encrypted application payload, including manifests, retries and partial
    transfers. They exclude HTTP/TLS overhead, static assets, backups and other services. They
    cannot cap or predict the provider's bill.
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
  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 13rem), 1fr));
    gap: 1rem;
  }
  label {
    display: block;
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
