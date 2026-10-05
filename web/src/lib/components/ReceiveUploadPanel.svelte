<script lang="ts">
  import { hasStatus } from "$lib/api-error";
  import { message as m, t, errorText, type DisplayText } from "$lib/i18n";

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
    error = $state<DisplayText>(""),
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
      error = m("thisLinkIsIncompleteAskTheSenderToCopy");
      return;
    }
    try {
      parseReceiveFragment(key);
    } catch {
      error = m("thisReceiveLinkIsInvalidOrUsesAnOlder");
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
        error = m("theReceiveLinkSEncryptionKeyDoesNotMatch");
        return;
      }
      if (!availability.available) {
        error =
          availability.remaining_files === 0
            ? m("thisLinkCannotAcceptMoreFilesItsFileAllowance")
            : availability.remaining_bytes === 0
              ? m("thisReceiveLinkSByteAllowanceIsExhaustedAsk")
              : availability.remaining_transfers === 0
                ? m("thisReceiveLinkSUploadBatchAllowanceIsExhausted")
                : m("thisReceiveLinkCannotAcceptUploadsUnderTheServer");
        return;
      }
      ready = true;
    } catch (e) {
      if (disposed || current !== generation) return;
      if (e instanceof GuestCapacityError) {
        error = m("couldNotCheckThisReceiveLinkSUploadAvailability");
        reconnect = true;
        return;
      }
      if (e instanceof TrafficLimitError || e instanceof TransferStateError) {
        error = errorText(e);
        reconnect = false;
        return;
      }
      reconnect = !hasStatus(e, 404, 410);
      error = hasStatus(e, 404, 410)
        ? m("thisReceiveLinkHasExpiredOrWasRevokedAsk")
        : m("couldNotConnectCheckYourConnectionAndRetry");
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

<svelte:head><title>{$t(availability?.title || m("sendFiles"))} · {$t(BRAND)}</title></svelte:head>
<section class="panel" aria-label={$t(m("sendToInbox"))}>
  {#if error}<h1>{$t(m("cannotOpenReceiveLink"))}</h1>
    <p role="alert" class="error">{$t(error)}</p>
    {#if reconnect}<button onclick={load}
        ><Icon name="Refresh" size={18} />{$t(m("reconnect"))}</button
      >{/if}{:else if ready}{#key `${slotId}:${key}`}<SendPanel
        {slotId}
        keyString={key}
        {onactive}
        destinationTitle={availability?.title}
      />{/key}{:else}<p role="status">{$t(m("openingReceiveLink"))}</p>{/if}
</section>
