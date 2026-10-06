#!/usr/bin/env python3
"""Linux-safe source gates. These do not replace a Swift/Xcode build or device tests."""

import json
import pathlib
import plistlib
import re
import xml.etree.ElementTree as ET

ROOT = pathlib.Path(__file__).resolve().parents[1]


def read(name):
    return (ROOT / name).read_text()


def plist(name):
    with (ROOT / name).open("rb") as stream:
        return plistlib.load(stream)


app = plist("Psst/Info.plist")
extension = plist("PsstShareExtension/Info.plist")
assert app["CFBundleDisplayName"] == extension["CFBundleDisplayName"] == "psst.zip"
assert app["CFBundleIdentifier"] == "zip.psst.ios"
assert extension["CFBundleIdentifier"] == "zip.psst.ios.share-extension"
# Catch malformed Interface Builder metadata without implying ibtool validation.
# Preserve the share extension's storyboard entry point and controller wiring.
share_storyboard_name = extension["NSExtension"]["NSExtensionMainStoryboard"]
share_storyboard = ET.fromstring(
    read(f"PsstShareExtension/{share_storyboard_name}.storyboard")
)
assert share_storyboard.attrib["targetRuntime"] == "iOS.CocoaTouch"
share_controller = share_storyboard.find(
    ".//viewController[@id='{}']".format(
        share_storyboard.attrib["initialViewController"]
    )
)
assert share_controller is not None
assert share_controller.attrib["customClass"] == "ShareViewController"
assert share_controller.attrib["customModule"] == "PsstShareExtension"
assert share_controller.attrib["customModuleProvider"] == "target"
for path in (
    "Psst/Psst.entitlements",
    "PsstShareExtension/PsstShareExtension.entitlements",
):
    entitlements = plist(path)
    assert entitlements["com.apple.security.application-groups"] == [
        "group.zip.psst.ios"
    ]
    assert entitlements["keychain-access-groups"] == [
        "$(AppIdentifierPrefix)zip.psst.ios.shared"
    ]
assert (
    app["SharedKeychainGroup"]
    == extension["SharedKeychainGroup"]
    == "$(AppIdentifierPrefix)zip.psst.ios.shared"
)
assert app["NSCameraUsageDescription"] and extension["NSCameraUsageDescription"]
assert "kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly" in read(
    "Shared/SessionStore.swift"
)
assert "completionHandler(nil)" in read("Shared/SessionStore.swift")
assert "sessionToken: session.token" in read("Shared/ServerConfigManager.swift")
assert "HistorySnapshot.load" in read("Psst/ViewModels/HistoryPageViewModel.swift")
assert '"auth/resources?limit=' in read("Shared/HistorySnapshot.swift")
assert "historyStore.completeReceivedTransfer" in read(
    "Psst/ViewModels/ReceiveViewModel.swift"
)
assert "record == nil" in read("Psst/ViewModels/ReceiveViewModel.swift")
assert "history.revoke" in read("Psst/Views/HistoryView.swift")
assert "group.zip.psst.ios" in read("Shared/AppConstants.swift")
assert "case .downloading: ProgressView" in read("Psst/Views/TransferDetailView.swift")
assert "PsstTests" in read("project.yml")
assert "testPartialSaveRetryKeepsOriginalSlotAndSkipsSuccessfulFile" in read(
    "PsstTests/TransferWorkflowTests.swift"
)
assert "testLateCallbacksRejectLogoutAccountServerAndSessionChanges" in read(
    "PsstTests/TransferWorkflowTests.swift"
)
# Verify regression wiring only; XCTest execution remains pending on macOS.
assert "testServerTimestampAcceptsWholeAndFractionalSeconds" in read(
    "PsstTests/TransferWorkflowTests.swift"
)
assert ".withFractionalSeconds" in read("Shared/ServerTimestamp.swift")
for name in (
    "Shared/SendViewModel.swift",
    "Shared/HistoryRefresh.swift",
    "Psst/ViewModels/ReceiveViewModel.swift",
):
    assert "ServerTimestamp.parse" in read(name)
    assert "ISO8601DateFormatter()" not in read(name)
# This production collector rejects the entire provider selection; these checks only verify wiring.
assert "ShareSelection.loadAll" in read("PsstShareExtension/ShareViewController.swift")
assert "ShareSelection.copyProviderFile" in read(
    "PsstShareExtension/ShareViewController.swift"
)
assert "testMixedValidAndOversizedShareSelectionRejectsAllAndCleansCopies" in read(
    "PsstTests/TransferWorkflowTests.swift"
)
assert "testUnknownProviderFailureDoesNotPrepareValidSubset" in read(
    "PsstTests/TransferWorkflowTests.swift"
)
assert "testProviderCopyRejectsBeyondStreamingCeilingBeforeCopying" in read(
    "PsstTests/TransferWorkflowTests.swift"
)
# Reject accidentally copied tokens in shared ordinary preferences.
for path in ROOT.rglob("*.swift"):
    text = path.read_text()
    assert not re.search(
        r"(?:defaults|sharedDefaults)\.set\([^\n]*(?:token|password)",
        text,
        re.IGNORECASE,
    ), path
    assert not re.search(
        r'(?:Text|Label|navigationTitle)\("(?:Psst|Secure Transfer|Secure File Transfer)',
        text,
    ), path
    # Balanced delimiters ignoring Swift string literals and comments; catches truncation, not types.
    # Raw strings may contain ordinary quotes and URLs. Consume the matching
    # hash-delimited string before considering quotes or // inside its body.
    cleaned = re.sub(
        r'(?P<raw>#+)".*?"(?P=raw)|"(?:\\.|[^"\\])*"|//[^\n]*|/\*.*?\*/',
        "",
        text,
        flags=re.DOTALL,
    )
    stack = []
    for char in cleaned:
        if char in "({[":
            stack.append(char)
        elif char in ")}]":
            assert stack and stack.pop() == {")": "(", "}": "{", "]": "["}[char], path
    assert not stack, path
for path in ROOT.rglob("*.json"):
    if "build" not in path.parts:
        json.loads(path.read_text())
# Guest receive source wiring: these assertions do not prove native compilation or runtime behavior.
assert app["UIFileSharingEnabled"] and app["LSSupportsOpeningDocumentsInPlace"]
assert "ScanReceiveView(isSelected: selectedTab == 1 && !settings)" in read(
    "Psst/ContentView.swift"
)
assert "guestTransfer.cancel()" in read("Psst/ContentView.swift")
assert "MergedDeviceHistoryViewModel" in read("Psst/Views/HistoryView.swift")
assert "BoundedHistoryMerge" in read(
    "Psst/ViewModels/MergedDeviceHistoryViewModel.swift"
)
assert "kind: kind" in read("Psst/ViewModels/HistoryPageViewModel.swift")
assert "historyOnly" not in read("Psst/Views/ScanReceiveView.swift")
assert "isActive: visible && isSelected" in read("Psst/Views/ScanReceiveView.swift")
assert "state.permitsStart" in read("Shared/SendViewModel.swift")
assert "ApiClient.companion.anonymous" in read(
    "Psst/ViewModels/GuestTransferModel.swift"
)
assert "validateForTransfer" in read("Psst/ViewModels/GuestTransferModel.swift")
assert "StreamedFiles.receive" in read("Psst/ViewModels/GuestTransferModel.swift")
assert "downloadFileChunks" in read("Shared/StreamedFiles.swift")
assert "createFileUpload" in read("Shared/BufferedUpload.swift")
assert "limits.get()" in read("Shared/SendViewModel.swift")
assert "sessionToken" not in read("Psst/ViewModels/GuestTransferModel.swift")
assert "GuestUploadCleanup" in read("Psst/ViewModels/GuestTransferModel.swift")
assert "SecretStore.write(key" in read("Psst/Services/GuestDownloadStore.swift")
assert "transfers.delete" not in read("Psst/Services/GuestDownloadStore.swift")
# Wiring only: independent receipt rows survive local history removal. Runtime
# preservation and retry behavior are covered by the guest storage tests.
assert 'kinds: ["receipt"]' in read("Psst/Services/GuestDownloadStore.swift")
assert "ScanInputClassifier.shared.classify" in read("Shared/ServerConfigManager.swift")
assert "testKillAfterPublicationReconcilesIntentBeforeReceipt" in read(
    "PsstTests/GuestDownloadTests.swift"
)
assert "testReceiptFailureKeepsSavedStateAndRetriesWithoutFileWorkAfterRemoval" in read(
    "PsstTests/GuestDownloadTests.swift"
)
# Administration/UX gates are wiring checks, not proof of native behavior.
assert '"Switch camera"' not in read("Shared/PairingScanner.swift")
assert "ScannerCameraSelection.ordered" in read("Shared/PairingScanner.swift")
assert "runtimeErrorNotification" in read("Shared/PairingScanner.swift")
assert "retryCamera" in read("Shared/PairingScanner.swift")
assert "must_change_password" in read("Shared/ServerConfigManager.swift")
assert 'response.user.role != "admin"' in read("Shared/ServerConfigManager.swift")
assert "refreshAccount()" in read("Psst/ContentView.swift")
assert "refreshAccount()" in read("PsstShareExtension/ShareExtensionView.swift")
assert "PasswordReplacementFields" in read("Shared/LoginFields.swift")
assert "textContentType(.newPassword)" in read("Shared/PasswordReplacementFields.swift")
assert "PasswordReplacementPolicy.valid" in read(
    "Shared/PasswordReplacementFields.swift"
)
assert "preservingLocalName" in read("Shared/TransferHistoryStore.swift")
assert "history.rename" in read("Psst/Views/HistoryView.swift")
history_view = read("Psst/Views/HistoryView.swift")
routine_task = history_view[
    history_view.index('.task(id: "\\(visible)') : history_view.index(
        ".onChange(of: scenePhase)"
    )
]
assert "await page.refresh(history: history, session: session)" in routine_task
assert "await refresh()" not in routine_task and "working" not in routine_task
assert "!deviceMode" in routine_task and "session.canTransfer" in routine_task
manual_refresh = history_view[
    history_view.index("private func refresh()") : history_view.index(
        "private func revoke("
    )
]
assert "guard !working" in manual_refresh
assert "if hasLoadedHistory, records.isEmpty" in history_view
assert "devicePage.loaded && devicePage.session == config.session" in history_view
assert "page.hasLoadedPage(for: config.session)" in history_view
assert "@State private var historyPage = HistoryPageViewModel()" in read(
    "Psst/ContentView.swift"
)
assert "page: historyPage" in read("Psst/ContentView.swift")
assert "@State private var page = HistoryPageViewModel()" not in history_view
history_appearance = history_view[
    history_view.index(".onAppear {") : history_view.index(".onDisappear {")
]
assert history_appearance.index("page.restoreCachedPage(") < history_appearance.index(
    "page.resumeRoutine()"
)
for path in (
    "Psst/Views/ScanReceiveView.swift",
    "Psst/Views/TransferDetailView.swift",
    "PsstShareExtension/ShareExtensionView.swift",
):
    progress_source = read(path)
    assert (
        "L10n.bytes(model.bytes)" in progress_source
        or "L10n.bytes(progress.sent)" in progress_source
    )
    assert (
        "L10n.bytes(model.total)" in progress_source
        or "L10n.bytes(progress.total)" in progress_source
    )
assert "localName: receiveName" in read("Psst/Views/HomeView.swift")
for name in (
    "testHistoryMigrationPersistenceAndRefreshKeepLocalNameWithoutTypeCollision",
    "testHistoryTitlesKeepUnicodeFilenameAndAdditionalCount",
    "testPollingAndStaleCheckpointPreserveRenameAndClearByTypedAccountIdentity",
    "testAdminAndRestrictedSessionsExposeOnlyDeviceDownloads",
    "testPasswordConfirmationAndBytePolicy",
    "testCameraSelectionPrefersRearAndKeepsFrontOnlyAndNoCameraCases",
):
    assert name in read("PsstTests/NavigationHistoryTests.swift")
# Entry-level recovery wiring only; portable execution and native Apple checks
# remain separate validation requirements.
assert "migrateReceiveJSONBatch" in read("Shared/TransferHistoryStore.swift")
assert "ReceiveHistoryStream" in read("Shared/ReceiveCheckpointStorage.swift")
assert "sqlite3_blob_read" in read("Shared/HistoryRecordDatabase.swift")
assert "receive_source_immutable_update" in read("Shared/HistoryRecordDatabase.swift")
assert "legacy.savedFiles" not in read("Shared/ReceiveCheckpointStorage.swift")
for name in (
    "testOversizedInboxStreamsBeforeLateIdentityAndResumesWithinObject",
    "testCompletionArrayBeyondRecordCapUsesBoundedTokenReadsAndState",
    "testStreamingStateRowsRollbackAndChangedSourceCannotResume",
    "testOldPartialJobRecoversCompletionBeforeMapsWithoutAdoptingNewerFiles",
    "testPaddedMetadataYieldsAtBoundedMemberBoundary",
):
    assert name in read("PsstTests/ReceiveCheckpointTests.swift")

print("iOS source/configuration gates passed (not a Swift build).")
