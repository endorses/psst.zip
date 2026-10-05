<script lang="ts">
  import { message as m, t, translate } from "$lib/i18n";

  import { BRAND } from "$lib/brand";
  let { codes, onacknowledge }: { codes: string[]; onacknowledge: () => void } = $props();
  let acknowledged = $state(false),
    downloaded = $state(false);
  function save() {
    const url = URL.createObjectURL(
      new Blob(
        [
          translate(
            m("valueAdministratorRecoveryCodesEachCodeCanBeUsed", {
              arg0: BRAND,
              arg1: codes.join("\n"),
            }),
          ),
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
  <h1 id="recovery-codes-title">{$t(m("saveYourRecoveryCodes"))}</h1>
  <p>{$t(m("theseTenCodesAreShownOnceEachCanReplace"))}</p>
  <p class="notice">{$t(m("allPreviousSessionsHaveEndedSignInAgainAfter"))}</p>
  <ul class="codes">
    {#each codes as code}<li><code>{$t(code)}</code></li>{/each}
  </ul>
  <button onclick={save}>{$t(m("saveRecoveryCodesAsText"))}</button>
  {#if downloaded}<p class="muted small">{$t(m("theBrowserWasAskedToSaveAFileCheck"))}</p>{/if}
  <label class="check"
    ><input type="checkbox" bind:checked={acknowledged} />{$t(
      m("iHaveSavedTheseRecoveryCodesSecurely"),
    )}</label
  >
  <button class="primary" disabled={!acknowledged} onclick={onacknowledge}
    >{$t(m("continueToSignIn"))}</button
  >
  <p class="muted small">{$t(m("thisPageKeepsCodesInMemoryOnlyLeavingOr"))}</p>
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
