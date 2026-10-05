<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { brandedQr } from "$lib/branded-qr";
  import { page } from "$app/stores";
  import { beforeNavigate, goto } from "$app/navigation";
  import AdministratorSecurity from "$lib/components/AdministratorSecurity.svelte";
  import AdministratorSecurityWarning from "$lib/components/AdministratorSecurityWarning.svelte";
  import RecentAuthentication from "$lib/components/RecentAuthentication.svelte";
  import RecoveryCodes from "$lib/components/RecoveryCodes.svelte";
  import { securityIdentityChanged } from "$lib/admin-security";
  import PasswordChange from "$lib/components/PasswordChange.svelte";
  import AdminOverview from "$lib/components/AdminOverview.svelte";
  import AdminResources from "$lib/components/AdminResources.svelte";
  import TrafficPanel from "$lib/components/TrafficPanel.svelte";
  import SecurityEvents from "$lib/components/SecurityEvents.svelte";
  import { labelFor, labelKey, compactTitle, type HistoryLabels } from "$lib/history-labels";
  import ServerSettings from "$lib/components/ServerSettings.svelte";
  import AbuseContactSettings from "$lib/components/AbuseContactSettings.svelte";
  import ResourcePolicySettings from "$lib/components/ResourcePolicySettings.svelte";
  import AccountUsage from "$lib/components/AccountUsage.svelte";
  import AccountTraffic from "$lib/components/AccountTraffic.svelte";
  import PublicTransferControl from "$lib/components/PublicTransferControl.svelte";
  import IncidentConfirmDialog from "$lib/components/IncidentConfirmDialog.svelte";
  import { loadResourcePage, loadUsersPage, HISTORY_PREVIOUS_WINDOW } from "$lib/resource-history";
  import { resourceFileCount, receivedFileCount } from "$lib/account";
  import ScanPanel from "$lib/components/ScanPanel.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import LinkCard from "$lib/components/LinkCard.svelte";
  import RevokeDialog from "$lib/components/RevokeDialog.svelte";
  import { BRAND } from "$lib/brand";
  import { formatSize } from "$lib/upload-job.svelte";
  import SendPanel, { type SendDraft } from "$lib/components/SendPanel.svelte";
  import {
    accountRequest as request,
    AccountError,
    type User,
    type Session,
    type Resource,
  } from "$lib/account";
  import { createSlot, getSlotInbox, type InboxPage } from "$lib/api";
  import {
    loadLocalHistory,
    saveLocalLink,
    removeLocalLink,
    updateLocalLabel,
    type LocalHistoryPage,
  } from "$lib/local-history";
  import { INBOX_PREVIOUS_WINDOW } from "$lib/inbox-page";
  import { exportKey } from "$lib/crypto";
  import { generateReceiveKeyPair } from "$lib/receive-crypto";
  import { storeReceiveKey, loadReceiveKey, removeReceiveKey } from "$lib/receive-keys";
  import OptionalLimit from "$lib/components/OptionalLimit.svelte";
  type Tab =
    | "Resources"
    | "Overview"
    | "Traffic"
    | "Security"
    | "Server"
    | "Scan"
    | "Send"
    | "Receive"
    | "History"
    | "Devices"
    | "Account"
    | "Users"
    | "Settings";
  let receiveUnavailable = $state(false);
  let receiveInfo = $state<InboxPage | null>(null),
    maxFiles = $state(0);
  let receiveNeedsKey = $state(false);
  const emptySendDraft = (): SendDraft => ({ files: [], maxDownloads: 0, policyLocked: false });
  let pendingDraft = emptySendDraft(),
    pendingOwner = "";
  let selectedDraft = emptySendDraft();
  let restoredDraft = $state<SendDraft>(emptySendDraft());
  let sendActive = $state(false),
    showPassword = $state(false),
    historyFilter = $state("all"),
    liveMessage = $state(""),
    lastUpdated = $state(0);
  let pairingFlow = 0;
  let epoch = 0,
    polling = false,
    failures = 0;
  let labels = $state<HistoryLabels>({});
  let receiveName = $state(""),
    renameId = $state(""),
    renameKind = $state<"transfers" | "slots">("transfers"),
    renameValue = $state("");
  let pairingId = $state(""),
    pairingStatus = $state("pending"),
    pairedDevice = $state("");
  const destinations = [
    "Resources",
    "Overview",
    "Traffic",
    "Security",
    "Server",
    "Scan",
    "Send",
    "Receive",
    "History",
    "Settings",
    "Devices",
    "Account",
    "Users",
  ];
  const adminDestinations = ["Overview", "Users", "Traffic", "Security", "Server"] as const;
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
      (user?.role === "admin" ? "Overview" : "Send")) as Tab;
  }
  $effect(() => {
    const next = routeTab();
    const slot = $page.url.searchParams.get("slot");
    untrack(() => {
      if (
        user &&
        !user.must_change_password &&
        (next !== tab || (next === "Receive" && slot && slot !== receiveId))
      )
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
  let factorRequired = $state(false),
    loginRecovery = $state(false),
    loginCode = $state("");
  let recoveryCodes = $state<string[]>([]);
  let securityMutationActive = $state(false),
    securityEpoch = 0;
  function securityMutation(active: boolean) {
    securityEpoch++;
    securityMutationActive = active;
  }
  beforeNavigate((navigation) => {
    if (securityMutationActive) navigation.cancel();
  });
  interface SessionsResponse {
    sessions: Session[];
    total_active_sessions: number;
    total_active_sessions_exact: boolean;
    sessions_limited: boolean;
  }
  let totalActiveSessions = $state(0),
    totalActiveSessionsExact = $state(true),
    sessionsLimited = $state(false);
  function updateSessions(result: SessionsResponse) {
    sessions = result.sessions;
    totalActiveSessions = result.total_active_sessions;
    totalActiveSessionsExact = result.total_active_sessions_exact;
    sessionsLimited = result.sessions_limited;
  }
  let users = $state<User[]>([]),
    sessions = $state<Session[]>([]),
    transfers = $state<Resource[]>([]),
    slots = $state<Resource[]>([]);
  let links = $state<Record<string, string>>({});
  let localHistoryWarning = $state("");
  let localRetry = $state<(() => Promise<void>) | null>(null);
  let usersCursor = $state(""),
    usersNext = $state<string | null>(null),
    usersPrevious = $state<string[]>([]);
  let historyCursor = $state(""),
    historyNext = $state<string | null>(null),
    historyPrevious = $state<string[]>([]),
    historyLoading = $state(false),
    historyPage = $state(1),
    historyError = $state("");
  let historyGeneration = 0;
  let historyRequest: AbortController | null = null;
  let receiveUrl = $state(""),
    receiveId = $state("");
  let pairingQr = $state(""),
    pairingExpires = $state(""),
    now = $state(Date.now());
  let newUsername = $state(""),
    newPassword = $state(""),
    newRole = $state("user"),
    resetId = $state(""),
    resetPassword = $state(""),
    resetConfirmation = $state(""),
    newConfirmation = $state("");

  let pendingDelete = $state<{ id: string; kind: "transfers" | "slots" } | null>(null);
  let accountAction = $state<{ account: User; mode: "disable" | "shutdown" } | null>(null);
  let received = $state<{ id: string; url: string; count: number }[]>([]);
  onMount(() => {
    void initialize();
    let timerPoll: ReturnType<typeof setTimeout>;
    let stopped = false;
    async function poll() {
      if (stopped) return;
      if (user && !document.hidden && !polling && !securityMutationActive) {
        polling = true;
        const owner = epoch,
          securityVersion = securityEpoch;
        try {
          const identity = await request<{ user: User }>("/auth/me");
          if (owner !== epoch || securityMutationActive || securityVersion !== securityEpoch)
            throw new Error("Account changed");
          if (identity.user.id !== user?.id) {
            clearAccount();
            error = "Your account changed in another tab. Sign in again.";
            throw new Error("Account changed");
          }
          if (
            identity.user.role !== user.role ||
            identity.user.must_change_password !== user.must_change_password
          ) {
            user = identity.user;
            await select(routeTab(), false);
          }
          if (user.must_change_password) {
            liveMessage = "";
          }
          if (!user.must_change_password && tab === "History" && !historyLoading) {
            if (!(await refreshHistory(owner))) throw new Error("Could not refresh history");
          }
          if (
            !user.must_change_password &&
            tab === "Receive" &&
            receiveId &&
            !receiveUnavailable &&
            !receiveLoading
          )
            await refreshReceived(receiveId, owner);
          if (
            !user.must_change_password &&
            tab === "Devices" &&
            pairingId &&
            pairingStatus === "pending"
          ) {
            const activePairing = pairingId;
            const result = await request<{ status: string; device_name?: string }>(
              `/auth/pairings/${activePairing}`,
            );
            if (owner === epoch && activePairing === pairingId) {
              pairingStatus = result.status;
              if (result.status !== "pending") pairingQr = "";
              if (result.status === "connected") {
                pairedDevice = result.device_name || "Phone";
                const devices = await request<SessionsResponse>("/auth/sessions");
                if (owner === epoch) updateSessions(devices);
              }
            }
          }
          if (owner === epoch) {
            failures = 0;
            lastUpdated = Date.now();
            liveMessage = "";
          }
        } catch (err) {
          if (owner === epoch && !securityMutationActive && securityVersion === securityEpoch) {
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
      if (event.key === "psst.auth-change" && (user || recoveryCodes.length || factorRequired)) {
        clearAccount();
        error = "Your sign-in changed in another tab. Sign in again.";
      }
    }
    document.addEventListener("visibilitychange", resume);
    window.addEventListener("storage", changed);
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => {
      historyGeneration++;
      historyRequest?.abort();
      receiveGeneration++;
      receiveRequest?.abort();
      stopped = true;
      document.removeEventListener("visibilitychange", resume);
      window.removeEventListener("storage", changed);
      clearInterval(timer);
      clearTimeout(timerPoll);
      void cancelPair();
      epoch++;
      securityIdentityChanged();
      password = "";
      loginCode = "";
      recoveryCodes = [];
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
    links = {};
    labels = {};
    localHistoryWarning = "";
    localRetry = null;
  }
  async function refreshHistory(
    owner = epoch,
    cursor = historyCursor,
    direction?: "next" | "previous" | "first",
  ) {
    if (owner !== epoch || tab !== "History") return true;
    const previousCursor = historyCursor,
      account = user?.id,
      generation = ++historyGeneration;
    historyRequest?.abort();
    const controller = new AbortController();
    historyRequest = controller;
    const current = () =>
      owner === epoch &&
      account === user?.id &&
      tab === "History" &&
      generation === historyGeneration &&
      previousCursor === historyCursor;
    historyLoading = true;
    try {
      const result = await loadResourcePage(cursor, false, controller.signal);
      if (!current()) return true;
      let local: LocalHistoryPage | null = null;
      try {
        local = await loadLocalHistory(
          account!,
          [
            ...result.transfers.map((item) => ({ kind: "transfers" as const, id: item.id })),
            ...result.slots.map((item) => ({ kind: "slots" as const, id: item.id })),
          ],
          controller.signal,
        );
      } catch {
        if (controller.signal.aborted) return true;
      }
      if (!current()) return true;
      if (
        direction === "next" &&
        result.next_cursor &&
        (result.next_cursor === historyCursor || historyPrevious.includes(result.next_cursor))
      )
        throw new Error(
          "This server returned a repeated history page. Refresh or contact its administrator.",
        );
      if (direction) {
        historyPrevious =
          direction === "next"
            ? [...historyPrevious, historyCursor].slice(-HISTORY_PREVIOUS_WINDOW)
            : direction === "previous"
              ? historyPrevious.slice(0, -1)
              : [];
        historyPage =
          direction === "next" ? historyPage + 1 : direction === "previous" ? historyPage - 1 : 1;
        historyCursor = cursor;
      }
      historyNext = result.next_cursor;
      transfers = result.transfers;
      slots = result.slots;
      links = local?.links ?? {};
      labels = local?.labels ?? {};
      if (!localRetry)
        localHistoryWarning = local
          ? ""
          : "Local links and names could not be loaded. Existing browser data is unchanged. Check storage permissions and free space, then refresh to retry.";
      historyError = "";
    } catch (cause) {
      if (!current()) return true;
      if (cause instanceof AccountError && cause.status === 401) {
        clearAccount(true);
        error = "Your session ended. Sign in again.";
      } else historyError = message(cause);
      return false;
    } finally {
      if (generation === historyGeneration) historyLoading = false;
    }
    return true;
  }
  async function turnHistory(direction: "next" | "previous" | "first") {
    if (historyLoading) return;
    const cursor =
      direction === "next" ? historyNext : direction === "previous" ? historyPrevious.at(-1) : "";
    if (cursor != null) await refreshHistory(epoch, cursor, direction);
  }
  async function turnUsers(direction: "next" | "previous") {
    const owner = epoch;
    const cursor = direction === "next" ? usersNext : usersPrevious.at(-1);
    if (cursor == null) return;
    await act(async () => {
      const result = await loadUsersPage(cursor);
      if (owner !== epoch) return;
      usersPrevious =
        direction === "next" ? [...usersPrevious, usersCursor] : usersPrevious.slice(0, -1);
      usersCursor = cursor;
      usersNext = result.next_cursor;
      users = result.users;
    });
  }
  let receiveCursor = $state(""),
    receiveNext = $state<string | null>(null),
    receivePrevious = $state<string[]>([]),
    receivePage = $state(1),
    receiveLoading = $state(false),
    receiveError = $state("");
  let receiveGeneration = 0;
  let receiveRequest: AbortController | null = null;
  function resetReceive(id: string) {
    receiveGeneration++;
    receiveRequest?.abort();
    receiveId = id;
    receiveCursor = "";
    receiveNext = null;
    receivePrevious = [];
    receivePage = 1;
    receiveInfo = null;
    receiveLoading = false;
    receiveError = "";
    received = [];
    receiveUnavailable = false;
  }
  async function refreshReceived(
    id: string,
    owner = epoch,
    cursor = receiveCursor,
    direction?: "next" | "previous" | "first",
  ) {
    if (owner !== epoch || id !== receiveId || tab !== "Receive") return;
    const originCursor = receiveCursor,
      account = user?.id;
    const generation = ++receiveGeneration;
    receiveRequest?.abort();
    const controller = new AbortController();
    receiveRequest = controller;
    const current = () =>
      owner === epoch &&
      account === user?.id &&
      id === receiveId &&
      originCursor === receiveCursor &&
      generation === receiveGeneration &&
      tab === "Receive";
    receiveLoading = true;
    try {
      const slot = await getSlotInbox(id, cursor, controller.signal);
      if (!current()) return;
      let local: LocalHistoryPage | null = null;
      try {
        local = await loadLocalHistory(account!, [{ kind: "slots", id }], controller.signal);
      } catch {
        if (controller.signal.aborted) return;
      }
      if (!current()) return;
      links = local?.links ?? {};
      labels = local?.labels ?? {};
      // Keep the active invitation copyable when persistence is unavailable.
      receiveUrl = links[id] || receiveUrl;
      if (!localRetry)
        localHistoryWarning = local
          ? ""
          : "Local links and names could not be loaded. Existing browser data is unchanged. Check storage permissions and free space, then refresh to retry.";
      if (
        direction === "next" &&
        slot.next_cursor &&
        (slot.next_cursor === receiveCursor || receivePrevious.includes(slot.next_cursor))
      )
        throw new Error("Repeated inbox page");
      if (direction) {
        receivePrevious =
          direction === "next"
            ? [...receivePrevious, receiveCursor].slice(-INBOX_PREVIOUS_WINDOW)
            : direction === "previous"
              ? receivePrevious.slice(0, -1)
              : [];
        receivePage =
          direction === "next" ? receivePage + 1 : direction === "previous" ? receivePage - 1 : 1;
        receiveCursor = cursor;
      }
      receiveNext = slot.next_cursor;
      receiveInfo = slot;
      receiveError = "";
      const pair = user ? loadReceiveKey(user.id, id) : null;
      receiveNeedsKey = slot.receive_protocol === 2 ? !pair : !links[id];
      pair?.privateKey.fill(0);
      const key = links[id] ? new URL(links[id]).hash : "";
      received = slot.transfers
        .filter((t) => t.status === "complete")
        .map((t) => ({
          id: t.transfer_id,
          count: t.file_count,
          url:
            slot.receive_protocol === 2
              ? `${location.origin}/d/${t.transfer_id}?inbox=${encodeURIComponent(id)}`
              : `${location.origin}/d/${t.transfer_id}${key}`,
        }));
    } catch (cause) {
      if (!current()) return;
      if (cause instanceof Error && /API (404|410)(?:$|:)/.test(cause.message)) {
        receiveUnavailable = true;
        receiveInfo = null;
        received = [];
      } else
        receiveError =
          "Could not refresh received files. The displayed page may be out of date. Retry to check again.";
    } finally {
      if (generation === receiveGeneration) receiveLoading = false;
    }
  }
  async function turnReceived(direction: "next" | "previous" | "first") {
    if (receiveLoading) return;
    const cursor =
      direction === "next" ? receiveNext : direction === "previous" ? receivePrevious.at(-1) : "";
    if (cursor == null) return;
    await refreshReceived(receiveId, epoch, cursor, direction);
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
    securityIdentityChanged();
    securityMutation(false);
    factorRequired = false;
    loginRecovery = false;
    loginCode = "";
    recoveryCodes = [];
    if (preserveSelection && user?.role === "user") {
      // A restricted login has no SendPanel. Its same-account selection remains in
      // the pending queue until the required password flow has fully completed.
      const draft =
        user.must_change_password && pendingOwner === user.id ? pendingDraft : selectedDraft;
      pendingOwner = user.id;
      pendingDraft = { ...draft, files: [...draft.files] };
    } else {
      pendingOwner = "";
      pendingDraft = emptySendDraft();
    }
    restoredDraft = emptySendDraft();
    selectedDraft = emptySendDraft();
    epoch++;
    void cancelPair();
    user = null;
    labels = {};
    renameId = "";
    receiveName = "";
    sendActive = false;
    resetReceive("");
    receiveInfo = null;
    receiveNeedsKey = false;
    maxFiles = 0;
    pendingDelete = null;
    accountAction = null;
    liveMessage = "";
    password = "";
    newPassword = "";
    resetPassword = "";
    resetConfirmation = "";
    newConfirmation = "";
    pairingQr = "";
    receiveUrl = "";
    links = {};
    localHistoryWarning = "";
    localRetry = null;
    received = [];
    transfers = [];
    slots = [];
    historyGeneration++;
    historyRequest?.abort();
    historyPage = 1;
    historyError = "";
    historyCursor = "";
    historyNext = null;
    historyPrevious = [];
    historyLoading = false;
    sessions = [];
    totalActiveSessions = 0;
    totalActiveSessionsExact = true;
    sessionsLimited = false;
    users = [];
    usersCursor = "";
    usersNext = null;
    usersPrevious = [];
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
      if (
        err instanceof AccountError &&
        (err.code === "password_change_required" || err.code === "admin_transfer_forbidden")
      ) {
        user = (await request<{ user: User }>("/auth/me")).user;
        tab = user.must_change_password ? "Account" : "Overview";
      }
      if (err instanceof AccountError && err.status === 401 && user) {
        clearAccount(true);
        error = "Your session ended. Sign in again.";
      }
    } finally {
      busy = false;
    }
  }
  function cancelFactorLogin() {
    password = "";
    loginCode = "";
    factorRequired = false;
    loginRecovery = false;
    error = "";
  }
  function securityChanged(codes?: string[]) {
    clearAccount();
    recoveryCodes = codes ?? [];
    try {
      localStorage.setItem("psst.auth-change", String(Date.now()));
    } catch {}
    notice = codes ? "" : "Authenticator disabled. All sessions ended. Sign in again.";
  }
  async function login() {
    await act(async () => {
      const owner = epoch;
      let result: { user: User };
      try {
        result = await request<{ user: User }>("/auth/login", "POST", {
          username,
          password,
          device_name: "Web browser",
          session_type: "web",
          ...(factorRequired
            ? loginRecovery
              ? { recovery_code: loginCode.trim() }
              : { code: loginCode.trim() }
            : {}),
        });
      } catch (cause) {
        if (owner !== epoch) return;
        if (
          cause instanceof AccountError &&
          (cause.code === "administrator_factor_required" ||
            cause.code === "administrator_factor_invalid")
        ) {
          factorRequired = true;
          error = cause.message;
          return;
        }
        if (
          !(cause instanceof AccountError && cause.code === "administrator_authentication_locked")
        )
          cancelFactorLogin();
        throw cause;
      }
      if (owner !== epoch) return;
      securityIdentityChanged();
      user = result.user;
      factorRequired = false;
      loginRecovery = false;
      loginCode = "";
      try {
        localStorage.setItem("psst.auth-change", String(Date.now()));
      } catch {}
      epoch++;
      if (pendingOwner !== user.id || user.role !== "user") {
        pendingDraft = emptySendDraft();
        pendingOwner = "";
      }
      restoredDraft = emptySendDraft();
      if (!user.must_change_password && user.role === "user") {
        restoredDraft =
          pendingOwner === user.id
            ? { ...pendingDraft, files: [...pendingDraft.files] }
            : emptySendDraft();
        pendingDraft = emptySendDraft();
        pendingOwner = "";
      }
      loadAccount();
    });
    if (user) await select(routeTab(), false);
    if (!factorRequired) password = "";
    loginCode = "";
  }
  async function remember(id: string, url: string, title?: string, size?: number) {
    if (!user) return;
    const account = user.id,
      owner = epoch;
    const retry = async () => {
      if (owner === epoch && user?.id === account) await remember(id, url, title, size);
    };
    try {
      await saveLocalLink(account, id, url);
      const label = title
        ? await updateLocalLabel(account, { kind: "transfers", id }, { title, size: size ?? 0 })
        : undefined;
      if (owner !== epoch || user?.id !== account) return;
      if (tab === "Send" || (tab === "Receive" && receiveId === id)) {
        links = { [id]: url };
        if (label) labels = { [labelKey("transfers", id)]: label };
      } else if (transfers.some((item) => item.id === id) || slots.some((item) => item.id === id)) {
        links = { ...links, [id]: url };
        if (label) labels = { ...labels, [labelKey("transfers", id)]: label };
      }
      localHistoryWarning = "";
      localRetry = null;
    } catch {
      if (owner !== epoch || user?.id !== account) return;
      localHistoryWarning =
        "This link or its name could not be saved on this device. Copy the full link before leaving, or check browser storage permissions and space, then retry.";
      localRetry = retry;
    }
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
      if (identity.user.role !== "user" || identity.user.must_change_password) {
        user = identity.user;
        await select(routeTab(), false);
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
    if (!user || securityMutationActive) return;
    if (user.must_change_password) {
      tab = "Account";
      return;
    }
    const allowed =
      user.role === "admin"
        ? [...adminDestinations, "Account", "Devices", "Resources"]
        : [...mainDestinations, "Account", "Devices"];
    if (!allowed.includes(next as never)) {
      next = user.role === "admin" ? "Overview" : "Send";
      navigate = true;
    }
    if (tab === "Devices" && next !== "Devices") await cancelPair();
    if (tab === "Receive" && next !== "Receive") {
      receiveGeneration++;
      receiveRequest?.abort();
      receiveLoading = false;
    }
    if (tab === "History" && next !== "History") {
      historyGeneration++;
      historyRequest?.abort();
      historyLoading = false;
    }
    tab = next;
    if (next === "Receive") {
      const id = $page.url.searchParams.get("slot");
      if (id) {
        if (receiveId !== id) resetReceive(id);
        receiveUrl = links[id] || "";
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
      if (next === "Receive" && receiveId) await refreshReceived(receiveId, owner);
      if (next === "Devices") {
        const result = await request<SessionsResponse>("/auth/sessions");
        if (owner === epoch) updateSessions(result);
      }
      if (next === "Users") {
        const result = await loadUsersPage(usersCursor);
        if (owner === epoch) {
          users = result.users;
          usersNext = result.next_cursor;
        }
      }
    });
  }
  async function createReceive() {
    await act(async () => {
      const owner = epoch;
      const account = user!.id;
      const selectedMaxFiles = maxFiles;
      const pair = await generateReceiveKeyPair();
      const publicKey = await exportKey(pair.publicKey);
      const slot = await createSlot(publicKey, selectedMaxFiles);
      try {
        if (owner !== epoch || user?.id !== account)
          throw new Error("Your account changed. Create the receive link again.");
        storeReceiveKey(account, slot.id, pair);
      } catch (cause) {
        if (slot.delete_token)
          await fetch(`/api/v1/slots/${slot.id}`, {
            method: "DELETE",
            credentials: "omit",
            headers: { Authorization: `Bearer ${slot.delete_token}` },
          }).catch(() => {});
        throw cause;
      } finally {
        pair.privateKey.fill(0);
      }
      resetReceive(slot.id);
      receiveUrl = "";
      // Preserve the owner record and local private key on transport failure so
      // History can manage the allocation; never publish an unverified invitation.
      let accepted: InboxPage;
      try {
        accepted = await getSlotInbox(slot.id);
      } catch {
        throw new Error(
          "Could not verify the new inbox. It remains in History for inspection or revocation. No receive link was shared.",
        );
      }
      if (
        accepted.receive_protocol !== 2 ||
        accepted.recipient_public_key !== publicKey ||
        accepted.max_files !== selectedMaxFiles
      ) {
        let removed = false;
        if (slot.delete_token) {
          const response = await fetch(`/api/v1/slots/${slot.id}`, {
            method: "DELETE",
            credentials: "omit",
            headers: { Authorization: `Bearer ${slot.delete_token}` },
          }).catch(() => null);
          removed = !!response && (response.ok || response.status === 404);
        }
        if (removed) {
          removeReceiveKey(account, slot.id);
          receiveId = "";
        }
        throw new Error(
          `This server did not accept the private inbox protocol or file limit. Ask its operator to update it.${removed ? " The unused inbox was removed." : " Revoke the unused inbox from History."}`,
        );
      }
      if (owner !== epoch || user?.id !== account) return;
      receiveUrl = `${location.origin}/u/${slot.id}#v2.${publicKey}`;
      await remember(slot.id, receiveUrl);
      if (owner !== epoch || user?.id !== account) return;
      if (user && receiveName.trim()) {
        try {
          const label = await updateLocalLabel(
            account,
            { kind: "slots", id: slot.id },
            { custom: receiveName.trim() },
          );
          if (owner !== epoch || user?.id !== account) return;
          labels = { [labelKey("slots", slot.id)]: label };
        } catch {
          if (owner !== epoch || user?.id !== account) return;
          localHistoryWarning =
            "The receive link was created, but its name could not be saved on this device. Check browser storage and retry the name from History.";
        }
      }
      receiveName = "";
      maxFiles = 0;
      receiveNeedsKey = false;
      await refreshReceived(slot.id, owner);
      await goto(`/?view=receive&slot=${encodeURIComponent(slot.id)}`, {
        keepFocus: true,
        noScroll: true,
      });
    });
  }
  async function confirmAccountAction() {
    if (!accountAction) return;
    const target = accountAction;
    const owner = epoch;
    await act(async () => {
      if (target.mode === "disable") {
        const result = await request<{ user: User }>(`/admin/users/${target.account.id}`, "PATCH", {
          disabled: true,
        });
        if (owner !== epoch) return;
        users = users.map((item) => (item.id === result.user.id ? result.user : item));
        notice =
          "Sign-in disabled and sessions/pairings revoked. Existing public links remain active; use incident shutdown to revoke them.";
      } else {
        const result = await request<{
          user: User;
          revoked_sessions: number;
          revoked_pairings: number;
          revoked_transfers: number;
          revoked_slots: number;
          cleanup_pending: boolean;
        }>(`/admin/users/${target.account.id}/shutdown`, "POST", {});
        if (owner !== epoch) return;
        users = users.map((item) => (item.id === result.user.id ? result.user : item));
        notice = `Incident shutdown applied: ${result.revoked_sessions} sessions, ${result.revoked_pairings} pairing grants, ${result.revoked_transfers} transfers and ${result.revoked_slots} receive links revoked. Server cleanup is pending. Downloaded copies remain. Re-enabling sign-in will not restore revoked links.`;
      }
      accountAction = null;
    });
  }
  async function checkReceived(id: string) {
    await act(async () => {
      if (receiveId !== id) resetReceive(id);
      receiveUrl = links[id] || "";
      await refreshReceived(id);
    });
  }
  async function openReceive(id: string) {
    resetReceive(id);
    receiveUrl = links[id] || "";
    received = [];
    await select("Receive");
    await checkReceived(id);
  }
  async function revoke() {
    if (!pendingDelete) return;
    const target = pendingDelete;
    await act(async () => {
      const account = user!.id,
        owner = epoch;
      await request(`/${target.kind}/${target.id}`, "DELETE");
      try {
        await removeLocalLink(account, target.id);
        if (target.kind === "slots") removeReceiveKey(account, target.id);
      } catch {
        if (owner === epoch && user?.id === account)
          localHistoryWarning =
            "The server link was revoked, but its local copy could not be removed. Refresh after checking browser storage.";
      }
      if (owner !== epoch || user?.id !== account) return;
      transfers = transfers.filter((t) => t.id !== target.id);
      slots = slots.filter((s) => s.id !== target.id);
      delete links[target.id];
      links = { ...links };
      pendingDelete = null;
      notice = "Link revoked and server files deleted.";
    });
  }
  async function changePassword(current: string, replacement: string) {
    await act(async () => {
      await request("/auth/password", "POST", { current_password: current, password: replacement });
      clearAccount(true);
      try {
        localStorage.setItem("psst.auth-change", String(Date.now()));
      } catch {}
      notice = "Password changed. Sign in with your new password.";
    });
  }
  function historyTitle(item: Resource & { kind: "transfers" | "slots" }) {
    const label = labelFor(labels, item.kind, item.id);
    return (
      label.custom ||
      label.title ||
      `${item.kind === "slots" ? "Receive link" : "Sent files"}${item.created_at ? " · " + new Date(item.created_at).toLocaleString() : ""}`
    );
  }
  async function rename() {
    if (!user) return;
    const account = user.id,
      owner = epoch,
      id = renameId,
      kind = renameKind,
      cursor = historyCursor,
      currentTab = tab,
      custom = renameValue.trim() || undefined;
    try {
      const label = await updateLocalLabel(account, { kind, id }, { custom });
      if (
        owner !== epoch ||
        user?.id !== account ||
        renameId !== id ||
        renameKind !== kind ||
        historyCursor !== cursor ||
        tab !== currentTab
      )
        return;
      labels = { ...labels, [labelKey(kind, id)]: label };
      if (!localRetry) localHistoryWarning = "";
      renameId = "";
    } catch {
      if (owner === epoch && user?.id === account)
        localHistoryWarning =
          "This name could not be saved on this device. Check browser storage permissions and space, then try Save name again.";
    }
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
{:else if recoveryCodes.length}<RecoveryCodes
    codes={recoveryCodes}
    onacknowledge={() => {
      recoveryCodes = [];
      notice =
        "Recovery codes acknowledged. Sign in again using the next authenticator code or a recovery code.";
    }}
  />
{:else if !user}
  <section class="panel login">
    <h1>Sign in to {BRAND}</h1>
    <p class="muted login-intro">Your private space to send and receive files.</p>
    {#if setupRequired}<p class="notice">
        This server needs its first administrator. The server operator must configure ADMIN_USERNAME
        and ADMIN_PASSWORD, then restart the server.
      </p>
    {:else if factorRequired}<form
        onsubmit={(event) => {
          event.preventDefault();
          void login();
        }}
      >
        <h2>Administrator verification</h2>
        <p>Finish signing in as {username}. No session is created until your code is accepted.</p>
        <label
          >{loginRecovery ? "Recovery code" : "Authenticator code"}<input
            type="text"
            inputmode={loginRecovery ? "text" : "numeric"}
            autocomplete="one-time-code"
            pattern={loginRecovery ? undefined : "[0-9]{6}"}
            maxlength={loginRecovery ? 128 : 6}
            required
            spellcheck={false}
            autocapitalize="none"
            disabled={busy}
            bind:value={loginCode}
          /></label
        >
        <button
          type="button"
          disabled={busy}
          onclick={() => {
            loginRecovery = !loginRecovery;
            loginCode = "";
            error = "";
          }}>{loginRecovery ? "Use authenticator code" : "Use a recovery code"}</button
        >
        <button class="primary" disabled={busy}>{busy ? "Verifying…" : "Verify and sign in"}</button
        >
        <button type="button" disabled={busy} onclick={cancelFactorLogin}
          >Back to password sign-in</button
        >
      </form>
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
{:else if user.must_change_password}
  <section class="panel login">
    <PasswordChange {busy} requiredChange onchange={changePassword} />
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button
      disabled={busy}
      onclick={() =>
        act(async () => {
          await request("/auth/logout", "POST");
          clearAccount();
        })}>Sign out</button
    >
  </section>
{:else}
  <div class="workspace">
    <aside class="sidebar">
      <p class="sidebar-label">{user.role === "admin" ? "Administration" : "Your workspace"}</p>
      <nav class:admin-nav={user.role === "admin"} aria-label="Account navigation">
        {#each user.role === "admin" ? adminDestinations : mainDestinations as item}
          <a
            href={destinationUrl(item)}
            aria-label={item === "Scan"
              ? "Scan QR code"
              : item === "Server"
                ? "Server settings"
                : item === "Security"
                  ? "Security activity"
                  : item}
            aria-current={tab === item
              ? "page"
              : (item === "Overview" && tab === "Resources") ||
                  (item === "Settings" && ["Account", "Devices"].includes(tab))
                ? "location"
                : undefined}
            class:active={tab === item ||
              (item === "Overview" && tab === "Resources") ||
              (item === "Settings" && ["Account", "Devices"].includes(tab))}
            data-sveltekit-keepfocus
            data-sveltekit-noscroll
          >
            <Icon name={item === "Scan" ? "QRCode" : item === "Server" ? "Settings" : item} /><span
              class="nav-label"
              >{item === "Scan"
                ? "Scan"
                : item === "Server"
                  ? "Server settings"
                  : item === "Security"
                    ? "Security activity"
                    : item}</span
            >
          </a>
        {/each}
      </nav>
      <section class="account-bar" aria-label="Signed-in account">
        <div class="account-identity">
          <span class="avatar" aria-hidden="true">{user.username.slice(0, 1).toUpperCase()}</span>
          <div class="identity">
            <span class="small muted">Signed in as</span><strong>{user.username}</strong>
          </div>
        </div>
        <div class="account-actions">
          {#if user.role === "admin"}<div class="admin-account">
              <a
                href="/?view=account"
                class:active={tab === "Account"}
                aria-current={tab === "Account" ? "page" : undefined}
              >
                <Icon name="Account" size={17} />Account
              </a>
              <a
                href="/?view=devices"
                class:active={tab === "Devices"}
                aria-current={tab === "Devices" ? "page" : undefined}
              >
                <Icon name="Devices" size={17} />Sessions
              </a>
            </div>{/if}
          <button
            class="sign-out"
            disabled={busy || securityMutationActive}
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
      </section>
    </aside>
    <div class="workspace-content">
      {#if user.role === "admin"}{#key user.id}<AdministratorSecurityWarning /><RecentAuthentication
            onconfirmed={() => {
              notice = "Identity confirmed. Review your pending action and submit it again.";
            }}
            onsessionended={() => {
              clearAccount();
              error = "Your session ended. Sign in again.";
            }}
          />{/key}{/if}
      {#if localHistoryWarning}<p class="notice" role="alert">{localHistoryWarning}</p>
        {#if localRetry}<button onclick={() => localRetry?.()}>Retry saving link</button>{/if}{/if}
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
        {#if user.role === "user"}{#key user.id}<div hidden={tab !== "Send"}>
              <SendPanel
                accountId={user.id}
                initialDraft={restoredDraft}
                onselection={(draft) => (selectedDraft = draft)}
                oncreated={remember}
                onactive={(value) => (sendActive = value)}
              />
            </div>{/key}{/if}
        {#if tab === "Scan"}<ScanPanel authorize={authorizeScanner} />{/if}
        {#if tab === "Overview"}<AdminOverview />
        {:else if tab === "Resources" && user.role === "admin"}{#key user.id}<AdminResources
            />{/key}
        {:else if tab === "Traffic"}<TrafficPanel />
        {:else if tab === "Security" && user.role === "admin"}{#key user.id}<SecurityEvents />{/key}
        {:else if tab === "Server"}<h1>Server settings</h1>
          <p><a href="/?view=resources">Inspect resources and cleanup</a></p>
          <PublicTransferControl />
          <ServerSettings />
          <AbuseContactSettings />
          <ResourcePolicySettings />
        {:else if tab === "Settings"}<h1>Settings</h1>
          <p class="muted">Your account, devices, and server access.</p>
          <AccountUsage />
          <AccountTraffic />
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
        {:else if tab === "Receive"}<h1>Receive files</h1>
          <p class="muted">
            Anyone with your receive link can send you encrypted files within the server’s limits.
          </p>
          {#if receiveUnavailable}<p class="notice" role="status">
              This receive link has expired or was revoked. Create a new link to get more files.
            </p>{:else if receiveUrl}<LinkCard
              url={receiveUrl}
              label="Receive link — share it with someone to get files."
            /><button disabled={busy || receiveLoading} onclick={() => checkReceived(receiveId)}
              ><Icon name="Refresh" size={17} />Refresh received files</button
            >
          {/if}
          {#if receiveInfo && !receiveUnavailable}
            {#if receiveInfo.receive_protocol !== 2}<p class="notice">
                This older inbox is read-only. Its original shared link may have allowed other
                holders to read submissions. Create a new receive link for future files.
              </p>{/if}
            {#if receiveNeedsKey}<p class="notice">
                This browser has no private key for this inbox. Use the device that created it to
                save files. You can still view its status and revoke it.
              </p>{/if}
            <p class="muted small">
              {receiveInfo.summary.state === "ready"
                ? `${receiveInfo.summary.completed_files} completed files`
                : "Received file totals are updating"} · {receiveInfo.remaining_files === null
                ? `${receiveInfo.reserved_files} file allocations used · No creator file limit`
                : `${receiveInfo.reserved_files} of ${receiveInfo.max_files} allowances used · ${receiveInfo.remaining_files} allocations remaining`}
            </p>
          {/if}
          <label
            >Link name (optional)<input
              maxlength="200"
              bind:value={receiveName}
              placeholder="For example, Wedding photos"
            /></label
          >
          <p class="muted small">The name stays in this browser and can be changed in History.</p>
          <OptionalLimit
            bind:value={maxFiles}
            disabled={busy}
            label="Limit files accepted"
            description="Counts file allocations across every sender. Unfinished or abandoned uploads count; retries of the same upload do not. Deleting files does not restore this fixed limit."
          />
          <button class="primary" disabled={busy} onclick={createReceive}
            >{receiveUrl
              ? "Create another receive link"
              : busy
                ? "Creating link…"
                : "Create receive link"}</button
          >
        {:else if tab === "History"}<div class="heading">
            <h1>Your transfers</h1>
            <button disabled={busy || historyLoading} onclick={() => select("History")}
              ><Icon name="Refresh" size={17} />Refresh</button
            >
          </div>
          <AccountUsage />
          <label
            >Show<select bind:value={historyFilter}
              ><option value="all">All transfers</option><option value="transfers">Sent</option
              ><option value="slots">Receive links</option></select
            ></label
          >
          <p class="muted small">
            Encryption keys stay on the device that created the link. This browser can reopen its
            own links; transfers from other devices can still be revoked.
          </p>
          {#if historyError}<p role="alert">
              {historyError} The displayed page may be out of date.
            </p>{/if}
          {#if !historyLoading && !historyError && !transfers.length && !slots.length}<p
              class="empty"
            >
              {historyCursor || historyNext ? "No transfers on this page." : "No transfers yet."}
            </p>{/if}
          {#each [...transfers.map( (t) => ({ ...t, kind: "transfers" as const }), ), ...slots.map( (s) => ({ ...s, kind: "slots" as const }), )]
            .filter((item) => historyFilter === "all" || item.kind === historyFilter)
            .sort((a, b) => Date.parse(b.created_at || "") - Date.parse(a.created_at || "")) as item}<article
              class="resource"
              data-resource-id={item.id}
            >
              <div>
                <span class="type-badge"
                  ><Icon
                    name={item.kind === "slots" ? "Receive" : "Send"}
                    size={15}
                  />{item.kind === "slots" ? "Receive link" : "Sent"}</span
                >
                <strong class="history-title" title={historyTitle(item)}
                  >{#if compactTitle(historyTitle(item)) !== historyTitle(item)}<span
                      aria-hidden="true">{compactTitle(historyTitle(item))}</span
                    ><span class="sr-only">{historyTitle(item)}</span>{:else}{historyTitle(
                      item,
                    )}{/if}</strong
                >
                {#if item.created_at}<p class="muted small">
                    Created {new Date(item.created_at).toLocaleString()}
                  </p>{/if}
                <p>
                  {(item.kind === "slots" ? receivedFileCount(item) : resourceFileCount(item)) ===
                  null
                    ? "File totals updating"
                    : item.kind === "slots"
                      ? `${receivedFileCount(item)} files received`
                      : `${resourceFileCount(item)} files · ${status(item)}`}
                </p>
                {#if item.kind === "slots" && item.receive_protocol === 2}<p class="muted small">
                    {item.remaining_files == null
                      ? `${item.reserved_files ?? 0} file allocations used · No creator file limit`
                      : `${item.reserved_files ?? 0} of ${item.max_files} allowances used · ${item.remaining_files} allocations remaining`}
                  </p>{:else if item.kind === "transfers" && item.max_downloads}<p
                    class="muted small"
                  >
                    {item.max_downloads} download attempts per file
                  </p>{/if}
                <p class="muted small">
                  {labelFor(labels, item.kind, item.id).size
                    ? formatSize(labelFor(labels, item.kind, item.id).size ?? 0) + " · "
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
                <button
                  onclick={() => {
                    renameId = item.id;
                    renameKind = item.kind;
                    renameValue = labelFor(labels, item.kind, item.id).custom || "";
                  }}>Rename</button
                >
                {#if links[item.id]}<button onclick={() => copy(links[item.id])}
                    ><Icon name="Copy" size={17} />Copy link</button
                  >{#if item.kind === "transfers"}<a class="button" href={links[item.id]}>Open</a
                    >{/if}{/if}{#if item.kind === "slots"}<button
                    disabled={busy}
                    onclick={() => openReceive(item.id)}>View files</button
                  >{/if}<button
                  class="danger"
                  disabled={busy}
                  onclick={() => {
                    error = "";
                    pendingDelete = { id: item.id, kind: item.kind };
                  }}><Icon name="Revoke" size={17} />Revoke</button
                >
              </div>
              <details>
                <summary>Technical details</summary>
                <p>ID: {item.id}</p>
              </details>
              {#if renameId === item.id && renameKind === item.kind}<form
                  class="rename-form"
                  onsubmit={(e) => {
                    e.preventDefault();
                    rename();
                  }}
                >
                  <label
                    >Name on this device<input bind:value={renameValue} maxlength="200" /></label
                  >
                  <p class="muted small">
                    Only saved in this browser. Leave empty to restore the automatic title.
                  </p>
                  <button class="primary">Save name</button><button
                    type="button"
                    onclick={() => (renameId = "")}>Cancel</button
                  >
                </form>{/if}
            </article>{/each}
          {#if ![...transfers.map( (t) => ({ ...t, kind: "transfers" }), ), ...slots.map( (t) => ({ ...t, kind: "slots" }), )].some((item) => historyFilter === "all" || item.kind === historyFilter) && (transfers.length || slots.length)}<p
              class="empty"
            >
              No transfers in this filter on this page.
            </p>{/if}
          {#if historyPage > 1 || historyNext}<nav class="history-pages" aria-label="History pages">
              <button
                disabled={busy || historyLoading || !historyPrevious.length}
                onclick={() => turnHistory("previous")}>Newer transfers</button
              >
              <span class="muted small">Page {historyPage}</span>
              <button
                disabled={busy || historyLoading || !historyNext}
                onclick={() => turnHistory("next")}>Older transfers</button
              >
              {#if historyPage > 1}<button
                  disabled={busy || historyLoading}
                  onclick={() => turnHistory("first")}>First page</button
                >{/if}
            </nav>{/if}
          {#if pendingDelete}
            <RevokeDialog
              {busy}
              {error}
              oncancel={() => (pendingDelete = null)}
              onconfirm={revoke}
            />
          {/if}
        {:else if tab === "Devices"}<a
            class="back-link"
            href={user.role === "admin" ? "/?view=overview" : "/?view=settings"}
            >← {user.role === "admin" ? "Overview" : "Back to Settings"}</a
          >
          {#if user.role === "user"}<h1>Connect mobile app</h1>
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
          {/if}
          <h2>{user.role === "admin" ? "Signed-in sessions" : "Connected devices"}</h2>
          <p class="muted">
            Revoke a session to sign that device out. Up to 32 active sessions are allowed per
            account. Signing in or connecting another device at the limit signs out an older
            eligible session.
          </p>
          {#if sessionsLimited}<p class="notice" role="status">
              Showing {sessions.length} of {totalActiveSessionsExact
                ? ""
                : "at least "}{totalActiveSessions} active sessions. Older sessions are being removed
              to apply the session limit. Reload this page to check the remaining sessions.
            </p>{/if}
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
                    const owner = epoch;
                    await request(`/auth/sessions/${session.id}`, "DELETE");
                    if (owner !== epoch) return;
                    if (session.current) clearAccount();
                    else {
                      const result = await request<SessionsResponse>("/auth/sessions");
                      if (owner === epoch) updateSessions(result);
                    }
                  })}>Revoke session</button
              >
            </article>{/each}
        {:else if tab === "Account"}<a
            class="back-link"
            href={user.role === "admin" ? "/?view=overview" : "/?view=settings"}
            >← {user.role === "admin" ? "Overview" : "Back to Settings"}</a
          >
          <PasswordChange busy={busy || securityMutationActive} onchange={changePassword} />
          {#if user.role === "admin"}{#key user.id}<AdministratorSecurity
                onchanged={securityChanged}
                onmutation={securityMutation}
              />{/key}{/if}
        {:else if tab === "Users"}
          <h1>Manage users</h1>
          <p class="muted">
            Only administrators can create accounts. Disabling sign-in signs out devices and cancels
            pairing grants; existing public links remain active. Incident shutdown also revokes all
            of the account's links and schedules server-file cleanup.
          </p>
          {#if usersPrevious.length || usersNext}<nav aria-label="Account pages">
              <button disabled={busy || !usersPrevious.length} onclick={() => turnUsers("previous")}
                >Previous accounts</button
              >
              <span class="muted small">Page {usersPrevious.length + 1}</span>
              <button disabled={busy || !usersNext} onclick={() => turnUsers("next")}
                >More accounts</button
              >
            </nav>{/if}
          {#each users as account}<article class="resource">
              <div>
                <strong>{account.username}</strong>
                <p class="muted small">
                  {account.role === "admin" ? "Administrator" : "User"} · {account.disabled
                    ? "Disabled"
                    : account.must_change_password
                      ? "Password change required"
                      : "Active"}
                </p>
              </div>
              <div class="actions">
                <button
                  disabled={busy}
                  onclick={() => {
                    resetId = account.id;
                    resetPassword = "";
                    resetConfirmation = "";
                  }}>Reset password</button
                ><button
                  disabled={busy || account.id === user.id}
                  onclick={() => {
                    if (!account.disabled) {
                      error = "";
                      accountAction = { account, mode: "disable" };
                      return;
                    }
                    void act(async () => {
                      const updated = await request<{ user: User }>(
                        `/admin/users/${account.id}`,
                        "PATCH",
                        { disabled: !account.disabled },
                      );
                      users = users.map((u) => (u.id === updated.user.id ? updated.user : u));
                    });
                  }}>{account.disabled ? "Enable sign-in" : "Disable sign-in"}</button
                >
                {#if account.role === "user"}<button
                    class="danger"
                    disabled={busy}
                    onclick={() => {
                      error = "";
                      accountAction = { account, mode: "shutdown" };
                    }}>Incident shutdown</button
                  >{/if}
              </div>
              {#if account.role === "user"}<AccountTraffic
                  accountId={account.id}
                  username={account.username}
                />{/if}
            </article>{/each}
          {#if accountAction}<IncidentConfirmDialog
              title={accountAction.mode === "disable"
                ? `Disable sign-in for ${accountAction.account.username}?`
                : `Shut down ${accountAction.account.username}'s transfers?`}
              description={accountAction.mode === "disable"
                ? "Disable future sign-in, revoke this account's sessions and cancel its pairing grants. Existing public receive and download links will keep working. To revoke those links too, cancel and choose Incident shutdown."
                : "Disable this account, revoke all sessions and pairing grants, revoke every existing send and receive link, stop active transfers, and schedule deletion of its server files. Already downloaded copies remain. Re-enabling the account will not restore these links."}
              action={accountAction.mode === "disable"
                ? "Disable sign-in only"
                : "Shut down account and revoke all links"}
              {busy}
              {error}
              oncancel={() => {
                accountAction = null;
                error = "";
              }}
              onconfirm={confirmAccountAction}
            />{/if}
          {#if resetId}<form
              class="confirm"
              onsubmit={(e) => {
                e.preventDefault();
                void act(async () => {
                  if (resetPassword !== resetConfirmation)
                    throw new Error("The passwords do not match.");
                  await request(`/admin/users/${resetId}`, "PATCH", { password: resetPassword });
                  const self = resetId === user?.id;
                  resetId = "";
                  resetPassword = "";
                  if (self) clearAccount();
                  else
                    notice =
                      "Password reset. Existing sessions have been revoked. Regular users must replace their temporary password at next sign-in.";
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
              ><label
                >Confirm new password<input
                  type="password"
                  autocomplete="new-password"
                  minlength="12"
                  required
                  bind:value={resetConfirmation}
                /></label
              >
              <p class="muted small">
                Regular users must replace this temporary password at their next sign-in. Existing
                sessions are revoked.
              </p>
              <button class="primary" disabled={busy}>Save new password</button><button
                type="button"
                onclick={() => (resetId = "")}>Cancel</button
              >
            </form>{/if}
          <h2>Create account</h2>
          <form
            onsubmit={(e) => {
              e.preventDefault();
              void act(async () => {
                if (newPassword !== newConfirmation) throw new Error("The passwords do not match.");
                const result = await request<{ user: User }>("/admin/users", "POST", {
                  username: newUsername,
                  password: newPassword,
                  role: newRole,
                });
                users = [...users, result.user].slice(-50);
                newUsername = "";
                newPassword = "";
                newConfirmation = "";
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
              >Confirm temporary password<input
                type="password"
                autocomplete="new-password"
                minlength="12"
                required
                bind:value={newConfirmation}
              /></label
            ><label
              >Role<select bind:value={newRole}
                ><option value="user">User</option><option value="admin">Administrator</option
                ></select
              ></label
            >
            <p class="muted small">
              Use at least 12 characters. Regular users must replace this temporary password at
              first sign-in. Administrators manage the server and cannot create transfers; their
              accounts are exempt from the first-change requirement.
            </p>
            <button class="primary" disabled={busy}>Create account</button>
          </form>
        {/if}
        {#if tab === "Receive" && receiveId && !receiveUnavailable}
          <section aria-label="Received files" aria-busy={receiveLoading}>
            <div class="heading">
              <h2>Received files</h2>
              <button disabled={busy || receiveLoading} onclick={() => checkReceived(receiveId)}
                >Refresh</button
              >
            </div>
            {#if receiveError}<p role="alert">{receiveError}</p>{/if}
            {#if receiveInfo}
              {#if !received.length}<p class="muted">
                  No completed submissions on this page. Arrivals update automatically.
                </p>{/if}
              {#each received as item (item.id)}{#if !receiveNeedsKey}<a
                    class="received"
                    href={item.url}>{item.count} file{item.count === 1 ? "" : "s"} · Save files</a
                  >{:else}<p>
                    {item.count} file{item.count === 1 ? "" : "s"} · Private key is on the creating device
                  </p>{/if}{/each}
              <nav class="inbox-pages" aria-label="Received files pages">
                <button
                  disabled={receiveLoading || !receivePrevious.length}
                  onclick={() => turnReceived("previous")}>Previous</button
                >
                <span class="muted small">Page {receivePage}</span>
                <button
                  disabled={receiveLoading || receiveNext === null}
                  onclick={() => turnReceived("next")}>Next</button
                >
                {#if receivePage > 1}<button
                    disabled={receiveLoading}
                    onclick={() => turnReceived("first")}>First page</button
                  >{/if}
              </nav>
              {#if receivePage > 1 && !receivePrevious.length}<p class="muted small">
                  Earlier pages are available from First page.
                </p>{/if}
            {/if}
          </section>
        {/if}
      </section>
    </div>
  </div>
{/if}

<style>
  .inbox-pages {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    align-items: center;
    margin-top: 1rem;
  }
  .login {
    max-width: 440px;
    margin: clamp(1rem, 5vh, 3rem) auto;
    padding: clamp(1.5rem, 4vw, 2.5rem);
    border-radius: 20px;
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
    margin-top: 2rem;
    border: 1px solid var(--divider);
    border-radius: 14px;
    background: var(--surface);
  }
  .account-identity {
    display: grid;
    grid-template-columns: 34px minmax(0, 1fr);
    align-items: center;
    gap: 0.65rem;
    padding: 0.9rem;
  }
  .account-actions {
    padding: 0.4rem;
    border-top: 1px solid var(--divider);
    border-radius: 0 0 13px 13px;
    background: var(--elevated);
  }
  .avatar {
    background: var(--accent);
    color: var(--primary);
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
    white-space: nowrap;
    flex-shrink: 0;
    width: 100%;
    justify-content: flex-start;
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
  .type-badge {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--primary);
    font-size: 0.75rem;
    font-weight: 650;
    margin-bottom: 0.4rem;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  .history-title {
    display: block;
  }
  .resource details,
  .rename-form {
    flex-basis: 100%;
    font-size: 0.85rem;
  }
  .resource details summary {
    cursor: pointer;
    color: var(--muted);
  }
  .admin-account {
    display: grid;
    gap: 0.15rem;
    padding-bottom: 0.4rem;
    margin-bottom: 0.4rem;
    border-bottom: 1px solid var(--divider);
  }
  .admin-account a {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    min-height: 44px;
    padding: 0.5rem 0.75rem;
    border-radius: 8px;
    color: var(--muted);
    text-decoration: none;
    font-size: 0.85rem;
  }
  .admin-account a:hover,
  .admin-account a.active {
    background: var(--accent);
    color: var(--primary);
  }

  .resource {
    flex-wrap: wrap;
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
      grid-template-columns: repeat(5, minmax(0, 1fr));
      border-bottom: 1px solid var(--divider);
      gap: 0;
    }
    nav a {
      justify-content: center;
      flex-wrap: nowrap;
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
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      align-items: center;
      margin: 0 0 1rem;
    }
    .account-identity {
      grid-template-columns: minmax(0, 1fr);
      padding: 0.75rem;
    }
    .account-actions {
      display: contents;
    }
    .sign-out {
      grid-column: 2;
      grid-row: 1;
      width: auto;
      margin: 0.4rem;
    }
    .admin-account {
      grid-column: 1 / -1;
      grid-row: 2;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 0.3rem;
      padding: 0.4rem;
      margin: 0;
      border-top: 1px solid var(--divider);
      border-bottom: 0;
      border-radius: 0 0 13px 13px;
      background: var(--elevated);
    }
    nav.admin-nav {
      grid-template-columns: repeat(5, minmax(0, 1fr));
    }
    nav.admin-nav a {
      flex-direction: column;
      justify-content: flex-start;
    }
    nav.admin-nav .nav-label {
      white-space: normal;
      text-align: center;
    }
  }
  @media (max-width: 600px) {
    nav a {
      flex-direction: column;
      gap: 0.5rem;
      min-height: 72px;
      font-size: 0.72rem;
      padding: 0.7rem 0.1rem;
    }
    .nav-label {
      white-space: nowrap;
    }
    nav.admin-nav a {
      font-size: 0.7rem;
    }
  }
  @media (max-width: 540px) {
    .resource {
      flex-wrap: wrap;
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
