<script lang="ts">
  import { BRAND } from "$lib/brand";
  import { message as m, t } from "$lib/i18n";
</script>

<svelte:head><title>{$t(m("lanSharing"))} · {$t(BRAND)}</title></svelte:head>
<h1>{$t(m("lanSharing"))}</h1>
<p>{$t(m("lanOpenSharedLink"))}</p>
