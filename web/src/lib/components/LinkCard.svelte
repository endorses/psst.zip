<script lang="ts">
  import Icon from "./Icon.svelte";
  import QRCode from "qrcode";
  let { url, label = "Share this link" }: { url: string; label?: string } = $props();
  let qr = $state(""),
    message = $state(""),
    field = $state<HTMLInputElement>();
  $effect(() => {
    let alive = true;
    QRCode.toDataURL(url, {
      scale: 8,
      margin: 4,
      color: { dark: "#172B2A", light: "#FFFFFF" },
    }).then((value) => {
      if (alive) qr = value;
    });
    return () => {
      alive = false;
    };
  });
  async function copy() {
    try {
      await navigator.clipboard.writeText(url);
      message = "Link copied.";
    } catch {
      field?.closest("details")?.setAttribute("open", "");
      field?.focus();
      field?.select();
      let copied = false;
      try {
        copied = document.execCommand("copy");
      } catch {}
      message = copied ? "Link copied." : "Link selected. Use your browser’s Copy command.";
    }
  }
  async function share() {
    if (navigator.share) {
      try {
        await navigator.share({ title: label, url });
      } catch (e) {
        if (!(e instanceof DOMException && e.name === "AbortError")) await copy();
      }
    } else await copy();
  }
</script>

<div class="link-card">
  <p>{label}</p>
  {#if qr}<img class="qr" src={qr} alt="QR code for shared link" />{/if}
  <div class="link-actions">
    <button class="primary" onclick={copy}><Icon name="Copy" size={18} />Copy link</button><button
      onclick={share}><Icon name="Share" size={18} />Share</button
    >
  </div>
  <p role="status" class="small">{message}</p>
  <details>
    <summary>Link and details</summary><input
      bind:this={field}
      aria-label="Full link"
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
  }
</style>
