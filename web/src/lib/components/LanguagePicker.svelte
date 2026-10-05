<script lang="ts">
  import Icon from "./Icon.svelte";
  import { preference, languageReady, chooseLanguage, t, message as m } from "$lib/i18n";
</script>

<label class="language-picker">
  <Icon name="Languages" size={18} />
  <span class="sr-only">{$t(m("language"))}</span>
  <select
    disabled={!$languageReady}
    value={$preference}
    onchange={(event) => chooseLanguage(event.currentTarget.value)}
    aria-label={$t(m("language"))}
  >
    <option value="system">{$t(m("system"))}</option>
    <option value="en" lang="en">English</option>
    <option value="de" lang="de">Deutsch</option>
  </select>
</label>

<style>
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  .language-picker {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--muted);
    margin: 0;
  }
  select {
    width: auto;
    max-width: 10rem;
    padding: 0.4rem 0.6rem;
    margin: 0;
    background-color: transparent;
    border-color: transparent;
    font-size: 0.85rem;
  }
  select:hover,
  select:focus-visible {
    border-color: var(--divider);
  }
</style>
