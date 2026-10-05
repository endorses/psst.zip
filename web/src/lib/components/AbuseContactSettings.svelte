<script lang="ts">
  import { message as m, t, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { validAbuseEmail } from "$lib/abuse-contact";
  let email = $state(""),
    busy = $state(false),
    loaded = $state(false),
    error = $state<DisplayText>(""),
    notice = $state<DisplayText>("");
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
      if (!disposed) error = m("couldNotLoadTheAbuseContactRetryWhenYour");
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    error = "";
    notice = "";
    if (email !== "" && !validAbuseEmail(email)) {
      error = m("enterASingleEmailAddressOrLeaveItBlank");
      return;
    }
    busy = true;
    try {
      const value = await accountRequest<{ email: unknown }>("/admin/abuse-contact", "PATCH", {
        email,
      });
      if (!disposed) {
        email = responseEmail(value);
        notice = email ? m("abuseContactPublished") : m("abuseContactDisabled");
        window.dispatchEvent(new Event("psst:abuse-contact-changed"));
      }
    } catch {
      if (!disposed) error = m("couldNotSaveTheAbuseContactCheckYourConnection");
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
  <h2 id="abuse-settings-title">{$t(m("abuseContact"))}</h2>
  <p class="muted">{$t(m("publishAnEmailAddressWherePeopleCanReportMisuse"))}</p>
  <form onsubmit={save}>
    <label for="abuse-email">{$t(m("publicContactEmailOptional"))}</label>
    <input
      id="abuse-email"
      type="email"
      maxlength="254"
      autocomplete="off"
      spellcheck="false"
      bind:value={email}
      disabled={!loaded || busy}
    />
    <p class="muted small">{$t(m("leaveBlankToDisableUseADedicatedAddressYou"))}</p>
    <button class="primary" disabled={!loaded || busy}
      >{$t(busy ? m("pleaseWait") : m("saveAbuseContact"))}</button
    >
    {#if !loaded && !busy}<button type="button" onclick={load}
        >{$t(m("retryLoadingContact"))}</button
      >{/if}
  </form>
  {#if error}<p role="alert" class="error">{$t(error)}</p>{/if}
  {#if notice}<p role="status" class="success">{$t(notice)}</p>{/if}
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
