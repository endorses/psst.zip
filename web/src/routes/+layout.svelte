<script lang="ts">
  import { page } from "$app/stores";
  import type { Snippet } from "svelte";
  import "$lib/theme.css";
  import { BRAND } from "$lib/brand";
  let { children }: { children: Snippet } = $props();
</script>

<div class="app">
  <header>
    <a href="/" class="logo">{BRAND}</a><span class="muted small">Quietly share something.</span>
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
    padding: 1rem max(1rem, calc((100vw - 760px) / 2));
    border-bottom: 1px solid var(--border);
    background: var(--surface);
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    align-items: center;
  }
  .logo {
    font-weight: 750;
    font-size: 1.4rem;
    text-decoration: none;
    color: var(--text);
  }
  main {
    flex: 1;
    width: 100%;
    max-width: 760px;
    margin: auto;
    padding: 1.5rem 1rem;
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
