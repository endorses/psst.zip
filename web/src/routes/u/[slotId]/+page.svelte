<script lang="ts">
  import { page } from "$app/stores";
  import ReceiveUploadPanel from "$lib/components/ReceiveUploadPanel.svelte";
</script>

{#key `${$page.params.slotId}:${$page.url.hash}`}
  <ReceiveUploadPanel slotId={$page.params.slotId ?? ""} keyString={$page.url.hash.slice(1)} />
{/key}
