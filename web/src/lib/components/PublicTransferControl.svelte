<script lang="ts">
  import { message as m, t, LocalizedError, errorText, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { validateIncidentState, type IncidentState } from "$lib/incident-state";
  import IncidentConfirmDialog from "./IncidentConfirmDialog.svelte";
  let incident = $state<IncidentState | null>(null),
    busy = $state(false),
    error = $state<DisplayText>(""),
    notice = $state<DisplayText>("");
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
        error = cause instanceof Error ? errorText(cause) : m("couldNotReadPublicTransferState");
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
        throw new LocalizedError(m("theServerDidNotAcceptTheRequestedPauseState"));
      if (!disposed) {
        incident = next;
        confirming = false;
        notice = paused
          ? m("publicTransfersPausedAdministrativeAndRecoveryControlsRemainAvailable")
          : m("publicTransfersResumedRevokedLinksRemainRevoked");
      }
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? errorText(cause) : m("couldNotUpdatePublicTransferState");
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
  <h2 id="public-transfer-control-title">{$t(m("publicTransferControl"))}</h2>
  {#if incident}<p
      class:notice={incident.public_transfers_paused}
      data-testid="public-transfer-state"
    >
      <strong
        >{$t(
          incident.public_transfers_paused
            ? m("publicTransfersArePaused")
            : m("publicTransfersAreEnabled"),
        )}</strong
      >
    </p>
    <p class="muted small">
      {$t(m("thisSettingPersistsAcrossServerRestartsSignInAdministration"))}
    </p>
    <button
      class:danger={!incident.public_transfers_paused}
      disabled={busy}
      onclick={() => {
        confirming = true;
        error = "";
      }}
      >{$t(
        incident.public_transfers_paused ? m("resumePublicTransfers") : m("pausePublicTransfers"),
      )}</button
    >
  {/if}
  <button disabled={busy} onclick={load}
    >{$t(busy ? m("loadingTransferState") : m("refreshTransferState"))}</button
  >
  {#if error && !confirming}<p class="error" role="alert">
      {$t(error)}
      {$t(
        incident
          ? m("theDisplayedStateMayBeStale")
          : m("otherAdministratorControlsRemainAvailable"),
      )}
    </p>{/if}
  {#if notice}<p class="success" role="status">{$t(notice)}</p>{/if}
</section>
{#if confirming && incident}<IncidentConfirmDialog
    title={$t(
      incident.public_transfers_paused
        ? m("resumePublicTransfers_7597c")
        : m("pausePublicTransfers_36e86"),
    )}
    description={incident.public_transfers_paused
      ? m("eligibleExistingLinksCanTransferFilesAgainRevokedLinks")
      : m("stopPublicFileAndManifestTransfersIncludingActiveStreams")}
    action={incident.public_transfers_paused
      ? m("resumePublicTransfers")
      : m("pausePublicTransfers")}
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
