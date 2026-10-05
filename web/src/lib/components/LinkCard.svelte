<script lang="ts">
  import { message as m, t, translate, type DisplayText } from "$lib/i18n";

  import Icon from "./Icon.svelte";
  import { brandedQr } from "$lib/branded-qr";
  let { url, label = m("shareThisLink") }: { url: string; label?: DisplayText } = $props();
  let qr = $state(""),
    message = $state<DisplayText>(""),
    field = $state<HTMLInputElement>();
  $effect(() => {
    let alive = true;
    brandedQr(url).then((value) => {
      if (alive) qr = value;
    });
    return () => {
      alive = false;
    };
  });
  async function copy() {
    try {
      await navigator.clipboard.writeText(url);
      message = m("linkCopied");
    } catch {
      field?.closest("details")?.setAttribute("open", "");
      field?.focus();
      field?.select();
      let copied = false;
      try {
        copied = document.execCommand("copy");
      } catch {}
      message = copied ? m("linkCopied") : m("linkSelectedUseYourBrowserSCopyCommand");
    }
  }
  async function share() {
    if (navigator.share) {
      try {
        await navigator.share({ title: translate(label), url });
      } catch (e) {
        if (!(e instanceof DOMException && e.name === "AbortError")) await copy();
      }
    } else await copy();
  }
</script>

<div class="link-card">
  <p>{$t(label)}</p>
  {#if qr}<img class="qr" src={qr} alt={$t(m("qrCodeForSharedLink"))} />{/if}
  <div class="link-actions">
    <button class="primary" onclick={copy}><Icon name="Copy" size={18} />{$t(m("copyLink"))}</button
    ><button onclick={share}><Icon name="Share" size={18} />{$t(m("share"))}</button>
  </div>
  <p role="status" class="small">{$t(message)}</p>
  <details>
    <summary>{$t(m("linkAndDetails"))}</summary><input
      bind:this={field}
      aria-label={$t(m("fullLink"))}
      readonly
      value={url}
      onclick={(e) => e.currentTarget.select()}
    />
  </details>
</div>

<style>
  .link-card {
    text-align: center;
  }
  .link-actions {
    justify-content: center;
  }
  .link-card p {
    margin-bottom: 0.4rem;
    overflow-wrap: anywhere;
  }
</style>
