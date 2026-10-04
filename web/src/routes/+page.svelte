<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { brandedQr } from "$lib/branded-qr";
  import { page } from "$app/stores";
  import { goto } from "$app/navigation";
  import ServerSettings from "$lib/components/ServerSettings.svelte";
  import ScanPanel from "$lib/components/ScanPanel.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import LinkCard from "$lib/components/LinkCard.svelte";
  import RevokeDialog from "$lib/components/RevokeDialog.svelte";
  import { BRAND } from "$lib/brand";
  import { formatSize } from "$lib/upload-job.svelte";
  import SendPanel from "$lib/components/SendPanel.svelte";
  import {
    accountRequest as request,
    AccountError,
    loadLinks,
    saveLinks,
    type User,
    type Session,
    type Resource,
  } from "$lib/account";
  import { createSlot, getSlotInfo } from "$lib/api";
  import { generateKey, exportKey } from "$lib/crypto";
  type Tab = "Scan" | "Send" | "Receive" | "History" | "Devices" | "Account" | "Users" | "Settings";
  let receiveUnavailable = $state(false);
  let pendingFiles: File[] = [],
    pendingOwner = "";
  let selectedFiles: File[] = [];
  let restoredFiles = $state<File[]>([]);
  let sendActive = $state(false),
    showPassword = $state(false),
    historyFilter = $state("all"),
    liveMessage = $state(""),
    lastUpdated = $state(0);
  let pairingFlow = 0;
  let epoch = 0,
    polling = false,
    failures = 0;
  let labels = $state<Record<string, { title: string; size: number }>>({});
  let pairingId = $state(""),
    pairingStatus = $state("pending"),
    pairedDevice = $state("");
  const destinations = [
    "Scan",
    "Send",
    "Receive",
    "History",
    "Settings",
    "Devices",
    "Account",
    "Users",
  ];
  const mainDestinations = ["Send", "Receive", "Scan", "History", "Settings"] as const;
  const settingsDestinations = [
    {
      name: "Account",
      title: "Account",
      description: "Manage your password and account security.",
    },
    {
      name: "Devices",
      title: "Connected devices",
      description: "Connect the mobile app or manage signed-in devices.",
    },
    {
      name: "Users",
      title: "Users",
      description: "Create accounts and manage access to your server.",
    },
  ] as const;
  function destinationUrl(next: string) {
    return `/?view=${next.toLowerCase()}${next === "Receive" && receiveId ? `&slot=${encodeURIComponent(receiveId)}` : ""}`;
  }
  function routeTab(): Tab {
    return (destinations.find((v) => v.toLowerCase() === $page.url.searchParams.get("view")) ||
      "Send") as Tab;
  }
  $effect(() => {
    const next = routeTab();
    const slot = $page.url.searchParams.get("slot");
    untrack(() => {
      if (user && (next !== tab || (next === "Receive" && slot && slot !== receiveId)))
        void select(next, false);
    });
  });
  let loading = $state(true),
    setupRequired = $state(false),
    busy = $state(false);
  let user = $state<User | null>(null),
    tab = $state<Tab>("Send");
  let username = $state(""),
    password = $state(""),
    error = $state(""),
    notice = $state("");
  let users = $state<User[]>([]),
    sessions = $state<Session[]>([]),
    transfers = $state<Resource[]>([]),
    slots = $state<Resource[]>([]);
  let links = $state<Record<string, string>>({});
  let receiveUrl = $state(""),
    receiveId = $state("");
  let pairingQr = $state(""),
    pairingExpires = $state(""),
    now = $state(Date.now());
  let newUsername = $state(""),
    newPassword = $state(""),
    newRole = $state("user"),
    resetId = $state(""),
    resetPassword = $state("");
  let currentPassword = $state(""),
    changedPassword = $state("");
  let allResources = $state(false);
  let pendingDelete = $state<{ id: string; kind: "transfers" | "slots" } | null>(null);
  let received = $state<{ id: string; url: string; count: number }[]>([]);
  onMount(() => {
    void initialize();
    let timerPoll: ReturnType<typeof setTimeout>;
    let stopped = false;
    async function poll() {
      if (stopped) return;
      if (user && !document.hidden && !polling) {
        polling = true;
        const owner = epoch;
        try {
          const identity = await request<{ user: User }>("/auth/me");
          if (owner !== epoch) throw new Error("Account changed");
          if (identity.user.id !== user?.id) {
            clearAccount();
            error = "Your account changed in another tab. Sign in again.";
            throw new Error("Account changed");
          }
          if (tab === "History") await refreshHistory(owner);
          if (tab === "Receive" && receiveId && !receiveUnavailable)
            await refreshReceived(receiveId, owner);
          if (tab === "Devices" && pairingId && pairingStatus === "pending") {
            const activePairing = pairingId;
            const result = await request<{ status: string; device_name?: string }>(
              `/auth/pairings/${activePairing}`,
            );
            if (owner === epoch && activePairing === pairingId) {
              pairingStatus = result.status;
              if (result.status !== "pending") pairingQr = "";
              if (result.status === "connected") {
                pairedDevice = result.device_name || "Phone";
                const devices = await request<{ sessions: Session[] }>("/auth/sessions");
                if (owner === epoch) sessions = devices.sessions;
              }
            }
          }
          if (owner === epoch) {
            failures = 0;
            lastUpdated = Date.now();
            liveMessage = "";
          }
        } catch (err) {
          if (owner === epoch) {
            failures++;
            liveMessage = `Offline — last updated ${lastUpdated ? new Date(lastUpdated).toLocaleTimeString() : "not yet"}. Reconnecting…`;
            if (err instanceof AccountError && err.status === 401) {
              clearAccount(true);
              error = "Your session ended. Sign in again.";
            }
          }
        } finally {
          polling = false;
        }
      }
      timerPoll = setTimeout(poll, Math.min(30000, 3000 * 2 ** Math.min(failures, 3)));
    }
    timerPoll = setTimeout(poll, 3000);
    function resume() {
      if (!document.hidden && !polling) {
        clearTimeout(timerPoll);
        void poll();
      }
    }
    function changed(event: StorageEvent) {
      if (event.key === "psst.auth-change" && user) {
        clearAccount();
        error = "Your sign-in changed in another tab. Sign in again.";
      }
    }
    document.addEventListener("visibilitychange", resume);
    window.addEventListener("storage", changed);
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => {
      stopped = true;
      document.removeEventListener("visibilitychange", resume);
      window.removeEventListener("storage", changed);
      clearInterval(timer);
      clearTimeout(timerPoll);
      void cancelPair();
      epoch++;
    };
  });
  async function initialize() {
    try {
      user = (await request<{ user: User }>("/auth/me")).user;
      loadAccount();
      await select(routeTab(), false);
    } catch (err) {
      if (!(err instanceof AccountError && err.status === 401)) error = message(err);
      try {
        setupRequired = (await request<{ setup_required: boolean }>("/auth/status")).setup_required;
      } catch {
        /* Main error remains visible. */
      }
    } finally {
      loading = false;
    }
  }
  function loadAccount() {
    if (!user) return;
    links = loadLinks(user.id);
    try {
      labels = JSON.parse(localStorage.getItem(`psst.labels.${user.id}`) || "{}");
    } catch {
      labels = {};
    }
  }
  async function refreshHistory(owner = epoch) {
    const result = await request<{ transfers: Resource[]; slots: Resource[] }>(
      `/auth/resources${allResources ? "?all=true" : ""}`,
    );
    if (owner !== epoch) return;
    transfers = result.transfers ?? [];
    slots = result.slots ?? [];
  }
  async function refreshReceived(id: string, owner = epoch) {
    let slot;
    try {
      slot = await getSlotInfo(id);
    } catch (err) {
      if (owner === epoch && err instanceof Error && /API (404|410):/.test(err.message)) {
        receiveUnavailable = true;
        received = [];
        return;
      }
      throw err;
    }
    if (owner !== epoch || id !== receiveId) return;
    const key = links[id] ? new URL(links[id]).hash : "";
    received = slot.transfers
      .filter((t) => t.status === "complete")
      .map((t) => ({
        id: t.transfer_id,
        count: t.file_count,
        url: `${location.origin}/d/${t.transfer_id}${key}`,
      }));
  }
  async function cancelPair() {
    pairingFlow++;
    const id = pairingId;
    pairingId = "";
    pairingQr = "";
    pairingStatus = "canceled";
    if (id)
      try {
        await request(`/auth/pairings/${id}`, "DELETE");
      } catch (err) {
        if (!(err instanceof AccountError && err.status === 409))
          error =
            "Could not cancel the code. It will expire automatically; retry from device settings.";
      }
  }
  function message(err: unknown) {
    return err instanceof Error ? err.message : "Something went wrong. Please try again.";
  }
  function clearAccount(preserveSelection = false) {
    if (preserveSelection && user) {
      pendingOwner = user.id;
      pendingFiles = [...selectedFiles];
    } else {
      pendingOwner = "";
      pendingFiles = [];
    }
    restoredFiles = [];
    selectedFiles = [];
    epoch++;
    void cancelPair();
    user = null;
    labels = {};
    sendActive = false;
    receiveId = "";
    pendingDelete = null;
    liveMessage = "";
    password = "";
    currentPassword = "";
    changedPassword = "";
    newPassword = "";
    resetPassword = "";
    pairingQr = "";
    receiveUrl = "";
    links = {};
    received = [];
    transfers = [];
    slots = [];
    sessions = [];
    users = [];

    allResources = false;
  }
  async function act(action: () => Promise<void>) {
    if (busy) return;
    busy = true;
    error = "";
    notice = "";
    try {
      await action();
    } catch (err) {
      error = message(err);
      if (err instanceof AccountError && err.status === 401 && user) {
        clearAccount(true);
        error = "Your session ended. Sign in again.";
      }
    } finally {
      busy = false;
    }
  }
  async function login() {
    await act(async () => {
      user = (
        await request<{ user: User }>("/auth/login", "POST", {
          username,
          password,
          device_name: "Web browser",
          session_type: "web",
        })
      ).user;
      try {
        localStorage.setItem("psst.auth-change", String(Date.now()));
      } catch {}
      epoch++;
      restoredFiles = pendingOwner === user.id ? pendingFiles : [];
      pendingFiles = [];
      pendingOwner = "";
      loadAccount();
    });
    if (user) await select(routeTab(), false);
    password = "";
  }
  function remember(id: string, url: string, title?: string, size?: number) {
    if (title && user) {
      labels = { ...labels, [id]: { title, size: size ?? 0 } };
      try {
        localStorage.setItem(`psst.labels.${user.id}`, JSON.stringify(labels));
      } catch {}
    }
    links = { ...links, [id]: url };
    if (user) saveLinks(user.id, links);
  }
  async function authorizeScanner(): Promise<boolean> {
    const owner = epoch;
    try {
      const identity = await request<{ user: User }>("/auth/me");
      if (owner !== epoch || tab !== "Scan" || !user) return false;
      if (identity.user.id !== user.id) {
        clearAccount();
        error = "Your account changed. Sign in again.";
        return false;
      }
      return true;
    } catch (err) {
      if (owner === epoch) {
        if (err instanceof AccountError && err.status === 401) {
          clearAccount();
          error = "Your session ended. Sign in again.";
        } else error = "Could not verify your session. Check your connection and try again.";
      }
      return false;
    }
  }
  async function select(next: Tab, navigate = true) {
    if (next === "Users" && user?.role !== "admin") next = "Settings";
    if (tab === "Devices" && next !== "Devices") await cancelPair();
    tab = next;
    if (next === "Receive") {
      const id = $page.url.searchParams.get("slot");
      if (id && links[id]) {
        receiveId = id;
        receiveUrl = links[id];
        receiveUnavailable = false;
      }
    }
    pendingDelete = null;
    if (navigate)
      await goto(
        `/?view=${next.toLowerCase()}${next === "Receive" && receiveId ? `&slot=${encodeURIComponent(receiveId)}` : ""}`,
        { keepFocus: true, noScroll: true },
      );
    await act(async () => {
      const owner = epoch;
      if (next === "History") await refreshHistory(owner);
      if (next === "Devices") {
        const result = await request<{ sessions: Session[] }>("/auth/sessions");
        if (owner === epoch) sessions = result.sessions;
      }
      if (next === "Users") {
        const result = await request<{ users: User[] }>("/admin/users");
        if (owner === epoch) users = result.users;
      }
    });
  }
  async function createReceive() {
    await act(async () => {
      const owner = epoch;
      const key = await exportKey(await generateKey());
      const slot = await createSlot();
      if (owner !== epoch) return;
      receiveUnavailable = false;
      receiveId = slot.id;
      received = [];
      receiveUrl = `${location.origin}/u/${slot.id}#${key}`;
      remember(slot.id, receiveUrl);
      await goto(`/?view=receive&slot=${encodeURIComponent(slot.id)}`, {
        keepFocus: true,
        noScroll: true,
      });
    });
  }
  async function checkReceived(id: string) {
    await act(async () => {
      receiveId = id;
      receiveUrl = links[id] || "";
      await refreshReceived(id);
      if (!received.length) notice = "Waiting for files. This view updates automatically.";
    });
  }
  async function openReceive(id: string) {
    receiveUnavailable = false;
    receiveId = id;
    receiveUrl = links[id] || "";
    received = [];
    await select("Receive");
    await checkReceived(id);
  }
  async function revoke() {
    if (!pendingDelete) return;
    const target = pendingDelete;
    await act(async () => {
      await request(`/${target.kind}/${target.id}`, "DELETE");
      transfers = transfers.filter((t) => t.id !== target.id);
      slots = slots.filter((s) => s.id !== target.id);
      delete links[target.id];
      links = { ...links };
      if (user) saveLinks(user.id, links);
      pendingDelete = null;
      notice = "Link revoked and server files deleted.";
    });
  }
  async function pair() {
    await act(async () => {
      const owner = epoch,
        flow = ++pairingFlow;
      const result = await request<{ id: string; code: string; expires_at: string }>(
        "/auth/pairings",
        "POST",
        pairingId && (pairingStatus === "pending" || pairingStatus === "expired")
          ? { replace_id: pairingId }
          : {},
      );
      if (owner !== epoch || flow !== pairingFlow) {
        try {
          await request(`/auth/pairings/${result.id}`, "DELETE");
        } catch {}
        return;
      }
      pairingId = result.id;
      pairingStatus = "pending";
      pairedDevice = "";
      pairingExpires = result.expires_at;
      const qr = await brandedQr(
        JSON.stringify({
          type: "psst-pairing",
          version: 1,
          server_url: location.origin,
          code: result.code,
        }),
      );
      if (owner === epoch && flow === pairingFlow) pairingQr = qr;
    });
  }
  async function copy(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      notice = "Link copied.";
      return;
    } catch {
      /* LAN HTTP does not expose the Clipboard API. */
    }
    const field = document.createElement("textarea");
    field.value = value;
    field.style.position = "fixed";
    field.style.opacity = "0";
    document.body.append(field);
    field.select();
    let copied = false;
    try {
      copied = document.execCommand("copy");
    } catch {
      /* Show a selectable link below. */
    }
    field.remove();
    notice = copied ? "Link copied." : `Copy this link: ${value}`;
  }
  function date(value: string) {
    const remaining = Date.parse(value) - now;
    return remaining <= 0
      ? "Expired"
      : remaining < 3600000
        ? `in ${Math.ceil(remaining / 60000)} minutes`
        : `in ${Math.ceil(remaining / 3600000)} hours`;
  }
  function status(item: Resource) {
    if (Date.parse(item.expires_at) < now) return "Expired";
    return item.downloaded_at
      ? "Downloaded"
      : item.status === "complete"
        ? item.download_count
          ? "Download started"
          : "Ready to download"
        : item.status === "pending"
          ? "Upload unfinished"
          : item.status === "revoked"
            ? "Revoked"
            : item.status || "Ready";
  }
</script>

<svelte:head><title>{user ? tab : "Sign in"} · {BRAND}</title></svelte:head>
{#if loading}<p role="status">Loading your account…</p>
{:else if !user}
  <section class="panel login">
    <div class="login-mark"><Icon name="Shield" size={26} /></div>
    <h1>Sign in to {BRAND}</h1>
    <p class="muted login-intro">Your private space to send and receive files.</p>
    {#if setupRequired}<p class="notice">
        This server needs its first administrator. The server operator must configure ADMIN_USERNAME
        and ADMIN_PASSWORD, then restart the server.
      </p>
    {:else}<form
        onsubmit={(e) => {
          e.preventDefault();
          void login();
        }}
      >
        <label for="login-username">Username</label>
        <input
          id="login-username"
          name="username"
          bind:value={username}
          autocomplete="username"
          autocapitalize="none"
          spellcheck={false}
          required
          disabled={busy}
        />
        <label for="login-password">Password</label>
        <div class="password-field">
          <input
            id="login-password"
            name="password"
            bind:value={password}
            type={showPassword ? "text" : "password"}
            autocomplete="current-password"
            required
            disabled={busy}
          />
          <button
            class="password-visibility"
            type="button"
            aria-label={showPassword ? "Hide password" : "Show password"}
            aria-pressed={showPassword}
            aria-controls="login-password"
            onclick={() => (showPassword = !showPassword)}
            ><Icon name={showPassword ? "EyeOff" : "Eye"} /></button
          >
        </div>
        <button class="primary login-submit" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}<Icon name="Arrow" size={18} />
        </button>
      </form>
      <p class="muted small login-help">
        Need an account or password reset?<br />Contact your server administrator.
      </p>{/if}
    <p class="muted small login-note">People using your shared links do not need an account.</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}{#if notice}<p
        class="notice"
        role="status"
      >
        {notice}
      </p>{/if}
  </section>
{:else}
  <div class="workspace">
    <aside class="sidebar">
      <p class="sidebar-label">Your workspace</p>
      <nav aria-label="Account navigation">
        {#each mainDestinations as item}
          <a
            href={destinationUrl(item)}
            aria-current={tab === item
              ? "page"
              : item === "Settings" && ["Account", "Devices", "Users"].includes(tab)
                ? "location"
                : undefined}
            class:active={tab === item ||
              (item === "Settings" && ["Account", "Devices", "Users"].includes(tab))}
            data-sveltekit-keepfocus
            data-sveltekit-noscroll
          >
            <Icon name={item === "Scan" ? "QRCode" : item} /><span
              >{item === "Scan" ? "Scan QR code" : item}</span
            >
          </a>
        {/each}
      </nav>
      <div class="account-bar">
        <span class="avatar" aria-hidden="true">{user.username.slice(0, 1).toUpperCase()}</span>
        <div class="identity">
          <span class="small muted">Signed in as</span><strong>{user.username}</strong>
        </div>
        <button
          class="sign-out"
          disabled={busy}
          onclick={() =>
            act(async () => {
              await cancelPair();
              await request("/auth/logout", "POST");
              try {
                localStorage.setItem("psst.auth-change", String(Date.now()));
              } catch {}
              clearAccount();
            })}><Icon name="SignOut" size={17} />Sign out</button
        >
      </div>
    </aside>
    <div class="workspace-content">
      {#if error}<p class="error" role="alert">{error}</p>{/if}{#if notice}<p
          class="notice"
          role="status"
        >
          {notice}
        </p>{/if}
      {#if sendActive && tab !== "Send"}<p class="notice">
          <button onclick={() => select("Send")}>Return to your transfer</button> Your selected files
          and upload stay here.
        </p>{/if}
      {#if liveMessage && (tab === "History" || tab === "Receive" || tab === "Devices")}<p
          role="status"
          class="notice"
        >
          {liveMessage}
        </p>{/if}
      <section class="workspace-panel">
        {#key user.id}<div hidden={tab !== "Send"}>
            <SendPanel
              accountId={user.id}
              initialFiles={restoredFiles}
              onselection={(files) => (selectedFiles = files)}
              oncreated={remember}
              onactive={(value) => (sendActive = value)}
            />
          </div>{/key}
        {#if tab === "Scan"}<ScanPanel authorize={authorizeScanner} />{/if}
        {#if tab === "Settings"}<h1>Settings</h1>
          <p class="muted">Your account, devices, and server access.</p>
          <div class="settings-list">
            {#each settingsDestinations.filter((item) => item.name !== "Users" || user?.role === "admin") as item}
              <a
                href={destinationUrl(item.name)}
                aria-label={item.title}
                data-sveltekit-keepfocus
                data-sveltekit-noscroll
              >
                <span class="setting-icon"><Icon name={item.name} size={22} /></span>
                <span
                  ><strong>{item.title}</strong><span class="muted small setting-description"
                    >{item.description}</span
                  ></span
                >
                <Icon name="Arrow" />
              </a>
            {/each}
          </div>
          {#if user.role === "admin"}<ServerSettings />{/if}
        {:else if tab === "Receive"}<h1>Receive files</h1>
          <p class="muted">
            Anyone with your receive link can send you encrypted files within the server’s limits.
          </p>
          {#if receiveUnavailable}<p class="notice" role="status">
              This receive link has expired or was revoked. Create a new link to get more files.
            </p>{:else if receiveUrl}<LinkCard
              url={receiveUrl}
              label="Receive link — share it with someone to get files."
            /><button disabled={busy} onclick={() => checkReceived(receiveId)}
              ><Icon name="Refresh" size={17} />Refresh received files</button
            >
            <p class="muted small" role="status">
              {received.length
                ? `${received.reduce((n, t) => n + t.count, 0)} files received. Ready to save below.`
                : "Waiting for files. Arrivals update automatically."}
            </p>{/if}
          <button class="primary" disabled={busy} onclick={createReceive}
            >{receiveUrl
              ? "Create another receive link"
              : busy
                ? "Creating link…"
                : "Create receive link"}</button
          >
        {:else if tab === "History"}<div class="heading">
            <h1>{allResources ? "All server resources" : "Your transfers"}</h1>
            <button disabled={busy} onclick={() => select("History")}
              ><Icon name="Refresh" size={17} />Refresh</button
            >
          </div>
          <label
            >Show<select bind:value={historyFilter}
              ><option value="all">All transfers</option><option value="transfers">Sent</option
              ><option value="slots">Receive links</option></select
            ></label
          >
          {#if user.role === "admin"}
            <label class="toggle"
              ><input
                type="checkbox"
                bind:checked={allResources}
                disabled={busy}
                onchange={() => select("History")}
              /> All server resources</label
            >
          {/if}
          <p class="muted small">
            Encryption keys stay on the device that created the link. This browser can reopen its
            own links; transfers from other devices can still be revoked.
          </p>
          {#if !transfers.length && !slots.length}<p class="empty">No transfers yet.</p>{/if}
          {#each [...transfers.map( (t) => ({ ...t, kind: "transfers" as const }), ), ...slots.map( (s) => ({ ...s, kind: "slots" as const }), )].filter((item) => historyFilter === "all" || item.kind === historyFilter) as item}<article
              class="resource"
              data-resource-id={item.id}
            >
              <div>
                <strong
                  >{labels[item.id]?.title ||
                    (item.kind === "slots" ? "Receive link" : "Sent files")}</strong
                >
                <p>
                  {item.kind === "slots"
                    ? `${item.transfers?.reduce((n, t) => n + t.file_count, 0) ?? item.file_count ?? 0} files received`
                    : `${item.file_count ?? 0} files · ${status(item)}`}
                </p>
                <p class="muted small">
                  {labels[item.id]
                    ? formatSize(labels[item.id].size) + " · "
                    : item.total_size
                      ? formatSize(item.total_size) + " stored · "
                      : ""}Expires {date(item.expires_at)}
                </p>
                {#if !links[item.id]}<p class="muted small">
                    This device has no encryption key. Use the device that created the link to open
                    or share it. You can still revoke it here.
                  </p>{/if}
              </div>
              <div class="actions">
                {#if links[item.id]}<button onclick={() => copy(links[item.id])}
                    ><Icon name="Copy" size={17} />Copy link</button
                  >{#if item.kind === "transfers"}<a class="button" href={links[item.id]}>Open</a
                    >{:else}<button disabled={busy} onclick={() => openReceive(item.id)}
                      >View files</button
                    >{/if}{/if}<button
                  class="danger"
                  disabled={busy}
                  onclick={() => {
                    error = "";
                    pendingDelete = { id: item.id, kind: item.kind };
                  }}><Icon name="Revoke" size={17} />Revoke</button
                >
              </div>
            </article>{/each}
          {#if pendingDelete}
            <RevokeDialog
              {busy}
              {error}
              oncancel={() => (pendingDelete = null)}
              onconfirm={revoke}
            />
          {/if}
        {:else if tab === "Devices"}<a class="back-link" href="/?view=settings"
            >← Back to Settings</a
          >
          <h1>Connect mobile app</h1>
          <p class="muted">
            In the app’s server settings, choose Scan login QR code. Keep this code private: it
            signs the scanning device in as you.
          </p>
          {#if pairingStatus === "connected"}<p class="notice" role="status">
              Phone connected: {pairedDevice}
            </p>
          {:else if pairingQr && now < Date.parse(pairingExpires)}<section
              aria-label="Connect mobile app"
            >
              <img class="qr" src={pairingQr} alt="Mobile app login QR code" />
              <p>
                Single use · expires in {Math.max(
                  0,
                  Math.ceil((Date.parse(pairingExpires) - now) / 1000),
                )} seconds
              </p>
              <button onclick={cancelPair}>Cancel pairing</button>
            </section>
          {:else if pairingId}<p class="notice" role="status">
              This code has expired. Generate a new code.
            </p>{/if}
          <button class="primary" disabled={busy} onclick={pair}
            ><Icon name="QRCode" size={18} />{pairingId
              ? "Generate new code"
              : "Show login QR code"}</button
          >
          <h2>Connected devices</h2>
          <p class="muted">Revoke a session to sign that device out.</p>
          {#each sessions as session}<article class="resource">
              <div>
                <strong
                  >{session.device_name || "Device"}{session.current
                    ? " (this browser)"
                    : ""}</strong
                >
                <p class="muted small">Expires {date(session.expires_at)}</p>
              </div>
              <button
                class="danger"
                disabled={busy}
                onclick={() =>
                  act(async () => {
                    await request(`/auth/sessions/${session.id}`, "DELETE");
                    if (session.current) clearAccount();
                    else sessions = sessions.filter((s) => s.id !== session.id);
                  })}>Revoke session</button
              >
            </article>{/each}
        {:else if tab === "Account"}<a class="back-link" href="/?view=settings"
            >← Back to Settings</a
          >
          <h1>Change password</h1>
          <p class="muted">Changing your password signs out all devices, including this browser.</p>
          <form
            onsubmit={(e) => {
              e.preventDefault();
              void act(async () => {
                await request("/auth/password", "POST", {
                  current_password: currentPassword,
                  password: changedPassword,
                });
                clearAccount();
                notice = "Password changed. Sign in with your new password.";
              });
            }}
          >
            <label
              >Current password<input
                type="password"
                autocomplete="current-password"
                required
                bind:value={currentPassword}
              /></label
            ><label
              >New password<input
                type="password"
                autocomplete="new-password"
                minlength="12"
                required
                bind:value={changedPassword}
              /></label
            >
            <p class="muted small">Use at least 12 characters (up to 72 UTF-8 bytes).</p>
            <button class="primary" disabled={busy}>Change password</button>
          </form>
        {:else if tab === "Users"}<a class="back-link" href="/?view=settings">← Back to Settings</a>
          <h1>Manage users</h1>
          <p class="muted">
            Only administrators can create accounts. Disabling an account signs out its devices.
          </p>
          {#each users as account}<article class="resource">
              <div>
                <strong>{account.username}</strong>
                <p class="muted small">
                  {account.role === "admin" ? "Administrator" : "User"} · {account.disabled
                    ? "Disabled"
                    : "Active"}
                </p>
              </div>
              <div class="actions">
                <button
                  disabled={busy}
                  onclick={() => {
                    resetId = account.id;
                    resetPassword = "";
                  }}>Reset password</button
                ><button
                  disabled={busy || account.id === user.id}
                  onclick={() =>
                    act(async () => {
                      const updated = await request<{ user: User }>(
                        `/admin/users/${account.id}`,
                        "PATCH",
                        { disabled: !account.disabled },
                      );
                      users = users.map((u) => (u.id === updated.user.id ? updated.user : u));
                    })}>{account.disabled ? "Enable" : "Disable"}</button
                >
              </div>
            </article>{/each}
          {#if resetId}<form
              class="confirm"
              onsubmit={(e) => {
                e.preventDefault();
                void act(async () => {
                  await request(`/admin/users/${resetId}`, "PATCH", { password: resetPassword });
                  const self = resetId === user?.id;
                  resetId = "";
                  resetPassword = "";
                  if (self) clearAccount();
                  else notice = "Password reset. Existing sessions have been revoked.";
                });
              }}
            >
              <label
                >New password for {users.find((u) => u.id === resetId)?.username}<input
                  type="password"
                  autocomplete="new-password"
                  minlength="12"
                  required
                  bind:value={resetPassword}
                /></label
              ><button class="primary" disabled={busy}>Save new password</button><button
                type="button"
                onclick={() => (resetId = "")}>Cancel</button
              >
            </form>{/if}
          <h2>Create account</h2>
          <form
            onsubmit={(e) => {
              e.preventDefault();
              void act(async () => {
                const result = await request<{ user: User }>("/admin/users", "POST", {
                  username: newUsername,
                  password: newPassword,
                  role: newRole,
                });
                users = [...users, result.user];
                newUsername = "";
                newPassword = "";
                notice = "Account created. Share the credentials privately with its owner.";
              });
            }}
          >
            <label>New username<input autocomplete="off" required bind:value={newUsername} /></label
            ><label
              >Temporary password<input
                type="password"
                autocomplete="new-password"
                minlength="12"
                required
                bind:value={newPassword}
              /></label
            ><label
              >Role<select bind:value={newRole}
                ><option value="user">User</option><option value="admin">Administrator</option
                ></select
              ></label
            >
            <p class="muted small">
              Use at least 12 characters. Users can change their password after signing in.
            </p>
            <button class="primary" disabled={busy}>Create account</button>
          </form>
        {/if}
        {#if (tab === "Receive" || tab === "History") && received.length}<h2>Received files</h2>
          {#each received as item}<a class="received" href={item.url}
              >{item.count} file{item.count === 1 ? "" : "s"} · Save files</a
            >{/each}{/if}
      </section>
    </div>
  </div>
{/if}

<style>
  .login {
    max-width: 440px;
    margin: clamp(1rem, 5vh, 3rem) auto;
    padding: clamp(1.5rem, 4vw, 2.5rem);
    border-radius: 20px;
  }
  .login-mark {
    width: 52px;
    height: 52px;
    display: grid;
    place-items: center;
    border-radius: 15px;
    background: var(--accent);
    color: var(--primary);
    margin-bottom: 1.5rem;
  }
  .login h1 {
    font-size: clamp(1.6rem, 3vw, 1.9rem);
    margin-bottom: 0.65rem;
  }
  .login-intro {
    margin-bottom: 1.75rem;
    font-size: 0.95rem;
  }
  .login form {
    gap: 0.5rem;
    margin: 0;
  }
  .login label {
    font-size: 0.85rem;
    font-weight: 550;
  }
  .login label:not(:first-child) {
    margin-top: 0.6rem;
  }
  .login input {
    min-height: 48px;
    border-radius: 10px;
    padding-inline: 0.8rem;
  }
  .password-field {
    position: relative;
  }
  .password-field input {
    padding-right: 3.4rem;
  }
  .password-visibility {
    position: absolute;
    right: 2px;
    top: 50%;
    transform: translateY(-50%);
    width: 44px;
    height: 44px;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--muted);
  }
  .password-visibility:active:not(:disabled) {
    transform: translateY(-50%);
  }
  .login-submit {
    margin-top: 1rem;
    min-height: 48px;
    gap: 0.65rem;
  }
  .login-help {
    text-align: center;
    margin: 1.5rem 0 0;
    font-size: 0.8rem;
  }
  .login-note {
    border-top: 1px solid var(--divider);
    padding-top: 1.25rem;
    margin: 1.25rem 0 0;
    text-align: center;
    font-size: 0.78rem;
  }
  .workspace {
    display: grid;
    grid-template-columns: 200px minmax(0, 1fr);
    gap: clamp(2rem, 5vw, 4.5rem);
    align-items: start;
  }
  .sidebar {
    position: sticky;
    top: 2rem;
  }
  .sidebar-label {
    font-size: 0.7rem;
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--muted);
    margin: 0.2rem 0.8rem 1rem;
  }
  nav {
    display: grid;
    gap: 0.35rem;
  }
  nav a {
    min-height: 48px;
    display: flex;
    gap: 0.8rem;
    align-items: center;
    padding: 0.65rem 0.85rem;
    border-radius: 10px;
    color: var(--muted);
    text-decoration: none;
    font-weight: 550;
  }
  nav a:hover {
    background: var(--hover);
    color: var(--text);
  }
  nav a.active {
    background: var(--accent);
    color: var(--primary);
    box-shadow: inset 3px 0 var(--primary);
  }
  .account-bar {
    display: grid;
    grid-template-columns: 34px minmax(0, 1fr);
    align-items: center;
    gap: 0.65rem;
    border-top: 1px solid var(--divider);
    padding: 1.25rem 0.6rem 0;
    margin-top: 2rem;
  }
  .avatar {
    background: var(--elevated);
    border-radius: 50%;
    width: 34px;
    height: 34px;
    display: grid;
    place-items: center;
    font-size: 0.85rem;
    font-weight: 650;
  }
  .identity {
    display: grid;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .identity .small {
    font-size: 0.72rem;
  }
  .identity strong {
    font-size: 0.9rem;
  }
  .sign-out {
    grid-column: 2;
    justify-self: start;
    border: 0;
    background: transparent;
    color: var(--muted);
    padding: 0.5rem 0.75rem;
    min-height: 44px;
    font-size: 0.85rem;
  }
  .workspace-content {
    min-width: 0;
  }
  .workspace-panel {
    min-width: 0;
    padding: 0;
  }
  .workspace-panel > :global(h1),
  .workspace-panel > :global(div > h1) {
    margin-bottom: 0.65rem;
  }
  .heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1rem;
  }
  .heading h1 {
    margin-bottom: 0;
  }
  .settings-list {
    display: grid;
    margin-top: 2rem;
  }
  .settings-list a {
    display: grid;
    grid-template-columns: 44px minmax(0, 1fr) 20px;
    align-items: center;
    gap: 1rem;
    padding: 1.25rem 0;
    border-bottom: 1px solid var(--divider);
    color: var(--text);
    text-decoration: none;
  }
  .settings-list a:hover {
    color: var(--primary);
  }
  .setting-icon {
    display: grid;
    place-items: center;
    width: 44px;
    height: 44px;
    border-radius: 12px;
    background: var(--elevated);
    color: var(--primary);
  }
  .setting-description {
    display: block;
    margin-top: 0.3rem;
  }
  .back-link {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
    margin-bottom: 1rem;
    font-size: 0.85rem;
    text-decoration: none;
  }
  .resource {
    padding: 1rem 0;
    border-bottom: 1px solid var(--divider);
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    align-items: center;
    overflow-wrap: anywhere;
  }
  .resource p {
    margin: 0.2rem 0;
  }
  .resource .actions {
    flex-shrink: 0;
  }
  .confirm {
    padding: 1rem;
    border: 1px solid var(--error);
    border-radius: 8px;
    margin: 1rem 0;
  }
  .confirm button {
    margin: 0.5rem;
  }
  .toggle {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin: 1rem 0;
  }
  .empty {
    text-align: center;
    padding: 2rem 0;
    color: var(--muted);
  }
  .received {
    display: block;
    padding: 0.75rem 0;
    min-height: 44px;
  }
  @media (max-width: 760px) {
    .workspace {
      display: block;
    }
    .sidebar {
      position: static;
      margin-bottom: 2rem;
      display: flex;
      flex-direction: column-reverse;
    }
    .sidebar-label,
    .avatar {
      display: none;
    }
    nav {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(min(100%, 4.3rem), 1fr));
      border-bottom: 1px solid var(--divider);
      gap: 0;
    }
    nav a {
      justify-content: center;
      flex-wrap: wrap;
      gap: 0.35rem;
      padding: 0.75rem 0.25rem;
      border-radius: 0;
      font-size: 0.85rem;
    }
    nav a.active {
      background: transparent;
      box-shadow: inset 0 -3px var(--primary);
    }
    .account-bar {
      display: flex;
      justify-content: space-between;
      margin: 0 0 0.75rem;
      padding: 0;
      border: 0;
      gap: 1rem;
    }
    .identity {
      display: flex;
      flex-wrap: wrap;
      gap: 0.35rem;
      align-items: baseline;
    }
    .identity .small {
      font-size: 0.8rem;
    }
  }
  @media (max-width: 540px) {
    .resource {
      flex-direction: column;
      align-items: stretch;
    }
    .panel {
      padding: 1rem;
    }

    .resource .actions {
      flex-shrink: 1;
    }
  }
</style>
