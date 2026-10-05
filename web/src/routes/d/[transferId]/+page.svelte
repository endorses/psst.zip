<script lang="ts">
  import { hasStatus, hasCode } from "$lib/api-error";
  import {
    message as m,
    t,
    number,
    LocalizedError,
    errorText,
    translate,
    type DisplayText,
  } from "$lib/i18n";

  import { TrafficLimitError, detectTransferStop } from "$lib/traffic-policy";
  import Icon from "$lib/components/Icon.svelte";
  import { beforeNavigate, goto } from "$app/navigation";
  import { BRAND } from "$lib/brand";
  import { page } from "$app/stores";
  import { onMount, onDestroy } from "svelte";
  import { importKey, exportKey, decryptManifest } from "$lib/crypto";
  import {
    getTransferInfo,
    getSlotTransferMembership,
    downloadManifest,
    acknowledgeDownload,
    responseError,
    type TransferInfo,
  } from "$lib/api";
  import { accountRequest, type User } from "$lib/account";
  import { TransferStateError } from "$lib/incident-state";
  import { loadReceiveKey } from "$lib/receive-keys";
  import { decodeReceiveEnvelope, openSubmissionKey } from "$lib/receive-crypto";
  import type { Manifest, FileManifestEntry } from "$lib/crypto";
  import { zipSync } from "fflate";

  import { assertFileSize, MAX_ZIP_BYTES } from "$lib/limits";

  import { decryptFileStream } from "$lib/chunked-files";
  import { createSaveSink, cleanAbandonedDownloads, LARGE_SAVE_MESSAGE } from "$lib/file-save";
  import { safeFilename, zipEntryName } from "$lib/filenames";
  import { validateDownload } from "$lib/download-validation";
  import { ReceiveStorageError } from "$lib/recipient-policy";
  import { downloadLinkExhausted, DOWNLOAD_LINK_CLOSED } from "$lib/link-title";
  import DownloadContext from "$lib/components/DownloadContext.svelte";

  type Status = "loading" | "ready" | "downloading" | "error";

  let controller: AbortController | null = null,
    disposed = false;
  const loadController = new AbortController();
  let currentFile = $state(""),
    downloadBytes = $state(0);
  onDestroy(() => {
    disposed = true;
    controller?.abort();
    loadController.abort();
  });
  let status = $state<Status>("loading");
  let errorMessage = $state<DisplayText>("");
  let manifest = $state<Manifest | null>(null);
  let transferId = $state("");
  let keyStr = $state("");
  let inboxId = $state("");
  let transferInfo = $state<TransferInfo | null>(null);
  let ownerUser = $state<User | null>(null);
  let signingOut = $state(false);
  let departureApproved = false;
  let metadataGeneration = 0;
  const returnUrl = $derived(inboxId ? `/?view=receive&slot=${encodeURIComponent(inboxId)}` : "/");
  async function logout() {
    if (signingOut) return;
    if (saving && !confirm(translate(m("stopSavingAndSignOutFilesAlreadySavedWill")))) return;
    controller?.abort();
    signingOut = true;
    try {
      await accountRequest("/auth/logout", "POST");
      try {
        localStorage.setItem("psst.auth-change", String(Date.now()));
      } catch {}
      ownerUser = null;
      departureApproved = true;
      await goto("/");
    } catch {
      errorMessage = m("couldNotSignOutCheckYourConnectionAndTry");
    } finally {
      signingOut = false;
      departureApproved = false;
    }
  }
  let downloadProgress = $state<Record<string, number>>({});
  let downloadedFileIds = $state<string[]>([]);
  let confirmation = $state<"idle" | "sending" | "confirmed" | "failed">("idle");
  const saving = $derived(
    status === "downloading" || Object.values(downloadProgress).some((value) => value < 100),
  );
  function unload(event: BeforeUnloadEvent) {
    if (saving) {
      event.preventDefault();
      event.returnValue = "";
    }
  }
  beforeNavigate(({ willUnload, cancel }) => {
    if (
      !willUnload &&
      !departureApproved &&
      saving &&
      !confirm(translate(m("stopSavingAndLeaveFilesAlreadySavedWillRemain")))
    )
      cancel();
  });
  const allFilesDownloaded = $derived(
    !!manifest?.files.length &&
      manifest.files.every((file) => downloadedFileIds.includes(file.blob_id)),
  );

  async function load() {
    transferId = $page.params.transferId ?? "";
    keyStr = window.location.hash.slice(1);
    inboxId = $page.url.searchParams.get("inbox") ?? "";

    if (!keyStr && !inboxId) {
      status = "error";
      errorMessage = m("thisLinkIsIncompleteAskTheSenderForThe");
      return;
    }

    try {
      if (inboxId) {
        if (!/^[0-9a-f-]{36}$/i.test(inboxId)) throw new LocalizedError(m("invalidInbox"));
        const { user } = await accountRequest<{ user: User }>("/auth/me");
        ownerUser = user;
        const pair = loadReceiveKey(user.id, inboxId);
        if (!pair) throw new LocalizedError(m("thisBrowserHasNoPrivateKeyForThisInbox"));
        try {
          const membership = await getSlotTransferMembership(
            inboxId,
            transferId,
            loadController.signal,
          );
          if (disposed) return;
          if (membership.recipient_public_key !== (await exportKey(pair.publicKey)))
            throw new LocalizedError(m("thisFileDoesNotMatchTheExpectedInbox"));
          transferInfo = await getTransferInfo(transferId, loadController.signal);
          if (downloadLinkExhausted(transferInfo)) throw new LocalizedError(DOWNLOAD_LINK_CLOSED);
          const encryptedManifestData = await downloadManifest(transferId, loadController.signal);
          const envelope = decodeReceiveEnvelope(new Uint8Array(encryptedManifestData));
          const key = await openSubmissionKey(
            pair.privateKey,
            pair.publicKey,
            inboxId,
            transferId,
            envelope.wrappedKey,
          );
          try {
            keyStr = await exportKey(key);
            manifest = await decryptManifest(key, envelope.encryptedManifest.buffer);
          } finally {
            key.fill(0);
          }
        } finally {
          pair.privateKey.fill(0);
        }
      } else {
        transferInfo = await getTransferInfo(transferId, loadController.signal);
        if (downloadLinkExhausted(transferInfo)) throw new LocalizedError(DOWNLOAD_LINK_CLOSED);
        const encryptedManifestData = await downloadManifest(transferId, loadController.signal);
        manifest = await decryptManifest(await importKey(keyStr), encryptedManifestData);
      }
      validateDownload(transferInfo, transferId, manifest);
      status = "ready";
    } catch (err) {
      status = "error";
      if (err instanceof TrafficLimitError || err instanceof TransferStateError) {
        errorMessage = errorText(err);
      } else if (err instanceof Error && errorText(err) === DOWNLOAD_LINK_CLOSED) {
        errorMessage = errorText(err);
      } else if (hasStatus(err, 401, 403)) {
        errorMessage = m("signInAsTheInboxOwnerToSaveThese");
      } else if (err instanceof Error && err instanceof LocalizedError) {
        errorMessage = errorText(err);
      } else if (hasStatus(err, 404, 410)) {
        errorMessage = m("thisTransferHasExpiredOrWasRevokedAskThe");
      } else {
        errorMessage = m("couldNotOpenTheseFilesCheckYourConnectionAnd");
      }
    }
  }
  onMount(() => {
    void load();
    void cleanAbandonedDownloads();
  });

  function formatSize(bytes: number): string {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return `${number(bytes / Math.pow(1024, i), { minimumFractionDigits: i ? 1 : 0, maximumFractionDigits: i ? 1 : 0 })} ${units[i]}`;
  }

  function triggerDownload(data: ArrayBuffer, filename: string, mimeType: string) {
    const blob = new Blob([data], { type: mimeType });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = safeFilename(filename);
    try {
      a.click();
    } finally {
      URL.revokeObjectURL(url);
    }
  }

  async function confirmDownload() {
    if (!allFilesDownloaded || confirmation === "sending" || confirmation === "confirmed") return;
    confirmation = "sending";
    try {
      await acknowledgeDownload(transferId);
      confirmation = "confirmed";
    } catch {
      // Files are already decrypted and handed to the browser. Retry only the
      // acknowledgment; never ask the recipient to download those files again.
      confirmation = "failed";
    }
  }

  function recordDownloadedFiles(ids: string[]) {
    downloadedFileIds = [...new Set([...downloadedFileIds, ...ids])];
    if (allFilesDownloaded && confirmation === "idle") void confirmDownload();
  }

  async function* downloadChunks(entry: FileManifestEntry, signal: AbortSignal) {
    const key = await importKey(keyStr);
    try {
      const response = await fetch(`/api/v1/transfers/${transferId}/files/${entry.blob_id}`, {
        signal,
        credentials: "same-origin",
        redirect: "error",
        cache: "no-store",
      });
      if (!response.ok || !response.body) {
        throw await responseError(response, signal);
      }
      yield* decryptFileStream(key, entry, response.body, signal);
    } catch (cause) {
      if (
        cause instanceof TrafficLimitError ||
        cause instanceof TransferStateError ||
        signal.aborted
      )
        throw cause;
      throw (
        (await detectTransferStop(`/transfers/${transferId}`, undefined, signal, "download")) ??
        cause
      );
    } finally {
      // The aborted payload signal must not abort the control refresh: even
      // an interrupted response consumes a download attempt. Never retry data.
      if (!disposed) {
        const generation = ++metadataGeneration;
        void getTransferInfo(transferId, loadController.signal)
          .then((fresh) => {
            if (manifest) validateDownload(fresh, transferId, manifest);
            if (!disposed && generation === metadataGeneration) transferInfo = fresh;
          })
          .catch(() => {});
      }
    }
  }

  async function downloadSingleFile(entry: FileManifestEntry) {
    errorMessage = "";
    controller = new AbortController();
    const signal = controller.signal;
    let sink: Awaited<ReturnType<typeof createSaveSink>> | undefined;
    try {
      assertFileSize(entry.size);
      downloadProgress = { ...downloadProgress, [entry.blob_id]: 0 };
      sink = await createSaveSink(entry, signal);
      signal.throwIfAborted();
      let saved = 0;
      for await (const chunk of downloadChunks(entry, signal)) {
        await sink.write(chunk);
        saved += chunk.length;
        downloadProgress = {
          ...downloadProgress,
          [entry.blob_id]: Math.min(99, Math.floor((saved / Math.max(1, entry.size)) * 100)),
        };
      }
      signal.throwIfAborted();
      await sink.close();
      sink = undefined;
      downloadProgress = { ...downloadProgress, [entry.blob_id]: 100 };
      recordDownloadedFiles([entry.blob_id]);

      setTimeout(() => {
        const { [entry.blob_id]: _, ...rest } = downloadProgress;
        downloadProgress = rest;
      }, 1000);
    } catch (err) {
      await sink?.abort().catch(() => {});
      errorMessage =
        err instanceof Error &&
        (err instanceof TrafficLimitError ||
          err instanceof TransferStateError ||
          err instanceof ReceiveStorageError ||
          errorText(err) === LARGE_SAVE_MESSAGE ||
          hasCode(err, "download_limit"))
          ? errorText(err)
          : err instanceof DOMException && err.name === "AbortError"
            ? m("savingStoppedStartedDownloadsStillCountTowardTheLimit")
            : m("couldNotSaveFilesCheckYourConnectionAndTry");
      const { [entry.blob_id]: _, ...rest } = downloadProgress;
      downloadProgress = rest;
    }
  }

  async function downloadAllAsZip() {
    if (!manifest) return;

    controller = new AbortController();
    const signal = controller.signal;
    status = "downloading";
    errorMessage = "";
    try {
      if (manifest.files.reduce((sum, file) => sum + file.size, 0) > MAX_ZIP_BYTES) {
        throw new LocalizedError(m("zipDownloadsAreLimitedToMiBTotalDownloadFiles"));
      }
      const zipData: Record<string, Uint8Array> = Object.create(null);

      for (let i = 0; i < manifest.files.length; i++) {
        const entry = manifest.files[i];
        assertFileSize(entry.size);
        currentFile = entry.name;
        downloadBytes = 0;
        const plaintext = new Uint8Array(entry.size);
        let offset = 0;
        for await (const chunk of downloadChunks(entry, signal)) {
          plaintext.set(chunk, offset);
          offset += chunk.length;
          downloadBytes = offset;
        }
        signal.throwIfAborted();
        zipData[zipEntryName(entry.name, i)] = plaintext;
      }

      const zipped = zipSync(zipData);
      triggerDownload(new Uint8Array(zipped).buffer, "files.zip", "application/zip");
      recordDownloadedFiles(manifest.files.map((entry) => entry.blob_id));
      status = "ready";
    } catch (err) {
      status = "ready";
      errorMessage =
        err instanceof Error &&
        (err instanceof TrafficLimitError ||
          err instanceof TransferStateError ||
          hasCode(err, "download_limit"))
          ? errorText(err)
          : err instanceof DOMException && err.name === "AbortError"
            ? m("savingStoppedStartedDownloadsStillCountTowardTheLimit")
            : m("couldNotSaveFilesCheckYourConnectionAndTry");
    }
  }
</script>

<svelte:window onbeforeunload={unload} />
<svelte:head>
  <title
    >{$t(transferInfo?.title || (manifest?.files.length === 1 ? m("saveFile") : m("saveFiles")))} · {$t(
      BRAND,
    )}</title
  >
</svelte:head>

<DownloadContext user={ownerUser} {inboxId} {returnUrl} {signingOut} onlogout={logout}>
  {#if status === "loading"}
    <section class="center">
      <div class="spinner"></div>
      <p>{$t(m("loadingTransfer"))}</p>
    </section>
  {:else if status === "error"}
    <section class="center">
      <h1>{$t(m("cannotOpenFiles"))}</h1>
      <p class="error" role="alert">{$t(errorMessage)}</p>
      <button
        onclick={() => {
          status = "loading";
          void load();
        }}><Icon name="Refresh" size={18} />{$t(m("reconnect"))}</button
      >
    </section>
  {:else if status === "downloading"}
    <section class="center">
      <div class="spinner"></div>
      <p>
        {$t(m("saving"))}
        {$t(currentFile)} · {$t(formatSize(downloadBytes))}
        {$t(m("received"))}
      </p>
      <button onclick={() => controller?.abort()}>{$t(m("cancelSaving"))}</button>
    </section>
  {:else if manifest}
    <section>
      <h1>
        {$t(transferInfo?.title || (manifest.files.length === 1 ? m("saveFile") : m("saveFiles")))}
      </h1>
      <p class="subtitle">
        {$t(m("fileCount", { count: manifest.files.length }))} &middot;
        {$t(formatSize(manifest.files.reduce((sum, f) => sum + f.size, 0)))}
        {$t(m("total"))}
      </p>
      <ul class="file-list">
        {#each manifest.files as entry}
          <li>
            <div class="file-info">
              <span class="file-name">{$t(entry.name)}</span>
              <span class="file-size">{$t(formatSize(entry.size))}</span>
              {#if transferInfo?.files?.find((file) => file.id === entry.blob_id)?.remaining_downloads != null}<span
                  class="muted small"
                >
                  {#if transferInfo.files.find((file) => file.id === entry.blob_id)?.remaining_downloads === 0}
                    {$t(m("downloadLimitReached"))}
                  {:else}
                    · {$t(
                      m("remainingAttemptsCount", {
                        count:
                          transferInfo.files.find((file) => file.id === entry.blob_id)
                            ?.remaining_downloads ?? 0,
                      }),
                    )}
                  {/if}</span
                >{/if}
            </div>
            <button
              class="btn"
              onclick={() => downloadSingleFile(entry)}
              disabled={Object.keys(downloadProgress).length > 0 ||
                transferInfo?.files?.find((file) => file.id === entry.blob_id)
                  ?.remaining_downloads === 0}
            >
              <Icon name="Download" size={18} />
              {#if entry.blob_id in downloadProgress}
                {$t(downloadProgress[entry.blob_id])}%
              {:else}
                {$t(downloadedFileIds.includes(entry.blob_id) ? m("saveAgain") : m("saveFile"))}
              {/if}
            </button>
          </li>
        {/each}
      </ul>

      {#if manifest.files.length > 1}
        <p class="muted small">
          {#if transferInfo?.files?.some((file) => file.remaining_downloads === 0)}
            {$t(m("zipIsUnavailableBecauseOneOrMoreFilesReached"))}
          {:else}
            {$t(m("zipDownloadsSupportUpToMiBTotalLargerTransfers"))}
          {/if}
        </p>
        <button
          class="primary"
          disabled={Object.keys(downloadProgress).length > 0 ||
            transferInfo?.files?.some((file) => file.remaining_downloads === 0) ||
            manifest.files.reduce((n, f) => n + f.size, 0) > MAX_ZIP_BYTES}
          onclick={downloadAllAsZip}
          ><Icon name="Download" size={18} />{$t(m("saveAllAsZIP"))}</button
        >
      {/if}

      {#if saving}<button onclick={() => controller?.abort()}>{$t(m("cancelSaving"))}</button>{/if}
      {#if errorMessage}
        <p class="error" role="alert">{$t(errorMessage)}</p>
      {/if}

      {#if allFilesDownloaded}
        <div class="download-confirmation" role="status">
          <p>{$t(m("allFilesHandedToYourBrowserCheckItsDownloads"))}</p>
          {#if confirmation === "sending"}
            <p>{$t(m("notifyingTheSender"))}</p>
          {:else if confirmation === "confirmed"}
            <p>{$t(m("senderNotified"))}</p>
          {:else if confirmation === "failed"}
            <p>{$t(m("filesDownloadedButTheSenderCouldNotBeNotified"))}</p>
            <button class="btn" onclick={confirmDownload}
              ><Icon name="Refresh" size={18} />{$t(m("retryConfirmation"))}</button
            >
          {/if}
        </div>
      {/if}
    </section>
  {/if}
</DownloadContext>

<style>
  .download-confirmation {
    margin-top: 1rem;
    padding: 1rem;
    background: var(--accent);
    border-radius: 8px;
  }
  .file-name {
    word-break: break-word;
  }
</style>
