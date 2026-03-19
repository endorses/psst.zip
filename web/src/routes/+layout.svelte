<script lang="ts">
  import type { Snippet } from "svelte";

  let { children }: { children: Snippet } = $props();
</script>

<div class="app">
  <header>
    <a href="/" class="logo">Secure File Transfer</a>
  </header>

  <main>
    {@render children()}
  </main>

  <footer>
    <p>End-to-end encrypted. Files are never readable by the server.</p>
  </footer>
</div>

<style>
  :global(*) {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
  }

  :global(body) {
    font-family:
      -apple-system,
      BlinkMacSystemFont,
      "Segoe UI",
      Roboto,
      Oxygen,
      Ubuntu,
      Cantarell,
      sans-serif;
    background: #fafafa;
    color: #1a1a1a;
    line-height: 1.6;
    -webkit-font-smoothing: antialiased;
  }

  .app {
    display: flex;
    flex-direction: column;
    min-height: 100vh;
  }

  header {
    padding: 1rem 1.5rem;
    border-bottom: 1px solid #e5e5e5;
    background: #fff;
  }

  .logo {
    font-size: 1.125rem;
    font-weight: 600;
    color: #1a1a1a;
    text-decoration: none;
  }

  main {
    flex: 1;
    width: 100%;
    max-width: 640px;
    margin: 0 auto;
    padding: 2rem 1.5rem;
  }

  footer {
    padding: 1.5rem;
    text-align: center;
    font-size: 0.8125rem;
    color: #888;
    border-top: 1px solid #e5e5e5;
  }

  @media (max-width: 480px) {
    main {
      padding: 1.25rem 1rem;
    }
  }
</style>
