<script lang="ts">
  import { BRAND } from "$lib/brand";
  import type { Snippet } from "svelte";
  import { onMount } from "svelte";
  import { page } from "$app/stores";
  import "$lib/theme.css";
  import { initializeLanguage, message as m, t } from "$lib/i18n";
  import LanguagePicker from "$lib/components/LanguagePicker.svelte";
  import ThemePicker from "$lib/components/ThemePicker.svelte";
  let { children }: { children: Snippet } = $props();
  const http = $derived($page.url.protocol === "http:");
  onMount(initializeLanguage);
</script>

<div class="lan-app">
  <a class="skip-link" href="#main">{$t(m("skipToContent"))}</a>
  <header>
    <a class="brand" href="/" data-sveltekit-reload>{$t(BRAND)}</a>
    <div class="tools"><ThemePicker /><LanguagePicker /></div>
  </header>
  <main id="main">
    <p class="muted small">{$t(m("lanSharing"))} · {$t(m("lanSessionNotice"))}</p>
    {#if http}
      <details class="notice" role="note">
        <summary>{$t(m("thisConnectionUsesHTTPUseTrustedHTTPS"))}</summary>
        <p>{$t(m("otherPeopleOnTheNetworkCanChangeThisPage"))}</p>
        {#if $page.url.pathname.startsWith("/d/")}
          <p>{$t(m("lanHTTPDownloadLimit"))}</p>
        {/if}
      </details>
    {/if}
    {#key $page.url.pathname}{@render children()}{/key}
  </main>
  <footer><a href="/legal">{$t(m("lanSourceAccess"))}</a></footer>
</div>

<style>
  .lan-app {
    min-height: 100dvh;
    display: flex;
    flex-direction: column;
  }
  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    padding: 1rem max(1.25rem, calc((100vw - 760px) / 2));
    border-bottom: 1px solid var(--divider);
    background: var(--surface);
  }
  .brand {
    color: var(--text);
    text-decoration: none;
    font-weight: 750;
    font-size: 1.6rem;
    letter-spacing: -0.055em;
  }
  .tools {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  main {
    flex: 1;
    width: 100%;
    max-width: 760px;
    margin: 0 auto;
    padding: 1.5rem 1.25rem;
  }
  .notice {
    margin-bottom: 1.5rem;
  }
  summary {
    cursor: pointer;
  }
  footer {
    text-align: center;
    padding: 1rem;
    font-size: 0.85rem;
  }
  .skip-link {
    position: fixed;
    top: 0.5rem;
    left: 0.5rem;
    transform: translateY(-200%);
    background: var(--surface);
    padding: 0.75rem;
    z-index: 10;
  }
  .skip-link:focus {
    transform: none;
  }
</style>
