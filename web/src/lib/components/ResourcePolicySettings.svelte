<script lang="ts">
  import { message as m, t, date, number, errorText, type DisplayText } from "$lib/i18n";

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
    error = $state<DisplayText>(""),
    notice = $state<DisplayText>(""),
    updated = $state<number | null>(null);
  let disposed = false;
  function accept(value: unknown) {
    const next = validateResourceSnapshot(value);
    if (disposed) return;
    snapshot = next;
    draft = Object.fromEntries(
      policyFields.map((field) => [field.key, next.policy[field.key] / field.factor]),
    );
    updated = Date.now();
  }
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    try {
      accept(await accountRequest("/admin/resource-policy"));
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? errorText(cause) : m("couldNotLoadResourcePolicy");
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
      if (!disposed) notice = m("resourcePolicySavedLowerQuotasBlockNewAllocationsWithout");
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? errorText(cause) : m("couldNotSaveResourcePolicy");
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
  <h2 id="resource-policy-heading">{$t(m("storageAndRetentionBudgets"))}</h2>
  <p class="muted">{$t(m("serverWideAndPerAccountLimitsApplyTogetherIncluding"))}</p>
  {#if error}<p role="alert" class="error">
      {$t(error)}
      {$t(
        snapshot
          ? m("previousUsageMayBeStaleUnsavedValuesRemainIn")
          : m("otherServerSettingsAndRecoveryActionsRemainAvailable"),
      )}
    </p>{/if}
  {#if notice}<p role="status" class="success">{$t(notice)}</p>{/if}
  <button disabled={busy} onclick={load}
    >{$t(snapshot ? m("refreshResourcePolicyAndUsage") : m("retryLoadingResourcePolicy"))}</button
  >
  {#if snapshot}
    <p class="muted small">
      {$t(m("usageLastUpdated"))}
      {$t(updated ? date(updated) : "")}{$t(m("refreshAfterAllocationsOrCleanup"))}
    </p>
    <ResourceUsage {snapshot} scope="server" />
    <form onsubmit={save}>
      {#each [{ id: "Storage", label: m("storage") }, { id: "Objects", label: m("objects") }, { id: "Retention", label: m("retention") }, { id: "disk-safety", label: m("diskSafety") }] as group}<fieldset
          disabled={busy}
        >
          <legend>{$t(group.label)}</legend>
          <div class="aligned-fields">
            {#each policyFields.filter((field) => field.group === group.id) as field}<label>
                {$t(field.label)}<input
                  type="number"
                  min={field.min}
                  max={field.max}
                  step={field.factor === 1 ? 1 : "any"}
                  required
                  bind:value={draft[field.key]}
                />
              </label>{/each}
          </div>
          {#if group.id === "Retention"}<p class="muted small">
              {$t(m("newLinksCannotOutliveMaximumRetentionIncompleteUploadsExpire"))}
            </p>{/if}
          {#if group.id === "disk-safety"}<p class="muted small">
              {$t(m("keepBothTheMinimumByteReserveAndPercentageReserve"))}
            </p>{/if}
        </fieldset>{/each}
      <button class="primary" disabled={busy}>{$t(m("saveResourcePolicy"))}</button>
    </form>
  {:else if busy}<p role="status">{$t(m("loadingResourcePolicy"))}</p>{/if}
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
  label {
    font-size: 0.9rem;
  }
  input {
    width: 100%;
  }
</style>
