#!/usr/bin/env python3
"""Linux-safe source gates. These do not replace a Swift/Xcode build or device tests."""

import json
import pathlib
import plistlib
import re

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
        r'(?:Text|Label|navigationTitle)\("(?:Psst|Psst|Secure Transfer)', text
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
assert "HistoryEntry.combine" in read("Psst/Views/HistoryView.swift")
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
print("iOS source/configuration gates passed (not a Swift build).")
