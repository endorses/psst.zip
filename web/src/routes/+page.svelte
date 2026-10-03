<script lang="ts">
  import { onMount, untrack } from "svelte";
  import QRCode from "qrcode";
  import { page } from "$app/stores";
  import { goto } from "$app/navigation";
  import LinkCard from "$lib/components/LinkCard.svelte";
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
  type Tab = "Send" | "Receive" | "History" | "Devices" | "Account" | "Users" | "Settings";
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
  const destinations = ["Send", "Receive", "History", "Settings", "Devices", "Account", "Users"];
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
      const qr = await QRCode.toDataURL(
        JSON.stringify({
          type: "psst-pairing",
          version: 1,
          server_url: location.origin,
          code: result.code,
        }),
        { width: 280, margin: 4 },
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

<svelte:head><title>{user ? "Your transfers" : "Sign in"} · {BRAND}</title></svelte:head>
{#if loading}<p role="status">Loading your account…</p>
{:else if !user}
  <section class="panel login">
    <p class="eyebrow">YOUR PRIVATE TRANSFER SERVER</p>
    <h1>Sign in to {BRAND}</h1>
    <p class="muted">
      Share files and create receive links. People using your links do not need an account.
    </p>
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
        <label
          >Username<input
            bind:value={username}
            autocomplete="username"
            required
            disabled={busy}
          /></label
        ><label
          >Password<input
            bind:value={password}
            type={showPassword ? "text" : "password"}
            autocomplete="current-password"
            required
            disabled={busy}
          /></label
        ><button
          type="button"
          aria-pressed={showPassword}
          onclick={() => (showPassword = !showPassword)}
          >{showPassword ? "Hide password" : "Show password"}</button
        ><button class="primary" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
      </form>
      <p class="muted small">
        Need an account or password reset? Contact your server administrator.
      </p>{/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}{#if notice}<p
        class="notice"
        role="status"
      >
        {notice}
      </p>{/if}
  </section>
{:else}
  <div class="account-bar">
    <span>Signed in as <strong>{user.username}</strong></span><button
      disabled={busy}
      onclick={() =>
        act(async () => {
          await cancelPair();
          await request("/auth/logout", "POST");
          try {
            localStorage.setItem("psst.auth-change", String(Date.now()));
          } catch {}
          clearAccount();
        })}>Sign out</button
    >
  </div>
  <nav aria-label="Account navigation">
    {#each ["Send", "Receive", "History", "Settings"] as item}<button
        aria-current={tab === item ? "page" : undefined}
        disabled={busy}
        onclick={() => select(item as Tab)}>{item}</button
      >{/each}
  </nav>
  {#if error}<p class="error" role="alert">{error}</p>{/if}{#if notice}<p
      class="notice"
      role="status"
    >
      {notice}
    </p>{/if}
  {#if sendActive && tab !== "Send"}<p class="notice">
      <button onclick={() => select("Send")}>Return to your transfer</button> Your selected files and
      upload stay here.
    </p>{/if}
  {#if liveMessage && (tab === "History" || tab === "Receive" || tab === "Devices")}<p
      role="status"
      class="notice"
    >
      {liveMessage}
    </p>{/if}
  <section class="panel">
    {#key user.id}<div hidden={tab !== "Send"}>
        <SendPanel
          accountId={user.id}
          initialFiles={restoredFiles}
          onselection={(files) => (selectedFiles = files)}
          oncreated={remember}
          onactive={(value) => (sendActive = value)}
        />
      </div>{/key}
    {#if tab === "Settings"}<h1>Settings</h1>
      <div class="actions">
        <button onclick={() => select("Account")}>Account</button><button
          onclick={() => select("Devices")}>Connected devices</button
        >{#if user.role === "admin"}<button onclick={() => select("Users")}>Users</button>{/if}
      </div>
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
          >Refresh received files</button
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
        <button disabled={busy} onclick={() => select("History")}>Refresh</button>
      </div>
      <label
        >Show<select bind:value={historyFilter}
          ><option value="all">All transfers</option><option value="transfers">Sent</option><option
            value="slots">Receive links</option
          ></select
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
        Encryption keys stay on the device that created the link. This browser can reopen its own
        links; transfers from other devices can still be revoked.
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
                This device has no encryption key. Use the device that created the link to open or
                share it. You can still revoke it here.
              </p>{/if}
          </div>
          <div class="actions">
            {#if links[item.id]}<button onclick={() => copy(links[item.id])}>Copy link</button
              >{#if item.kind === "transfers"}<a class="button" href={links[item.id]}>Open</a
                >{:else}<button disabled={busy} onclick={() => openReceive(item.id)}
                  >View files</button
                >{/if}{/if}<button
              class="danger"
              disabled={busy}
              onclick={() => (pendingDelete = { id: item.id, kind: item.kind })}>Revoke</button
            >
          </div>
        </article>{/each}
      {#if pendingDelete}<div class="confirm" role="alert">
          <p>
            Revoke this link and delete its server files? Existing downloaded copies will remain.
          </p>
          <button class="danger" disabled={busy} onclick={revoke}>Revoke and delete</button><button
            disabled={busy}
            onclick={() => (pendingDelete = null)}>Cancel</button
          >
        </div>{/if}
    {:else if tab === "Devices"}<button onclick={() => select("Settings")}>Back to Settings</button>
      <h1>Connect mobile app</h1>
      <p class="muted">
        In the app’s server settings, choose Scan login QR code. Keep this code private: it signs
        the scanning device in as you.
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
        >{pairingId ? "Generate new code" : "Show login QR code"}</button
      >
      <h2>Connected devices</h2>
      <p class="muted">Revoke a session to sign that device out.</p>
      {#each sessions as session}<article class="resource">
          <div>
            <strong
              >{session.device_name || "Device"}{session.current ? " (this browser)" : ""}</strong
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
    {:else if tab === "Account"}<button onclick={() => select("Settings")}>Back to Settings</button>
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
    {:else if tab === "Users"}<button onclick={() => select("Settings")}>Back to Settings</button>
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
            ><option value="user">User</option><option value="admin">Administrator</option></select
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
{/if}

<style>
  .login {
    max-width: 480px;
    margin: 1rem auto;
  }
  .eyebrow {
    font-size: 0.7rem;
    letter-spacing: 0.1em;
    color: var(--muted);
  }
  .account-bar span {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .account-bar,
  .heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1rem;
  }
  nav {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-bottom: 1rem;
  }
  nav button {
    flex: 1;
  }
  .resource {
    padding: 1rem 0;
    border-bottom: 1px solid var(--border);
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
  @media (max-width: 540px) {
    .resource {
      flex-direction: column;
      align-items: stretch;
    }
    .panel {
      padding: 1rem;
    }
    .account-bar {
      font-size: 0.85rem;
    }
    nav button {
      padding: 0.5rem;
    }
    .resource .actions {
      flex-shrink: 1;
    }
  }
</style>
