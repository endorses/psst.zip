<script lang="ts">
  import { message as m, t, date, number, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { validateResourceSnapshot, type ResourceSnapshot } from "$lib/resource-policy";
  import ResourceUsage from "./ResourceUsage.svelte";
  let snapshot = $state<ResourceSnapshot | null>(null),
    busy = $state(false),
    error = $state<DisplayText>(""),
    updated = $state<number | null>(null);
  let disposed = false;
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    try {
      const next = validateResourceSnapshot(await accountRequest("/auth/usage"));
      if (!disposed) {
        snapshot = next;
        updated = Date.now();
      }
    } catch {
      if (!disposed) error = m("couldNotRefreshAccountUsagePreviousMeasurementsIfShown");
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

<details class="account-usage">
  <summary>{$t(m("accountStorageAndResourceUsage"))}</summary>
  {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}
  {#if snapshot}<p class="muted small">
      {$t(m("lastUpdated"))}
      {$t(updated ? date(updated) : "")}{$t(m("refreshAfterCreatingOrDeletingResources"))}
    </p>
    <ResourceUsage {snapshot} />{/if}
  <button disabled={busy} onclick={load}
    >{$t(busy ? m("loadingAccountUsage") : m("refreshAccountUsage"))}</button
  >
</details>

<style>
  .account-usage {
    margin: 1.25rem 0;
  }
  summary {
    cursor: pointer;
  }
</style>
