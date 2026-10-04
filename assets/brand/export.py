#!/usr/bin/env python3
"""Export psst.zip vector masters into app and web assets (no network access)."""

import json
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as ET
from io import BytesIO
from pathlib import Path

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont
from PIL import Image

BRAND = Path(__file__).resolve().parent
ROOT = BRAND.parent.parent
SVG_NS = "http://www.w3.org/2000/svg"
ANDROID_NS = "http://schemas.android.com/apk/res/android"


def svg(body, box="0 0 128 128", title="psst.zip"):
    return f'<svg xmlns="{SVG_NS}" viewBox="{box}" fill="none"><title>{title}</title>{body}</svg>\n'


def body_of(path):
    tree = ET.parse(path).getroot()
    return "".join(
        ET.tostring(child, encoding="unicode")
        for child in tree
        if child.tag != f"{{{SVG_NS}}}title"
    )


def write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(data)


def render(source, size):
    with tempfile.TemporaryDirectory(prefix="psst-brand-") as scratch:
        output = Path(scratch) / "image.png"
        subprocess.run(
            [
                "inkscape",
                str(source),
                "--export-type=png",
                f"--export-filename={output}",
                f"--export-width={size}",
                "--export-background-opacity=0",
            ],
            check=True,
            stdout=subprocess.DEVNULL,
        )
        return Image.open(BytesIO(output.read_bytes())).convert("RGBA")


def wordmark():
    font = instantiateVariableFont(TTFont(BRAND / "fonts/Nunito.ttf"), {"wght": 800})
    glyphs = font.getGlyphSet()
    cmap = font.getBestCmap()
    scale = 86 / font["head"].unitsPerEm
    x = 146.0
    paths = []
    for character in "psst.zip":
        glyph = glyphs[cmap[ord(character)]]
        pen = SVGPathPen(glyphs)
        glyph.draw(TransformPen(pen, (scale, 0, 0, -scale, x, 86)))
        paths.append(f'<path d="{pen.getCommands()}"/>')
        x += glyph.width * scale - 1.5
    return "".join(paths), x + 6


def android_vector(source, destination, adaptive=False):
    tree = ET.parse(source).getroot()
    attributes = {
        "xmlns:android": ANDROID_NS,
        "android:width": "108dp" if adaptive else "128dp",
        "android:height": "108dp" if adaptive else "128dp",
        "android:viewportWidth": "128",
        "android:viewportHeight": "128",
    }
    vector = ET.Element("vector", attributes)
    parent = vector
    if adaptive:
        # The visible mark stays inside the guaranteed adaptive-icon safe circle.
        parent = ET.SubElement(
            vector,
            "group",
            {
                "android:scaleX": ".60",
                "android:scaleY": ".60",
                "android:translateX": "25.6",
                "android:translateY": "20.8",
            },
        )
    for path in tree.findall(f"{{{SVG_NS}}}path"):
        attrs = {
            "android:pathData": path.attrib["d"],
            "android:fillColor": path.attrib["fill"],
        }
        if "stroke" in path.attrib:
            attrs.update(
                {
                    "android:strokeColor": path.attrib["stroke"],
                    "android:strokeWidth": path.attrib["stroke-width"],
                    "android:strokeLineJoin": "round",
                }
            )
        ET.SubElement(parent, "path", attrs)
    ET.indent(vector, space="    ")
    write(
        destination,
        '<?xml version="1.0" encoding="utf-8"?>\n'
        + ET.tostring(vector, encoding="unicode")
        + "\n",
    )


def main():
    symbol = body_of(BRAND / "symbol.svg")
    dark_symbol = symbol.replace("#5eead4", "#ecf5f1").replace("#0f766e", "#5eead4")
    write(BRAND / "symbol-dark.svg", svg(dark_symbol))
    outlines, width = wordmark()
    for appearance, color in (("light", "#172b2a"), ("dark", "#ecf5f1")):
        content = symbol
        if appearance == "dark":
            content = dark_symbol
        write(
            BRAND / f"logo-{appearance}.svg",
            svg(content + f'<g fill="{color}">{outlines}</g>', f"0 0 {width:.2f} 128"),
        )
        editable = (
            content
            + f'<text x="146" y="86" fill="{color}" font-family="Nunito" font-size="86" font-weight="800" letter-spacing="-1.5">psst.zip</text>'
        )
        write(
            BRAND / f"logo-{appearance}-editable.svg",
            svg(editable, f"0 0 {width:.2f} 128"),
        )
    # Opaque icon background with generous breathing room for OS masks.
    write(
        BRAND / "app-icon.svg",
        svg(
            '<path fill="#0b1917" d="M0 0H128V128H0Z"/>'
            + f'<g transform="translate(9 2) scale(.86)">{dark_symbol}</g>'
        ),
    )
    web = ROOT / "web/static"
    for name in (
        "symbol.svg",
        "symbol-monochrome.svg",
        "logo-light.svg",
        "logo-dark.svg",
    ):
        destination = web / "brand" / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(BRAND / name, destination)
    shutil.copyfile(BRAND / "app-icon.svg", web / "favicon.svg")
    icon = render(BRAND / "app-icon.svg", 1024)
    for size in (16, 32, 48, 192):
        icon.resize((size, size), Image.Resampling.LANCZOS).save(
            web / f"favicon-{size}.png"
        )
    icon.resize((32, 32), Image.Resampling.LANCZOS).save(web / "favicon.png")
    icon.resize((180, 180), Image.Resampling.LANCZOS).convert("RGB").save(
        web / "apple-touch-icon.png"
    )
    icon.save(web / "favicon.ico", sizes=[(16, 16), (32, 32), (48, 48)])
    ios = ROOT / "ios/Psst/Assets.xcassets/AppIcon.appiconset"
    icon.convert("RGB").save(ios / "AppIcon.png")
    write(
        ios / "Contents.json",
        json.dumps(
            {
                "images": [
                    {
                        "filename": "AppIcon.png",
                        "idiom": "universal",
                        "platform": "ios",
                        "size": "1024x1024",
                    }
                ],
                "info": {"author": "xcode", "version": 1},
            },
            indent=2,
        )
        + "\n",
    )
    ios_symbol = ROOT / "ios/Psst/Assets.xcassets/BrandSymbol.imageset"
    ios_symbol.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(BRAND / "symbol.svg", ios_symbol / "symbol.svg")
    write(
        ios_symbol / "Contents.json",
        json.dumps(
            {
                "images": [{"filename": "symbol.svg", "idiom": "universal"}],
                "info": {"author": "xcode", "version": 1},
                "properties": {"preserves-vector-representation": True},
            },
            indent=2,
        )
        + "\n",
    )
    extension_assets = ROOT / "ios/PsstShareExtension/Assets.xcassets"
    write(
        extension_assets / "Contents.json",
        json.dumps({"info": {"author": "xcode", "version": 1}}, indent=2) + "\n",
    )
    shutil.copytree(
        ios_symbol, extension_assets / "BrandSymbol.imageset", dirs_exist_ok=True
    )
    android = ROOT / "android/app/src/main/res"
    android_vector(BRAND / "symbol.svg", android / "drawable/brand_symbol.xml")
    android_vector(
        BRAND / "symbol-dark.svg",
        android / "drawable/ic_launcher_foreground.xml",
        adaptive=True,
    )
    android_vector(
        BRAND / "symbol-monochrome.svg",
        android / "drawable/ic_launcher_monochrome.xml",
        adaptive=True,
    )
    for density, size in (
        ("mdpi", 48),
        ("hdpi", 72),
        ("xhdpi", 96),
        ("xxhdpi", 144),
        ("xxxhdpi", 192),
    ):
        target = android / f"mipmap-{density}"
        target.mkdir(parents=True, exist_ok=True)
        for name in ("ic_launcher", "ic_launcher_round"):
            icon.resize((size, size), Image.Resampling.LANCZOS).save(
                target / f"{name}.png"
            )
    adaptive = '<?xml version="1.0" encoding="utf-8"?>\n<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">\n    <background android:drawable="@color/ic_launcher_background" />\n    <foreground android:drawable="@drawable/ic_launcher_foreground" />\n    <monochrome android:drawable="@drawable/ic_launcher_monochrome" />\n</adaptive-icon>\n'
    for name in ("ic_launcher", "ic_launcher_round"):
        write(android / "mipmap-anydpi-v33" / f"{name}.xml", adaptive)
    write(
        android / "values/ic_launcher_background.xml",
        '<?xml version="1.0" encoding="utf-8"?>\n<resources>\n    <color name="ic_launcher_background">#0b1917</color>\n</resources>\n',
    )


if __name__ == "__main__":
    main()
