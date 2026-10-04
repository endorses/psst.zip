<script lang="ts">
  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { validateIncidentState, type IncidentState } from "$lib/incident-state";
  import IncidentConfirmDialog from "./IncidentConfirmDialog.svelte";
  let incident = $state<IncidentState | null>(null),
    busy = $state(false),
    error = $state(""),
    notice = $state("");
  let confirming = $state(false),
    disposed = false;
  async function load() {
    if (busy) return;
    busy = true;
    error = "";
    try {
      const next = validateIncidentState(await accountRequest("/admin/incident-state"));
      if (!disposed) incident = next;
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? cause.message : "Could not read public transfer state.";
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function save() {
    if (!incident || busy) return;
    const paused = !incident.public_transfers_paused;
    busy = true;
    error = "";
    notice = "";
    try {
      const next = validateIncidentState(
        await accountRequest("/admin/incident-state", "PATCH", { public_transfers_paused: paused }),
      );
      if (next.public_transfers_paused !== paused)
        throw new Error(
          "The server did not accept the requested pause state. Refresh before trying again.",
        );
      if (!disposed) {
        incident = next;
        confirming = false;
        notice = paused
          ? "Public transfers paused. Administrative and recovery controls remain available."
          : "Public transfers resumed. Revoked links remain revoked.";
      }
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? cause.message : "Could not update public transfer state.";
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

<section class="transfer-control" aria-labelledby="public-transfer-control-title">
  <h2 id="public-transfer-control-title">Public transfer control</h2>
  {#if incident}<p
      class:notice={incident.public_transfers_paused}
      data-testid="public-transfer-state"
    >
      <strong
        >{incident.public_transfers_paused
          ? "Public transfers are paused"
          : "Public transfers are enabled"}</strong
      >
    </p>
    <p class="muted small">
      This setting persists across server restarts. Sign-in, administration, metadata inspection and
      revocation remain available during a pause. Downloaded copies remain on recipients' devices.
    </p>
    <button
      class:danger={!incident.public_transfers_paused}
      disabled={busy}
      onclick={() => {
        confirming = true;
        error = "";
      }}
      >{incident.public_transfers_paused
        ? "Resume public transfers"
        : "Pause public transfers"}</button
    >
  {/if}
  <button disabled={busy} onclick={load}
    >{busy ? "Loading transfer state…" : "Refresh transfer state"}</button
  >
  {#if error && !confirming}<p class="error" role="alert">
      {error}
      {incident
        ? "The displayed state may be stale."
        : "Other administrator controls remain available."}
    </p>{/if}
  {#if notice}<p class="success" role="status">{notice}</p>{/if}
</section>
{#if confirming && incident}<IncidentConfirmDialog
    title={incident.public_transfers_paused
      ? "Resume public transfers?"
      : "Pause public transfers?"}
    description={incident.public_transfers_paused
      ? "Eligible existing links can transfer files again. Revoked links stay revoked; disabled accounts stay disabled. This does not restore deleted server files."
      : "Stop public file and manifest transfers, including active streams, and block new transfer creation and completion. Existing links remain stored. Sign-in, administrator controls, metadata and revocation stay available. Downloaded copies cannot be recalled."}
    action={incident.public_transfers_paused ? "Resume public transfers" : "Pause public transfers"}
    {busy}
    {error}
    oncancel={() => {
      confirming = false;
      error = "";
    }}
    onconfirm={save}
  />{/if}

<style>
  .transfer-control {
    margin: 1.5rem 0;
    padding: 1rem;
    border: 1px solid var(--divider);
    border-radius: 0.75rem;
  }
  h2 {
    margin-top: 0;
  }
</style>
