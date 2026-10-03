<script lang="ts">
  import { page } from "$app/stores";
  import { onMount } from "svelte";
  import { importKey } from "$lib/crypto";
  import { getSlotInfo } from "$lib/api";
  import SendPanel from "$lib/components/SendPanel.svelte";
  import { BRAND } from "$lib/brand";
  let reconnect = $state(false);
  let ready = $state(false),
    error = $state(""),
    key = $state("");
  async function load() {
    error = "";
    ready = false;
    reconnect = false;
    key = location.hash.slice(1);
    if (!key) {
      error =
        "This link is incomplete. Ask the sender to copy the full link, including the part after #.";
      return;
    }
    try {
      await importKey(key);
    } catch {
      error =
        "This link has an invalid encryption key. Ask the sender to copy the full link again.";
      return;
    }
    try {
      await getSlotInfo($page.params.slotId ?? "");
      ready = true;
    } catch (e) {
      reconnect = !(e instanceof Error && (e.message.includes("404") || e.message.includes("410")));
      error =
        e instanceof Error && (e.message.includes("404") || e.message.includes("410"))
          ? "This receive link has expired or was revoked. Ask for a new link."
          : "Could not connect. Check your connection and retry.";
    }
  }
  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>Send files · {BRAND}</title></svelte:head>
<section class="panel">
  {#if error}<h1>Cannot open receive link</h1>
    <p role="alert" class="error">{error}</p>
    {#if reconnect}<button onclick={load}>Reconnect</button>{/if}{:else if ready}<SendPanel
      slotId={$page.params.slotId}
      keyString={key}
    />{:else}<p role="status">Opening receive link…</p>{/if}
</section>
