<script lang="ts">
  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { utcTime, type Overview } from "$lib/admin";
  import { formatSize } from "$lib/upload-job.svelte";
  import PublicTransferControl from "./PublicTransferControl.svelte";
  let data = $state<Overview | null>(null),
    busy = $state(false),
    error = $state("");
  let disposed = false;
  async function load() {
    busy = true;
    error = "";
    try {
      const result = await accountRequest<Overview>("/admin/overview");
      if (!disposed) data = result;
    } catch (e) {
      if (!disposed) error = e instanceof Error ? e.message : "Overview is unavailable.";
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
  <h1>Overview</h1>
  <button disabled={busy} onclick={load}>Refresh</button>
</div>
<p class="muted">Manage your private transfer server.</p>
<PublicTransferControl />
{#if error}<p class="error" role="alert">
    {error}
    {data ? "Previous metrics may be stale." : ""}
  </p>{/if}
{#if data}
  {#if data.enabled_users === 0}<section class="first-user">
      <h2>Create your first user</h2>
      <p>
        Administrator accounts manage the service. Create a regular account to send files and
        receive links, including for your own transfers.
      </p>
      <a class="button primary" href="/?view=users">Create your first user</a>
    </section>{/if}
  <div class="metrics">
    <article>
      <span>Enabled regular users</span><strong>{data.enabled_users}</strong><a href="/?view=users"
        >Manage users</a
      >
    </article>
    <article>
      <span>Active transfers</span><strong>{data.active_transfers}</strong><small
        >Current resources</small
      >
    </article>
    <article>
      <span>Active receive links</span><strong>{data.active_receive_links}</strong><small
        >Current resources</small
      >
    </article>
    <article>
      <span>Stored encrypted data</span><strong>{formatSize(data.stored_bytes)}</strong><small
        >Current stored bytes</small
      >
    </article>
    <article>
      <span>Files uploaded</span><strong>{data.files_uploaded}</strong><small
        >Counted once when a transfer finalizes</small
      >
    </article>
    <article>
      <span>Files with confirmed delivery</span><strong>{data.files_delivered}</strong><small
        >Recipient-reported save confirmations, counted once</small
      >
    </article>
  </div>
  <p class="muted small">
    Activity counted since {utcTime(data.recording_started_at)}. Uploaded files: {data.standalone_files_uploaded}
    standalone sends, {data.received_files_uploaded} through receive links. Historical counts remain after
    deletion; current resource gauges do not.
  </p>
  <section>
    <h2>Current-cycle traffic</h2>
    {#if data.traffic.status !== "ok"}<p class="error" role="alert">
        Traffic accounting is degraded; totals may be incomplete.
      </p>{/if}
    <p>
      <strong>{formatSize(data.traffic.cycle.counted_bytes)}</strong> · {data.traffic.settings
        .basis === "outbound"
        ? "Outbound"
        : "Combined"} · {data.traffic.cycle.start} to {data.traffic.cycle.end} (exclusive, UTC)
    </p>
    <p class="muted small">
      Measured since {utcTime(data.traffic.recording_started_at)}; the initial cycle may be partial.
      Application payload measurements may differ from provider billing.
    </p>
    <a href="/?view=traffic">View traffic, budgets and allowance</a>
  </section>
{:else}<p role="status">
    {busy ? "Loading overview…" : "Overview is unavailable. Retry with Refresh."}
  </p>{/if}
<div class="shortcuts">
  <a class="button" href="/?view=users">Manage users</a><a class="button" href="/?view=server"
    >Server settings</a
  >
</div>
<h2>Operational resources</h2>
<p class="muted">
  Inspect ownership, status and storage, or revoke a link. Encryption keys and decrypted file names
  are unavailable to the server.
</p>
<a class="button" href="/?view=resources">View resources</a>

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
