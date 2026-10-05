<script lang="ts">
  import { untrack } from "svelte";
  let {
    value = $bindable(0),
    label,
    description,
    disabled = false,
  }: { value?: number; label: string; description: string; disabled?: boolean } = $props();
  const id = $props.id();
  let opened = $state(untrack(() => value !== 0));
  const invalid = $derived(
    value !== 0 && (!Number.isInteger(value) || value < 1 || value > 2_147_483_647),
  );
</script>

<details
  class="optional-limit"
  open={opened}
  ontoggle={(event) => (opened = event.currentTarget.open)}
>
  <summary
    >Link limits <span class="muted small">{value === 0 ? "Optional" : "Limit enabled"}</span
    ></summary
  >
  <div class="limit-fields">
    <label>
      <input
        type="checkbox"
        {disabled}
        checked={value !== 0}
        onchange={(event) => (value = event.currentTarget.checked ? 1 : 0)}
      />
      {label}
    </label>
    {#if value !== 0}
      <label
        >Maximum
        <input
          aria-label={label}
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
          Enter a whole number between 1 and 2147483647, or turn the limit off.
        </p>{/if}
      <p id={`${id}-help`} class="muted small">{description}</p>
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
</style>
