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
assert 'path: "auth/resources"' in read("Shared/HistoryRefresh.swift")
assert "checkpoint.completed" in read("Psst/ViewModels/ReceiveViewModel.swift")
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
assert "testProviderCopyRejectsElevenMiBBeforeCopying" in read(
    "PsstTests/TransferWorkflowTests.swift"
)
# Reject accidentally copied tokens in shared ordinary preferences.
for path in ROOT.rglob("*.swift"):
    text = path.read_text()
    assert not re.search(
        r"(?:defaults|sharedDefaults)\.set\([^\n]*(?:token|password)", text, re.I
    ), path
    assert not re.search(
        r'(?:Text|Label|navigationTitle)\("(?:Psst|Psst|Secure Transfer)', text
    ), path
    # Balanced delimiters ignoring Swift string literals and comments; catches truncation, not types.
    cleaned = re.sub(r'//[^\n]*|/\*.*?\*/|#?"(?:\\.|[^"\\])*"#?', "", text, flags=re.S)
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
print("iOS source/configuration gates passed (not a Swift build).")
