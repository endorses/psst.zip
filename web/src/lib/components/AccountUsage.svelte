<script lang="ts">
  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { validateResourceSnapshot, type ResourceSnapshot } from "$lib/resource-policy";
  import ResourceUsage from "./ResourceUsage.svelte";
  let snapshot = $state<ResourceSnapshot | null>(null),
    busy = $state(false),
    error = $state(""),
    updated = $state("");
  let disposed = false;
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    try {
      const next = validateResourceSnapshot(await accountRequest("/auth/usage"));
      if (!disposed) {
        snapshot = next;
        updated = new Date().toLocaleString();
      }
    } catch {
      if (!disposed)
        error = "Could not refresh account usage. Previous measurements, if shown, may be stale.";
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
  <summary>Account storage and resource usage</summary>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if snapshot}<p class="muted small">
      Last updated {updated}. Refresh after creating or deleting resources.
    </p>
    <ResourceUsage {snapshot} />{/if}
  <button disabled={busy} onclick={load}
    >{busy ? "Loading account usage…" : "Refresh account usage"}</button
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
