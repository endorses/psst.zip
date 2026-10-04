<script lang="ts">
  import { onMount } from "svelte";
  import { abuseReference, abuseMailto } from "$lib/abuse-contact";
  let {
    email,
    origin,
    pathname,
    onclose,
  }: { email: string; origin: string; pathname: string; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let notice = $state("");
  const reference = $derived(abuseReference(origin, pathname));
  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      notice = "Copied.";
    } catch {
      notice = "Select and copy the text below. Clipboard access is unavailable.";
    }
  }
  onMount(() => {
    const opener = document.activeElement;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    dialog.showModal();
    return () => {
      dialog.close();
      document.body.style.overflow = previous;
      if (opener instanceof HTMLElement && opener.isConnected)
        opener.focus({ preventScroll: true });
    };
  });
</script>

<dialog
  bind:this={dialog}
  aria-labelledby="abuse-report-title"
  oncancel={(event) => {
    event.preventDefault();
    onclose();
  }}
>
  <h2 id="abuse-report-title">Report abuse</h2>
  <p>
    Contact the administrator of this server. Nothing is sent until you send an email in your mail
    app.
  </p>
  <label for="abuse-contact-address">Contact</label>
  <input id="abuse-contact-address" readonly value={email} />
  <button onclick={() => copy(email)}>Copy contact</button>
  <label for="abuse-report-reference">Report reference</label>
  <textarea id="abuse-report-reference" readonly rows="4" value={reference}></textarea>
  <button onclick={() => copy(reference)}>Copy report reference</button>
  <p class="muted small">
    The reference identifies this server and, on a file link page, its resource. It contains no
    encryption key. Do not include the complete link or file contents in your report.
  </p>
  <p class="muted small">No mail app? Copy the address and reference into your email service.</p>
  {#if notice}<p role="status">{notice}</p>{/if}
  <div class="actions">
    <button onclick={onclose}>Close</button>
    <a class="primary" href={abuseMailto(email, origin, pathname)} rel="noreferrer"
      >Open email draft</a
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
    text-align: left;
  }
  dialog::backdrop {
    background: #0008;
  }
  label {
    display: block;
    margin: 1rem 0 0.4rem;
  }
  input,
  textarea {
    width: 100%;
    font: inherit;
    padding: 0.6rem;
    border: 1px solid var(--border);
    border-radius: 0.5rem;
    background: var(--background);
    color: var(--text);
  }
  textarea {
    resize: vertical;
  }
  button {
    margin-top: 0.5rem;
  }
  .actions {
    margin-top: 1rem;
    align-items: center;
  }
  .actions a {
    padding: 0.65rem 1rem;
    border-radius: 0.6rem;
    text-decoration: none;
    background: var(--primary);
    color: var(--on-primary);
  }
  h2 {
    margin-top: 0;
  }
</style>
