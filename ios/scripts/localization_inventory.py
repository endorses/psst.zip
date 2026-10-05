"""Swift literal inventory for active UI keys; protocol/SQL identifiers stay untranslatable."""

import json
import re
from pathlib import Path


def strings(s):
    i = 0
    while i < len(s):
        if s.startswith("//", i):
            i = s.find("\n", i) + 1 or len(s)
            continue
        if s.startswith("/*", i):
            j = s.find("*/", i + 2)
            i = j + 2 if j >= 0 else len(s)
            continue
        if s[i] == "#":
            raw = re.match(r'(#+)"', s[i:])
            if raw:
                end = s.find('"' + raw.group(1), i + len(raw.group(0)))
                i = end + 1 + len(raw.group(1)) if end >= 0 else len(s)
                continue
        if s.startswith('"""', i):
            end = s.find('"""', i + 3)
            i = end + 3 if end >= 0 else len(s)
            continue
        if s[i] == '"':
            start = i
            i += 1
            while i < len(s):
                if s.startswith("\\(", i):
                    i = expression(s, i + 2)
                    continue
                if s[i] == "\\":
                    i += 2
                    continue
                if s[i] == '"':
                    i += 1
                    break
                i += 1
            yield start, i, s[start + 1 : i - 1]
        else:
            i += 1


def expression(s, i):
    depth = 1
    while i < len(s):
        if s[i] == '"':
            i += 1
            while i < len(s):
                if s[i] == "\\":
                    i += 2
                elif s[i] == '"':
                    i += 1
                    break
                else:
                    i += 1
            continue
        if s[i] == "(":
            depth += 1
        if s[i] == ")":
            depth -= 1
            if depth == 0:
                return i + 1
        i += 1
    return i


NON_UI = {
    "init(coder:) is not supported",
    "BEGIN IMMEDIATE",
    "EEE, dd MMM yyyy HH:mm:ss zzz",  # HTTP Retry-After protocol date, never UI.
    "Bearer ",
    "retained in immutable original JSON range",
}


def active_keys(root):
    keys = set()
    for folder in ("Shared", "Psst", "PsstShareExtension"):
        for path in (root / folder).rglob("*.swift"):
            if path.name in {"Localization.swift", "HistoryRecordDatabase.swift"}:
                continue
            source = path.read_text()
            for start, end, value in strings(source):
                if value in NON_UI or "\\(" in value:
                    continue
                context = source[max(0, start - 80) : start]
                localized = bool(
                    re.search(
                        r"(?:L10n\.(?:text|format|message|datedMessage)|LocalizedStringKey)\($",
                        context,
                    )
                )
                human = " " in value and bool(re.search(r"[a-zA-Z]{2}", value))
                if human or localized:
                    try:
                        key = json.loads('"' + value + '"')
                    except ValueError:
                        continue
                    if key.strip():
                        keys.add(key)
    return keys
