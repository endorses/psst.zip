<script lang="ts">
  import { message as m, t } from "$lib/i18n";

  import { page } from "$app/stores";
  import type { Snippet } from "svelte";
  import { onMount } from "svelte";
  import "$lib/theme.css";
  import { initializeLanguage } from "$lib/i18n";
  import LanguagePicker from "$lib/components/LanguagePicker.svelte";
  import ThemePicker from "$lib/components/ThemePicker.svelte";
  import AbuseContact from "$lib/components/AbuseContact.svelte";
  import { BRAND } from "$lib/brand";
  let { children }: { children: Snippet } = $props();
  let httpWarning = $state(false);
  onMount(() => {
    httpWarning = location.protocol === "http:";
    return initializeLanguage();
  });
</script>

<div
  class="app"
  class:workspace-shell={$page.url.pathname === "/" || $page.url.searchParams.has("inbox")}
>
  <a class="skip-link" href="#main">{$t(m("skipToContent"))}</a>
  <header>
    <a href="/" data-sveltekit-reload class="logo" aria-label={$t(m("valueHome", { arg0: BRAND }))}>
      <img
        class="brand-light"
        src="/brand/logo-light.svg"
        alt={$t(BRAND)}
        width="168"
        height="42"
      />
      <img
        class="brand-dark"
        src="/brand/logo-dark.svg"
        alt=""
        aria-hidden="true"
        width="168"
        height="42"
      />
    </a>
    <div class="header-tools">
      <span class="tagline muted small">{$t(m("quietlyShareSomething"))}</span>
      <ThemePicker />
      <LanguagePicker />
    </div>
  </header>
  <main id="main">
    {#if httpWarning}<details class="notice http-notice" role="note">
        <summary>{$t(m("thisConnectionUsesHTTPUseTrustedHTTPS"))}</summary>
        <p>{$t(m("otherPeopleOnTheNetworkCanChangeThisPage"))}</p>
      </details>{/if}
    {#key $page.url.pathname}{@render children()}{/key}
  </main>
  <footer>
    <details class="encryption-details">
      <summary>{$t(m("encryptedOnYourDevice"))}</summary>
      <p>{$t(m("useTrustedHTTPSAndClientSoftwareThisWebsiteDepends"))}</p>
    </details>
    {#key $page.url.origin}<AbuseContact
        origin={$page.url.origin}
        pathname={$page.url.pathname}
      />{/key}
  </footer>
</div>

<style>
  .app {
    display: flex;
    flex-direction: column;
    min-height: 100dvh;
  }
  .http-notice {
    padding: 0 0.8rem;
    margin-top: 0;
    margin-bottom: 1rem;
    font-size: 0.85rem;
  }
  .http-notice summary {
    cursor: pointer;
  }
  header {
    padding: 1.15rem max(1.25rem, calc((100vw - 1180px) / 2));
    border-bottom: 1px solid var(--divider);
    background: var(--surface);
    display: flex;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 1rem;
    align-items: center;
  }
  .header-tools {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    flex-wrap: wrap;
  }
  .logo {
    font-weight: 750;
    font-size: 1.55rem;
    letter-spacing: -0.055em;
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    text-decoration: none;
    color: var(--text);
  }
  .logo img {
    width: 168px;
    height: 42px;
    object-fit: contain;
  }
  .brand-dark {
    display: none;
  }
  :global(:root[data-theme="dark"]) .brand-light {
    display: none;
  }
  :global(:root[data-theme="dark"]) .brand-dark {
    display: block;
  }
  @media (prefers-color-scheme: dark) {
    :global(:root:not([data-theme="light"])) .brand-light {
      display: none;
    }
    :global(:root:not([data-theme="light"])) .brand-dark {
      display: block;
    }
  }
  main {
    flex: 1;
    width: 100%;
    max-width: 760px;
    margin: 0 auto;
    padding: clamp(1.5rem, 4vw, 3rem) 1.25rem;
  }
  .workspace-shell main {
    max-width: 1180px;
  }
  .skip-link {
    position: fixed;
    top: 0.5rem;
    left: 0.5rem;
    z-index: 10;
    padding: 0.75rem;
    background: var(--surface);
    transform: translateY(-200%);
  }
  .skip-link:focus {
    transform: none;
  }
  footer {
    padding: 1rem;
    text-align: center;
    font-size: 0.8rem;
    color: var(--muted);
  }
  .encryption-details summary {
    cursor: pointer;
    display: inline-block;
    padding: 0.5rem;
  }
  .encryption-details p {
    max-width: 46rem;
    margin: 0.5rem auto;
  }
  @media (max-width: 600px) {
    header {
      padding: 0.75rem 1rem;
      gap: 0.4rem;
    }
    .logo img {
      width: 104px;
      height: 30px;
    }
    .header-tools {
      gap: 0.25rem;
    }
    main {
      padding-top: 1rem;
    }
    .tagline {
      display: none;
    }
  }
</style>
