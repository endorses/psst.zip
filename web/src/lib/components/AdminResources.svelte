<script lang="ts">
  import { onMount, tick } from "svelte";
  import { AccountError } from "$lib/account";
  import { utcTime } from "$lib/admin";
  import { formatSize } from "$lib/upload-job.svelte";
  import {
    isResourceID,
    loadAdminResources,
    loadAdminResource,
    loadCleanupOverview,
    mutateAdminResource,
    resourceLabel,
    resourceStatus,
    cleanupLabel,
    cleanupFailure,
    cleanupReason,
    type AdminResource,
    type AdminResourcePage,
    type ResourceType,
    type ResourceFilters,
    type CleanupOverview,
  } from "$lib/admin-resources";
  import SecurityEvents from "./SecurityEvents.svelte";
  import AdminResourceRevoke from "./AdminResourceRevoke.svelte";
  let result = $state<AdminResourcePage | null>(null),
    overview = $state<CleanupOverview | null>(null);
  let detail = $state<AdminResource | null>(null),
    selected = $state<{ type: ResourceType; id: string } | null>(null);
  let absent = $state(false),
    removed = $state(false),
    auditVersion = $state(0);
  let busy = $state(false),
    error = $state(""),
    detailError = $state(""),
    cleanupError = $state(""),
    notice = $state("");
  let type = $state(""),
    status = $state(""),
    owner = $state("");
  let lookupType = $state<ResourceType>("transfer"),
    lookupID = $state("");
  let applied: ResourceFilters = { type: "", status: "", owner_id: "" };
  let cursor = $state(""),
    previous = $state<string[]>([]),
    pageNumber = $state(1);
  let confirmation = $state<AdminResource | null>(null);
  let detailHeading = $state<HTMLHeadingElement>();
  let disposed = false;
  const controller = new AbortController();
  function message(cause: unknown, fallback: string) {
    if (cause instanceof AccountError && cause.code === "recent_authentication_required")
      return "Confirm your administrator credentials, then explicitly try this action again.";
    if (cause instanceof AccountError && cause.status === 401)
      return "Your session ended. Sign in again.";
    if (cause instanceof AccountError && cause.status === 403)
      return "Administrator access is required. Confirm your credentials and try again.";
    if (cause instanceof AccountError && cause.status === 409)
      return "Cleanup is available only after the resource is revoked or expires. Refresh its details.";
    return fallback;
  }
  async function refreshOverview() {
    try {
      const value = await loadCleanupOverview(controller.signal);
      if (!disposed) {
        overview = value;
        cleanupError = "";
      }
    } catch {
      if (!disposed)
        cleanupError =
          "Cleanup status could not be loaded. Previous status, if shown, may be stale.";
    }
  }
  async function list(direction: "refresh" | "next" | "previous" | "apply" = "refresh") {
    if (busy) return;
    const filters =
      direction === "apply" ? { type, status, owner_id: owner.trim().toLowerCase() } : applied;
    if (filters.owner_id && !isResourceID(filters.owner_id)) {
      error = "Enter the account ID only, without a URL or link secret.";
      return;
    }
    const after =
      direction === "next"
        ? result?.next_cursor
        : direction === "previous"
          ? previous.at(-1)
          : direction === "apply"
            ? ""
            : cursor;
    if (after == null) return;
    busy = true;
    error = "";
    try {
      const [pageResult] = await Promise.allSettled([
        loadAdminResources(filters, after, controller.signal),
        refreshOverview(),
      ]);
      if (disposed) return;
      if (pageResult.status === "rejected") throw pageResult.reason;
      const page = pageResult.value;
      previous =
        direction === "next"
          ? [...previous, cursor].slice(-200)
          : direction === "previous"
            ? previous.slice(0, -1)
            : direction === "apply"
              ? []
              : previous;
      pageNumber =
        direction === "next"
          ? pageNumber + 1
          : direction === "previous"
            ? pageNumber - 1
            : direction === "apply"
              ? 1
              : pageNumber;
      cursor = after;
      applied = filters;
      result = page;
    } catch (cause) {
      if (!disposed)
        error = message(cause, "Resources could not be loaded. Try Refresh resources.");
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function inspect(type: ResourceType, id: string, focus = true) {
    if (busy) return;
    id = id.trim().toLowerCase();
    if (!isResourceID(id)) {
      detailError = "Enter the resource ID only, without a URL or link secret.";
      return;
    }
    busy = true;
    detailError = "";
    notice = "";
    try {
      const next = await loadAdminResource(type, id, controller.signal);
      if (disposed) return;
      selected = { type, id };
      detail = next;
      absent = false;
      removed = false;
      auditVersion++;
    } catch (cause) {
      if (disposed) return;
      if (cause instanceof AccountError && cause.status === 404) {
        selected = { type, id };
        detail = null;
        absent = true;
        removed = false;
        auditVersion++;
      } else
        detailError = message(
          cause,
          "Resource details could not be loaded. Previously loaded details may be stale.",
        );
    } finally {
      if (!disposed) {
        busy = false;
        if (focus) {
          await tick();
          detailHeading?.focus();
        }
      }
    }
  }
  async function mutate(action: "revoke" | "cleanup") {
    const target = action === "revoke" ? confirmation : detail;
    if (!target || busy) return;
    busy = true;
    detailError = "";
    notice = "";
    try {
      const response = await mutateAdminResource(target.type, target.id, action, controller.signal);
      if (disposed) return;
      confirmation = null;
      selected = { type: target.type, id: target.id };
      auditVersion++;
      if (response.state === "removed") {
        detail = null;
        absent = true;
        removed = true;
        if (result)
          result = {
            ...result,
            resources: result.resources.filter(
              (item) => item.type !== target.type || item.id !== target.id,
            ),
          };
        notice = "Resource removed. Server cleanup is complete. Existing downloaded copies remain.";
      } else {
        detail = {
          ...target,
          status: action === "revoke" ? "revoked" : target.status,
          cleanup: response.cleanup,
        };
        absent = false;
        removed = false;
        notice =
          response.state === "complete"
            ? "Server cleanup is complete. Resource metadata remains available."
            : action === "revoke"
              ? "The link is revoked. Server cleanup is still pending."
              : "Cleanup queued. Refresh details to confirm its progress.";
        if (result)
          result = {
            ...result,
            resources: result.resources.map((item) =>
              item.type === target.type && item.id === target.id ? detail! : item,
            ),
          };
        try {
          const latest = await loadAdminResource(target.type, target.id, controller.signal);
          if (!disposed) {
            detail = latest;
            if (result)
              result = {
                ...result,
                resources: result.resources.map((item) =>
                  item.type === latest.type && item.id === latest.id ? latest : item,
                ),
              };
          }
        } catch (cause) {
          if (!disposed) {
            if (cause instanceof AccountError && cause.status === 404) {
              detail = null;
              absent = true;
              notice =
                action === "revoke"
                  ? "The link was revoked and the resource is no longer present."
                  : "The resource is no longer present.";
            } else
              detailError =
                "Could not refresh cleanup progress. The last known cleanup state remains below.";
          }
        }
      }
      await refreshOverview();
    } catch (cause) {
      if (!disposed)
        detailError = message(
          cause,
          cause instanceof AccountError && cause.status === 404
            ? "The resource was not found. Refresh its details to check whether it has already been removed."
            : "The action could not be confirmed. Refresh resource details before retrying; the action may already have taken effect.",
        );
    } finally {
      if (!disposed) busy = false;
    }
  }
  onMount(() => {
    void list();
    return () => {
      disposed = true;
      controller.abort();
    };
  });
</script>

<a href="/?view=overview">Back to Overview</a>
<div class="heading">
  <h1>Resources</h1>
  <button disabled={busy} onclick={() => list()}>Refresh resources</button>
</div>
<p class="muted">
  Inspect encrypted transfers and receive links, revoke access, and check server cleanup. File
  names, contents and encryption keys are unavailable.
</p>
<section aria-label="Cleanup overview" class="cleanup-overview">
  <h2>Cleanup</h2>
  {#if cleanupError}<p class="error" role="alert">{cleanupError}</p>{/if}
  {#if overview}
    <p>
      {overview.pending_count} pending · {overview.failed_count} failed · {overview.busy_count} waiting
      for file operations or received transfers
    </p>
    {#if overview.oldest_pending_at}<p class="muted small">
        Oldest pending: {utcTime(overview.oldest_pending_at)}
      </p>{/if}
    <p class="muted small">
      {overview.discovery_pending
        ? "Expired-resource discovery is in progress; pending counts may increase."
        : "The last discovery pass completed."}{overview.last_discovery_at
        ? ` Last scan: ${utcTime(overview.last_discovery_at)}.`
        : " No completed scan recorded yet."} This is database cleanup status, not a physical disk inventory.
    </p>
  {:else if !cleanupError}<p role="status">Loading cleanup status…</p>{/if}
</section>
<form
  class="filters"
  onsubmit={(event) => {
    event.preventDefault();
    void list("apply");
  }}
>
  <label
    >Resource type<select bind:value={type} disabled={busy}
      ><option value="">All types</option><option value="transfer">Transfers</option><option
        value="slot">Receive links</option
      ></select
    ></label
  >
  <label
    >Status<select bind:value={status} disabled={busy}
      ><option value="">All statuses</option><option value="pending">Upload unfinished</option
      ><option value="complete">Ready to download</option><option value="waiting"
        >Receive links</option
      ><option value="revoked">Revoked</option></select
    ></label
  >
  <label
    >Owner account ID<input
      bind:value={owner}
      disabled={busy}
      autocomplete="off"
      spellcheck="false"
      placeholder="Optional account ID"
    /></label
  >
  <button disabled={busy} type="submit">Apply filters</button>
</form>
{#if error}<p class="error" role="alert">
    {error}
    {result ? "The previous resource page remains below and may be stale." : ""}
  </p>{/if}
{#if result}<nav aria-label="Resource pages">
    <button disabled={busy || !previous.length} onclick={() => list("previous")}
      >Newer resources</button
    ><span class="muted small" role="status"
      >Page {pageNumber} · {result.resources.length} resources</span
    ><button disabled={busy || !result.next_cursor} onclick={() => list("next")}
      >Older resources</button
    >
  </nav>
  {#if !result.resources.length}<p>No resources found.</p>{/if}
  {#each result.resources as item (`${item.type}:${item.id}`)}
    <article class="resource" data-resource-id={item.id}>
      <div>
        <strong>{resourceLabel(item)}</strong>
        <p>
          {resourceStatus(item)} · {item.file_count} files · {formatSize(
            item.occupied_bytes_estimate,
          )} estimated occupied
        </p>
        <p>
          Owner: {item.owner_username ?? item.owner_id ?? "Unknown account"}{item.owner_disabled
            ? " (sign-in disabled)"
            : ""}
        </p>
        <p class="muted small">
          Created {utcTime(item.created_at)} · Expires {utcTime(item.expires_at)}
        </p>
        <code>{item.id}</code>{#if item.cleanup.state !== "none"}<p
            class:error={item.cleanup.state === "failed"}
          >
            {cleanupLabel(item.cleanup)}
          </p>{/if}
      </div>
      <button disabled={busy} onclick={() => inspect(item.type, item.id)}>Inspect</button>
    </article>
  {/each}
{:else if !error}<p role="status">Loading resources…</p>{/if}
<section class="lookup">
  <h2>Find a resource</h2>
  <p class="muted small">
    Use the resource ID from a report or security event. Enter only the ID; do not paste a full
    share link or its secret.
  </p>
  <form
    class="filters"
    onsubmit={(event) => {
      event.preventDefault();
      void inspect(lookupType, lookupID);
    }}
  >
    <label
      >Lookup type<select bind:value={lookupType} disabled={busy}
        ><option value="transfer">Transfer</option><option value="slot">Receive link</option
        ></select
      ></label
    ><label
      >Resource ID<input
        bind:value={lookupID}
        disabled={busy}
        autocomplete="off"
        spellcheck="false"
      /></label
    ><button type="submit" disabled={busy}>Find resource</button>
  </form>
</section>
{#if detailError && !confirmation}<p class="error" role="alert">{detailError}</p>{/if}
{#if notice}<p class="notice" role="status">{notice}</p>{/if}
{#if selected}
  <section class="resource-detail" aria-label="Resource details">
    <div class="heading">
      <h2 bind:this={detailHeading} tabindex="-1">Resource details</h2>
      <button disabled={busy} onclick={() => selected && inspect(selected.type, selected.id, false)}
        >Refresh details</button
      >
    </div>
    <code>{selected.id}</code>
    {#if absent}<p>
        {removed
          ? "This resource has been removed."
          : "Resource not found. It may have expired or already been removed. This is different from a server connection failure."}
        Retained security activity may still be available below.
      </p>{/if}
    {#if detail}
      <h3>{resourceLabel(detail)} · {resourceStatus(detail)}</h3>
      <dl>
        <div>
          <dt>Owner</dt>
          <dd>
            {detail.owner_username ?? "Unknown account"}{detail.owner_disabled
              ? " (sign-in disabled)"
              : ""}<code>{detail.owner_id ?? "No owner recorded"}</code>
          </dd>
        </div>
        <div>
          <dt>Files</dt>
          <dd>{detail.file_count}</dd>
        </div>
        <div>
          <dt>Received transfers</dt>
          <dd>{detail.child_transfer_count}</dd>
        </div>
        <div>
          <dt>Created</dt>
          <dd>{utcTime(detail.created_at)}</dd>
        </div>
        <div>
          <dt>Expires</dt>
          <dd>{utcTime(detail.expires_at)}</dd>
        </div>
        {#if detail.pending_expires_at}<div>
            <dt>Unfinished upload expires</dt>
            <dd>{utcTime(detail.pending_expires_at)}</dd>
          </div>{/if}
        <div>
          <dt>Reserved storage</dt>
          <dd>{formatSize(detail.reserved_bytes)}</dd>
        </div>
        <div>
          <dt>Estimated occupied storage</dt>
          <dd>{formatSize(detail.occupied_bytes_estimate)}</dd>
        </div>
        <div>
          <dt>Encrypted manifest portion</dt>
          <dd>{formatSize(detail.manifest_bytes)}</dd>
        </div>
        {#if detail.parent_slot_id}<div>
            <dt>Parent receive link</dt>
            <dd>
              <button
                disabled={busy}
                onclick={() => detail?.parent_slot_id && inspect("slot", detail.parent_slot_id)}
                >{detail.parent_slot_id}</button
              >
            </dd>
          </div>{/if}
      </dl>
      <p class="muted small">
        Storage totals include encrypted manifests. Occupied bytes are an estimate; reserved storage
        may remain charged until cleanup succeeds.
      </p>
      <h3>{cleanupLabel(detail.cleanup)}</h3>
      {#if cleanupReason(detail.cleanup)}<p>{cleanupReason(detail.cleanup)}</p>{/if}
      {#if cleanupFailure(detail.cleanup)}<p class="error" role="alert">
          {cleanupFailure(detail.cleanup)}{detail.cleanup.last_failure_at
            ? ` Last failure: ${utcTime(detail.cleanup.last_failure_at)}.`
            : ""}
        </p>{/if}
      {#if detail.cleanup.state !== "none"}<p>
          {detail.cleanup.attempt_count} cleanup attempts{detail.cleanup.pending_since
            ? ` · Pending since ${utcTime(detail.cleanup.pending_since)}`
            : ""}
        </p>
        <p class="muted small">
          {detail.cleanup.last_attempt_at
            ? `Last attempt: ${utcTime(detail.cleanup.last_attempt_at)}.`
            : "No cleanup attempt recorded yet."}
          {detail.cleanup.next_retry_at
            ? `Next scheduled retry: ${utcTime(detail.cleanup.next_retry_at)}.`
            : ""}
        </p>{/if}
      <div class="actions">
        {#if detail.status !== "revoked"}<button
            class="danger"
            disabled={busy}
            onclick={() => {
              confirmation = detail;
              detailError = "";
            }}>Revoke</button
          >{/if}
        {#if detail.status === "revoked" || detail.cleanup.state !== "none" || Date.parse(detail.expires_at) <= Date.now()}<button
            disabled={busy}
            onclick={() => mutate("cleanup")}>Retry cleanup</button
          >{/if}
      </div>
    {/if}
    {#key `${selected.type}:${selected.id}:${auditVersion}`}<SecurityEvents
        resource={selected}
      />{/key}
  </section>
{/if}
{#if confirmation}<AdminResourceRevoke
    resource={confirmation}
    {busy}
    error={detailError}
    oncancel={() => (confirmation = null)}
    onconfirm={() => mutate("revoke")}
  />{/if}

<style>
  .heading,
  nav,
  .actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.75rem;
  }
  .actions {
    justify-content: flex-start;
  }
  .cleanup-overview {
    padding: 1rem;
    margin: 1.2rem 0;
    background: var(--elevated);
    border-radius: 12px;
  }
  .filters {
    display: flex;
    align-items: end;
    flex-wrap: wrap;
    gap: 0.8rem;
    margin: 1rem 0;
  }
  label {
    flex: 1 1 10rem;
    min-width: 0;
  }
  input,
  select {
    width: 100%;
  }
  .resource {
    display: flex;
    justify-content: space-between;
    align-items: start;
    gap: 1rem;
    padding: 1.2rem 0;
    border-bottom: 1px solid var(--divider);
  }
  .resource > div {
    min-width: 0;
  }
  .resource button {
    flex-shrink: 0;
  }
  .resource p {
    margin: 0.45rem 0;
  }
  code,
  dd,
  p {
    overflow-wrap: anywhere;
  }
  code {
    display: block;
    font-size: 0.82rem;
  }
  .lookup,
  .resource-detail {
    margin-top: 2rem;
    border-top: 1px solid var(--divider);
    padding-top: 1rem;
  }
  dl {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 1rem;
  }
  dt {
    color: var(--muted);
    font-size: 0.85rem;
    margin-bottom: 0.3rem;
  }
  dd {
    margin: 0;
  }
  dd button {
    max-width: 100%;
    overflow-wrap: anywhere;
  }
  @media (max-width: 480px) {
    dl {
      grid-template-columns: minmax(0, 1fr);
    }
    .resource {
      flex-direction: column;
    }
  }
</style>
