<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { page } from "$app/stores";
  import { onMount } from "svelte";
  import { parseReceiveFragment } from "$lib/receive-keys";
  import { getSlotAvailability, type SlotAvailability } from "$lib/api";
  import SendPanel from "$lib/components/SendPanel.svelte";
  import { BRAND } from "$lib/brand";
  import { TransferStateError } from "$lib/incident-state";
  let reconnect = $state(false);
  let availability = $state<SlotAvailability | null>(null);
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
      parseReceiveFragment(key);
    } catch {
      error =
        "This receive link is invalid or uses an older version. Ask its owner for a new link.";
      return;
    }
    try {
      availability = await getSlotAvailability($page.params.slotId ?? "");
      if (
        availability.receive_protocol !== 2 ||
        availability.recipient_public_key !== parseReceiveFragment(key).encoded
      ) {
        error =
          "The receive link's encryption key does not match this inbox. Ask its owner for a new link.";
        return;
      }
      if (!availability.available) {
        error = "This receive link cannot accept more files. Ask its owner for a new link.";
        return;
      }
      ready = true;
    } catch (e) {
      if (e instanceof TransferStateError) {
        error = e.message;
        reconnect = false;
        return;
      }
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
    {#if reconnect}<button onclick={load}><Icon name="Refresh" size={18} />Reconnect</button
      >{/if}{:else if ready}{#if availability?.remaining_files !== null && availability?.remaining_files !== undefined}<p
        class="notice"
      >
        {availability.remaining_files} file allocations remaining. Unfinished uploads also count.
      </p>{/if}<SendPanel slotId={$page.params.slotId} keyString={key} />{:else}<p role="status">
      Opening receive link…
    </p>{/if}
</section>
