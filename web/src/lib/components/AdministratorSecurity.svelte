<script lang="ts">
  import { message as m, t, errorText, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { brandedQr } from "$lib/branded-qr";
  import {
    validateAdministratorSecurity,
    validateEnrollment,
    validateRecoveryCodes,
    type AdministratorSecurity,
    type Enrollment,
  } from "$lib/admin-security";
  import IncidentConfirmDialog from "./IncidentConfirmDialog.svelte";
  let {
    onchanged,
    onmutation,
  }: { onchanged: (codes?: string[]) => void; onmutation: (active: boolean) => void } = $props();
  let security = $state<AdministratorSecurity | null>(null),
    enrollment = $state<Enrollment | null>(null),
    qr = $state(""),
    code = $state("");
  let busy = $state(false),
    error = $state<DisplayText>(""),
    confirmation = $state<"disable" | "regenerate" | null>(null),
    now = $state(Date.now());
  let generation = 0,
    disposed = false;
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    const run = generation;
    try {
      const next = validateAdministratorSecurity(await accountRequest("/admin/security"));
      if (!disposed && run === generation) security = next;
    } catch (cause) {
      if (!disposed && run === generation)
        error = cause instanceof Error ? errorText(cause) : m("couldNotLoadAdministratorSecurity");
    } finally {
      if (!disposed && run === generation) busy = false;
    }
  }
  async function begin() {
    if (busy) return;
    busy = true;
    error = "";
    const run = generation;
    try {
      const next = validateEnrollment(
        await accountRequest("/admin/security/enrollment", "POST", {}),
      );
      if (disposed || run !== generation) return;
      enrollment = next;
      const image = await brandedQr(next.otpauth_url);
      if (!disposed && run === generation) qr = image;
    } catch (cause) {
      if (!disposed && run === generation)
        error = cause instanceof Error ? errorText(cause) : m("couldNotStartAuthenticatorSetup");
    } finally {
      if (!disposed && run === generation) busy = false;
    }
  }
  async function cancel() {
    generation++;
    enrollment = null;
    qr = "";
    code = "";
    busy = false;
    error = "";
    try {
      await accountRequest("/admin/security/enrollment", "DELETE", {});
    } catch {
      if (!disposed) error = m("theLocalSetupSecretWasDiscardedServerCancellationCould");
    }
  }
  async function confirm(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !enrollment || Date.parse(enrollment.expires_at) <= Date.now()) return;
    busy = true;
    error = "";
    const run = generation;
    onmutation(true);
    try {
      const codes = validateRecoveryCodes(
        await accountRequest("/admin/security/enrollment/confirm", "POST", { code: code.trim() }),
      );
      if (disposed || run !== generation) return;
      enrollment = null;
      qr = "";
      code = "";
      onchanged(codes);
    } catch (cause) {
      if (!disposed && run === generation)
        error = cause instanceof Error ? errorText(cause) : m("couldNotConfirmTheAuthenticator");
    } finally {
      onmutation(false);
      if (!disposed && run === generation) {
        busy = false;
        code = "";
      }
    }
  }
  async function changeFactor() {
    if (busy || !confirmation) return;
    busy = true;
    error = "";
    const run = generation;
    onmutation(true);
    try {
      if (confirmation === "disable") {
        await accountRequest("/admin/security/factor", "DELETE", {});
        if (!disposed && run === generation) onchanged();
      } else {
        const codes = validateRecoveryCodes(
          await accountRequest("/admin/security/recovery-codes", "POST", {}),
        );
        if (!disposed && run === generation) onchanged(codes);
      }
    } catch (cause) {
      if (!disposed && run === generation)
        error =
          cause instanceof Error ? errorText(cause) : m("couldNotChangeAuthenticatorSecurity");
    } finally {
      onmutation(false);
      if (!disposed && run === generation) busy = false;
    }
  }
  onMount(() => {
    void load();
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => {
      disposed = true;
      generation++;
      clearInterval(timer);
      if (enrollment)
        void accountRequest("/admin/security/enrollment", "DELETE", {}).catch(() => {});
      enrollment = null;
      qr = "";
      code = "";
    };
  });
</script>

<section aria-labelledby="administrator-security-title">
  <h2 id="administrator-security-title">{$t(m("administratorAccountSecurity"))}</h2>
  <p class="muted">{$t(m("useAnAuthenticatorAppPlusYourPasswordTOTPHelps"))}</p>
  {#if error && !confirmation}<p class="error" role="alert">{$t(error)}</p>{/if}
  {#if security}
    <p>
      <strong
        >{$t(security.enabled ? m("authenticatorEnabled") : m("authenticatorNotSetUp"))}</strong
      >{#if security.enabled}
        · {$t(security.recovery_codes_remaining)} {$t(m("recoveryCodesRemaining"))}{/if}
    </p>
    {#if enrollment}
      <p>{$t(m("scanThisCodeWithYourAuthenticatorAppOrEnter"))}</p>
      {#if qr}<img class="qr" src={qr} alt={$t(m("authenticatorEnrollmentQRCode"))} />{/if}
      <label
        >{$t(m("manualSetupKey"))}<input
          readonly
          value={enrollment.secret}
          autocomplete="off"
          spellcheck={false}
        /></label
      >
      {#if Date.parse(enrollment.expires_at) <= now}<p class="error" role="alert">
          {$t(m("thisEnrollmentExpiredCancelItAndStartAgain"))}
        </p>
      {:else}<p class="muted small">
          {$t(m("setupExpiresIn"))}
          {$t(Math.max(0, Math.ceil((Date.parse(enrollment.expires_at) - now) / 1000)))}
          {$t(m("seconds_5d992"))}
        </p>{/if}
      <form onsubmit={confirm}>
        <label
          >{$t(m("authenticatorSetupCode"))}<input
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            pattern={"[0-9]{6}"}
            maxlength="6"
            required
            disabled={busy || Date.parse(enrollment.expires_at) <= now}
            bind:value={code}
          /></label
        >
        <button class="primary" disabled={busy || Date.parse(enrollment.expires_at) <= now}
          >{$t(m("confirmAuthenticator"))}</button
        >
        <button type="button" disabled={busy} onclick={cancel}
          >{$t(m("cancelAuthenticatorSetup"))}</button
        >
      </form>
      <p class="muted small">{$t(m("confirmingEndsAllSessionsSaveTheRecoveryCodesShown"))}</p>
    {:else if security.enabled}
      <button
        disabled={busy}
        onclick={() => {
          confirmation = "regenerate";
          error = "";
        }}>{$t(m("regenerateRecoveryCodes"))}</button
      >
      <button
        class="danger"
        disabled={busy}
        onclick={() => {
          confirmation = "disable";
          error = "";
        }}>{$t(m("disableAuthenticator"))}</button
      >
    {:else}<p class="notice">
        {$t(m("yourAdministratorAccountCurrentlyReliesOnItsPasswordAlone"))}
      </p>
      <button class="primary" disabled={busy} onclick={begin}>{$t(m("setUpAuthenticator"))}</button
      >{/if}
  {/if}
  {#if !enrollment}<button disabled={busy} onclick={load}
      >{$t(busy ? m("loadingSecuritySettings") : m("refreshAdministratorSecurity"))}</button
    >{/if}
</section>
{#if confirmation}<IncidentConfirmDialog
    title={$t(
      confirmation === "disable"
        ? m("disableAdministratorAuthenticator")
        : m("replaceAllRecoveryCodes"),
    )}
    description={confirmation === "disable"
      ? m("removeYourAuthenticatorAndEveryRecoveryCodeAllSessions")
      : m("invalidateEveryOldRecoveryCodeAndEndAllSessions")}
    action={confirmation === "disable"
      ? m("disableAuthenticatorAndSignOut")
      : m("replaceCodesAndSignOut")}
    {busy}
    {error}
    oncancel={() => {
      confirmation = null;
      error = "";
    }}
    onconfirm={changeFactor}
  />{/if}

<style>
  section {
    margin: 2rem 0;
    padding-top: 1.5rem;
    border-top: 1px solid var(--divider);
  }
  label {
    display: block;
  }
  input {
    width: 100%;
  }
  .qr {
    display: block;
    width: min(18rem, 100%);
    height: auto;
    margin: 1rem auto;
  }
</style>
