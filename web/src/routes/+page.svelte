<script lang="ts">
  import { onMount } from "svelte";
  import QRCode from "qrcode";
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
  type Tab = "Share" | "Receive" | "History" | "Devices" | "Account" | "Users";
  let loading = $state(true),
    setupRequired = $state(false),
    busy = $state(false);
  let user = $state<User | null>(null),
    tab = $state<Tab>("Share");
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
    receiveQr = $state(""),
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
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(timer);
  });
  async function initialize() {
    try {
      user = (await request<{ user: User }>("/auth/me")).user;
      links = loadLinks(user.id);
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
  function message(err: unknown) {
    return err instanceof Error ? err.message : "Something went wrong. Please try again.";
  }
  function clearAccount() {
    user = null;
    password = "";
    currentPassword = "";
    changedPassword = "";
    newPassword = "";
    resetPassword = "";
    pairingQr = "";
    receiveUrl = "";
    receiveQr = "";
    links = {};
    received = [];
    transfers = [];
    slots = [];
    sessions = [];
    users = [];
    tab = "Share";
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
        clearAccount();
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
      links = loadLinks(user.id);
    });
    password = "";
  }
  function remember(id: string, url: string) {
    links = { ...links, [id]: url };
    if (user) saveLinks(user.id, links);
  }
  async function select(next: Tab) {
    tab = next;
    pendingDelete = null;
    await act(async () => {
      if (next === "History") {
        const result = await request<{ transfers: Resource[]; slots: Resource[] }>(
          `/auth/resources${allResources ? "?all=true" : ""}`,
        );
        transfers = result.transfers ?? [];
        slots = result.slots ?? [];
      }
      if (next === "Devices")
        sessions = (await request<{ sessions: Session[] }>("/auth/sessions")).sessions;
      if (next === "Users") users = (await request<{ users: User[] }>("/admin/users")).users;
    });
  }
  async function createReceive() {
    await act(async () => {
      const key = await exportKey(await generateKey());
      const slot = await createSlot();
      receiveId = slot.id;
      received = [];
      receiveUrl = `${location.origin}/u/${slot.id}#${key}`;
      remember(slot.id, receiveUrl);
      receiveQr = await QRCode.toDataURL(receiveUrl, { width: 256, margin: 2 });
    });
  }
  async function checkReceived(id: string) {
    await act(async () => {
      const slot = await getSlotInfo(id);
      const key = new URL(links[id]).hash;
      received = slot.transfers
        .filter((t) => t.status === "complete")
        .map((t) => ({
          id: t.transfer_id,
          count: t.file_count,
          url: `${location.origin}/d/${t.transfer_id}${key}`,
        }));
      if (!received.length) notice = "No files received yet. Check again after someone uploads.";
    });
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
      const result = await request<{ code: string; expires_at: string }>(
        "/auth/pairings",
        "POST",
        {},
      );
      pairingExpires = result.expires_at;
      pairingQr = await QRCode.toDataURL(
        JSON.stringify({
          type: "psst-pairing",
          version: 1,
          server_url: location.origin,
          code: result.code,
        }),
        { width: 280, margin: 2 },
      );
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
    return new Date(value).toLocaleString();
  }
  function status(item: Resource) {
    return item.downloaded_at
      ? "Downloaded"
      : item.status === "complete"
        ? "Ready to download"
        : item.status === "pending"
          ? "Uploading"
          : item.status || "Ready";
  }
</script>

<svelte:head><title>{user ? "Your transfers" : "Sign in"} · Psst</title></svelte:head>
{#if loading}<p role="status">Loading your account…</p>
{:else if !user}
  <section class="panel login">
    <p class="eyebrow">YOUR PRIVATE TRANSFER SERVER</p>
    <h1>Sign in to Psst</h1>
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
            type="password"
            autocomplete="current-password"
            required
            disabled={busy}
          /></label
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
          await request("/auth/logout", "POST");
          clearAccount();
        })}>Sign out</button
    >
  </div>
  <nav aria-label="Account navigation">
    {#each ["Share", "Receive", "History", "Devices", "Account", ...(user.role === "admin" ? ["Users"] : [])] as item}<button
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
  <section class="panel">
    {#if tab === "Share"}<SendPanel oncreated={remember} />
    {:else if tab === "Receive"}<h1>Receive files</h1>
      <p class="muted">
        Anyone with your receive link can send you encrypted files within the server’s limits.
      </p>
      {#if receiveUrl}<img class="qr" src={receiveQr} alt="QR code for receive link" />
        <div class="link-row">
          <input
            aria-label="Receive link"
            readonly
            value={receiveUrl}
            onclick={(e) => e.currentTarget.select()}
          /><button onclick={() => copy(receiveUrl)}>Copy</button>
        </div>
        <button disabled={busy} onclick={() => checkReceived(receiveId)}
          >Check for received files</button
        >{/if}<button class="primary" disabled={busy} onclick={createReceive}
        >{receiveUrl ? "Create another receive link" : "Create receive link"}</button
      >
    {:else if tab === "History"}<div class="heading">
        <h1>{allResources ? "All server resources" : "Your transfers"}</h1>
        <button disabled={busy} onclick={() => select("History")}>Refresh</button>
      </div>
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
      {#each [...transfers.map( (t) => ({ ...t, kind: "transfers" as const }), ), ...slots.map( (s) => ({ ...s, kind: "slots" as const }), )] as item}<article
          class="resource"
        >
          <div>
            <strong>{item.kind === "slots" ? "Receive link" : "Sent files"}</strong>
            <p>
              {item.kind === "slots"
                ? `${item.transfers?.reduce((n, t) => n + t.file_count, 0) ?? item.file_count ?? 0} files received`
                : `${item.file_count ?? 0} files · ${status(item)}`}
            </p>
            <p class="muted small">Expires {date(item.expires_at)}</p>
          </div>
          <div class="actions">
            {#if links[item.id]}<button onclick={() => copy(links[item.id])}>Copy link</button
              >{#if item.kind === "transfers"}<a class="button" href={links[item.id]}>Open</a
                >{:else}<button disabled={busy} onclick={() => checkReceived(item.id)}
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
    {:else if tab === "Devices"}<h1>Connected devices</h1>
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
      <h2>Connect mobile app</h2>
      <p class="muted">
        In the app’s server settings, choose Scan login QR code. This code signs the scanning device
        in as you.
      </p>
      {#if pairingQr && now < Date.parse(pairingExpires)}<img
          class="qr"
          src={pairingQr}
          alt="Mobile app login QR code"
        />
        <p class="small">
          Single use · Expires in {Math.max(
            0,
            Math.ceil((Date.parse(pairingExpires) - now) / 1000),
          )} seconds. Keep this code private.
        </p>
        <button onclick={() => (pairingQr = "")}>Hide code</button>{:else if pairingQr}<p
          class="notice"
        >
          This code has expired. Generate a new code.
        </p>{/if}<button class="primary" disabled={busy} onclick={pair}>Show login QR code</button>
    {:else if tab === "Account"}<h1>Change password</h1>
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
    {:else if tab === "Users"}<h1>Manage users</h1>
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
          >{item.count} file{item.count === 1 ? "" : "s"} · Open download</a
        >{/each}{/if}
  </section>
{/if}

<style>
  h1 {
    font-size: 1.6rem;
    line-height: 1.25;
    margin-bottom: 0.75rem;
  }
  h2 {
    font-size: 1.15rem;
    margin: 1.75rem 0 0.5rem;
  }
  .panel {
    background: white;
    border: 1px solid #e5e7eb;
    border-radius: 14px;
    padding: 1.5rem;
  }
  .login {
    max-width: 480px;
    margin: 2rem auto;
  }
  .eyebrow {
    font-size: 0.65rem;
    font-weight: 700;
    letter-spacing: 0.12em;
    color: #52677b;
    margin-bottom: 1rem;
  }
  .muted {
    color: #606973;
    margin-bottom: 1rem;
  }
  .small {
    font-size: 0.8rem;
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
    border-color: transparent;
    background: transparent;
  }
  nav button[aria-current="page"] {
    background: #172c40;
    color: white;
  }
  button,
  .button {
    font: inherit;
    font-size: 0.85rem;
    cursor: pointer;
    border: 1px solid #d1d5db;
    border-radius: 7px;
    padding: 0.55rem 0.85rem;
    background: white;
    color: #172c40;
    text-decoration: none;
    display: inline-block;
  }
  button:hover,
  .button:hover {
    background: #f0f4f8;
  }
  button:disabled {
    opacity: 0.55;
    cursor: wait;
  }
  .primary {
    background: #172c40;
    border-color: #172c40;
    color: white;
    margin-top: 0.75rem;
  }
  .primary:hover {
    background: #284862;
  }
  .danger {
    color: #a82a31;
  }
  form {
    display: grid;
    gap: 1rem;
    margin: 1.25rem 0;
  }
  label {
    display: grid;
    gap: 0.35rem;
    font-size: 0.85rem;
    font-weight: 600;
  }
  input,
  select {
    min-width: 0;
    width: 100%;
    border: 1px solid #c9d0d7;
    border-radius: 7px;
    padding: 0.7rem;
    font: inherit;
    background: white;
  }
  input:focus,
  select:focus,
  button:focus-visible {
    outline: 2px solid #307ab7;
    outline-offset: 2px;
  }
  .error,
  .notice {
    padding: 0.8rem;
    border-radius: 8px;
    margin: 1rem 0;
    overflow-wrap: anywhere;
  }
  .error {
    background: #fff0ef;
    color: #a52229;
  }
  .notice {
    background: #edf5fb;
    color: #224c70;
  }
  .qr {
    display: block;
    width: 220px;
    max-width: 100%;
    height: auto;
    margin: 1rem auto;
  }
  .link-row {
    display: flex;
    gap: 0.5rem;
    margin: 1rem 0;
  }
  .link-row input {
    font-size: 0.8rem;
  }
  .toggle {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 1rem;
  }
  .toggle input {
    width: auto;
  }
  .resource {
    padding: 1rem 0;
    border-bottom: 1px solid #edf0f2;
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    align-items: center;
    overflow-wrap: anywhere;
  }
  .resource p {
    margin: 0;
    font-size: 0.85rem;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.35rem;
    justify-content: flex-end;
  }
  .confirm {
    padding: 1rem;
    border: 1px solid #e7c3c4;
    border-radius: 8px;
    margin-top: 1rem;
  }
  .confirm button {
    margin: 0.5rem 0.5rem 0 0;
  }
  .empty {
    padding: 2rem 0;
    text-align: center;
    color: #606973;
  }
  .received {
    display: block;
    padding: 0.7rem 0;
    color: #245d8e;
  }
  @media (max-width: 540px) {
    .panel {
      padding: 1rem;
    }
    .resource {
      flex-direction: column;
      align-items: flex-start;
    }
    .actions {
      justify-content: flex-start;
    }
    .account-bar {
      font-size: 0.8rem;
    }
    nav {
      gap: 0.1rem;
    }
    nav button {
      padding: 0.5rem 0.6rem;
    }
  }
</style>
