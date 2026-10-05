<script lang="ts">
  import AdminServerPanel from "./AdminServerPanel.svelte";
  import TrafficPanel from "./TrafficPanel.svelte";
  let { activePage }: { activePage: string } = $props();
  let seenServer = $state(false),
    seenTraffic = $state(false);
  $effect(() => {
    if (activePage === "Server") seenServer = true;
    if (activePage === "Traffic") seenTraffic = true;
  });
</script>

{#if seenServer || activePage === "Server"}
  <div hidden={activePage !== "Server"}><AdminServerPanel active={activePage === "Server"} /></div>
{/if}
{#if seenTraffic || activePage === "Traffic"}
  <div hidden={activePage !== "Traffic"}><TrafficPanel /></div>
{/if}
