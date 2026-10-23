<script lang="ts">
  import { BRAND } from "$lib/brand";
  import { onMount } from "svelte";
  import { message as m, t } from "$lib/i18n";
  import { safeSourceURL } from "$lib/release-source";
  let source = $state("");
  let archive = $state("");
  let revision = $state("");
  onMount(() => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 5000);
    void fetch("/licenses/SOURCE.json", {
      signal: controller.signal,
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
    })
      .then(async (response) => {
        if (!response.ok) return;
        const text = await response.text();
        if (text.length > 262144) return;
        const data = JSON.parse(text);
        if (controller.signal.aborted || data?.product !== BRAND) return;
        if (typeof data.revision === "string" && /^[a-f0-9]{40}$/.test(data.revision))
          revision = data.revision;
        if (data.source_dirty !== false) return;
        source = safeSourceURL(data.source) ?? "";
        archive = safeSourceURL(data.source_archive) ?? "";
      })
      .catch(() => {})
      .finally(() => clearTimeout(timer));
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  });
</script>

<svelte:head><title>{$t(m("lanSourceAccess"))} · {$t(BRAND)}</title></svelte:head>
<h1>{$t(m("lanSourceAccess"))}</h1>
<p>{$t(m("lanSourceDescription"))}</p>
<p>{$t(m("legalProjectLicense"))}</p>
<ul>
  <li><a href="/licenses/AGPL-3.0-only.txt">{$t(m("legalLicense"))}</a></li>
  <li><a href="/licenses/THIRD_PARTY_NOTICES.txt">{$t(m("legalNotices"))}</a></li>
  <li><a href="/licenses/backend/THIRD_PARTY_NOTICES.txt">{$t(m("legalBackendNotices"))}</a></li>
  <li><a href="/licenses/dependency-inventory.json">{$t(m("legalInventory"))}</a></li>
  <li><a href="/licenses/SOURCE.json">{$t(m("lanSourceMetadata"))}</a></li>
</ul>
{#if revision}<p><code>{revision}</code></p>{/if}
{#if archive}<p><a href={archive} rel="noreferrer">{$t(m("legalExactSource"))}</a></p>{/if}
{#if source}<p><a href={source} rel="noreferrer">{$t(m("legalProjectSource"))}</a></p>
{:else}<p role="status">{$t(m("lanSourceUnavailable"))}</p>{/if}

<style>
  li {
    margin-bottom: 0.75rem;
  }
  code {
    overflow-wrap: anywhere;
  }
</style>
