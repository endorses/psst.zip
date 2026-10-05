<script lang="ts">
  import { message as m, t, errorText, type DisplayText } from "$lib/i18n";

  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { loadServerLimits, fileLimitLabel, fileLimitMessage } from "$lib/limits";
  let maximum = $state<number | undefined>(undefined),
    current = $state<number | null>(null),
    ceiling = $state<number | null>(null),
    busy = $state(false),
    error = $state<DisplayText>(""),
    notice = $state<DisplayText>("");
  let disposed = false;
  async function load() {
    busy = true;
    error = "";
    try {
      const value = await loadServerLimits();
      if (!disposed) {
        current = value.max_file_size;
        ceiling = value.max_file_size_ceiling;
        maximum = value.max_file_size / 1024 / 1024;
      }
    } catch (cause) {
      if (!disposed)
        error = cause instanceof Error ? errorText(cause) : m("couldNotLoadServerSettings");
    } finally {
      if (!disposed) busy = false;
    }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault();
    notice = "";
    error = "";
    const bytes = Math.round((maximum ?? 0) * 1024 * 1024);
    if (
      !Number.isSafeInteger(bytes) ||
      bytes < 1024 * 1024 ||
      ceiling === null ||
      bytes > ceiling
    ) {
      error = m("chooseAFileLimitBetweenMiBAndValue", { arg0: fileLimitMessage(ceiling ?? 0) });
      return;
    }
    busy = true;
    try {
      const result = await accountRequest<{ max_file_size: number }>("/admin/settings", "PATCH", {
        max_file_size: bytes,
      });
      if (!disposed) {
        current = result.max_file_size;
        maximum = result.max_file_size / 1024 / 1024;
        notice = m("fileLimitSavedItAppliesToNewUploadsExisting");
      }
    } catch {
      if (!disposed) error = m("couldNotSaveTheFileLimitCheckYourConnection");
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

<section class="server-settings" aria-labelledby="server-settings-heading">
  <h2 id="server-settings-heading">{$t(m("serverUploadLimit"))}</h2>
  <p class="muted">{$t(m("maximumSizeOfEachFileBeforeEncryptionAppliesTo"))}</p>
  <form onsubmit={save}>
    <label for="max-file-size">{$t(m("maximumFileSizeMiB"))}</label>
    <input
      id="max-file-size"
      type="number"
      min="1"
      max={ceiling === null ? undefined : ceiling / 1024 / 1024}
      step="0.01"
      bind:value={maximum}
      disabled={busy || current === null}
      required
    />
    <p class="muted small">
      {$t(m("chooseAtLeastMiBUpToTheServerCeiling"))}{$t(
        ceiling === null ? "" : m("ofValue", { arg0: fileLimitLabel(ceiling) }),
      )}. {$t(
        current !== null
          ? m("currentLimitValue", { arg0: fileLimitLabel(current) })
          : m("loadingCurrentLimit"),
      )}
    </p>
    <button class="primary" type="submit" disabled={busy || current === null}
      >{$t(busy ? m("pleaseWait") : m("saveFileLimit"))}</button
    >
    {#if current === null && !busy}<button type="button" onclick={load}
        >{$t(m("retryLoadingSettings"))}</button
      >{/if}
  </form>
  {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}
  {#if notice}<p class="success" role="status">{$t(notice)}</p>{/if}
</section>

<style>
  .server-settings {
    margin-top: 2rem;
    padding-top: 1.5rem;
    border-top: 1px solid var(--divider);
  }
  label {
    display: block;
    margin-bottom: 0.5rem;
  }
  input {
    max-width: 14rem;
  }
</style>
