#!/usr/bin/env python3
"""Verify native catalogs and keep untranslated source text explicitly reviewable."""

from __future__ import annotations

import collections
import json
import re
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RES = ROOT / "android/app/src/main/res"
SOURCES = ROOT / "android/app/src/main/java"
HERE = Path(__file__).resolve().parent
PLACEHOLDER = re.compile(r"%(\d+)\$([a-zA-Z])")


def catalog(language: str) -> dict[str, dict[str, str]]:
    entries: dict[str, dict[str, str]] = {}
    for path in sorted((RES / language).glob("*.xml")):
        for entry in ET.parse(path).getroot():
            if entry.tag not in {"string", "plurals"}:
                continue
            name = entry.attrib["name"]
            assert name not in entries, f"Duplicate {language}/{name}"
            values = (
                {"string": entry.text or ""}
                if entry.tag == "string"
                else {item.attrib["quantity"]: item.text or "" for item in entry}
            )
            if entry.tag == "plurals":
                assert len(values) == len(entry), f"Duplicate plural category {name}"
                assert {"one", "other"} <= values.keys(), (
                    f"Missing native plural variants {name}"
                )
            for value in values.values():
                assert value.strip() or name == "stage_none", (
                    f"Empty resource {language}/{name}"
                )
                assert not re.search(r"\\\\[nt]", value), (
                    f"Double-escaped newline {language}/{name}"
                )
                assert not re.search(r"%(?!\d+\$[a-zA-Z]|%)", value), (
                    f"Non-positional interpolation {name}"
                )
            entries[name] = values
    return entries


def literals(source: str):
    """Kotlin comments are excluded; string values remain exact (including templates)."""
    token = re.compile(r'//[^\n]*|/\*[\s\S]*?\*/|"""[\s\S]*?"""|"(?:\\.|[^"\\])*"')
    for match in token.finditer(source):
        raw = match.group()
        if raw.startswith(("//", "/*")):
            continue
        value = raw[3:-3] if raw.startswith('"""') else raw[1:-1]
        yield value, source[: match.start()].count("\n") + 1


def visible_candidates():
    candidates = []
    for path in sorted(SOURCES.rglob("*.kt")):
        source = path.read_text()
        for literal, line in literals(source):
            # Natural-language prose plus the few deliberate visible technical/autonym literals.
            if re.search(r"[A-Za-z]{3} [A-Za-z]{2}", literal) or literal in {
                "psst.zip",
                "English",
                "Deutsch",
                "Downloads/psst.zip",
            }:
                candidates.append(
                    {
                        "file": str(path.relative_to(ROOT)),
                        "literal": literal,
                        "line": line,
                    }
                )
    return candidates


def main():
    english, german = catalog("values"), catalog("values-de")
    assert english.keys() == german.keys(), (
        f"Catalog mismatch: German missing {english.keys() - german.keys()}, extra {german.keys() - english.keys()}"
    )
    for key, variants in english.items():
        assert variants.keys() == german[key].keys(), f"Variant mismatch {key}"
        for variant, text in variants.items():
            assert collections.Counter(
                PLACEHOLDER.findall(text)
            ) == collections.Counter(PLACEHOLDER.findall(german[key][variant])), (
                f"Interpolation mismatch {key}/{variant}"
            )
    exceptions = json.loads((HERE / "nontranslatable.json").read_text())
    allowed = {
        (entry["file"], entry["literal"]): entry["reason"]
        for entry in exceptions["source_literals"]
    }
    found = {(entry["file"], entry["literal"]) for entry in visible_candidates()}
    assert found <= allowed.keys(), "Unclassified English source literals: " + repr(
        found - allowed.keys()
    )
    assert allowed.keys() <= found, "Stale source exceptions: " + repr(
        allowed.keys() - found
    )
    assert all(reason.strip() for reason in allowed.values())
    for key, reason in exceptions["native_resources"].items():
        assert english[key] == german[key], (
            f"Nontranslated technical value changed: {key}"
        )
        assert reason.strip()
    inventory = json.loads((HERE / "inventory.json").read_text())
    assert inventory.keys() == english.keys(), "Translation inventory is stale"
    for key, entry in inventory.items():
        assert entry["english"] == english[key], f"Inventory text drift {key}"
        assert entry["purpose"] and entry["translator_note"]
    source = (SOURCES / "zip/psst/android/viewmodel/ScanViewModel.kt").read_text()
    assert not re.search(r'stage\s*[!=]=\s*"', source), (
        "Rendered captions drive scanner behavior"
    )
    assert "val stage: ScanStage" in source
    main = (SOURCES / "zip/psst/android/MainActivity.kt").read_text()
    assert "AppCompatActivity()" in main and "onConfigurationChanged" in main
    manifest = (ROOT / "android/app/src/main/AndroidManifest.xml").read_text()
    assert (
        "locale|layoutDirection" in manifest
        and "autoStoreLocales" in manifest
        and "locales_config" in manifest
    )
    supported = [
        element.attrib["{http://schemas.android.com/apk/res/android}name"]
        for element in ET.parse(RES / "xml/locales_config.xml").getroot()
    ]
    assert supported == ["en", "de"], f"Unexpected supported locales {supported}"
    print(
        f"Android localization: {sum('string' in v for v in english.values())} strings, {sum('other' in v for v in english.values())} plural groups, exact English/German coverage; {len(found)} explicit technical/diagnostic exceptions."
    )


if __name__ == "__main__":
    try:
        main()
    except (AssertionError, ET.ParseError, KeyError) as error:
        sys.exit(str(error))
