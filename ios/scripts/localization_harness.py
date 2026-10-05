"""Shared native resources for portable Swift packages (no Apple UI or keychain stubs)."""

from pathlib import Path
import re
import shutil

ROOT = Path(__file__).resolve().parents[1]


def add_localization(sources, package, target, app_constants=True):
    shutil.copy2(ROOT / "Shared/Localization.swift", sources)
    if app_constants:
        shutil.copy2(ROOT / "Shared/AppConstants.swift", sources)
    for language in ("en", "de"):
        shutil.copytree(
            ROOT / "Shared" / f"{language}.lproj", sources / f"{language}.lproj"
        )
    source = package.read_text()
    source = re.sub(
        r'(Package\(name: "[^"]+"),', r'\1, defaultLocalization: "en",', source
    )
    source = re.sub(
        r'(\.target\(name: "' + target + r'"[^\n]*?)(\),)',
        r'\1, resources: [.copy("en.lproj"), .copy("de.lproj")]\2',
        source,
    )
    package.write_text(source)
