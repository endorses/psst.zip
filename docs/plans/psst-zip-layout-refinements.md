# psst.zip layout refinements

## Scope

Address the reported cramped Android Send layout, undersized QR codes with redundant white padding, plural per-file download buttons, and the web workspace's oversized button navigation and heavy framing. Preserve account boundaries, transfer continuity, public links, configured server URLs, and the approved light/dark teal palette.

## Implementation

- [x] Use the available vertical space for Android's empty Send state and selected-file list, retaining reachable Add files and Send files actions. Check the corresponding iOS main-app and extension layouts for fixed-height restrictions.
- [x] Enlarge shared QR codes to the available phone content width, with reasonable desktop/tablet limits. Preserve a four-module quiet zone, remove redundant padding, and render sharply across Android, iOS, and web.
- [x] Label individual web downloads **Save file**, preserving **Save again** and **Save all as ZIP**.
- [x] Replace the web workspace's large button-style destinations with semantic navigation links, a desktop sidebar and compact mobile navigation. Preserve destination URLs, browser history, active upload continuity, and Settings access.
- [x] Refine web typography, spacing, file selection, action hierarchy, surfaces, and borders across authenticated/public pages in both appearances without introducing a new framework.

## Verification and delivery

- [x] Inspect Android empty and populated Send layouts and the enlarged QR in the emulator, including enlarged text. Build the APK.
- [x] Inspect web desktop/phone layouts in light/dark appearances, keyboard focus and overflow. Run affected navigation, upload, download, revocation and pairing browser checks, Svelte checks, and the production build.
- [x] Format iOS changes and run available source checks. Record native build/XCTest/device validation as pending when macOS is unavailable.
- [x] Format and commit the changes and this plan; deploy to the development LAN address; provide the rebuilt APK; remove disposable test services and task-owned temporary artifacts.

## Evidence

Verification results are recorded below. This is a focused follow-up to user-reported layout issues; the earlier UX plan's external native/device validation limitations remain in effect.

### Mobile verification

Android's outer Send scroll container and 280 dp list cap were replaced with a weighted content region. Emulator inspection at 1080×2400 showed eight visible file rows at normal text scale and six at 150%, with both actions retained at the bottom. The empty state centers in the remaining area. The final APK assembled successfully.

Android QR rendering now uses an unpadded module matrix with its four-module quiet zone and whole physical pixels; it fills the phone content width, capped at 400 dp on wider screens. The receive QR measured 974×974 pixels in the emulator. ZXing successfully decoded both the Android and web screenshots to full links including key fragments. Physical optical scanning was not performed.

iOS main-app and extension send content already uses uncapped full-screen scrolling. Their shared QR now uses available width up to 400 pt, one four-module quiet zone, and integer pixel scaling. SwiftFormat 0.62.1 and the source/configuration gate passed. The added QR decode/quiet-zone XCTest, native compilation, and device rendering remain pending on macOS as documented in `ios/README.md`.

### Web verification

The workspace uses real URL navigation links, a desktop sidebar, and a compact mobile navigation bar that wraps at enlarged text sizes. Settings uses descriptive links. The file picker has a clear primary action; shared typography, spacing and softer decorative dividers apply to public and authenticated pages. Individual download actions say **Save file**.

Svelte checks passed with zero errors/warnings and the production build passed. The 29-case browser run passed 28 cases; its Back-navigation test raced the new link navigation before the History route committed. The test now explicitly waits for the History URL/heading before Back and for Settings before reloading. Both affected navigation/theme tests then passed, retaining all original behavior assertions. Together these runs cover all 29 browser scenarios, including account isolation, upload continuity, pairing, revocation, public downloads, LAN HTTP and receive retries.

Inspected desktop (1280×900) and phone (390×844) screenshots in light and dark appearances, public download screens, Settings keyboard focus, receive QR/copy actions, and 200% text. Fixed the enlarged-text navigation overlap observed during this check; labels now wrap without horizontal overflow. Copy/Share remain visible below the enlarged QR at normal phone text scale. Android QR/copy/share also remained visible at 150% text in both themes.

### Delivery artifacts

Android APK: `android/app/build/outputs/apk/debug/app-debug.apk`, SHA-256 `84ea8de154d1d4e60c58963018afdbceb796d310e0b077e9bfaeef147663d5f9`. Native iOS compilation, XCTest execution and physical-device scanning remain explicitly unverified; available source and formatting checks passed.

Deployed to **http://192.168.178.29** with the existing database and server configuration preserved. The live Chromium smoke check passed health, login, the four navigation links, Settings and logout. The disposable test deployment and emulator were removed/stopped, and task-owned temporary screenshots, scripts, sample files, logs and formatter downloads were cleaned up.

### History revocation confirmation follow-up

- [x] Replace the confirmation below the history list with a centered native modal dialog. Keep the current scroll position, make the page behind it inert, initially focus Cancel, contain keyboard focus, and return focus to the original Revoke button on cancellation.
- [x] Keep revocation errors inside the dialog for retry, prevent dismissal while deletion is in progress, and close only after successful deletion or explicit cancellation.
- [x] Verify long-history behavior at desktop and phone widths and preserve the existing real revocation workflow. All three focused browser checks passed, including Escape/Cancel without deletion, focus return, viewport position, error retry, and successful revocation. Inspected light desktop and dark phone screenshots; Svelte checks and the production build passed.

The fix is deployed to the existing LAN instance. It changes only the web confirmation presentation; the existing server revocation operation remains unchanged.
