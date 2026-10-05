<script lang="ts">
  import { message as m, t } from "$lib/i18n";

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

<div class="theme-picker header-picker">
  <Icon
    name={appearance === "light" ? "Sun" : appearance === "dark" ? "Moon" : "Monitor"}
    size={18}
  />
  <select
    aria-label={$t(m("appearance"))}
    disabled={!ready}
    value={appearance}
    onchange={(event) => choose(event.currentTarget.value)}
  >
    <option value="system">{$t(m("system"))}</option>
    <option value="light">{$t(m("light"))}</option>
    <option value="dark">{$t(m("dark"))}</option>
  </select>
</div>
