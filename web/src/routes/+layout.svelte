<script lang="ts">
  import { page } from "$app/stores";
  import type { Snippet } from "svelte";
  import "$lib/theme.css";
  import { BRAND } from "$lib/brand";
  let { children }: { children: Snippet } = $props();
</script>

<div class="app" class:workspace-shell={$page.url.pathname === "/"}>
  <a class="skip-link" href="#main">Skip to content</a>
  <header>
    <a href="/" class="logo" aria-label={`${BRAND} home`}>{BRAND}</a><span class="muted small"
      >Quietly share something.</span
    >
  </header>
  <main id="main">
    {#key $page.url.pathname}{@render children()}{/key}
  </main>
  <footer>End-to-end encrypted. Files are never readable by the server.</footer>
</div>

<style>
  .app {
    display: flex;
    flex-direction: column;
    min-height: 100dvh;
  }
  header {
    padding: 1.15rem max(1.25rem, calc((100vw - 1180px) / 2));
    border-bottom: 1px solid var(--divider);
    background: var(--surface);
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    align-items: center;
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
  @media (max-width: 400px) {
    header span {
      display: none;
    }
  }
</style>
