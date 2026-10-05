<script lang="ts">
  import { message as m, t } from "$lib/i18n";

  import { untrack } from "svelte";
  let {
    value = $bindable(0),
    label,
    description,
    disabled = false,
  }: { value?: number; label: string; description: string; disabled?: boolean } = $props();
  const id = $props.id();
  // Bind the native disclosure state so edits inside it cannot reapply a stale
  // closed value. Restored policies start expanded; subsequent changes are native.
  let opened = $state(untrack(() => value !== 0));
  const invalid = $derived(
    value !== 0 && (!Number.isInteger(value) || value < 1 || value > 2_147_483_647),
  );
</script>

<details class="optional-limit" bind:open={opened}>
  <summary
    >{$t(m("linkLimits"))}
    <span class="muted small">{$t(value === 0 ? m("optional") : m("limitEnabled"))}</span></summary
  >
  <div class="limit-fields">
    <label class="limit-toggle">
      <input
        type="checkbox"
        {disabled}
        checked={value !== 0}
        onchange={(event) => (value = event.currentTarget.checked ? 1 : 0)}
      />
      {$t(label)}
    </label>
    {#if value !== 0}
      <label
        >{$t(m("maximum"))}
        <input
          aria-label={$t(label)}
          aria-invalid={invalid}
          aria-describedby={`${id}-help${invalid ? ` ${id}-error` : ""}`}
          type="number"
          {disabled}
          min="1"
          max="2147483647"
          step="1"
          value={Number.isNaN(value) ? "" : value}
          oninput={(event) => {
            const entered = event.currentTarget.valueAsNumber;
            value = entered >= 1 ? entered : Number.NaN;
          }}
        />
      </label>
      {#if invalid}<p id={`${id}-error`} class="error small" role="alert">
          {$t(m("enterAWholeNumberBetweenAndOrTurnThe"))}
        </p>{/if}
      <p id={`${id}-help`} class="muted small">{$t(description)}</p>
    {/if}
  </div>
</details>

<style>
  .optional-limit {
    margin: 1rem 0;
  }
  summary {
    cursor: pointer;
    padding: 0.75rem 0;
    font-weight: 600;
  }
  summary span {
    margin-left: 0.6rem;
    font-weight: 400;
  }
  .limit-fields {
    padding: 0.5rem 0;
  }
  input[type="checkbox"] {
    width: auto;
  }
  .limit-toggle {
    display: flex;
    align-items: center;
    justify-content: flex-start;
    gap: 0.65rem;
    min-height: 44px;
    cursor: pointer;
  }
  .limit-toggle input {
    flex: none;
    margin: 0;
    width: 1.1rem;
    height: 1.1rem;
  }
</style>
