<script lang="ts">
  import { message as m, t, errorText, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest, AccountError } from "$lib/account";
  import {
    onRecentAuthenticationRequired,
    validateAdministratorSecurity,
    validateRecentAuthentication,
    type AdministratorSecurity,
  } from "$lib/admin-security";
  let { onconfirmed, onsessionended }: { onconfirmed: () => void; onsessionended: () => void } =
    $props();
  let open = $state(false),
    busy = $state(false),
    error = $state<DisplayText>(""),
    password = $state(""),
    code = $state(""),
    recovery = $state(false);
  let security = $state<AdministratorSecurity | null>(null),
    dialog = $state<HTMLDialogElement>();
  let generation = 0,
    controller: AbortController | undefined;
  function discard() {
    generation++;
    controller?.abort();
    controller = undefined;
    password = "";
    code = "";
    recovery = false;
    security = null;
    error = "";
    busy = false;
    open = false;
  }
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    const run = generation;
    try {
      const next = validateAdministratorSecurity(await accountRequest("/admin/security"));
      if (run === generation) security = next;
    } catch (cause) {
      if (run !== generation) return;
      if (cause instanceof AccountError && cause.status === 401) {
        discard();
        onsessionended();
        return;
      }
      error = cause instanceof Error ? errorText(cause) : m("couldNotLoadAdministratorSecurity");
    } finally {
      if (run === generation) busy = false;
    }
  }
  async function verify(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !security) return;
    busy = true;
    error = "";
    const run = generation;
    controller = new AbortController();
    try {
      const result = await accountRequest(
        "/admin/security/reauth",
        "POST",
        {
          password,
          ...(security.enabled
            ? recovery
              ? { recovery_code: code.trim() }
              : { code: code.trim() }
            : {}),
        },
        controller.signal,
      );
      if (run !== generation) return;
      validateRecentAuthentication(result);
      discard();
      onconfirmed();
    } catch (cause) {
      if (run !== generation) return;
      // Incorrect proof is a local error; /auth/me polling separately detects session expiry.
      error = cause instanceof Error ? errorText(cause) : m("couldNotConfirmYourIdentity");
    } finally {
      if (run === generation) {
        busy = false;
        password = "";
        code = "";
      }
    }
  }
  onMount(() => {
    const remove = onRecentAuthenticationRequired(() => {
      if (!open) {
        open = true;
        void load();
      }
    });
    return () => {
      remove();
      discard();
    };
  });
  $effect(() => {
    if (!dialog) return;
    const activeDialog = dialog,
      opener = document.activeElement,
      overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    activeDialog.showModal();
    return () => {
      activeDialog.close();
      document.body.style.overflow = overflow;
      if (opener instanceof HTMLElement && opener.isConnected)
        opener.focus({ preventScroll: true });
    };
  });
</script>

{#if open}<dialog
    bind:this={dialog}
    aria-labelledby="recent-auth-title"
    aria-describedby="recent-auth-description"
    oncancel={(event) => {
      event.preventDefault();
      discard();
    }}
  >
    <h2 id="recent-auth-title">{$t(m("confirmAdministratorIdentity"))}</h2>
    <p id="recent-auth-description">
      {$t(m("sensitiveChangesRequireVerificationWithinTheLastFiveMinutes"))}
    </p>
    {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}
    {#if security}<form onsubmit={verify}>
        <label
          >{$t(m("administratorPassword"))}<input
            type="password"
            autocomplete="current-password"
            required
            disabled={busy}
            bind:value={password}
          /></label
        >
        {#if security.enabled}<label
            >{$t(recovery ? m("recoveryCode") : m("authenticatorCode"))}<input
              type="text"
              autocomplete="one-time-code"
              inputmode={recovery ? "text" : "numeric"}
              pattern={recovery ? undefined : "[0-9]{6}"}
              maxlength={recovery ? 128 : 6}
              spellcheck={false}
              autocapitalize="none"
              required
              disabled={busy}
              bind:value={code}
            /></label
          >
          <button
            type="button"
            disabled={busy}
            onclick={() => {
              recovery = !recovery;
              code = "";
            }}>{$t(recovery ? m("useAuthenticatorCode") : m("useARecoveryCode"))}</button
          >{/if}
        <div class="actions">
          <button type="button" onclick={discard}>{$t(m("cancelVerification"))}</button><button
            class="primary"
            disabled={busy}>{$t(busy ? m("verifying") : m("confirmIdentity"))}</button
          >
        </div>
      </form>{:else}<button disabled={busy} onclick={load}
        >{$t(busy ? m("loadingSecuritySettings") : m("retrySecuritySettings"))}</button
      ><button onclick={discard}>{$t(m("cancelVerification"))}</button>{/if}
  </dialog>{/if}

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
  label {
    display: block;
  }
  input {
    width: 100%;
  }
  .actions {
    justify-content: flex-end;
    margin-top: 1.5rem;
  }
</style>
