<script lang="ts">
  import { page } from "$app/stores";
  import type { Snippet } from "svelte";
  import { onMount } from "svelte";
  import "$lib/theme.css";
  import ThemePicker from "$lib/components/ThemePicker.svelte";
  import AbuseContact from "$lib/components/AbuseContact.svelte";
  import { BRAND } from "$lib/brand";
  let { children }: { children: Snippet } = $props();
  let httpWarning = $state(false);
  onMount(() => {
    httpWarning = location.protocol === "http:";
  });
</script>

<div
  class="app"
  class:workspace-shell={$page.url.pathname === "/" || $page.url.searchParams.has("inbox")}
>
  <a class="skip-link" href="#main">Skip to content</a>
  <header>
    <a href="/" data-sveltekit-reload class="logo" aria-label={`${BRAND} home`}>
      <img class="brand-light" src="/brand/logo-light.svg" alt={BRAND} width="168" height="42" />
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
      <span class="tagline muted small">Quietly share something.</span>
      <ThemePicker />
    </div>
  </header>
  <main id="main">
    {#if httpWarning}<details class="notice http-notice" role="note">
        <summary>This connection uses HTTP. Use trusted HTTPS.</summary>
        <p>
          Other people on the network can change this page and access files or account credentials.
          Use trusted HTTPS for sensitive files.
        </p>
      </details>{/if}
    {#key $page.url.pathname}{@render children()}{/key}
  </main>
  <footer>
    <details class="encryption-details">
      <summary>Encrypted on your device</summary>
      <p>
        Use trusted HTTPS and client software: this website depends on the server that delivers it.
        Encryption does not verify the sender or make a file safe.
      </p>
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
    gap: 1.25rem;
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
    main {
      padding-top: 1rem;
    }
    .tagline {
      display: none;
    }
  }
</style>
