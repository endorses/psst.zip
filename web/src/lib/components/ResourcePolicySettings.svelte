<script lang="ts">
  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import {
    policyFields,
    validateResourcePolicy,
    validateResourceSnapshot,
    type ResourcePolicy,
    type ResourceSnapshot,
  } from "$lib/resource-policy";
  import ResourceUsage from "./ResourceUsage.svelte";
  let snapshot = $state<ResourceSnapshot | null>(null),
    draft = $state<Partial<Record<keyof ResourcePolicy, number>>>({});
  let busy = $state(false),
    error = $state(""),
    notice = $state(""),
    updated = $state("");
  let disposed = false;
  function accept(value: unknown) {
    const next = validateResourceSnapshot(value);
    if (disposed) return;
    snapshot = next;
    draft = Object.fromEntries(
      policyFields.map((field) => [field.key, next.policy[field.key] / field.factor]),
    );
    updated = new Date().toLocaleString();
  }
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    try {
      accept(await accountRequest("/admin/resource-policy"));
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? cause.message : "Could not load resource policy.";
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !snapshot) return;
    error = "";
    notice = "";
    try {
      const policy = validateResourcePolicy(
        Object.fromEntries(
          policyFields.map((field) => [field.key, (draft[field.key] ?? NaN) * field.factor]),
        ),
      );
      busy = true;
      accept(await accountRequest("/admin/resource-policy", "PATCH", policy));
      if (!disposed)
        notice =
          "Resource policy saved. Lower quotas block new allocations without deleting existing data.";
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? cause.message : "Could not save resource policy.";
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

<section aria-labelledby="resource-policy-heading">
  <h2 id="resource-policy-heading">Storage and retention budgets</h2>
  <p class="muted">
    Server-wide and per-account limits apply together, including uploads through receive links.
    Lowering quotas preserves existing data and administrative access; it can stop new allocations.
  </p>
  {#if error}<p role="alert" class="error">
      {error}
      {snapshot
        ? "Previous usage may be stale. Unsaved values remain in the form."
        : "Other server settings and recovery actions remain available."}
    </p>{/if}
  {#if notice}<p role="status" class="success">{notice}</p>{/if}
  <button disabled={busy} onclick={load}
    >{snapshot ? "Refresh resource policy and usage" : "Retry loading resource policy"}</button
  >
  {#if snapshot}
    <p class="muted small">Usage last updated {updated}. Refresh after allocations or cleanup.</p>
    <ResourceUsage {snapshot} scope="server" />
    <form onsubmit={save}>
      {#each ["Storage", "Objects", "Retention", "Disk safety"] as group}<fieldset disabled={busy}>
          <legend>{group}</legend>
          <div class="fields">
            {#each policyFields.filter((field) => field.group === group) as field}<label>
                {field.label}<input
                  type="number"
                  min={field.min}
                  max={field.max}
                  step={field.factor === 1 ? 1 : "any"}
                  required
                  bind:value={draft[field.key]}
                />
              </label>{/each}
          </div>
          {#if group === "Retention"}<p class="muted small">
              New links cannot outlive maximum retention. Incomplete uploads expire sooner according
              to their separate lifetime.
            </p>{/if}
          {#if group === "Disk safety"}<p class="muted small">
              Keep both the minimum byte reserve and percentage reserve free. Other processes can
              consume disk space after admission, so uploads can still stop before filling the host.
            </p>{/if}
        </fieldset>{/each}
      <button class="primary" disabled={busy}>Save resource policy</button>
    </form>
  {:else if busy}<p role="status">Loading resource policy…</p>{/if}
</section>

<style>
  section {
    margin-top: 2rem;
    padding-top: 1.5rem;
    border-top: 1px solid var(--divider);
  }
  fieldset {
    margin: 1rem 0;
    border: 1px solid var(--divider);
    border-radius: 0.5rem;
    min-width: 0;
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
  input {
    width: 100%;
  }
</style>
