# psst.zip brand assets

The symbol is a pair of softly shaped lips and a vertical finger making a shushing gesture. `symbol.svg` is the editable path master. `symbol-dark.svg` and `symbol-monochrome.svg` support dark backgrounds and monochrome launcher treatments. The horizontal lockups keep the exact **psst.zip** spelling; use the symbol alone for launcher icons and favicons.

The artwork was redrawn as vector paths from the approved AI-generated concept. It contains no emoji artwork or traced raster pixels and is distributed under the repository's Apache-2.0 license. Nunito at weight 800 provides the wordmark; the unmodified variable font and its SIL Open Font License are in `fonts/`. Source: [Google Fonts / Nunito](https://github.com/google/fonts/tree/main/ofl/nunito). Outlined production exports require no installed font. Editable wordmark SVGs use live text; install the bundled Nunito font before editing them.

The palette is teal `#0f766e`, mint `#5eead4`, deep green `#172b2a` / `#0b1917`, and off-white `#ecf5f1` / `#f6f8f7`. Keep SVG edges flat; do not add gradients, emoji facial details or an exclamation mark to the wordmark.

## Reproducible exports

Install Inkscape and Python with the versions recorded in `requirements.txt`, then run:

```sh
uv run --with-requirements assets/brand/requirements.txt python assets/brand/export.py
```

The script uses local masters and the bundled font; it does not download artwork or fonts. It writes outlined/editable lockups, web SVG/PNG/ICO favicons and Apple touch artwork, Android vector/adaptive/themed/legacy icon resources, and the iOS app icon and symbol catalogs for the app and share extension. PNG resizing uses Lanczos. Android adaptive foregrounds include a deliberate safe-zone inset, and iOS app artwork is opaque with square corners for the OS mask.

Inspect exports at their actual sizes, especially 16/32 px favicons and launcher masks. QR code producers use the symbol with a small white backing and high error correction; the artwork must never cover finder patterns or the quiet zone.
