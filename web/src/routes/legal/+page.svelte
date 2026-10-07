<script lang="ts">
  import { onMount } from "svelte";
  import { message as m, t } from "$lib/i18n";
  import { loadReleaseSource, type ReleaseSource } from "$lib/release-source";
  let release = $state<ReleaseSource | null>(null);
  let loading = $state(true);
  onMount(() => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 10_000);
    void loadReleaseSource(fetch, controller.signal)
      .then((value) => {
        release = value;
      })
      .catch(() => {})
      .finally(() => {
        loading = false;
        clearTimeout(timeout);
      });
    return () => {
      clearTimeout(timeout);
      controller.abort();
    };
  });
</script>

<svelte:head><title>{$t(m("legalTitle"))}</title></svelte:head>
<h1>{$t(m("legalTitle"))}</h1>
<p>{$t(m("legalProjectLicense"))}</p>
<ul>
  <li><a href="/licenses/AGPL-3.0-only.txt">{$t(m("legalLicense"))}</a></li>
  <li><a href="/licenses/THIRD_PARTY_NOTICES.txt">{$t(m("legalNotices"))}</a></li>
  {#if release?.noticeFiles.includes("/licenses/backend/THIRD_PARTY_NOTICES.txt")}
    <li><a href="/licenses/backend/THIRD_PARTY_NOTICES.txt">{$t(m("legalBackendNotices"))}</a></li>
  {/if}
  {#if release?.noticeFiles.includes("/licenses/runtime/THIRD_PARTY_NOTICES.txt")}
    <li><a href="/licenses/runtime/THIRD_PARTY_NOTICES.txt">{$t(m("legalRuntimeNotices"))}</a></li>
  {/if}
  <li><a href="/licenses/dependency-inventory.json">{$t(m("legalInventory"))}</a></li>
</ul>
<h2>{$t(m("legalHostedVersion"))}</h2>
{#if loading}<p aria-live="polite">{$t(m("legalLoading"))}</p>
{:else if release}
  <dl>
    <dt>{$t(m("legalVersion"))}</dt>
    <dd>{release.version}</dd>
    <dt>{$t(m("legalRevision"))}</dt>
    <dd><code>{release.revision}</code></dd>
  </dl>
  <p><a href={release.sourceArchive} rel="noreferrer">{$t(m("legalExactSource"))}</a></p>
  <p><a href={release.source} rel="noreferrer">{$t(m("legalProjectSource"))}</a></p>
{:else}<p role="status">{$t(m("legalUnavailable"))}</p>{/if}
<p><a href="/">{$t(m("legalBack"))}</a></p>

<style>
  dd {
    margin: 0 0 1rem;
    overflow-wrap: anywhere;
  }
  ul {
    line-height: 2;
  }
</style>
