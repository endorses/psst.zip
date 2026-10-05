<script lang="ts">
  import { message as m, t, number, LocalizedError, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { utcTime } from "$lib/admin";
  import Icon from "./Icon.svelte";
  import { resourcePath, type ResourceType } from "$lib/admin-resources";
  let { resource }: { resource?: { type: ResourceType; id: string } } = $props();
  const endpoint = $derived(
    resource ? `${resourcePath(resource.type, resource.id)}/events` : "/admin/security-events",
  );

  interface SecurityEvent {
    id: number;
    occurred_at: string;
    kind: string;
    origin: string;
    actor_id?: string;
    target_type: string;
    target_id?: string;
    outcome: string;
    count: number;
  }
  interface EventPage {
    events: SecurityEvent[];
    next_before?: number | null;
    retention_days: number;
    max_events: number;
    degraded: boolean;
  }
  const actions = new Map([
    ["settings.file_size_changed", m("fileSizeLimitChanged")],
    ["settings.abuse_contact_changed", m("abuseContactChanged")],
    ["settings.resource_policy_changed", m("resourceLimitsChanged")],
    ["settings.traffic_policy_changed", m("trafficPolicyChanged")],
    ["settings.traffic_chart_changed", m("trafficMonitorSettingsChanged")],
    ["settings.account_traffic_changed", m("accountTrafficLimitChanged")],
    ["transfers.paused", m("publicTransfersPaused")],
    ["transfers.resumed", m("publicTransfersResumed")],
    ["account.created", m("accountCreated")],
    ["account.enabled", m("accountEnabled")],
    ["account.disabled", m("accountSignInDisabled")],
    ["account.shutdown", m("accountShutDown")],
    ["account.password_changed", m("passwordChanged")],
    ["account.password_reset", m("passwordReset")],
    ["session.revoked", m("sessionRevoked")],
    ["pairing.created", m("loginQRCodeCreated")],
    ["pairing.redeemed", m("deviceConnected")],
    ["pairing.revoked", m("loginQRCodeRevoked")],
    ["administrator.signed_in", m("administratorSignedIn")],
    ["administrator.reauthenticated", m("administratorIdentityVerified")],
    ["administrator.factor_enabled", m("authenticatorEnabled")],
    ["administrator.factor_disabled", m("authenticatorDisabled")],
    ["administrator.factor_reset", m("authenticatorReset")],
    ["administrator.recovery_rotated", m("recoveryCodesReplaced")],
    ["administrator.recovery_used", m("recoveryCodeUsed")],
    ["transfer.revoked", m("sendLinkRevoked")],
    ["slot.revoked", m("receiveLinkRevoked")],
    ["authentication.login_rejected", m("signInRejected")],
    ["authentication.pairing_rejected", m("deviceConnectionRejected")],
    ["authentication.factor_rejected", m("administratorVerificationRejected")],
    ["authentication.throttled", m("authenticationRateLimited")],
  ]);
  const origins = new Map([
    ["administrator", m("administrator")],
    ["account", m("account")],
    ["capability", m("publicLink")],
    ["local", m("localOperator")],
    ["system", m("system")],
  ]);
  const outcomes = new Map([
    ["succeeded", m("succeeded")],
    ["rejected", m("rejected")],
  ]);
  const targets = new Map([
    ["user", m("account")],
    ["session", m("session")],
    ["transfer", m("transfer")],
    ["slot", m("receiveLink")],
    ["server", m("server")],
    ["pairing", m("loginQRCode")],
    ["authentication", m("authentication")],
  ]);
  let result = $state<EventPage | null>(null);
  let busy = $state(false);
  let error = $state<DisplayText>("");
  let before = $state<number | undefined>();
  let previous = $state<(number | undefined)[]>([]);
  let disposed = false;
  let controller: AbortController | undefined;

  function validPage(value: EventPage, cursor: number | undefined): boolean {
    const integer = (n: unknown) => typeof n === "number" && Number.isSafeInteger(n) && n > 0;
    const text = (s: unknown) => typeof s === "string" && s.length <= 128;
    if (
      !value ||
      !Array.isArray(value.events) ||
      value.events.length > 50 ||
      value.retention_days !== 90 ||
      value.max_events !== 10000 ||
      typeof value.degraded !== "boolean"
    )
      return false;
    let last = cursor ?? Number.MAX_SAFE_INTEGER;
    for (const event of value.events) {
      if (
        !event ||
        !integer(event.id) ||
        event.id >= last ||
        !integer(event.count) ||
        !text(event.occurred_at) ||
        !Number.isFinite(Date.parse(event.occurred_at)) ||
        !text(event.kind) ||
        !text(event.origin) ||
        !text(event.target_type) ||
        !text(event.outcome) ||
        (event.actor_id !== undefined && !text(event.actor_id)) ||
        (event.target_id !== undefined && !text(event.target_id))
      )
        return false;
      last = event.id;
    }
    return (
      value.next_before == null ||
      (integer(value.next_before) && value.events.length > 0 && value.next_before <= last)
    );
  }

  async function load(direction: "refresh" | "older" | "newer" = "refresh") {
    if (busy) return;
    const cursor =
      direction === "older"
        ? (result?.next_before ?? undefined)
        : direction === "newer"
          ? previous.at(-1)
          : undefined;
    if (direction === "older" && (cursor === undefined || previous.length >= 199)) return;
    if (direction === "newer" && previous.length === 0) return;
    busy = true;
    error = "";
    controller = new AbortController();
    try {
      const next = await accountRequest<EventPage>(
        `${endpoint}?limit=50${cursor === undefined ? "" : `&before=${cursor}`}`,
        "GET",
        undefined,
        controller.signal,
      );
      if (disposed) return;
      if (!validPage(next, cursor)) throw new LocalizedError(m("invalidActivityResponse"));
      previous =
        direction === "older"
          ? [...previous, before]
          : direction === "newer"
            ? previous.slice(0, -1)
            : [];
      before = cursor;
      result = next;
    } catch {
      if (!disposed) error = m("securityActivityCouldNotBeLoadedTryAgain");
    } finally {
      if (!disposed) busy = false;
    }
  }
  onMount(() => {
    void load();
    return () => {
      disposed = true;
      controller?.abort();
    };
  });
</script>

{#if resource}<h2>{$t(m("relatedSecurityActivity"))}</h2>{:else}<h1>
    {$t(m("securityActivity"))}
  </h1>{/if}
<p class="muted">
  {$t(
    resource
      ? m("activityForThisResourceIncludingRelevantOwnerAndReceive")
      : m("recentAccountAndAdministrationEventsOnThisServer"),
  )}
</p>
<p class="muted small">{$t(m("retainedForUpToDaysAndEntriesWithSeparate"))}</p>
<p class="muted small">{$t(m("recordsAccountIDsActionsAndOutcomesWithoutPasswordsLogin"))}</p>
<div class="activity-controls">
  <button onclick={() => load()} disabled={busy}
    ><Icon name="Refresh" /> {$t(m("latestActivity"))}</button
  >
  <nav aria-label={$t(m("securityActivityPages"))}>
    <button onclick={() => load("newer")} disabled={busy || !previous.length}
      >{$t(m("newer"))}</button
    >
    <button
      onclick={() => load("older")}
      disabled={busy || !result?.next_before || previous.length >= 199}>{$t(m("older"))}</button
    >
  </nav>
</div>
{#if error}<p class="error" role="alert">
    {$t(error)}
    {$t(result ? m("thePreviousActivityRemainsBelowAndMayBeStale") : "")}
  </p>{/if}
{#if result?.degraded}<p class="error" role="alert">
    {$t(m("securityRecordingIsDegradedSomeEventsMayBeMissing"))}
  </p>{/if}
<p class="muted small" role="status">
  {$t(
    busy
      ? m("loadingSecurityActivity")
      : result
        ? m("activityPageCount", { page: previous.length + 1, count: result.events.length })
        : m("securityActivityUnavailable"),
  )}
</p>
{#if result}
  {#if !result.events.length}<p>{$t(m("noSecurityActivityInThisRetainedPage"))}</p>
  {:else}
    <ol class="activity-list" aria-label={$t(m("securityEvents"))} aria-busy={busy}>
      {#each result.events as event (event.id)}
        <li>
          <div class="activity-heading">
            <strong>{$t(actions.get(event.kind) ?? m("securityEvent"))}</strong><span
              class="outcome">{$t(outcomes.get(event.outcome) ?? m("unknownOutcome"))}</span
            >
          </div>
          <time datetime={event.occurred_at} class="muted small"
            >{$t(utcTime(event.occurred_at))}</time
          >
          <dl>
            <div>
              <dt>{$t(m("source"))}</dt>
              <dd>{$t(origins.get(event.origin) ?? m("otherSource"))}</dd>
            </div>
            <div>
              <dt>{$t(m("occurrences"))}</dt>
              <dd>{$t(number(event.count))}</dd>
            </div>
            <div>
              <dt>{$t(m("actor"))}</dt>
              <dd>
                {#if event.actor_id}<code>{$t(event.actor_id)}</code>{:else}{$t(
                    m("notRecorded"),
                  )}{/if}
              </dd>
            </div>
            <div>
              <dt>{$t(m("target"))}</dt>
              <dd>
                {$t(targets.get(event.target_type) ?? m("otherResource"))}{#if event.target_id}<code
                    >{$t(event.target_id)}</code
                  >{/if}
              </dd>
            </div>
          </dl>
        </li>
      {/each}
    </ol>
  {/if}
{/if}

<style>
  .activity-controls,
  .activity-controls nav,
  .activity-heading {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .activity-controls {
    justify-content: space-between;
    margin: 1.5rem 0;
  }
  .activity-list {
    list-style: none;
    padding: 0;
    margin: 0;
  }
  li {
    padding: 1.2rem 0;
    border-top: 1px solid var(--divider);
  }
  .activity-heading {
    justify-content: space-between;
    margin-bottom: 0.4rem;
  }
  .outcome {
    color: var(--muted);
    font-size: 0.9rem;
  }
  dl {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.8rem 1.5rem;
    margin: 0.8rem 0 0;
  }
  dt {
    font-size: 0.8rem;
    color: var(--muted);
    margin-bottom: 0.2rem;
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  code {
    display: block;
    font-size: 0.85rem;
    overflow-wrap: anywhere;
  }
  @media (max-width: 480px) {
    dl {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
