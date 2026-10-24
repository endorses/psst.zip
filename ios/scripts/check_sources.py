#!/usr/bin/env python3
"""Validate iOS identity, entitlements, extension resources and configuration.

Swift compilation and XCTest own implementation and behavior checks. This small
validator deliberately does not assert source snippets, test names or delimiters.
"""

import json
import plistlib
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def plist(name):
    with (ROOT / name).open("rb") as stream:
        return plistlib.load(stream)


app = plist("Psst/Info.plist")
extension = plist("PsstShareExtension/Info.plist")
assert app["CFBundleDisplayName"] == extension["CFBundleDisplayName"] == "psst.zip"
assert app["CFBundleIdentifier"] == "zip.psst.ios"
assert extension["CFBundleIdentifier"] == "zip.psst.ios.share-extension"
assert app["UIFileSharingEnabled"] and app["LSSupportsOpeningDocumentsInPlace"]
for bundle in (app, extension):
    assert bundle["NSCameraUsageDescription"]
    assert bundle["NSLocalNetworkUsageDescription"]
    # User-selected LAN HTTP servers remain supported in both native targets.
    assert bundle["NSAppTransportSecurity"]["NSAllowsArbitraryLoads"]
    assert bundle["SharedKeychainGroup"] == "$(AppIdentifierPrefix)zip.psst.ios.shared"

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

configuration = extension["NSExtension"]
assert configuration["NSExtensionPointIdentifier"] == "com.apple.share-services"
activation = configuration["NSExtensionAttributes"]["NSExtensionActivationRule"]
for name in (
    "NSExtensionActivationSupportsFileWithMaxCount",
    "NSExtensionActivationSupportsImageWithMaxCount",
    "NSExtensionActivationSupportsMovieWithMaxCount",
):
    assert isinstance(activation[name], int) and activation[name] > 0

# Parse actual Interface Builder metadata, preserving the extension entry point.
# This does not imply ibtool compilation or native controller lifecycle validation.
storyboard = ET.parse(
    ROOT
    / "PsstShareExtension"
    / f"{configuration['NSExtensionMainStoryboard']}.storyboard"
).getroot()
assert storyboard.attrib["targetRuntime"] == "iOS.CocoaTouch"
controller = storyboard.find(
    f".//viewController[@id='{storyboard.attrib['initialViewController']}']"
)
assert controller is not None
assert controller.attrib["customClass"] == "ShareViewController"
assert controller.attrib["customModule"] == "PsstShareExtension"
assert controller.attrib["customModuleProvider"] == "target"

for path in ROOT.rglob("*.json"):
    if not {"build", ".build"}.intersection(path.parts):
        json.loads(path.read_text())

print(
    "iOS identity, entitlement, extension and configuration checks passed (not a Swift build)."
)
