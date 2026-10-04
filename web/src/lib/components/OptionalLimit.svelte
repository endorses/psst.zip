<script lang="ts">
  let {
    value = $bindable(0),
    label,
    description,
    disabled = false,
  }: { value?: number; label: string; description: string; disabled?: boolean } = $props();
</script>

<div class="optional-limit">
  <label
    ><input
      type="checkbox"
      {disabled}
      checked={value !== 0}
      onchange={(event) => (value = event.currentTarget.checked ? 1 : 0)}
    />
    {label}</label
  >
  {#if value !== 0}<label
      >Maximum<input
        aria-label={label}
        type="number"
        {disabled}
        min="1"
        max="2147483647"
        step="1"
        {value}
        oninput={(event) => {
          const entered = event.currentTarget.valueAsNumber;
          value = entered >= 1 ? entered : Number.NaN;
        }}
      /></label
    >
    <p class="muted small">{description}</p>{/if}
</div>

<style>
  .optional-limit {
    margin: 1rem 0;
  }
  input[type="checkbox"] {
    width: auto;
  }
</style>
