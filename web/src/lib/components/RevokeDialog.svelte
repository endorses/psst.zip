<script lang="ts">
  import { message as m, t, type DisplayText } from "$lib/i18n";

  import Icon from "./Icon.svelte";
  import { onMount } from "svelte";

  let {
    busy,
    error,
    oncancel,
    onconfirm,
  }: {
    busy: boolean;
    error: DisplayText;
    oncancel: () => void;
    onconfirm: () => void;
  } = $props();
  let dialog: HTMLDialogElement;

  onMount(() => {
    const opener = document.activeElement;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    dialog.showModal();
    return () => {
      dialog.close();
      document.body.style.overflow = overflow;
      if (opener instanceof HTMLElement && opener.isConnected)
        opener.focus({ preventScroll: true });
    };
  });
</script>

<dialog
  bind:this={dialog}
  aria-labelledby="revoke-title"
  aria-describedby="revoke-description"
  onkeydown={(event) => {
    if (event.key !== "Tab") return;
    const buttons = dialog.querySelectorAll<HTMLButtonElement>("button:not(:disabled)");
    const first = buttons[0];
    const last = buttons[buttons.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first?.focus();
    }
  }}
  oncancel={(event) => {
    event.preventDefault();
    if (!busy) oncancel();
  }}
>
  <h2 id="revoke-title">{$t(m("revokeThisLink"))}</h2>
  <p id="revoke-description">{$t(m("theLinkWillStopWorkingAndItsServerFiles"))}</p>
  {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}
  <div class="actions">
    <button disabled={busy} onclick={oncancel}>{$t(m("cancel"))}</button>
    <button class="danger" disabled={busy} onclick={onconfirm}
      ><Icon name="Revoke" size={18} />{$t(busy ? m("revoking") : m("revokeAndDelete"))}</button
    >
  </div>
</dialog>

<style>
  dialog {
    width: min(30rem, calc(100% - 2rem));
    max-height: calc(100dvh - 2rem);
    overflow: auto;
    margin: auto;
    padding: 1.5rem;
    border: 1px solid var(--divider);
    border-radius: 16px;
    background: var(--surface);
    color: var(--text);
    box-shadow: 0 16px 64px #0004;
  }
  dialog::backdrop {
    background: #0008;
  }
  h2 {
    margin-top: 0;
  }
  .actions {
    justify-content: flex-end;
    margin-top: 1.5rem;
  }
</style>
