<script lang="ts">
  import { hasStatus } from "$lib/api-error";
  import {
    message as m,
    t,
    date,
    number,
    dateArgument,
    LocalizedError,
    errorText,
    translate,
    type DisplayText,
  } from "$lib/i18n";

  import { onMount, untrack, tick } from "svelte";
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
  import AdminSettingsWorkspace from "$lib/components/AdminSettingsWorkspace.svelte";
  import SecurityEvents from "$lib/components/SecurityEvents.svelte";
  import { labelFor, labelKey, compactTitle, type HistoryLabels } from "$lib/history-labels";
  import AccountUsage from "$lib/components/AccountUsage.svelte";
  import AccountTraffic from "$lib/components/AccountTraffic.svelte";
  import IncidentConfirmDialog from "$lib/components/IncidentConfirmDialog.svelte";
  import { loadUsersPage, HISTORY_PREVIOUS_WINDOW } from "$lib/resource-history";
  import { HistoryController } from "$lib/history-controller";
  import { historyScope } from "$lib/history-cache";
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
  import { createSlot, getSlotInbox, renameLinkTitle, type InboxPage } from "$lib/api";
  import {
    loadLocalHistory,
    saveLocalLink,
    removeLocalLink,
    updateLocalLabel,
    type LocalHistoryPage,
  } from "$lib/local-history";
  import { INBOX_PREVIOUS_WINDOW } from "$lib/inbox-page";
  import { rememberInboxPosition, restoreInboxPosition } from "$lib/inbox-navigation";
  import { exportKey } from "$lib/crypto";
  import { generateReceiveKeyPair } from "$lib/receive-crypto";
  import { storeReceiveKey, loadReceiveKey, removeReceiveKey } from "$lib/receive-keys";
  import OptionalLimit from "$lib/components/OptionalLimit.svelte";
  import { normalizeLinkTitle, downloadLinkExhausted } from "$lib/link-title";
  type Tab =
    | "Usage"
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
  let receiveSharing = $state(false);
  let receiveCreating = $state(false);
  const emptySendDraft = (): SendDraft => ({ files: [], maxDownloads: 0, policyLocked: false });
  let pendingDraft = emptySendDraft(),
    pendingOwner = "";
  let selectedDraft = emptySendDraft();
  let restoredDraft = $state<SendDraft>(emptySendDraft());
  let sendActive = $state(false),
    showPassword = $state(false),
    historyFilter = $state("all"),
    liveMessage = $state<DisplayText>(""),
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
    pairedDevice = $state<DisplayText>("");
  const destinations = [
    "Usage",
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
    { name: "Usage", title: m("usage"), description: m("storageAndTraffic") },
    {
      name: "Account",
      title: m("account"),
      description: m("accountSecurityHelp"),
    },
    {
      name: "Devices",
      title: m("connectedDevices"),
      description: m("connectedDevicesHelp"),
    },
    {
      name: "Users",
      title: m("users"),
      description: m("createAccountsHelp"),
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
    error = $state<DisplayText>(""),
    notice = $state<DisplayText>("");
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
    if (securityMutationActive) {
      navigation.cancel();
      return;
    }
    const target = navigation.to?.url;
    if (
      user &&
      tab === "Receive" &&
      receiveId &&
      target?.origin === location.origin &&
      target.pathname.startsWith("/d/") &&
      target.searchParams.get("inbox") === receiveId
    ) {
      rememberInboxPosition(sessionStorage, user.id, receiveId, {
        cursor: receiveCursor,
        previous: receivePrevious,
        page: receivePage,
        scroll: window.scrollY,
      });
    }
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
  let localHistoryWarning = $state<DisplayText>("");
  let localRetry = $state<(() => Promise<void>) | null>(null);
  let usersCursor = $state(""),
    usersNext = $state<string | null>(null),
    usersPrevious = $state<string[]>([]);
  let historyCursor = $state(""),
    historyNext = $state<string | null>(null),
    historyPrevious = $state<string[]>([]),
    historyLoading = $state(false),
    historyPage = $state(1),
    historyError = $state<DisplayText>("");
  let historyGeneration = 0;
  let historyController: HistoryController | null = null;
  let historySelection = "";
  let pendingHistoryTurn: { cursor: string; direction: "next" | "previous" | "first" } | null =
    null;
  let receiveUrl = $state(""),
    receiveId = $state("");
  const receiveTitle = $derived(
    receiveInfo?.title || labelFor(labels, "slots", receiveId).custom || m("receiveFiles"),
  );
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
            throw new LocalizedError(m("accountChanged"));
          if (identity.user.id !== user?.id) {
            clearAccount();
            error = m("yourAccountChangedInAnotherTabSignInAgain");
            throw new LocalizedError(m("accountChanged"));
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
                pairedDevice = result.device_name || m("phone");
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
            liveMessage = m("offlineLastUpdatedValueReconnecting", {
              arg0: lastUpdated ? dateArgument(lastUpdated) : m("notYet"),
            });
            if (err instanceof AccountError && err.status === 401) {
              clearAccount(true);
              error = m("yourSessionEndedSignInAgain");
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
      historyController?.visibility(!document.hidden);
      if (!document.hidden && !polling) {
        clearTimeout(timerPoll);
        void poll();
      }
    }
    function changed(event: StorageEvent) {
      if (event.key === "psst.auth-change" && (user || recoveryCodes.length || factorRequired)) {
        clearAccount();
        error = m("yourSignInChangedInAnotherTabSignIn");
      }
    }
    document.addEventListener("visibilitychange", resume);
    window.addEventListener("storage", changed);
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => {
      historyGeneration++;
      historyController?.dispose();
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
    const owner = epoch;
    try {
      const account = (await request<{ user: User }>("/auth/me")).user;
      // A route change can destroy this instance while its account request is
      // pending. Only the live instance may consume the inbox return checkpoint.
      if (owner !== epoch) return;
      user = account;
      loadAccount();
      // Authentication has completed; History may now show cached rows while
      // its cancellable metadata refresh is still awaiting the network.
      loading = false;
      await select(routeTab(), false);
    } catch (err) {
      if (owner !== epoch) return;
      if (!(err instanceof AccountError && err.status === 401)) error = message(err);
      try {
        const setup = (await request<{ setup_required: boolean }>("/auth/status")).setup_required;
        if (owner === epoch) setupRequired = setup;
      } catch {
        /* Main error remains visible. */
      }
    } finally {
      if (owner === epoch) loading = false;
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
    if (owner !== epoch || tab !== "History" || !user) return true;
    const account = user.id;
    if (!historyController) {
      historyController = new HistoryController({
        scope: historyScope(location.origin, account),
        busy: (value) => {
          if (owner === epoch) historyLoading = value;
        },
        error: (cause) => {
          if (owner !== epoch || user?.id !== account || tab !== "History") return;
          if (cause instanceof AccountError && cause.status === 401) {
            clearAccount(true);
            error = m("yourSessionEndedSignInAgain");
          } else historyError = message(cause);
        },
        page: async (result) => {
          const revision = historyGeneration,
            turn = pendingHistoryTurn;
          let local: LocalHistoryPage | null = null;
          try {
            local = await loadLocalHistory(account, [
              ...result.transfers.map((item) => ({ kind: "transfers" as const, id: item.id })),
              ...result.slots.map((item) => ({ kind: "slots" as const, id: item.id })),
            ]);
          } catch {
            /* Server metadata remains usable without private storage. */
          }
          if (
            owner !== epoch ||
            user?.id !== account ||
            tab !== "History" ||
            revision !== historyGeneration
          )
            return;
          if (turn && turn === pendingHistoryTurn) {
            if (
              turn.direction === "next" &&
              result.next_cursor &&
              (result.next_cursor === historyCursor || historyPrevious.includes(result.next_cursor))
            )
              throw new LocalizedError(m("thisServerReturnedARepeatedHistoryPageRefreshOr"));
            historyPrevious =
              turn.direction === "next"
                ? [...historyPrevious, historyCursor].slice(-HISTORY_PREVIOUS_WINDOW)
                : turn.direction === "previous"
                  ? historyPrevious.slice(0, -1)
                  : [];
            historyPage =
              turn.direction === "next"
                ? historyPage + 1
                : turn.direction === "previous"
                  ? historyPage - 1
                  : 1;
            historyCursor = turn.cursor;
            pendingHistoryTurn = null;
          }
          historyNext = result.next_cursor;
          if (JSON.stringify(transfers) !== JSON.stringify(result.transfers))
            transfers = result.transfers;
          if (JSON.stringify(slots) !== JSON.stringify(result.slots)) slots = result.slots;
          links = local?.links ?? {};
          labels = local?.labels ?? {};
          if (!localRetry)
            localHistoryWarning = local ? "" : m("localLinksAndNamesCouldNotBeLoadedExisting");
          historyError = "";
        },
      });
      historyController.visibility(!document.hidden);
    }
    const filter =
      historyFilter === "transfers" ? "transfer" : historyFilter === "slots" ? "slot" : undefined;
    const selection = `${filter ?? ""}:${cursor}`;
    if (!direction && selection === historySelection) return historyController.refresh();
    historyGeneration++;
    pendingHistoryTurn = direction ? { cursor, direction } : null;
    historySelection = selection;
    const result = await historyController.enter(filter, cursor);
    if (!result && pendingHistoryTurn) {
      pendingHistoryTurn = null;
      historySelection = "";
    }
    return result;
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
    receiveError = $state<DisplayText>("");
  let receiveGeneration = 0;
  let receiveRequest: AbortController | null = null;
  let receiveReturnScroll: number | null = null;
  function resetReceive(id: string) {
    receiveGeneration++;
    receiveRequest?.abort();
    receiveId = id;
    receiveCursor = "";
    receiveNext = null;
    receivePrevious = [];
    receivePage = 1;
    receiveSharing = false;
    receiveCreating = false;
    receiveReturnScroll = null;
    if (user && typeof sessionStorage !== "undefined") {
      const position = restoreInboxPosition(sessionStorage, user.id, id);
      if (position) {
        receiveCursor = position.cursor;
        receivePrevious = position.previous;
        receivePage = position.page;
        receiveReturnScroll = position.scroll;
      }
    }
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
        localHistoryWarning = local ? "" : m("localLinksAndNamesCouldNotBeLoadedExisting");
      if (
        direction === "next" &&
        slot.next_cursor &&
        (slot.next_cursor === receiveCursor || receivePrevious.includes(slot.next_cursor))
      )
        throw new LocalizedError(m("repeatedInboxPage"));
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
      if (receiveReturnScroll !== null) {
        const scroll = receiveReturnScroll;
        receiveReturnScroll = null;
        await tick();
        if (current()) window.scrollTo({ top: scroll, behavior: "instant" });
      }
    } catch (cause) {
      if (!current()) return;
      if (hasStatus(cause, 404, 410)) {
        receiveUnavailable = true;
        receiveInfo = null;
        received = [];
      } else receiveError = m("couldNotRefreshReceivedFilesTheDisplayedPageMay");
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
          error = m("couldNotCancelTheCodeItWillExpireAutomatically");
      }
  }
  function message(err: unknown) {
    return err instanceof Error ? errorText(err) : m("somethingWentWrongPleaseTryAgain");
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
    historyController?.dispose();
    historyController = null;
    historySelection = "";
    pendingHistoryTurn = null;
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
        error = m("yourSessionEndedSignInAgain");
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
    notice = codes ? "" : m("authenticatorDisabledAllSessionsEndedSignInAgain");
  }
  async function login() {
    await act(async () => {
      const owner = epoch;
      let result: { user: User };
      try {
        result = await request<{ user: User }>("/auth/login", "POST", {
          username,
          password,
          device_name: translate(m("webBrowser")),
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
          error = errorText(cause);
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
      localHistoryWarning = m("thisLinkOrItsNameCouldNotBeSaved");
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
        error = m("yourAccountChangedSignInAgain");
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
          error = m("yourSessionEndedSignInAgain");
        } else error = m("couldNotVerifyYourSessionCheckYourConnectionAnd");
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
        : [...mainDestinations, "Account", "Devices", "Usage"];
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
      historyController?.stop();
      historySelection = "";
      pendingHistoryTurn = null;
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
      const selectedTitle = normalizeLinkTitle(receiveName);
      const pair = await generateReceiveKeyPair();
      const publicKey = await exportKey(pair.publicKey);
      const slot = await createSlot(publicKey, selectedMaxFiles, selectedTitle);
      try {
        if (owner !== epoch || user?.id !== account)
          throw new LocalizedError(m("yourAccountChangedCreateTheReceiveLinkAgain"));
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
        throw new LocalizedError(m("couldNotVerifyTheNewInboxItRemainsIn"));
      }
      if (
        accepted.receive_protocol !== 2 ||
        accepted.recipient_public_key !== publicKey ||
        accepted.max_files !== selectedMaxFiles ||
        (accepted.title ?? null) !== selectedTitle
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
        throw new LocalizedError(
          m("thisServerDidNotAcceptThePrivateInboxProtocol", {
            arg0: removed ? m("unusedInboxRemoved") : m("unusedInboxRevoke"),
          }),
        );
      }
      if (owner !== epoch || user?.id !== account) return;
      receiveUrl = `${location.origin}/u/${slot.id}#v2.${publicKey}`;
      await remember(slot.id, receiveUrl);
      if (owner !== epoch || user?.id !== account) return;
      receiveName = "";
      receiveCreating = false;
      receiveSharing = false;
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
        notice = m("signInDisabledAndSessionsPairingsRevokedExistingPublic");
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
        notice = m("incidentShutdownAppliedValueSessionsValuePairingGrantsValue", {
          arg0: result.revoked_sessions,
          arg1: result.revoked_pairings,
          arg2: result.revoked_transfers,
          arg3: result.revoked_slots,
        });
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
          localHistoryWarning = m("theServerLinkWasRevokedButItsLocalCopy");
      }
      if (owner !== epoch || user?.id !== account) return;
      transfers = transfers.filter((t) => t.id !== target.id);
      slots = slots.filter((s) => s.id !== target.id);
      delete links[target.id];
      links = { ...links };
      pendingDelete = null;
      notice = m("linkRevokedAndServerFilesDeleted");
      historyController?.mutation();
    });
  }
  async function changePassword(current: string, replacement: string) {
    await act(async () => {
      await request("/auth/password", "POST", { current_password: current, password: replacement });
      clearAccount(true);
      try {
        localStorage.setItem("psst.auth-change", String(Date.now()));
      } catch {}
      notice = m("passwordChangedSignInWithYourNewPassword");
    });
  }
  function historyTitle(item: Resource & { kind: "transfers" | "slots" }) {
    const label = labelFor(labels, item.kind, item.id);
    return (
      item.title ||
      label.custom ||
      label.title ||
      (item.kind === "slots" ? m("receiveLink") : m("sentFiles"))
    );
  }
  async function rename() {
    if (!user || busy) return;
    const account = user.id,
      owner = epoch,
      id = renameId,
      kind = renameKind;
    await act(async () => {
      const result = await renameLinkTitle(kind, id, normalizeLinkTitle(renameValue));
      if (owner !== epoch || user?.id !== account) return;
      transfers = transfers.map((item) =>
        item.id === id && kind === "transfers" ? { ...item, title: result.title } : item,
      );
      slots = slots.map((item) =>
        item.id === id && kind === "slots" ? { ...item, title: result.title } : item,
      );
      if (kind === "slots" && receiveId === id && receiveInfo)
        receiveInfo = { ...receiveInfo, title: result.title };
      try {
        const label = await updateLocalLabel(account, { kind, id }, { custom: undefined });
        if (owner === epoch && user?.id === account)
          labels = { ...labels, [labelKey(kind, id)]: label };
      } catch {
        if (owner === epoch && user?.id === account)
          localHistoryWarning = m("titleSavedLocalHistoryCouldNotBeUpdated");
      }
      if (owner === epoch && user?.id === account && renameId === id && renameKind === kind)
        renameId = "";
      historyController?.mutation();
    });
  }
  async function filterHistory(event: Event) {
    historyFilter = (event.currentTarget as HTMLSelectElement).value;
    historyCursor = "";
    historyPrevious = [];
    historyPage = 1;
    historyNext = null;
    transfers = [];
    slots = [];
    await refreshHistory();
  }
  function beginRename(kind: "transfers" | "slots", id: string, title?: string | null) {
    renameId = id;
    renameKind = kind;
    renameValue = title || labelFor(labels, kind, id).custom || "";
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
      notice = m("linkCopied");
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
    notice = copied ? m("linkCopied") : m("copyThisLinkValue", { arg0: value });
  }
  function expiry(value: string) {
    const remaining = Date.parse(value) - now;
    return remaining <= 0
      ? m("expired")
      : remaining < 3600000
        ? m("inValueMinutes", { arg0: Math.ceil(remaining / 60000) })
        : m("inValueHours", { arg0: Math.ceil(remaining / 3600000) });
  }
  function status(item: Resource) {
    if (Date.parse(item.expires_at) < now) return m("expired");
    if (downloadLinkExhausted(item)) return m("downloadLimitReached");
    return item.downloaded_at
      ? m("downloaded")
      : item.status === "complete"
        ? item.download_count
          ? m("downloadStarted")
          : m("readyToDownload")
        : item.status === "pending"
          ? m("uploadUnfinished")
          : item.status === "revoked"
            ? m("revoked")
            : item.status || m("ready");
  }
  function tabLabel(value: string): DisplayText {
    return (
      (
        {
          Send: m("send"),
          Receive: m("receive"),
          Scan: m("scan"),
          History: m("history"),
          Settings: m("settings"),
          Overview: m("overview"),
          Users: m("users"),
          Traffic: m("traffic"),
          Security: m("security"),
          Server: m("server"),
          Resources: m("resources"),
          Account: m("account"),
          Devices: m("devices"),
          Usage: m("usage"),
        } as Record<string, DisplayText>
      )[value] ?? value
    );
  }
</script>

<svelte:head><title>{$t(user ? tabLabel(tab) : m("signIn"))} · {$t(BRAND)}</title></svelte:head>
{#if loading}<p role="status">{$t(m("loadingYourAccount"))}</p>
{:else if recoveryCodes.length}<RecoveryCodes
    codes={recoveryCodes}
    onacknowledge={() => {
      recoveryCodes = [];
      notice = m("recoveryCodesAcknowledged");
    }}
  />
{:else if !user}
  <section class="panel login">
    <h1>{$t(m("signInTo"))} {$t(BRAND)}</h1>
    <p class="muted login-intro">{$t(m("yourPrivateSpaceToSendAndReceiveFiles"))}</p>
    {#if setupRequired}<p class="notice">
        {$t(m("thisServerNeedsItsFirstAdministratorTheServerOperator"))}
      </p>
    {:else if factorRequired}<form
        onsubmit={(event) => {
          event.preventDefault();
          void login();
        }}
      >
        <h2>{$t(m("administratorVerification"))}</h2>
        <p>
          {$t(m("finishSigningInAs"))}
          {$t(username)}{$t(m("noSessionIsCreatedUntilYourCodeIsAccepted"))}
        </p>
        <label
          >{$t(loginRecovery ? m("recoveryCode") : m("authenticatorCode"))}<input
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
          }}>{$t(loginRecovery ? m("useAuthenticatorCode") : m("useARecoveryCode"))}</button
        >
        <button class="primary" disabled={busy}
          >{$t(busy ? m("verifying") : m("verifyAndSignIn"))}</button
        >
        <button type="button" disabled={busy} onclick={cancelFactorLogin}
          >{$t(m("backToPasswordSignIn"))}</button
        >
      </form>
    {:else}<form
        onsubmit={(e) => {
          e.preventDefault();
          void login();
        }}
      >
        <label for="login-username">{$t(m("username"))}</label>
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
        <label for="login-password">{$t(m("password"))}</label>
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
            aria-label={$t(showPassword ? m("hidePassword") : m("showPassword"))}
            aria-pressed={showPassword}
            aria-controls="login-password"
            onclick={() => (showPassword = !showPassword)}
            ><Icon name={showPassword ? "EyeOff" : "Eye"} /></button
          >
        </div>
        <button class="primary login-submit" disabled={busy}>
          {$t(busy ? m("signingIn") : m("signIn"))}<Icon name="Arrow" size={18} />
        </button>
      </form>
      <p class="muted small login-help">
        {$t(m("needAnAccountOrPasswordReset"))}<br />{$t(m("contactYourServerAdministrator"))}
      </p>{/if}
    <p class="muted small login-note">{$t(m("peopleUsingYourSharedLinksDoNotNeedAn"))}</p>
    {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}{#if notice}<p
        class="notice"
        role="status"
      >
        {$t(notice)}
      </p>{/if}
  </section>
{:else if user.must_change_password}
  <section class="panel login">
    <PasswordChange {busy} requiredChange onchange={changePassword} />
    {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}
    <button
      disabled={busy}
      onclick={() =>
        act(async () => {
          await request("/auth/logout", "POST");
          clearAccount();
        })}>{$t(m("signOut"))}</button
    >
  </section>
{:else}
  <div class="workspace">
    <aside class="sidebar">
      <p class="sidebar-label">
        {$t(user.role === "admin" ? m("administration") : m("yourWorkspace"))}
      </p>
      <nav class:admin-nav={user.role === "admin"} aria-label={$t(m("accountNavigation"))}>
        {#each user.role === "admin" ? adminDestinations : mainDestinations as item}
          <a
            href={destinationUrl(item)}
            aria-label={$t(
              item === "Scan"
                ? m("scanQRCode")
                : item === "Server"
                  ? m("serverSettings")
                  : item === "Security"
                    ? m("securityActivity")
                    : tabLabel(item),
            )}
            aria-current={tab === item
              ? "page"
              : (item === "Overview" && tab === "Resources") ||
                  (item === "Settings" && ["Account", "Devices", "Usage"].includes(tab))
                ? "location"
                : undefined}
            class:active={tab === item ||
              (item === "Overview" && tab === "Resources") ||
              (item === "Settings" && ["Account", "Devices", "Usage"].includes(tab))}
            data-sveltekit-keepfocus
            data-sveltekit-noscroll
          >
            <Icon name={item === "Scan" ? "QRCode" : item === "Server" ? "Settings" : item} /><span
              class="nav-label"
              >{$t(
                item === "Scan"
                  ? m("scan")
                  : item === "Server"
                    ? m("serverSettings")
                    : item === "Security"
                      ? m("securityActivity")
                      : tabLabel(item),
              )}</span
            >
          </a>
        {/each}
      </nav>
      <section class="account-bar" aria-label={$t(m("signedInAccount"))}>
        <div class="account-identity">
          <span class="avatar" aria-hidden="true"
            >{$t(user.username.slice(0, 1).toUpperCase())}</span
          >
          <div class="identity">
            <span class="small muted">{$t(m("signedInAs"))}</span><strong
              >{$t(user.username)}</strong
            >
          </div>
        </div>
        <div class="account-actions">
          {#if user.role === "admin"}<div class="admin-account">
              <a
                href="/?view=account"
                class:active={tab === "Account"}
                aria-current={tab === "Account" ? "page" : undefined}
              >
                <Icon name="Account" size={17} />{$t(m("account"))}
              </a>
              <a
                href="/?view=devices"
                class:active={tab === "Devices"}
                aria-current={tab === "Devices" ? "page" : undefined}
              >
                <Icon name="Devices" size={17} />{$t(m("sessions"))}
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
              })}><Icon name="SignOut" size={17} />{$t(m("signOut"))}</button
          >
        </div>
      </section>
    </aside>
    <div class="workspace-content">
      {#if user.role === "admin"}{#key user.id}<AdministratorSecurityWarning /><RecentAuthentication
            onconfirmed={() => {
              notice = m("identityConfirmedReview");
            }}
            onsessionended={() => {
              clearAccount();
              error = m("yourSessionEndedSignInAgain");
            }}
          />{/key}{/if}
      {#if localHistoryWarning}<p class="notice" role="alert">{$t(localHistoryWarning)}</p>
        {#if localRetry}<button onclick={() => localRetry?.()}>{$t(m("retrySavingLink"))}</button
          >{/if}{/if}
      {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}{#if notice}<p
          class="notice"
          role="status"
        >
          {$t(notice)}
        </p>{/if}
      {#if sendActive && tab !== "Send"}<p class="notice">
          <button onclick={() => select("Send")}>{$t(m("returnToYourTransfer"))}</button>
          {$t(m("yourSelectedFilesAndUploadStayHere"))}
        </p>{/if}
      {#if liveMessage && (tab === "History" || tab === "Receive" || tab === "Devices")}<p
          role="status"
          class="notice"
        >
          {$t(liveMessage)}
        </p>{/if}
      <section class="workspace-panel">
        {#if user.role === "user"}{#key user.id}<div hidden={tab !== "Send"}>
              <SendPanel
                accountId={user.id}
                visible={tab === "Send"}
                initialDraft={restoredDraft}
                onselection={(draft) => (selectedDraft = draft)}
                oncreated={remember}
                onactive={(value) => (sendActive = value)}
              />
            </div>{/key}{/if}
        {#if tab === "Scan"}<ScanPanel authorize={authorizeScanner} />{/if}
        {#if user.role === "admin"}{#key user.id}<AdminSettingsWorkspace
              activePage={tab}
            />{/key}{/if}
        {#if tab === "Overview"}<AdminOverview />
        {:else if tab === "Resources" && user.role === "admin"}{#key user.id}<AdminResources
            />{/key}
        {:else if tab === "Security" && user.role === "admin"}{#key user.id}<SecurityEvents />{/key}
        {:else if tab === "Usage" && user.role === "user"}
          <a href="/?view=settings" class="back-link">{$t(m("settings_e1124"))}</a>
          <h1>{$t(m("usage"))}</h1>
          <AccountUsage /><AccountTraffic />
        {:else if tab === "Settings"}<h1>{$t(m("settings"))}</h1>
          <p class="muted">{$t(m("yourAccountDevicesAndServerAccess"))}</p>
          <div class="settings-list">
            {#each settingsDestinations.filter((item) => item.name !== "Users" || user?.role === "admin") as item}
              <a
                href={destinationUrl(item.name)}
                aria-label={$t(item.title)}
                data-sveltekit-keepfocus
                data-sveltekit-noscroll
              >
                <span class="setting-icon"
                  ><Icon name={item.name === "Usage" ? "Traffic" : item.name} size={22} /></span
                >
                <span
                  ><strong>{$t(item.title)}</strong><span class="muted small setting-description"
                    >{$t(item.description)}</span
                  ></span
                >
                <Icon name="Arrow" />
              </a>
            {/each}
          </div>
        {:else if tab === "Receive"}
          <div class="heading">
            <h1 title={$t(receiveId && !receiveCreating ? receiveTitle : undefined)}>
              {$t(receiveId && !receiveCreating ? receiveTitle : m("receiveFiles"))}
            </h1>
            {#if receiveId && !receiveCreating && !receiveUnavailable}<button
                disabled={busy}
                onclick={() => beginRename("slots", receiveId, receiveInfo?.title)}
                >{$t(m("rename"))}</button
              >{/if}
          </div>
          {#if receiveUnavailable}<p class="notice" role="status">
              {$t(m("thisReceiveLinkIsNoLongerAvailable"))}
            </p>{/if}
          {#if !receiveId || receiveCreating || receiveUnavailable}
            <form
              class="receive-create"
              onsubmit={(event) => {
                event.preventDefault();
                void createReceive();
              }}
            >
              <label
                >{$t(m("linkTitleOptional"))}<input
                  maxlength="400"
                  bind:value={receiveName}
                  placeholder={$t(m("forExampleWeddingPhotos"))}
                /></label
              >
              <p class="muted small">{$t(m("shownToPeopleUsingThisLink"))}</p>
              <OptionalLimit
                bind:value={maxFiles}
                disabled={busy}
                label={$t(m("limitFilesAccepted"))}
                description={$t(m("unfinishedUploadsCountDeletingFilesDoesNotRestoreThe"))}
              />
              <div class="actions">
                <button class="primary" disabled={busy}
                  >{$t(busy ? m("creatingLink") : m("createReceiveLink"))}</button
                >
                {#if receiveId && !receiveUnavailable}<button
                    type="button"
                    onclick={() => (receiveCreating = false)}>{$t(m("cancel"))}</button
                  >{/if}
              </div>
            </form>
          {:else}
            {#if renameId === receiveId && renameKind === "slots"}<form
                class="rename-form"
                onsubmit={(event) => {
                  event.preventDefault();
                  void rename();
                }}
              >
                <label>{$t(m("linkTitle"))}<input maxlength="400" bind:value={renameValue} /></label
                >
                <p class="muted small">{$t(m("shownToPeopleUsingThisLink"))}</p>
                <div class="actions">
                  <button class="primary" disabled={busy}>{$t(m("saveName"))}</button><button
                    type="button"
                    disabled={busy}
                    onclick={() => (renameId = "")}>{$t(m("cancel"))}</button
                  >
                </div>
              </form>{/if}
            {#if receiveNeedsKey}<p class="notice">
                {$t(m("thePrivateKeyIsOnTheDeviceThatCreated"))}
              </p>{/if}
            {#if receiveInfo?.remaining_files != null}<p class="muted small">
                {$t(m("remainingFilesCount", { count: receiveInfo.remaining_files }))}
              </p>{/if}
            {#if receiveInfo?.receive_protocol !== 2 && receiveInfo}<p class="notice">
                {$t(m("thisOlderInboxIsReadOnlyCreateANew"))}
              </p>{/if}
            {#if receiveId && !receiveUnavailable && ((receiveInfo?.summary.completed_files ?? 0) > 0 || receivePage > 1 || receiveInfo?.summary.state === "updating" || receiveError)}
              <section aria-label={$t(m("receivedFiles"))} aria-busy={receiveLoading}>
                <div class="heading">
                  <div>
                    <h2>{$t(m("receivedFiles"))}</h2>
                    {#if receiveInfo?.summary.state === "updating"}<p class="muted small">
                        {$t(m("receivedFileTotalsAreUpdating"))}
                      </p>
                    {:else if receiveInfo}<p class="muted small">
                        {$t(m("receivedFileCount", { count: receiveInfo.summary.completed_files }))}
                      </p>{/if}
                  </div>
                  <button disabled={busy || receiveLoading} onclick={() => checkReceived(receiveId)}
                    ><Icon name="Refresh" size={17} />{$t(m("refresh"))}</button
                  >
                </div>
                {#if receiveError}<p role="alert">{$t(receiveError)}</p>{/if}
                {#if receiveInfo}
                  {#if !received.length}<p class="muted">{$t(m("noFilesOnThisPage"))}</p>{/if}
                  {#each received as item (item.id)}{#if !receiveNeedsKey}<a
                        class="received"
                        href={item.url}
                        >{$t(m("fileCount", { count: item.count }))}
                        · {$t(m("viewFiles"))}</a
                      >{:else}<p>
                        {$t(m("fileCount", { count: item.count }))}
                        {$t(m("privateKeyIsOnTheCreatingDevice"))}
                      </p>{/if}{/each}
                  {#if receivePage > 1 || receiveNext}<nav
                      class="inbox-pages"
                      aria-label={$t(m("receivedFilesPages"))}
                    >
                      <button
                        disabled={receiveLoading || !receivePrevious.length}
                        onclick={() => turnReceived("previous")}>{$t(m("previous"))}</button
                      >
                      <span class="muted small">{$t(m("page"))} {$t(receivePage)}</span>
                      <button
                        disabled={receiveLoading || receiveNext === null}
                        onclick={() => turnReceived("next")}>{$t(m("next"))}</button
                      >
                      {#if receivePage > 1}<button
                          disabled={receiveLoading}
                          onclick={() => turnReceived("first")}>{$t(m("firstPage"))}</button
                        >{/if}
                    </nav>{/if}
                  {#if receivePage > 1 && !receivePrevious.length}<p class="muted small">
                      {$t(m("earlierPagesAreAvailableFromFirstPage"))}
                    </p>{/if}
                {/if}
              </section>
            {/if}
            {#if receiveUrl}
              {#if (receiveInfo?.summary.completed_files ?? 0) === 0}<LinkCard
                  url={receiveUrl}
                  label={$t(m("shareThisLinkToReceiveFiles"))}
                />
              {:else}<details class="receive-share" bind:open={receiveSharing}>
                  <summary>{$t(m("showQRShareLink"))}</summary><LinkCard
                    url={receiveUrl}
                    label={receiveTitle}
                  />
                </details>{/if}
            {/if}
            {#if receiveInfo?.summary.state !== "updating" && (receiveInfo?.summary.completed_files ?? 0) === 0 && receivePage === 1 && !receiveError}<div
                class="actions"
              >
                <button disabled={busy || receiveLoading} onclick={() => checkReceived(receiveId)}
                  ><Icon name="Refresh" size={17} />{$t(m("refresh"))}</button
                >
              </div>{/if}
            <details class="receive-details">
              <summary>{$t(m("details"))}</summary>
              <p>{$t(m("expires"))} {$t(receiveInfo ? expiry(receiveInfo.expires_at) : "…")}</p>
              {#if receiveInfo && receiveInfo.max_files > 0}<p>
                  {$t(
                    m("fileAllowancesUsed", {
                      used: receiveInfo.reserved_files,
                      count: receiveInfo.max_files,
                    }),
                  )}
                </p>{/if}
              <p class="muted small">{$t(m("id"))} {$t(receiveId)}</p>
            </details>
            <button
              class="new-inbox"
              disabled={busy}
              onclick={() => {
                receiveCreating = true;
                receiveName = "";
                maxFiles = 0;
              }}>{$t(m("createAnotherLink"))}</button
            >
          {/if}
        {:else if tab === "History"}<div class="heading">
            <h1>{$t(m("yourTransfers"))}</h1>
            <button disabled={busy || historyLoading} onclick={() => select("History")}
              ><Icon name="Refresh" size={17} />{$t(m("refresh"))}</button
            >
          </div>
          <label
            >{$t(m("show"))}<select
              aria-label={$t(m("historyFilter"))}
              value={historyFilter}
              onchange={filterHistory}
              ><option value="all">{$t(m("allTransfers"))}</option><option value="transfers"
                >{$t(m("sent"))}</option
              ><option value="slots">{$t(m("receiveLinks"))}</option></select
            ></label
          >
          {#if historyError}<p role="alert">
              {$t(historyError)}
              {$t(m("theDisplayedPageMayBeOutOfDate"))}
            </p>{/if}
          {#if !historyLoading && !historyError && !transfers.length && !slots.length}<p
              class="empty"
            >
              {$t(historyCursor || historyNext ? m("noTransfersOnThisPage") : m("noTransfersYet"))}
            </p>{/if}
          {#each [...transfers.map( (t) => ({ ...t, kind: "transfers" as const }), ), ...slots.map( (s) => ({ ...s, kind: "slots" as const }), )].sort((a, b) => Date.parse(b.created_at || "") - Date.parse(a.created_at || "")) as item}
            {@const storedSize = labelFor(labels, item.kind, item.id).size || item.total_size || 0}
            {@const activeLink =
              !downloadLinkExhausted(item) &&
              item.status !== "revoked" &&
              Date.parse(item.expires_at) > now}
            <article class="resource" data-resource-id={item.id}>
              <div>
                <span class="type-badge"
                  ><Icon name={item.kind === "slots" ? "Receive" : "Send"} size={15} />{$t(
                    item.kind === "slots" ? m("receiveLink") : m("sent"),
                  )}</span
                >
                <strong class="history-title" title={$t(historyTitle(item))}
                  >{#if compactTitle($t(historyTitle(item))) !== $t(historyTitle(item))}<span
                      aria-hidden="true">{$t(compactTitle($t(historyTitle(item))))}</span
                    ><span class="sr-only">{$t(historyTitle(item))}</span>{:else}{$t(
                      historyTitle(item),
                    )}{/if}</strong
                >
                {#if item.created_at}<p class="muted small">
                    {$t(m("created"))}
                    {$t(date(item.created_at))}
                  </p>{/if}
                <p>
                  {$t(
                    (item.kind === "slots" ? receivedFileCount(item) : resourceFileCount(item)) ===
                      null
                      ? m("fileTotalsUpdating")
                      : item.kind === "slots"
                        ? m("valueFilesReceived", { arg0: receivedFileCount(item) })
                        : m("valueFilesValue", {
                            arg0: resourceFileCount(item),
                            arg1: status(item),
                          }),
                  )}
                </p>
                {#if storedSize || activeLink}<p class="muted small">
                    {#if storedSize}{$t(formatSize(storedSize))}
                      {$t(m("stored"))}{$t(activeLink ? " · " : "")}{/if}
                    {#if activeLink}{$t(m("expires"))} {$t(expiry(item.expires_at))}{/if}
                  </p>{/if}
                {#if !links[item.id] && !downloadLinkExhausted(item)}<p class="muted small">
                    {$t(m("encryptionKeyIsOnAnotherDevice"))}
                  </p>{/if}
              </div>
              <div class="actions">
                {#if downloadLinkExhausted(item)}<a class="button" href="/?view=send"
                    >{$t(m("newSendLink"))}</a
                  >{/if}
                <button
                  onclick={() => {
                    beginRename(item.kind, item.id, item.title);
                  }}>{$t(m("rename"))}</button
                >
                {#if links[item.id] && !downloadLinkExhausted(item)}<button
                    onclick={() => copy(links[item.id])}
                    ><Icon name="Copy" size={17} />{$t(m("copyLink"))}</button
                  >{#if item.kind === "transfers"}<a class="button" href={links[item.id]}
                      >{$t(m("open"))}</a
                    >{/if}{/if}{#if item.kind === "slots"}<button
                    disabled={busy}
                    onclick={() => openReceive(item.id)}>{$t(m("viewFiles"))}</button
                  >{/if}<button
                  class="danger"
                  disabled={busy}
                  onclick={() => {
                    error = "";
                    pendingDelete = { id: item.id, kind: item.kind };
                  }}><Icon name="Revoke" size={17} />{$t(m("revoke"))}</button
                >
              </div>
              <details>
                <summary>{$t(m("details"))}</summary>
                <p>{$t(m("id"))} {$t(item.id)}</p>
                {#if item.kind === "slots" && item.max_files}<p>
                    {$t(
                      m("fileAllowancesUsed", {
                        used: item.reserved_files ?? 0,
                        count: item.max_files,
                      }),
                    )}
                  </p>{:else if item.kind === "transfers" && item.max_downloads}<p>
                    {$t(m("downloadAttemptsPerFile", { count: item.max_downloads }))}
                  </p>{/if}
              </details>
              {#if renameId === item.id && renameKind === item.kind}<form
                  class="rename-form"
                  onsubmit={(e) => {
                    e.preventDefault();
                    rename();
                  }}
                >
                  <label
                    >{$t(m("linkTitle"))}<input bind:value={renameValue} maxlength="400" /></label
                  >
                  <p class="muted small">{$t(m("shownToPeopleUsingThisLinkLeaveEmptyTo"))}</p>
                  <button class="primary">{$t(m("saveName"))}</button><button
                    type="button"
                    onclick={() => (renameId = "")}>{$t(m("cancel"))}</button
                  >
                </form>{/if}
            </article>{/each}
          {#if historyPage > 1 || historyNext}<nav
              class="history-pages"
              aria-label={$t(m("historyPages"))}
            >
              <button
                disabled={busy || historyLoading || !historyPrevious.length}
                onclick={() => turnHistory("previous")}>{$t(m("newerTransfers"))}</button
              >
              <span class="muted small">{$t(m("page"))} {$t(historyPage)}</span>
              <button
                disabled={busy || historyLoading || !historyNext}
                onclick={() => turnHistory("next")}>{$t(m("olderTransfers"))}</button
              >
              {#if historyPage > 1}<button
                  disabled={busy || historyLoading}
                  onclick={() => turnHistory("first")}>{$t(m("firstPage"))}</button
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
            >← {$t(user.role === "admin" ? m("overview") : m("backToSettings"))}</a
          >
          {#if user.role === "user"}<h1>{$t(m("connectMobileApp"))}</h1>
            <p class="muted">{$t(m("inTheAppSServerSettingsChooseScanLogin"))}</p>
            {#if pairingStatus === "connected"}<p class="notice" role="status">
                {$t(m("phoneConnected"))}
                {$t(pairedDevice)}
              </p>
            {:else if pairingQr && now < Date.parse(pairingExpires)}<section
                aria-label={$t(m("connectMobileApp"))}
              >
                <img class="qr" src={pairingQr} alt={$t(m("mobileAppLoginQRCode"))} />
                <p>
                  {$t(m("singleUseExpiresIn"))}
                  {$t(Math.max(0, Math.ceil((Date.parse(pairingExpires) - now) / 1000)))}
                  {$t(m("seconds"))}
                </p>
                <button onclick={cancelPair}>{$t(m("cancelPairing"))}</button>
              </section>
            {:else if pairingId}<p class="notice" role="status">
                {$t(m("thisCodeHasExpiredGenerateANewCode"))}
              </p>{/if}
            <button class="primary" disabled={busy} onclick={pair}
              ><Icon name="QRCode" size={18} />{$t(
                pairingId ? m("generateNewCode") : m("showLoginQRCode"),
              )}</button
            >
          {/if}
          <h2>{$t(user.role === "admin" ? m("signedInSessions") : m("connectedDevices"))}</h2>
          <p class="muted">{$t(m("revokeASessionToSignThatDeviceOutUp"))}</p>
          {#if sessionsLimited}<p class="notice" role="status">
              {$t(m("showing"))}
              {$t(sessions.length)}
              {$t(m("of"))}
              {$t(totalActiveSessionsExact ? "" : m("atLeast"))}{$t(totalActiveSessions)}
              {$t(m("activeSessionsOlderSessionsAreBeingRemovedToApply"))}
            </p>{/if}
          {#each sessions as session}<article class="resource">
              <div>
                <strong
                  >{$t(session.device_name || m("device"))}{$t(
                    session.current ? m("thisBrowser") : "",
                  )}</strong
                >
                <p class="muted small">{$t(m("expires"))} {$t(expiry(session.expires_at))}</p>
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
                  })}>{$t(m("revokeSession"))}</button
              >
            </article>{/each}
        {:else if tab === "Account"}<a
            class="back-link"
            href={user.role === "admin" ? "/?view=overview" : "/?view=settings"}
            >← {$t(user.role === "admin" ? m("overview") : m("backToSettings"))}</a
          >
          <PasswordChange busy={busy || securityMutationActive} onchange={changePassword} />
          {#if user.role === "admin"}{#key user.id}<AdministratorSecurity
                onchanged={securityChanged}
                onmutation={securityMutation}
              />{/key}{/if}
        {:else if tab === "Users"}
          <h1>{$t(m("manageUsers"))}</h1>
          <p class="muted">{$t(m("onlyAdministratorsCanCreateAccountsDisablingSignInSigns"))}</p>
          {#if usersPrevious.length || usersNext}<nav aria-label={$t(m("accountPages"))}>
              <button disabled={busy || !usersPrevious.length} onclick={() => turnUsers("previous")}
                >{$t(m("previousAccounts"))}</button
              >
              <span class="muted small">{$t(m("page"))} {$t(usersPrevious.length + 1)}</span>
              <button disabled={busy || !usersNext} onclick={() => turnUsers("next")}
                >{$t(m("moreAccounts"))}</button
              >
            </nav>{/if}
          {#each users as account}<article class="resource">
              <div>
                <strong>{$t(account.username)}</strong>
                <p class="muted small">
                  {$t(account.role === "admin" ? m("administrator") : m("user"))} · {$t(
                    account.disabled
                      ? m("disabled")
                      : account.must_change_password
                        ? m("passwordChangeRequired")
                        : m("active"),
                  )}
                </p>
              </div>
              <div class="actions">
                <button
                  disabled={busy}
                  onclick={() => {
                    resetId = account.id;
                    resetPassword = "";
                    resetConfirmation = "";
                  }}>{$t(m("resetPassword"))}</button
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
                  }}>{$t(account.disabled ? m("enableSignIn") : m("disableSignIn"))}</button
                >
                {#if account.role === "user"}<button
                    class="danger"
                    disabled={busy}
                    onclick={() => {
                      error = "";
                      accountAction = { account, mode: "shutdown" };
                    }}>{$t(m("incidentShutdown"))}</button
                  >{/if}
              </div>
              {#if account.role === "user"}<AccountTraffic
                  accountId={account.id}
                  username={account.username}
                />{/if}
            </article>{/each}
          {#if accountAction}<IncidentConfirmDialog
              title={$t(
                accountAction.mode === "disable"
                  ? m("disableNamedAccount", { username: accountAction.account.username })
                  : m("shutDownValueSTransfers", { arg0: accountAction.account.username }),
              )}
              description={accountAction.mode === "disable"
                ? m("disableFutureSignInRevokeThisAccountSSessions")
                : m("disableThisAccountRevokeAllSessionsAndPairingGrants")}
              action={accountAction.mode === "disable"
                ? m("disableSignInOnly")
                : m("shutDownAccountAndRevokeAllLinks")}
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
                    throw new LocalizedError(m("thePasswordsDoNotMatch"));
                  await request(`/admin/users/${resetId}`, "PATCH", { password: resetPassword });
                  const self = resetId === user?.id;
                  resetId = "";
                  resetPassword = "";
                  if (self) clearAccount();
                  else notice = m("passwordResetNotice");
                });
              }}
            >
              <label
                >{$t(m("newPasswordFor"))}
                {$t(users.find((u) => u.id === resetId)?.username)}<input
                  type="password"
                  autocomplete="new-password"
                  minlength="12"
                  required
                  bind:value={resetPassword}
                /></label
              ><label
                >{$t(m("confirmNewPassword"))}<input
                  type="password"
                  autocomplete="new-password"
                  minlength="12"
                  required
                  bind:value={resetConfirmation}
                /></label
              >
              <p class="muted small">
                {$t(m("regularUsersMustReplaceThisTemporaryPasswordAtTheir"))}
              </p>
              <button class="primary" disabled={busy}>{$t(m("saveNewPassword"))}</button><button
                type="button"
                onclick={() => (resetId = "")}>{$t(m("cancel"))}</button
              >
            </form>{/if}
          <h2>{$t(m("createAccount"))}</h2>
          <form
            onsubmit={(e) => {
              e.preventDefault();
              void act(async () => {
                if (newPassword !== newConfirmation)
                  throw new LocalizedError(m("thePasswordsDoNotMatch"));
                const result = await request<{ user: User }>("/admin/users", "POST", {
                  username: newUsername,
                  password: newPassword,
                  role: newRole,
                });
                users = [...users, result.user].slice(-50);
                newUsername = "";
                newPassword = "";
                newConfirmation = "";
                notice = m("accountCreatedNotice");
              });
            }}
          >
            <label
              >{$t(m("newUsername"))}<input
                autocomplete="off"
                required
                bind:value={newUsername}
              /></label
            ><label
              >{$t(m("temporaryPassword"))}<input
                type="password"
                autocomplete="new-password"
                minlength="12"
                required
                bind:value={newPassword}
              /></label
            ><label
              >{$t(m("confirmTemporaryPassword"))}<input
                type="password"
                autocomplete="new-password"
                minlength="12"
                required
                bind:value={newConfirmation}
              /></label
            ><label
              >{$t(m("role"))}<select bind:value={newRole}
                ><option value="user">{$t(m("user"))}</option><option value="admin"
                  >{$t(m("administrator"))}</option
                ></select
              ></label
            >
            <p class="muted small">{$t(m("useAtLeastCharactersRegularUsersMustReplaceThis"))}</p>
            <button class="primary" disabled={busy}>{$t(m("createAccount"))}</button>
          </form>
        {/if}
      </section>
    </div>
  </div>
{/if}

<style>
  .receive-create {
    max-width: 38rem;
  }
  .receive-create > p {
    margin: -0.5rem 0 0;
  }
  .receive-share,
  .receive-details {
    margin-top: 1rem;
  }
  .receive-share summary,
  .receive-details summary {
    cursor: pointer;
    min-height: 44px;
    padding: 0.65rem 0;
  }
  .new-inbox {
    margin-top: 1rem;
  }

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
    flex: 1;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
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
      margin-bottom: 1rem;
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
      max-width: 100%;
      overflow-wrap: anywhere;
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
      white-space: normal;
      overflow-wrap: anywhere;
      max-width: 100%;
      text-align: center;
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
