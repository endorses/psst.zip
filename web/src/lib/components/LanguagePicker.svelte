<script lang="ts">
  import { preference, languageReady, chooseLanguage, t, message as m } from "$lib/i18n";
</script>

<label class="language-picker">
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
    margin: 0;
  }
  select {
    width: auto;
    max-width: 10rem;
    padding: 0.55rem 1.8rem 0.55rem 0.6rem;
    margin: 0;
    background-color: transparent;
    border-color: transparent;
    font-size: 0.9rem;
  }
  select:hover,
  select:focus-visible {
    border-color: var(--divider);
  }
</style>
