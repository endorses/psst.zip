<script lang="ts">
  import { onMount } from "svelte";
  import Icon from "$lib/components/Icon.svelte";

  type Appearance = "system" | "light" | "dark";
  let appearance = $state<Appearance>("system");
  let ready = $state(false);
  function normalize(value: string | null | undefined): Appearance {
    return value === "light" || value === "dark" ? value : "system";
  }
  function apply(value: string | null) {
    appearance = normalize(value);
    document.documentElement.dataset.theme = appearance;
  }
  function choose(value: string) {
    apply(value);
    try {
      localStorage.setItem("psst.theme", appearance);
    } catch {
      // The current page can still change appearance when storage is unavailable.
    }
  }
  onMount(() => {
    apply(document.documentElement.dataset.theme ?? null);
    ready = true;
    const sync = (event: StorageEvent) => {
      if (event.key === "psst.theme" || event.key === null) apply(event.newValue);
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  });
</script>

<div class="theme-picker">
  <Icon
    name={appearance === "light" ? "Sun" : appearance === "dark" ? "Moon" : "Monitor"}
    size={18}
  />
  <select
    aria-label="Appearance"
    disabled={!ready}
    value={appearance}
    onchange={(event) => choose(event.currentTarget.value)}
  >
    <option value="system">System</option>
    <option value="light">Light</option>
    <option value="dark">Dark</option>
  </select>
</div>

<style>
  .theme-picker {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    flex-shrink: 0;
    color: var(--muted);
  }
  select {
    width: auto;
    min-height: 44px;
    padding: 0.4rem 0.6rem;
    border-color: transparent;
    background: transparent;
    color: var(--text);
    font-size: 0.85rem;
    cursor: pointer;
  }
  select:hover {
    background: var(--hover);
  }
</style>
