<script lang="ts">
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
    ["settings.file_size_changed", "File size limit changed"],
    ["settings.resource_policy_changed", "Resource limits changed"],
    ["settings.traffic_policy_changed", "Traffic policy changed"],
    ["settings.traffic_chart_changed", "Traffic monitor settings changed"],
    ["settings.account_traffic_changed", "Account traffic limit changed"],
    ["transfers.paused", "Public transfers paused"],
    ["transfers.resumed", "Public transfers resumed"],
    ["account.created", "Account created"],
    ["account.enabled", "Account enabled"],
    ["account.disabled", "Account sign-in disabled"],
    ["account.shutdown", "Account shut down"],
    ["account.password_changed", "Password changed"],
    ["account.password_reset", "Password reset"],
    ["session.revoked", "Session revoked"],
    ["pairing.created", "Login QR code created"],
    ["pairing.redeemed", "Device connected"],
    ["pairing.revoked", "Login QR code revoked"],
    ["administrator.signed_in", "Administrator signed in"],
    ["administrator.reauthenticated", "Administrator identity verified"],
    ["administrator.factor_enabled", "Authenticator enabled"],
    ["administrator.factor_disabled", "Authenticator disabled"],
    ["administrator.factor_reset", "Authenticator reset"],
    ["administrator.recovery_rotated", "Recovery codes replaced"],
    ["administrator.recovery_used", "Recovery code used"],
    ["transfer.revoked", "Send link revoked"],
    ["slot.revoked", "Receive link revoked"],
    ["authentication.login_rejected", "Sign-in rejected"],
    ["authentication.pairing_rejected", "Device connection rejected"],
    ["authentication.factor_rejected", "Administrator verification rejected"],
    ["authentication.throttled", "Authentication rate limited"],
  ]);
  const origins = new Map([
    ["administrator", "Administrator"],
    ["account", "Account"],
    ["capability", "Public link"],
    ["local", "Local operator"],
    ["system", "System"],
  ]);
  const outcomes = new Map([
    ["succeeded", "Succeeded"],
    ["rejected", "Rejected"],
  ]);
  const targets = new Map([
    ["user", "Account"],
    ["session", "Session"],
    ["transfer", "Transfer"],
    ["slot", "Receive link"],
    ["server", "Server"],
    ["pairing", "Login QR code"],
    ["authentication", "Authentication"],
  ]);
  let result = $state<EventPage | null>(null);
  let busy = $state(false);
  let error = $state("");
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
      if (!validPage(next, cursor)) throw new Error("Invalid activity response");
      previous =
        direction === "older"
          ? [...previous, before]
          : direction === "newer"
            ? previous.slice(0, -1)
            : [];
      before = cursor;
      result = next;
    } catch {
      if (!disposed) error = "Security activity could not be loaded. Try again.";
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

{#if resource}<h2>Related security activity</h2>{:else}<h1>Security activity</h1>{/if}
<p class="muted">
  {resource
    ? "Activity for this resource, including relevant owner and receive-link changes while its metadata remains available."
    : "Recent account and administration events on this server."}
</p>
<p class="muted small">
  Retained for up to 90 days and 10,000 entries, with separate limits for event categories. Repeated
  events may be grouped. Older activity is removed as these limits are reached.
</p>
<p class="muted small">
  Records account IDs, actions and outcomes, without passwords, login codes, link secrets, filenames
  or file contents.
</p>
<div class="activity-controls">
  <button onclick={() => load()} disabled={busy}><Icon name="Refresh" /> Latest activity</button>
  <nav aria-label="Security activity pages">
    <button onclick={() => load("newer")} disabled={busy || !previous.length}>Newer</button>
    <button
      onclick={() => load("older")}
      disabled={busy || !result?.next_before || previous.length >= 199}>Older</button
    >
  </nav>
</div>
{#if error}<p class="error" role="alert">
    {error}
    {result ? "The previous activity remains below and may be stale." : ""}
  </p>{/if}
{#if result?.degraded}<p class="error" role="alert">
    Security recording is degraded. Some events may be missing; this history is incomplete.
  </p>{/if}
<p class="muted small" role="status">
  {busy
    ? "Loading security activity…"
    : result
      ? `Page ${previous.length + 1} · ${result.events.length} ${result.events.length === 1 ? "entry" : "entries"}`
      : "Security activity unavailable."}
</p>
{#if result}
  {#if !result.events.length}<p>No security activity in this retained page.</p>
  {:else}
    <ol class="activity-list" aria-label="Security events" aria-busy={busy}>
      {#each result.events as event (event.id)}
        <li>
          <div class="activity-heading">
            <strong>{actions.get(event.kind) ?? "Security event"}</strong><span class="outcome"
              >{outcomes.get(event.outcome) ?? "Unknown outcome"}</span
            >
          </div>
          <time datetime={event.occurred_at} class="muted small">{utcTime(event.occurred_at)}</time>
          <dl>
            <div>
              <dt>Source</dt>
              <dd>{origins.get(event.origin) ?? "Other source"}</dd>
            </div>
            <div>
              <dt>Occurrences</dt>
              <dd>{event.count.toLocaleString()}</dd>
            </div>
            <div>
              <dt>Actor</dt>
              <dd>
                {#if event.actor_id}<code>{event.actor_id}</code>{:else}Not recorded{/if}
              </dd>
            </div>
            <div>
              <dt>Target</dt>
              <dd>
                {targets.get(event.target_type) ?? "Other resource"}{#if event.target_id}<code
                    >{event.target_id}</code
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
