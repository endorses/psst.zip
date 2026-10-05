<script lang="ts">
  import { message as m, t, type DisplayText } from "$lib/i18n";

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
  import StorageChecks from "./StorageChecks.svelte";
  import CounterChecks from "./CounterChecks.svelte";
  import OrphanChecks from "./OrphanChecks.svelte";
  import AdminResourceRevoke from "./AdminResourceRevoke.svelte";
  let result = $state<AdminResourcePage | null>(null),
    overview = $state<CleanupOverview | null>(null);
  let detail = $state<AdminResource | null>(null),
    selected = $state<{ type: ResourceType; id: string } | null>(null);
  let absent = $state(false),
    removed = $state(false),
    auditVersion = $state(0);
  let busy = $state(false),
    error = $state<DisplayText>(""),
    detailError = $state<DisplayText>(""),
    cleanupError = $state<DisplayText>(""),
    notice = $state<DisplayText>("");
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
  function message(cause: unknown, fallback: DisplayText) {
    if (cause instanceof AccountError && cause.code === "recent_authentication_required")
      return m("confirmYourAdministratorCredentialsThenExplicitlyTryThisAction");
    if (cause instanceof AccountError && cause.status === 401)
      return m("yourSessionEndedSignInAgain");
    if (cause instanceof AccountError && cause.status === 403)
      return m("administratorAccessIsRequiredConfirmYourCredentialsAndTry");
    if (cause instanceof AccountError && cause.status === 409)
      return m("cleanupIsAvailableOnlyAfterTheResourceIsRevoked");
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
      if (!disposed) cleanupError = m("cleanupStatusCouldNotBeLoadedPreviousStatusIf");
    }
  }
  async function list(direction: "refresh" | "next" | "previous" | "apply" = "refresh") {
    if (busy) return;
    const filters =
      direction === "apply" ? { type, status, owner_id: owner.trim().toLowerCase() } : applied;
    if (filters.owner_id && !isResourceID(filters.owner_id)) {
      error = m("enterTheAccountIDOnlyWithoutAURLOr");
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
      if (!disposed) error = message(cause, m("resourcesCouldNotBeLoadedTryRefreshResources"));
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function inspect(type: ResourceType, id: string, focus = true) {
    if (busy) return;
    id = id.trim().toLowerCase();
    if (!isResourceID(id)) {
      detailError = m("enterTheResourceIDOnlyWithoutAURLOr");
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
        detailError = message(cause, m("resourceDetailsCouldNotBeLoadedPreviouslyLoadedDetails"));
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
        notice = m("resourceRemovedServerCleanupIsCompleteExistingDownloadedCopies");
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
            ? m("serverCleanupIsCompleteResourceMetadataRemainsAvailable")
            : action === "revoke"
              ? m("theLinkIsRevokedServerCleanupIsStillPending")
              : m("cleanupQueuedRefreshDetailsToConfirmItsProgress");
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
                  ? m("theLinkWasRevokedAndTheResourceIsNo")
                  : m("theResourceIsNoLongerPresent");
            } else detailError = m("couldNotRefreshCleanupProgressTheLastKnownCleanup");
          }
        }
      }
      await refreshOverview();
    } catch (cause) {
      if (!disposed)
        detailError = message(
          cause,
          cause instanceof AccountError && cause.status === 404
            ? m("theResourceWasNotFoundRefreshItsDetailsTo")
            : m("theActionCouldNotBeConfirmedRefreshResourceDetails"),
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

<a href="/?view=overview">{$t(m("backToOverview"))}</a>
<div class="heading">
  <h1>{$t(m("resources"))}</h1>
  <button disabled={busy} onclick={() => list()}>{$t(m("refreshResources"))}</button>
</div>
<p class="muted">{$t(m("inspectEncryptedTransfersAndReceiveLinksRevokeAccessAnd"))}</p>
<section aria-label={$t(m("cleanupOverview"))} class="cleanup-overview">
  <h2>{$t(m("cleanup"))}</h2>
  {#if cleanupError}<p class="error" role="alert">{$t(cleanupError)}</p>{/if}
  {#if overview}
    <p>
      {$t(overview.pending_count)}
      {$t(m("pending"))}
      {$t(overview.failed_count)}
      {$t(m("failed"))}
      {$t(overview.busy_count)}
      {$t(m("waitingForFileOperationsOrReceivedTransfers"))}
    </p>
    {#if overview.oldest_pending_at}<p class="muted small">
        {$t(m("oldestPending"))}
        {$t(utcTime(overview.oldest_pending_at))}
      </p>{/if}
    <p class="muted small">
      {$t(
        overview.discovery_pending
          ? m("expiredResourceDiscoveryIsInProgressPendingCountsMay")
          : m("theLastDiscoveryPassCompleted"),
      )}{$t(
        overview.last_discovery_at
          ? m("lastScanValue", { arg0: utcTime(overview.last_discovery_at) })
          : m("noCompletedScanRecordedYet"),
      )}
      {$t(m("thisIsDatabaseCleanupStatusNotAPhysicalDisk"))}
    </p>
  {:else if !cleanupError}<p role="status">{$t(m("loadingCleanupStatus"))}</p>{/if}
</section>
<StorageChecks />
<CounterChecks />
<OrphanChecks />
<form
  class="filters"
  onsubmit={(event) => {
    event.preventDefault();
    void list("apply");
  }}
>
  <label
    >{$t(m("resourceType"))}<select bind:value={type} disabled={busy}
      ><option value="">{$t(m("allTypes"))}</option><option value="transfer"
        >{$t(m("transfers"))}</option
      ><option value="slot">{$t(m("receiveLinks"))}</option></select
    ></label
  >
  <label
    >{$t(m("status"))}<select bind:value={status} disabled={busy}
      ><option value="">{$t(m("allStatuses"))}</option><option value="pending"
        >{$t(m("uploadUnfinished"))}</option
      ><option value="complete">{$t(m("readyToDownload"))}</option><option value="waiting"
        >{$t(m("receiveLinks"))}</option
      ><option value="revoked">{$t(m("revoked"))}</option></select
    ></label
  >
  <label
    >{$t(m("ownerAccountID"))}<input
      bind:value={owner}
      disabled={busy}
      autocomplete="off"
      spellcheck="false"
      placeholder={$t(m("optionalAccountID"))}
    /></label
  >
  <button disabled={busy} type="submit">{$t(m("applyFilters"))}</button>
</form>
{#if error}<p class="error" role="alert">
    {$t(error)}
    {$t(result ? m("thePreviousResourcePageRemainsBelowAndMayBe") : "")}
  </p>{/if}
{#if result}<nav aria-label={$t(m("resourcePages"))}>
    <button disabled={busy || !previous.length} onclick={() => list("previous")}
      >{$t(m("newerResources"))}</button
    ><span class="muted small" role="status"
      >{$t(m("page"))}
      {$t(pageNumber)} · {$t(result.resources.length)}
      {$t(m("resources_41d31"))}</span
    ><button disabled={busy || !result.next_cursor} onclick={() => list("next")}
      >{$t(m("olderResources"))}</button
    >
  </nav>
  {#if !result.resources.length}<p>{$t(m("noResourcesFound"))}</p>{/if}
  {#each result.resources as item (`${item.type}:${item.id}`)}
    <article class="resource" data-resource-id={item.id}>
      <div>
        <strong>{$t(resourceLabel(item))}</strong>
        <p>
          {$t(resourceStatus(item))}
          {#if item.totals_available}
            · {$t(item.file_count)}
            {$t(m("files_b5c9c"))}
            {$t(formatSize(item.occupied_bytes_estimate))}
            {$t(m("estimatedOccupied"))}
          {/if}
        </p>
        {#if !item.totals_available}<p class="muted">{$t(m("storageTotalsAwaitingRepair"))}</p>{/if}
        <p>
          {$t(m("owner_9a638"))}
          {$t(item.owner_username ?? item.owner_id ?? m("unknownAccount"))}{$t(
            item.owner_disabled ? m("signInDisabled") : "",
          )}
        </p>
        <p class="muted small">
          {$t(m("created"))}
          {$t(utcTime(item.created_at))}
          {$t(m("expires_f8716"))}
          {$t(utcTime(item.expires_at))}
        </p>
        <code>{$t(item.id)}</code>{#if item.cleanup.state !== "none"}<p
            class:error={item.cleanup.state === "failed"}
          >
            {$t(cleanupLabel(item.cleanup))}
          </p>{/if}
      </div>
      <button disabled={busy} onclick={() => inspect(item.type, item.id)}>{$t(m("inspect"))}</button
      >
    </article>
  {/each}
{:else if !error}<p role="status">{$t(m("loadingResources"))}</p>{/if}
<section class="lookup">
  <h2>{$t(m("findAResource"))}</h2>
  <p class="muted small">{$t(m("useTheResourceIDFromAReportOrSecurity"))}</p>
  <form
    class="filters"
    onsubmit={(event) => {
      event.preventDefault();
      void inspect(lookupType, lookupID);
    }}
  >
    <label
      >{$t(m("lookupType"))}<select bind:value={lookupType} disabled={busy}
        ><option value="transfer">{$t(m("transfer"))}</option><option value="slot"
          >{$t(m("receiveLink"))}</option
        ></select
      ></label
    ><label
      >{$t(m("resourceID"))}<input
        bind:value={lookupID}
        disabled={busy}
        autocomplete="off"
        spellcheck="false"
      /></label
    ><button type="submit" disabled={busy}>{$t(m("findResource"))}</button>
  </form>
</section>
{#if detailError && !confirmation}<p class="error" role="alert">{$t(detailError)}</p>{/if}
{#if notice}<p class="notice" role="status">{$t(notice)}</p>{/if}
{#if selected}
  <section class="resource-detail" aria-label={$t(m("resourceDetails"))}>
    <div class="heading">
      <h2 bind:this={detailHeading} tabindex="-1">{$t(m("resourceDetails"))}</h2>
      <button disabled={busy} onclick={() => selected && inspect(selected.type, selected.id, false)}
        >{$t(m("refreshDetails"))}</button
      >
    </div>
    <code>{$t(selected.id)}</code>
    {#if absent}<p>
        {$t(
          removed
            ? m("thisResourceHasBeenRemoved")
            : m("resourceNotFoundItMayHaveExpiredOrAlready"),
        )}
        {$t(m("retainedSecurityActivityMayStillBeAvailableBelow"))}
      </p>{/if}
    {#if detail}
      <h3>{$t(resourceLabel(detail))} · {$t(resourceStatus(detail))}</h3>
      {#if !detail.totals_available}<p class="muted">{$t(m("storageTotalsAwaitingRepair"))}</p>{/if}
      <dl>
        <div>
          <dt>{$t(m("owner"))}</dt>
          <dd>
            {$t(detail.owner_username ?? m("unknownAccount"))}{$t(
              detail.owner_disabled ? m("signInDisabled") : "",
            )}<code>{$t(detail.owner_id ?? m("noOwnerRecorded"))}</code>
          </dd>
        </div>
        {#if detail.totals_available}<div>
            <dt>{$t(m("files"))}</dt>
            <dd>{$t(detail.file_count)}</dd>
          </div>
          <div>
            <dt>{$t(m("receivedTransfers"))}</dt>
            <dd>{$t(detail.child_transfer_count)}</dd>
          </div>{/if}
        <div>
          <dt>{$t(m("created"))}</dt>
          <dd>{$t(utcTime(detail.created_at))}</dd>
        </div>
        <div>
          <dt>{$t(m("expires"))}</dt>
          <dd>{$t(utcTime(detail.expires_at))}</dd>
        </div>
        {#if detail.pending_expires_at}<div>
            <dt>{$t(m("unfinishedUploadExpires"))}</dt>
            <dd>{$t(utcTime(detail.pending_expires_at))}</dd>
          </div>{/if}
        {#if detail.totals_available}<div>
            <dt>{$t(m("reservedStorage"))}</dt>
            <dd>{$t(formatSize(detail.reserved_bytes))}</dd>
          </div>
          <div>
            <dt>{$t(m("estimatedOccupiedStorage"))}</dt>
            <dd>{$t(formatSize(detail.occupied_bytes_estimate))}</dd>
          </div>
          <div>
            <dt>{$t(m("encryptedManifestPortion"))}</dt>
            <dd>{$t(formatSize(detail.manifest_bytes))}</dd>
          </div>{/if}
        {#if detail.parent_slot_id}<div>
            <dt>{$t(m("parentReceiveLink"))}</dt>
            <dd>
              <button
                disabled={busy}
                onclick={() => detail?.parent_slot_id && inspect("slot", detail.parent_slot_id)}
                >{$t(detail.parent_slot_id)}</button
              >
            </dd>
          </div>{/if}
      </dl>
      <p class="muted small">{$t(m("storageTotalsIncludeEncryptedManifestsOccupiedBytesAreAn"))}</p>
      <h3>{$t(cleanupLabel(detail.cleanup))}</h3>
      {#if cleanupReason(detail.cleanup)}<p>{$t(cleanupReason(detail.cleanup))}</p>{/if}
      {#if cleanupFailure(detail.cleanup)}<p class="error" role="alert">
          {$t(cleanupFailure(detail.cleanup))}{$t(
            detail.cleanup.last_failure_at
              ? m("lastFailureValue", { arg0: utcTime(detail.cleanup.last_failure_at) })
              : "",
          )}
        </p>{/if}
      {#if detail.cleanup.state !== "none"}<p>
          {$t(detail.cleanup.attempt_count)}
          {$t(m("cleanupAttempts"))}{$t(
            detail.cleanup.pending_since
              ? m("pendingSinceValue", { arg0: utcTime(detail.cleanup.pending_since) })
              : "",
          )}
        </p>
        <p class="muted small">
          {$t(
            detail.cleanup.last_attempt_at
              ? m("lastAttemptValue", { arg0: utcTime(detail.cleanup.last_attempt_at) })
              : m("noCleanupAttemptRecordedYet"),
          )}
          {$t(
            detail.cleanup.next_retry_at
              ? m("nextScheduledRetryValue", { arg0: utcTime(detail.cleanup.next_retry_at) })
              : "",
          )}
        </p>{/if}
      <div class="actions">
        {#if detail.status !== "revoked"}<button
            class="danger"
            disabled={busy}
            onclick={() => {
              confirmation = detail;
              detailError = "";
            }}>{$t(m("revoke"))}</button
          >{/if}
        {#if detail.status === "revoked" || detail.cleanup.state !== "none" || Date.parse(detail.expires_at) <= Date.now()}<button
            disabled={busy}
            onclick={() => mutate("cleanup")}>{$t(m("retryCleanup"))}</button
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
