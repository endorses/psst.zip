<script lang="ts">
  import { message as m, t, type DisplayText } from "$lib/i18n";
  import { adminSectionUrl } from "$lib/admin-section";
  let {
    view,
    active,
    items,
  }: {
    view: "server" | "traffic";
    active: string;
    items: { id: string; label: DisplayText }[];
  } = $props();
</script>

<nav class="admin-section-nav" aria-label={$t(m("adminPageSections"))}>
  {#each items as item}
    <a
      href={adminSectionUrl(view, item.id)}
      class:active={active === item.id}
      aria-current={active === item.id ? "page" : undefined}
      data-sveltekit-keepfocus
      data-sveltekit-noscroll>{$t(item.label)}</a
    >
  {/each}
</nav>

<style>
  nav {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
    margin: 1.5rem 0;
    padding-bottom: 0.65rem;
    border-bottom: 1px solid var(--divider);
  }
  a {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    padding: 0.6rem 0.85rem;
    border-radius: 0.6rem;
    color: var(--muted);
    text-decoration: none;
    font-size: 0.9rem;
    font-weight: 550;
  }
  a:hover {
    background: var(--hover);
    color: var(--text);
  }
  a.active {
    background: var(--accent);
    color: var(--primary);
  }
</style>
