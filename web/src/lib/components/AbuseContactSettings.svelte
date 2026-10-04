<script lang="ts">
  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { validAbuseEmail } from "$lib/abuse-contact";
  let email = $state(""),
    busy = $state(false),
    loaded = $state(false),
    error = $state(""),
    notice = $state("");
  let disposed = false;
  function responseEmail(value: { email: unknown }): string {
    if (value.email !== "" && !validAbuseEmail(value.email)) throw new Error();
    return value.email as string;
  }
  async function load() {
    busy = true;
    error = "";
    try {
      const value = await accountRequest<{ email: unknown }>("/admin/abuse-contact");
      if (!disposed) {
        email = responseEmail(value);
        loaded = true;
      }
    } catch {
      if (!disposed)
        error = "Could not load the abuse contact. Retry when your connection is available.";
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    error = "";
    notice = "";
    if (email !== "" && !validAbuseEmail(email)) {
      error = "Enter a single email address, or leave it blank to disable the contact.";
      return;
    }
    busy = true;
    try {
      const value = await accountRequest<{ email: unknown }>("/admin/abuse-contact", "PATCH", {
        email,
      });
      if (!disposed) {
        email = responseEmail(value);
        notice = email ? "Abuse contact published." : "Abuse contact disabled.";
        window.dispatchEvent(new Event("psst:abuse-contact-changed"));
      }
    } catch {
      if (!disposed)
        error =
          "Could not save the abuse contact. Check your connection and administrator session, then retry.";
    } finally {
      if (!disposed) busy = false;
    }
  }
  onMount(() => {
    void load();
    return () => {
      disposed = true;
    };
  });
</script>

<section aria-labelledby="abuse-settings-title">
  <h2 id="abuse-settings-title">Abuse contact</h2>
  <p class="muted">
    Publish an email address where people can report misuse of this server. This address will be
    public. Reports are prepared in the person's own mail app; this server does not send or store
    them.
  </p>
  <form onsubmit={save}>
    <label for="abuse-email">Public contact email (optional)</label>
    <input
      id="abuse-email"
      type="email"
      maxlength="254"
      autocomplete="off"
      spellcheck="false"
      bind:value={email}
      disabled={!loaded || busy}
    />
    <p class="muted small">
      Leave blank to disable. Use a dedicated address you are comfortable publishing.
    </p>
    <button class="primary" disabled={!loaded || busy}
      >{busy ? "Please wait…" : "Save abuse contact"}</button
    >
    {#if !loaded && !busy}<button type="button" onclick={load}>Retry loading contact</button>{/if}
  </form>
  {#if error}<p role="alert" class="error">{error}</p>{/if}
  {#if notice}<p role="status" class="success">{notice}</p>{/if}
</section>

<style>
  section {
    margin-top: 2rem;
    padding-top: 1.5rem;
    border-top: 1px solid var(--divider);
  }
  label {
    display: block;
    margin-bottom: 0.5rem;
  }
  input {
    width: 100%;
    max-width: 28rem;
  }
</style>
