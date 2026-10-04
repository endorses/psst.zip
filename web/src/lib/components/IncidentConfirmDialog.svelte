<script lang="ts">
  import { onMount } from "svelte";
  let {
    title,
    description,
    action,
    busy = false,
    error = "",
    oncancel,
    onconfirm,
  }: {
    title: string;
    description: string;
    action: string;
    busy?: boolean;
    error?: string;
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
  aria-labelledby="incident-confirm-title"
  aria-describedby="incident-confirm-description"
  oncancel={(event) => {
    event.preventDefault();
    if (!busy) oncancel();
  }}
>
  <h2 id="incident-confirm-title">{title}</h2>
  <p id="incident-confirm-description">{description}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <div class="actions">
    <button disabled={busy} onclick={oncancel}>Cancel</button><button
      class="danger"
      disabled={busy}
      onclick={onconfirm}>{busy ? "Applying…" : action}</button
    >
  </div>
</dialog>

<style>
  dialog {
    width: min(32rem, calc(100% - 2rem));
    max-height: calc(100dvh - 2rem);
    overflow: auto;
    margin: auto;
    padding: 1.5rem;
    border: 1px solid var(--divider);
    border-radius: 16px;
    background: var(--surface);
    color: var(--text);
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
