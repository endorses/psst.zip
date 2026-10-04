<script lang="ts">
  import { onMount } from "svelte";
  import {
    accountRequest,
    resourceFileCount,
    receivedFileCount,
    type Resource,
    type User,
  } from "$lib/account";
  import { utcTime, type Overview } from "$lib/admin";
  import { formatSize } from "$lib/upload-job.svelte";
  import RevokeDialog from "./RevokeDialog.svelte";
  import PublicTransferControl from "./PublicTransferControl.svelte";
  import { loadResourcePage } from "$lib/resource-history";
  let data = $state<Overview | null>(null),
    resources = $state<(Resource & { kind: "transfers" | "slots" })[]>([]),
    users = $state<User[]>([]),
    busy = $state(false),
    error = $state(""),
    notice = $state(""),
    resourceError = $state(""),
    resourcesLoaded = $state(false);
  let target = $state<(Resource & { kind: "transfers" | "slots" }) | null>(null),
    expanded = $state(false),
    disposed = false;
  let resourceCursor = $state(""),
    resourceNext = $state<string | null>(null),
    resourcePrevious = $state<string[]>([]);
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
  async function inspect(direction: "refresh" | "next" | "previous" = "refresh") {
    if (busy) return;
    const cursor =
      direction === "next"
        ? resourceNext
        : direction === "previous"
          ? resourcePrevious.at(-1)
          : resourceCursor;
    if (cursor == null) return;
    expanded = true;
    busy = true;
    resourceError = "";
    try {
      const [r, u] = await Promise.all([
        loadResourcePage(cursor, true),
        accountRequest<{ users: User[] }>("/admin/users"),
      ]);
      if (disposed) return;
      resources = [
        ...(r.transfers ?? []).map((t) => ({ ...t, kind: "transfers" as const })),
        ...(r.slots ?? []).map((t) => ({ ...t, kind: "slots" as const })),
      ].sort((a, b) => Date.parse(b.created_at || "") - Date.parse(a.created_at || ""));
      users = u.users;
      resourcePrevious =
        direction === "next"
          ? [...resourcePrevious, resourceCursor]
          : direction === "previous"
            ? resourcePrevious.slice(0, -1)
            : resourcePrevious;
      resourceCursor = cursor;
      resourceNext = r.next_cursor;
      resourcesLoaded = true;
    } catch (e) {
      if (!disposed) resourceError = e instanceof Error ? e.message : "Could not load resources.";
    } finally {
      busy = false;
    }
  }
  async function revoke() {
    if (!target) return;
    busy = true;
    error = "";
    try {
      await accountRequest(`/${target.kind}/${target.id}`, "DELETE");
      resources = resources.filter((r) => r.id !== target?.id || r.kind !== target?.kind);
      target = null;
      notice = "Resource revoked and its server files deleted.";
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : "Revocation failed.";
    } finally {
      busy = false;
    }
  }
  function resourceStatus(item: Resource & { kind: "transfers" | "slots" }) {
    if (Date.parse(item.expires_at) <= Date.now()) return "Expired";
    if (item.status === "revoked") return "Revoked";
    if (item.kind === "slots") {
      const count = receivedFileCount(item);
      return count === null
        ? "File status unavailable"
        : count > 0
          ? "Files received"
          : "Waiting for files";
    }
    if (item.downloaded_at) return "Delivery confirmed";
    if (item.status === "complete")
      return item.download_count ? "Download started" : "Ready to download";
    return "Upload unfinished";
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
  </p>{/if}{#if notice}<p class="notice" role="status">{notice}</p>{/if}
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
<button disabled={busy} onclick={() => inspect()}
  >{expanded ? "Refresh resources" : "View resources"}</button
>
{#if resourceError}<p class="error" role="alert">
    {resourceError}
    {resourcesLoaded
      ? "Previous resources may be stale."
      : "Resources could not be loaded. Retry with Refresh resources."}
  </p>{/if}
{#if expanded && resourcesLoaded && !resourceError && !busy && !resources.length}<p>
    No resources found.
  </p>{/if}
{#if expanded && (resourcePrevious.length || resourceNext)}<nav aria-label="Resource pages">
    <button disabled={busy || !resourcePrevious.length} onclick={() => inspect("previous")}
      >Newer resources</button
    >
    <span class="muted small">Page {resourcePrevious.length + 1}</span>
    <button disabled={busy || !resourceNext} onclick={() => inspect("next")}>Older resources</button
    >
  </nav>{/if}
{#if expanded}{#each resources as item}<article class="resource" data-resource-id={item.id}>
      <div>
        <strong>{item.kind === "slots" ? "Receive link" : "Sent transfer"}</strong>
        <p>
          Owner: {users.find((u) => u.id === item.owner_id)?.username ||
            item.owner_id ||
            "Unknown account"} · {resourceFileCount(item) === null
            ? "File count unavailable"
            : `${resourceFileCount(item)} files`} · {item.total_size === undefined
            ? "Size unavailable"
            : formatSize(item.total_size)}
        </p>
        <p class="muted small">
          {resourceStatus(item)} · {item.created_at
            ? "Created " + new Date(item.created_at).toLocaleString() + " · "
            : ""}Expires {new Date(item.expires_at).toLocaleString()}
        </p>
        <details>
          <summary>Technical details</summary>
          <p>Resource ID: {item.id}</p>
          <p>Owner ID: {item.owner_id || "Unavailable"}</p>
        </details>
      </div>
      <button
        class="danger"
        disabled={busy}
        onclick={() => {
          target = item;
          error = "";
        }}>Revoke</button
      >
    </article>{/each}{/if}
{#if target}<RevokeDialog {busy} {error} oncancel={() => (target = null)} onconfirm={revoke} />{/if}

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
  .resource {
    display: flex;
    justify-content: space-between;
    align-items: start;
    gap: 1rem;
    border-bottom: 1px solid var(--divider);
    padding: 1rem 0;
    overflow-wrap: anywhere;
  }
  .resource p {
    margin: 0.4rem 0;
  }
  .resource button {
    flex-shrink: 0;
  }
  summary {
    cursor: pointer;
    min-height: 32px;
    color: var(--muted);
  }
  @media (max-width: 480px) {
    .resource {
      flex-direction: column;
    }
  }
</style>
