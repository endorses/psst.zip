<script lang="ts">
  import { BRAND } from "$lib/brand";
  let { codes, onacknowledge }: { codes: string[]; onacknowledge: () => void } = $props();
  let acknowledged = $state(false),
    downloaded = $state(false);
  function save() {
    const url = URL.createObjectURL(
      new Blob(
        [
          `${BRAND} administrator recovery codes\nEach code can be used once. Keep these private and separate from your authenticator.\n\n${codes.join("\n")}\n`,
        ],
        { type: "text/plain;charset=utf-8" },
      ),
    );
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "psst.zip-recovery-codes.txt";
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
    downloaded = true;
  }
</script>

<section class="panel" aria-labelledby="recovery-codes-title">
  <h1 id="recovery-codes-title">Save your recovery codes</h1>
  <p>
    These ten codes are shown once. Each can replace an authenticator code for one sign-in or
    identity check. Keep them somewhere private, separate from your authenticator.
  </p>
  <p class="notice">
    All previous sessions have ended. Sign in again after saving these codes. Changing or
    regenerating codes invalidates the old set.
  </p>
  <ul class="codes">
    {#each codes as code}<li><code>{code}</code></li>{/each}
  </ul>
  <button onclick={save}>Save recovery codes as text</button>
  {#if downloaded}<p class="muted small">
      The browser was asked to save a file. Check that it was saved successfully.
    </p>{/if}
  <label class="check"
    ><input type="checkbox" bind:checked={acknowledged} />I have saved these recovery codes
    securely.</label
  >
  <button class="primary" disabled={!acknowledged} onclick={onacknowledge}
    >Continue to sign in</button
  >
  <p class="muted small">
    This page keeps codes in memory only. Leaving or reloading it discards them. The server retains
    hashes, so these codes cannot be displayed again.
  </p>
</section>

<style>
  .codes {
    padding: 1rem;
    background: var(--elevated);
    list-style: none;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 14rem), 1fr));
    gap: 0.5rem;
  }
  code {
    overflow-wrap: anywhere;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    margin: 1rem 0;
  }
  .check input {
    width: auto;
  }
</style>
