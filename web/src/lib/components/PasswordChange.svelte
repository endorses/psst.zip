<script lang="ts">
  import Icon from "./Icon.svelte";
  let {
    busy = false,
    requiredChange = false,
    onchange,
  }: {
    busy?: boolean;
    requiredChange?: boolean;
    onchange: (current: string, password: string) => Promise<void>;
  } = $props();
  let current = $state(""),
    password = $state(""),
    confirmation = $state(""),
    visible = $state(false),
    mismatch = $state("");
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    mismatch = "";
    if (password !== confirmation) {
      mismatch = "The passwords do not match.";
      return;
    }
    if (password === current) {
      mismatch = "Choose a password different from your current password.";
      return;
    }
    await onchange(current, password);
  }
</script>

<h1>{requiredChange ? "Choose your own password" : "Change password"}</h1>
<p class="muted">
  {requiredChange
    ? "Replace your temporary password before using your account."
    : "Changing your password signs out all devices, including this browser."} You will sign in again
  with your new password.
</p>
<form onsubmit={submit}>
  <label
    >Current password<input
      type={visible ? "text" : "password"}
      autocomplete="current-password"
      required
      bind:value={current}
      disabled={busy}
    /></label
  >
  <label
    >New password<input
      type={visible ? "text" : "password"}
      autocomplete="new-password"
      minlength="12"
      required
      bind:value={password}
      disabled={busy}
    /></label
  >
  <label
    >Confirm password<input
      type={visible ? "text" : "password"}
      autocomplete="new-password"
      minlength="12"
      required
      bind:value={confirmation}
      disabled={busy}
      aria-describedby={mismatch ? "password-mismatch" : undefined}
      aria-invalid={!!mismatch}
    /></label
  >
  <button
    type="button"
    class="visibility"
    aria-pressed={visible}
    onclick={() => (visible = !visible)}
    ><Icon name={visible ? "EyeOff" : "Eye"} />{visible
      ? "Hide passwords"
      : "Show passwords"}</button
  >
  <p class="muted small">Use at least 12 characters (up to 72 UTF-8 bytes).</p>
  {#if mismatch}<p id="password-mismatch" class="error" role="alert">{mismatch}</p>{/if}
  <button class="primary" disabled={busy}>{busy ? "Changing password…" : "Change password"}</button>
</form>

<style>
  form {
    max-width: 32rem;
  }
  .visibility {
    justify-self: start;
  }
</style>
