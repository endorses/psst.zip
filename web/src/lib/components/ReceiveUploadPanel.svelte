<script lang="ts">
  import { TrafficLimitError } from "$lib/traffic-policy";
  import Icon from "$lib/components/Icon.svelte";
  import { onMount } from "svelte";
  import { parseReceiveFragment } from "$lib/receive-keys";
  import { getSlotAvailability, type SlotAvailability } from "$lib/api";
  import SendPanel from "$lib/components/SendPanel.svelte";
  import { TransferStateError } from "$lib/incident-state";
  import { BRAND } from "$lib/brand";
  import { GuestCapacityError } from "$lib/guest-capacity";
  let {
    slotId,
    keyString,
    onactive = () => {},
  }: {
    slotId: string;
    keyString: string;
    onactive?: (active: boolean) => void;
  } = $props();
  let disposed = false;
  let generation = 0;
  let controller: AbortController | undefined;
  let reconnect = $state(false);
  let availability = $state<SlotAvailability | null>(null);
  let ready = $state(false),
    error = $state(""),
    key = $state("");
  async function load() {
    const current = ++generation;
    controller?.abort();
    controller = new AbortController();
    error = "";
    ready = false;
    reconnect = false;
    key = keyString;
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
      const result = await getSlotAvailability(
        slotId,
        AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
      );
      if (disposed || current !== generation) return;
      availability = result;
      if (
        availability.receive_protocol !== 2 ||
        availability.recipient_public_key !== parseReceiveFragment(key).encoded
      ) {
        error =
          "The receive link's encryption key does not match this inbox. Ask its owner for a new link.";
        return;
      }
      if (!availability.available) {
        error =
          availability.remaining_files === 0
            ? "This link cannot accept more files. Its file allowance is exhausted. Ask its owner for a new link."
            : availability.remaining_bytes === 0
              ? "This receive link's byte allowance is exhausted. Ask its owner for a new link."
              : availability.remaining_transfers === 0
                ? "This receive link's upload batch allowance is exhausted. Ask its owner for a new link."
                : "This receive link cannot accept uploads under the server's current limits. Ask its owner for help.";
        return;
      }
      ready = true;
    } catch (e) {
      if (disposed || current !== generation) return;
      if (e instanceof GuestCapacityError) {
        error =
          "Could not check this receive link's upload availability. Try reconnecting in a moment.";
        reconnect = true;
        return;
      }
      if (e instanceof TrafficLimitError || e instanceof TransferStateError) {
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
    return () => {
      disposed = true;
      generation++;
      controller?.abort();
      onactive(false);
    };
  });
</script>

<svelte:head><title>{availability?.title || "Send files"} · {BRAND}</title></svelte:head>
<section class="panel" aria-label="Send to inbox">
  {#if error}<h1>Cannot open receive link</h1>
    <p role="alert" class="error">{error}</p>
    {#if reconnect}<button onclick={load}><Icon name="Refresh" size={18} />Reconnect</button
      >{/if}{:else if ready}{#key `${slotId}:${key}`}<SendPanel
        {slotId}
        keyString={key}
        {onactive}
        destinationTitle={availability?.title}
      />{/key}{:else}<p role="status">Opening receive link…</p>{/if}
</section>
