<script lang="ts">
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
    error = $state(""),
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
        error = cause instanceof Error ? cause.message : "Could not load administrator security.";
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
        error = cause instanceof Error ? cause.message : "Could not start authenticator setup.";
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
      if (!disposed)
        error =
          "The local setup secret was discarded. Server cancellation could not be confirmed; pending enrollment expires within five minutes.";
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
        error = cause instanceof Error ? cause.message : "Could not confirm the authenticator.";
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
        error = cause instanceof Error ? cause.message : "Could not change authenticator security.";
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
  <h2 id="administrator-security-title">Administrator account security</h2>
  <p class="muted">
    Use an authenticator app plus your password. TOTP helps protect server administration but is not
    phishing-resistant. Use a trusted HTTPS connection and keep recovery codes private.
  </p>
  {#if error && !confirmation}<p class="error" role="alert">{error}</p>{/if}
  {#if security}
    <p>
      <strong>{security.enabled ? "Authenticator enabled" : "Authenticator not set up"}</strong
      >{#if security.enabled}
        · {security.recovery_codes_remaining} recovery codes remaining{/if}
    </p>
    {#if enrollment}
      <p>
        Scan this code with your authenticator app, or enter the setup key manually. The QR code is
        generated in this browser; no secret is sent to an external QR service.
      </p>
      {#if qr}<img class="qr" src={qr} alt="Authenticator enrollment QR code" />{/if}
      <label
        >Manual setup key<input
          readonly
          value={enrollment.secret}
          autocomplete="off"
          spellcheck={false}
        /></label
      >
      {#if Date.parse(enrollment.expires_at) <= now}<p class="error" role="alert">
          This enrollment expired. Cancel it and start again.
        </p>
      {:else}<p class="muted small">
          Setup expires in {Math.max(
            0,
            Math.ceil((Date.parse(enrollment.expires_at) - now) / 1000),
          )} seconds.
        </p>{/if}
      <form onsubmit={confirm}>
        <label
          >Authenticator setup code<input
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
          >Confirm authenticator</button
        >
        <button type="button" disabled={busy} onclick={cancel}>Cancel authenticator setup</button>
      </form>
      <p class="muted small">
        Confirming ends all sessions. Save the recovery codes shown next, then sign in again. Wait
        for the next authenticator code, or use a recovery code for immediate sign-in.
      </p>
    {:else if security.enabled}
      <button
        disabled={busy}
        onclick={() => {
          confirmation = "regenerate";
          error = "";
        }}>Regenerate recovery codes</button
      >
      <button
        class="danger"
        disabled={busy}
        onclick={() => {
          confirmation = "disable";
          error = "";
        }}>Disable authenticator</button
      >
    {:else}<p class="notice">
        Your administrator account currently relies on its password alone. Existing accounts can
        continue signing in while setting up protection.
      </p>
      <button class="primary" disabled={busy} onclick={begin}>Set up authenticator</button>{/if}
  {/if}
  {#if !enrollment}<button disabled={busy} onclick={load}
      >{busy ? "Loading security settings…" : "Refresh administrator security"}</button
    >{/if}
</section>
{#if confirmation}<IncidentConfirmDialog
    title={confirmation === "disable"
      ? "Disable administrator authenticator?"
      : "Replace all recovery codes?"}
    description={confirmation === "disable"
      ? "Remove your authenticator and every recovery code. All sessions will end, including this browser. Your next sign-in will rely on your password alone until you set up another authenticator."
      : "Invalidate every old recovery code and end all sessions, including this browser. Save the new codes shown next, then sign in again. Your authenticator remains enabled."}
    action={confirmation === "disable"
      ? "Disable authenticator and sign out"
      : "Replace codes and sign out"}
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
