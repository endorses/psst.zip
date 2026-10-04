<script lang="ts">
  import { onMount } from "svelte";
  import { accountRequest } from "$lib/account";
  import { loadServerLimits, fileLimitLabel } from "$lib/limits";
  let maximum = $state<number | undefined>(undefined),
    current = $state<number | null>(null),
    ceiling = $state<number | null>(null),
    busy = $state(false),
    error = $state(""),
    notice = $state("");
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
        error = cause instanceof Error ? cause.message : "Could not load server settings.";
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
      error = `Choose a file limit between 1 MiB and ${fileLimitLabel(ceiling ?? 0)}.`;
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
        notice = "File limit saved. It applies to new uploads; existing links stay available.";
      }
    } catch {
      if (!disposed)
        error =
          "Could not save the file limit. Check your connection and administrator session, then retry.";
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
  <h2 id="server-settings-heading">Server upload limit</h2>
  <p class="muted">
    Maximum size of each file, before encryption. Applies to account uploads and receive links.
  </p>
  <form onsubmit={save}>
    <label for="max-file-size">Maximum file size (MiB)</label>
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
      Choose at least 1 MiB, up to the server ceiling{ceiling === null
        ? ""
        : ` of ${fileLimitLabel(ceiling)}`}. {current !== null
        ? `Current limit: ${fileLimitLabel(current)}.`
        : "Loading current limit…"}
    </p>
    <button class="primary" type="submit" disabled={busy || current === null}
      >{busy ? "Please wait…" : "Save file limit"}</button
    >
    {#if current === null && !busy}<button type="button" onclick={load}
        >Retry loading settings</button
      >{/if}
  </form>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if notice}<p class="success" role="status">{notice}</p>{/if}
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
