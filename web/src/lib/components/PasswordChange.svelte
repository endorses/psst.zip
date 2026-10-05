<script lang="ts">
  import { message as m, t, type DisplayText } from "$lib/i18n";

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
    mismatch = $state<DisplayText>("");
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    mismatch = "";
    if (password !== confirmation) {
      mismatch = m("thePasswordsDoNotMatch");
      return;
    }
    if (password === current) {
      mismatch = m("chooseAPasswordDifferentFromYourCurrentPassword");
      return;
    }
    await onchange(current, password);
  }
</script>

<h1>{$t(requiredChange ? m("chooseYourOwnPassword") : m("changePassword"))}</h1>
<p class="muted">
  {$t(
    requiredChange
      ? m("replaceYourTemporaryPasswordBeforeUsingYourAccount")
      : m("changingYourPasswordSignsOutAllDevicesIncludingThis"),
  )}
  {$t(m("youWillSignInAgainWithYourNewPassword"))}
</p>
<form onsubmit={submit}>
  <label
    >{$t(m("currentPassword"))}<input
      type={visible ? "text" : "password"}
      autocomplete="current-password"
      required
      bind:value={current}
      disabled={busy}
    /></label
  >
  <label
    >{$t(m("newPassword"))}<input
      type={visible ? "text" : "password"}
      autocomplete="new-password"
      minlength="12"
      required
      bind:value={password}
      disabled={busy}
    /></label
  >
  <label
    >{$t(m("confirmPassword"))}<input
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
    ><Icon name={visible ? "EyeOff" : "Eye"} />{$t(
      visible ? m("hidePasswords") : m("showPasswords"),
    )}</button
  >
  <p class="muted small">{$t(m("useAtLeastCharactersUpToUTFBytes"))}</p>
  {#if mismatch}<p id="password-mismatch" class="error" role="alert">{$t(mismatch)}</p>{/if}
  <button class="primary" disabled={busy}
    >{$t(busy ? m("changingPassword") : m("changePassword"))}</button
  >
</form>

<style>
  form {
    max-width: 32rem;
  }
  .visibility {
    justify-self: start;
  }
</style>
