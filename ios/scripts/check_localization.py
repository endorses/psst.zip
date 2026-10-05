#!/usr/bin/env python3
"""Native catalog coverage/placeholder/plural gates, not an Xcode resource build."""

import json
import pathlib
import plistlib
import re
import xml.etree.ElementTree as ET

from localization_inventory import active_keys

ROOT = pathlib.Path(__file__).resolve().parents[1]


def strings(path):
    source = re.sub(r"/\*.*?\*/", "", path.read_text(), flags=re.S)
    pairs = re.findall(r'("(?:\\.|[^"\\])*")\s*=\s*("(?:\\.|[^"\\])*")\s*;', source)
    rest = re.sub(r'"(?:\\.|[^"\\])*"\s*=\s*"(?:\\.|[^"\\])*"\s*;', "", source)
    assert not rest.strip(), f"Invalid strings syntax: {path}"
    result = {}
    for key, value in pairs:
        key, value = json.loads(key), json.loads(value)
        assert key not in result, f"Duplicate key: {key}"
        result[key] = value
    return result


def arguments(value):
    return re.findall(r"%(?:[0-9]+\$)?(?:lld|@|f)", value)


def main():
    inventory = json.loads((ROOT / "Shared/localization-inventory.json").read_text())
    expected = {item["key"]: item for item in inventory}
    assert len(expected) == len(inventory)
    assert not active_keys(ROOT) - expected.keys(), active_keys(ROOT) - expected.keys()
    for language in ("en", "de"):
        folder = ROOT / "Shared" / f"{language}.lproj"
        ordinary = strings(folder / "Localizable.strings")
        xml = ET.parse(folder / "Localizable.stringsdict")
        for dictionary in xml.iter("dict"):
            xml_keys = [child.text for child in dictionary if child.tag == "key"]
            assert len(xml_keys) == len(
                set(xml_keys)
            ), "Duplicate plural dictionary key"
        with (folder / "Localizable.stringsdict").open("rb") as stream:
            plurals = plistlib.load(stream)
        assert not ordinary.keys() & plurals.keys()
        assert ordinary.keys() | plurals.keys() == expected.keys(), (
            language,
            expected.keys() - (ordinary.keys() | plurals.keys()),
        )
        for key, item in expected.items():
            if item["plural"]:
                rule = plurals[key]["count"]
                assert rule["NSStringFormatSpecTypeKey"] == "NSStringPluralRuleType"
                assert rule["NSStringFormatValueTypeKey"] == "lld"
                assert {"one", "other"} <= rule.keys()
                plural_index = plurals[key].get("PsstPluralArgumentIndex", 0)
                integer_arguments = sum(
                    value.endswith("lld") for value in item["arguments"]
                )
                assert (
                    isinstance(plural_index, int)
                    and 0 <= plural_index < integer_arguments
                ), key
                assert (
                    arguments(rule["one"])
                    == item["arguments"]
                    == arguments(rule["other"])
                ), key
            else:
                assert arguments(ordinary[key]) == item["arguments"], key
            assert key.strip(), "Empty UI key"
        assert ordinary["psst.zip"] == "psst.zip"
        if language == "de":
            untranslated = {key for key, value in ordinary.items() if key == value} - {
                "psst.zip",
                "English",
                "Deutsch",
                "System",
                "Server",
                "Dateien",
                "Name (optional)",
                "Details",
            }
            assert not untranslated, untranslated
        for target in ("Psst", "PsstShareExtension"):
            permissions = strings(ROOT / target / f"{language}.lproj/InfoPlist.strings")
            assert permissions["CFBundleDisplayName"] == "psst.zip"
            assert (
                permissions["NSCameraUsageDescription"]
                and permissions["NSLocalNetworkUsageDescription"]
            )
    project = (ROOT / "project.yml").read_text()
    assert "knownRegions: [en, de]" in project and "path: Shared" in project
    assert project.count("templates: [SharedFramework]") == 2
    assert "PsstLanguage()" in (ROOT / "Psst/PsstApp.swift").read_text()
    assert (
        "PsstLanguage()"
        in (ROOT / "PsstShareExtension/ShareExtensionView.swift").read_text()
    )
    scanner = (ROOT / "Psst/Views/ScanReceiveView.swift").read_text()
    assert 'model.stage == "' not in scanner
    assert "refreshLanguage()" in (ROOT / "Shared/PairingScanner.swift").read_text()
    assert "arguments" in (ROOT / "Shared/ClientErrorPresentation.swift").read_text()
    assert "String(localized:" not in "\n".join(
        p.read_text()
        for folder in ("Psst", "Shared", "PsstShareExtension")
        for p in (ROOT / folder).rglob("*.swift")
    )
    print(
        f"iOS catalog/source gates passed: {len(expected)} keys, {sum(bool(item['plural']) for item in inventory)} plural messages in English/German, both permission bundles (not a native build)."
    )


if __name__ == "__main__":
    main()
