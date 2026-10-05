<script lang="ts">
  import { message as m, t } from "$lib/i18n";

  import { onMount } from "svelte";
  import { loadAbuseContact } from "$lib/abuse-contact";
  import AbuseReportDialog from "./AbuseReportDialog.svelte";
  let { origin, pathname }: { origin: string; pathname: string } = $props();
  let email = $state(""),
    open = $state(false);
  onMount(() => {
    let current: AbortController | undefined;
    async function load() {
      current?.abort();
      const controller = new AbortController();
      current = controller;
      email = "";
      open = false;
      try {
        const value = await loadAbuseContact(
          AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
        );
        if (!controller.signal.aborted) email = value;
      } catch {
        /* Optional contact must not block public file or recovery pages. */
      }
    }
    void load();
    window.addEventListener("psst:abuse-contact-changed", load);
    return () => {
      current?.abort();
      window.removeEventListener("psst:abuse-contact-changed", load);
    };
  });
</script>

{#if email}<p>
    <button class="text-button" onclick={() => (open = true)}>{$t(m("reportAbuse"))}</button>
  </p>{/if}
{#if open && email}<AbuseReportDialog
    {email}
    {origin}
    {pathname}
    onclose={() => (open = false)}
  />{/if}
