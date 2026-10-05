# English and German localization

## Status and scope

Implementation complete, 2026-10-05. Closure outcome: **CLOSED_WITH_DEFERRALS**.
The bounded review and affected corrections are complete. Executed checks are
recorded below; native validation gates remain explicitly unrun.

Add complete English and German UI support to the web UI, Android app, iOS app and
iOS share extension. Prepare a maintainable foundation for additional languages.
Include shared Kotlin and backend changes needed to make errors language-neutral.
Include regular-user, administrator, authenticated, public-link and native guest
flows under their existing access rules.

Preserve psst.zip branding, operator-configured URLs, protocol/application
identities, encryption, account isolation, link limits, reader-aware exhaustion,
receipts and resource/traffic safeguards. Language selection must not change
permissions, restart transfers or spend another download allowance.

This builds on [transfer workflows and shared titles](transfer-workflow-ux-and-shared-link-titles.md),
[mobile appearance](mobile-appearance.md) and
[administration and transfer UX](administration-dashboard-traffic-and-ux.md).
It does not reopen their completed reviews or require translating repository
source comments, operator logs or every documentation page.

Implementation and native validation are separate. Lack of macOS/Xcode does not
exclude iOS or the share extension. Complete available local checks and record
unavailable native builds, XCTest and device checks as pending. Existing CI may
provide evidence when available; creating new CI infrastructure or obtaining CI
access is not a prerequisite for implementation.

## Current foundation

| Area           | Assessment baseline                                                                                                                                                                                                                                          |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Android        | 97 English string resources and four plural groups; no German resources. Many newer views, view models and helpers still contain English display text. No app-language preference or locale configuration; MainActivity currently extends ComponentActivity. |
| iOS            | 119 English entries in Shared/en.lproj/Localizable.strings, shared by the app and extension through project.yml. Incomplete dynamic/error coverage; no German resources, plural resources, localized permission descriptions or language preference.         |
| Web            | No translation catalog or locale state. Text is embedded in Svelte components and TypeScript helpers; app.html fixes the document language to English. Formatting mixes browser-default dates/numbers with hardcoded size/expiry formatting.                 |
| Shared/backend | Some failures already have stable types/codes, but clients also display English exception messages and recognize English prose. Android scanner stage strings currently drive behavior as well as presentation.                                              |

These counts describe the existing resource tables, not the final translation
inventory. Do not translate every source string mechanically: identifiers, SQL,
user content and diagnostics are not UI messages. Some existing translation
entries also contain obsolete fixed-size explanations and should be reconciled
with current behavior before translation.

## 1. Language behavior

- [x] Support the preference values System, English and Deutsch. Show language
      names in their own language; avoid country flags as language identifiers.
      Default to System and preserve the explicit choice locally across restarts.
- [x] Under System, select the first supported language from the device/browser
      preference list, matching regional variants such as de-DE, de-AT and de-CH
      to German. Fall back to English when no supported preference matches.
- [x] Keep the preference independent of the selected server and account. It
      remains available before sign-in, after sign-out and in guest/public-link
      flows. Do not introduce a server-side account-language migration or send a
      language preference inside transfer links, QR codes or pairing credentials.
- [x] Put the web selector in the global shell so login, workspace and public
      pages can use it. Keep it compact alongside appearance controls, including
      narrow screens. Put the native selector in the appearance/language section
      of Settings without adding explanatory clutter.
- [x] Honor platform per-app language selection and System behavior. Make the
      Android picker synchronize with the supported OS/AndroidX locale APIs.
      Verify the hosting-activity/context requirements for older supported
      Android versions before changing MainActivity or theme integration.
- [x] Share an explicit iOS preference with the share extension through existing
      App Group preferences. Resolve ordinary view strings, model/error messages
      and formatters consistently; changing only SwiftUI's locale environment is
      not sufficient for every String/localized-description call.
- [x] Package English/German native permission descriptions and test their OS
      language resolution separately. System permission dialogs follow platform
      localization behavior; do not use unsupported private language APIs.
- [x] Apply language changes safely. Web switching updates presentation without
      a document reload; native locale changes may recreate views/activities but
      must preserve selected files, uploads/downloads, authentication, drafts,
      navigation and checkpoints. Do not refetch payloads merely to translate UI.

## 2. Language-neutral state and errors

Complete these boundaries before replacing operational strings with translations.

- [x] Replace Android scanner English stage comparisons with stable stage
      identifiers/enums. Audit analogous comparisons in web and iOS, including
      error-message matching, validation, progress, cancellation and retry paths.
- [x] Define typed client failures with stable semantic identifiers and bounded
      arguments, such as file count, allowed bytes or retry timestamp. Shared
      Kotlin stays independent of Android/iOS translation resources; native
      presenters map these failures to their own equivalent localized messages.
- [x] Extend stable backend error codes where ordinary user-facing failures
      currently provide only prose. Preserve response status, existing fields,
      security boundaries and error-body limits; keep English diagnostic messages
      for compatibility and operator tooling. The backend does not select UI
      translations or require locale headers.
- [x] Refactor web safe-message handling and native exception presentation to
      use typed failures/codes instead of English-message allowlists or regexes.
      Preserve safe unknown-error fallback, retry instructions and redaction;
      never display arbitrary server bodies, HTML, key fragments or credentials.
- [x] Store status, progress and recoverable failures as language-neutral values
      where persisted. Render them using the current language, including after
      switching language while a failure is visible. Preserve old records;
      unclassified legacy messages must not drive behavior.
- [x] Compute generated History titles, count summaries and fallback names at
      presentation time. Keep user-entered shared titles, local labels, usernames,
      filenames and server addresses unchanged; do not automatically publish or
      translate previously stored user content.

## 3. Translation resources and complete coverage

- [x] Build an inventory of active user-visible messages across all clients.
      Record semantic purpose, interpolation arguments, plural needs and useful
      translator comments. Reuse common messages where their meaning matches;
      distinguish different meanings rather than concatenating generic fragments.
- [x] Establish complete English source resources and complete German resources
      for the supported release. Reconcile obsolete strings and dynamic configured
      limits; do not translate stale fixed 25 MiB policy descriptions.
- [x] Android: move display literals into native strings/plurals, add values-de
      resources, and configure supported app locales. Resolve strings through the
      effective locale context, including view-model/helper-generated messages,
      accessibility labels and share/clipboard text.
- [x] iOS: extend the existing native en.lproj/de.lproj resource structure with
      Localizable.strings and plural .stringsdict resources shared by both targets.
      Add target-specific localized InfoPlist.strings where needed. Use deliberate
      localized interpolation for dynamic views, UIKit labels and errors. Update
      project.yml/source checks to verify resource inclusion in both bundles.
- [x] Web: introduce typed message keys, English/German catalogs and reactive
      locale state. Support named arguments and whole-message plural variants;
      keep translation text escaped. Keep catalogs bundled with the self-hosted
      UI, available without external translation services or runtime downloads.
- [x] Cover sending, receiving, public downloads/uploads, scanned links, pairing,
      History, saved-file actions, appearance/language, server/account settings,
      passwords, administration, users/sessions, security controls, traffic,
      storage/resource policies, reports, dialogs and transient notices.
- [x] Include document titles, navigation/region labels, screen-reader text,
      announcements, tooltips, form errors, password visibility, camera/torch
      controls, share-extension text and report email subjects/body templates.
      Preserve technical resource identifiers in reports and exported data.
- [x] Update the web document language to the effective language, including
      before/at initial rendering. Preserve CSP: locale initialization must use
      the established allowed-script pattern. Handle unavailable browser storage
      and synchronize explicit preference changes across tabs safely.
- [x] Make native lookup and web missing-key fallback deterministic. English is
      the runtime fallback, while completeness checks catch missing supported
      German messages before release. Do not silently present internal key names.

## 4. German terminology, formatting and plurals

- [x] Use consistent informal German du. Maintain a glossary across platforms,
      beginning with Dateien senden, Empfangslink, Verlauf and Datei speichern.
      Review ambiguous actions such as removing local History versus revoking a
      server link so translations preserve their different consequences.
- [x] Translate full sentences and reorder named/positional arguments as needed.
      Do not assemble sentences from English-oriented fragments. Handle counts
      for selected, received, saved and remaining files, attempts, users, sessions
      and durations through native/web plural support.
- [x] Centralize locale-aware date, time, number, percentage, file-size, bandwidth
      and relative-expiry presentation per platform. Replace fixed English date
      ordering and mixed hardcoded decimal formatting. Preserve compatible
      regional preferences and time zones; retain explicit UTC where used for
      administrator accounting cycles or exact diagnostics.
- [x] Keep measurement semantics accurate: preserve byte values and distinguish
      MB from MiB rather than changing units as part of translation. Verify
      German decimal/grouping conventions and meaningful precision.
- [x] Keep wire/storage timestamps, JSON numbers, limit values, cursor formats,
      hashes and identifiers locale-independent. Display formatting must not be
      fed back into API serialization. Validate numeric input without ambiguous
      decimal/grouping conversion or changing whole-number policy semantics.
- [x] Translate user-facing relative durations and singular/plural labels
      correctly for 0, 1 and multiple items. Preserve truthful inactive/saved/
      receipt states and hide expiry countdowns for inactive links as before.

## 5. Layout, accessibility and continuity

- [x] Review German text at ordinary and enlarged sizes on narrow phones and
      desktop. Adjust wrapping, spacing and accessible truncation without
      shrinking text or hiding primary actions. Keep tabs/navigation congruent,
      QR codes readable, sending actions within reach and received files prominent.
- [x] Verify accessible names and announcements match the displayed language,
      and focus remains usable through language changes. Use directional layout
      conventions where practical so future languages are not blocked by new
      left/right assumptions; this release does not claim full RTL validation.
- [x] Verify changes preserve server-versus-device History selection, filters,
      bounded pagination, offline saved records, inbox return context and active
      transfer checkpoints. System-generated labels may change; record identity,
      user titles and save locations must not.
- [x] Exercise language switching during selected-file, active-transfer, failed
      transfer, receipt-retry, camera, login and unsaved administrator-form states.
      Keep language choice a presentation operation; do not resubmit mutations,
      restart recognition, discard drafts or grant administrator transfer access.

## 6. Tests and contributor workflow

- [x] Add resource/catalog checks for missing and duplicate keys, invalid syntax,
      mismatched interpolation names/types and required plural variants. Maintain
      an explicit exception list for non-translatable text such as psst.zip,
      rather than allowing arbitrary English literals.
- [x] Add locale-resolution tests for System/overrides, supported regional
      variants, preference ordering, unsupported locales, absent browser storage,
      persistence and app/extension agreement.
- [x] Test locale-neutral stages, errors and saved state independently of their
      display text. Verify policy/account/permission handling is identical in
      English and German, including unknown-code fallback and final allowances.
- [x] Test counts, durations, decimal separators, date ordering, size/rate units
      and numeric-input serialization. Cover 0/1/2, boundaries and large values.
- [x] Give existing browser/native tests an explicit locale where they assert
      presentation text. Keep representative English/German accessible-label
      assertions; prefer stable domain identifiers for business-only assertions.
- [x] Add focused bilingual UI checks for regular and admin paths, login/public
      pages, scanner, send/receive, History and Settings. Include both appearances,
      keyboard focus, enlarged text and long user titles without translating them.
- [x] Extend available iOS source/portable checks to validate resources,
      placeholders, plural behavior and extension wiring. Record native resource
      packaging, app/extension build, XCTest and UI/device checks separately.
- [x] Document adding a language: resource locations, supported-locale registry,
      glossary, placeholders/plurals, translator comments, fallback behavior and
      verification commands. Keep translation contributions reviewable in the
      repository; no hosted translation platform is required initially.

## Implementation order and delivery

Representative entry points are Android strings.xml, MainActivity, PrefsManager,
ScanViewModel and UI/data presenters; shared API exceptions; iOS Shared resources,
model errors, project.yml and both targets; web app.html, the global layout,
account/api/error helpers and components; backend error helpers and codes.

- [x] Establish the message inventory, terminology, locale contract and stable
      state/error boundaries first. Preserve existing behavior with focused tests.
- [x] Implement resource lookup, locale selection/persistence and formatting
      foundations on all clients, including the iOS extension.
- [x] Extract active English text and complete German translations across the
      defined surface, then verify resource coverage and code/argument mappings.
- [x] Run appropriate shared/Android tests and builds, web unit/type/build and
      focused browser checks, and backend tests for affected error contracts.
      Perform available iOS formatting/source/portable checks. Do not repeat
      unrelated security-plan audits or label source checks as native builds.
- [x] Review bilingual layouts and transfer continuity with available runtime
      checks. Leave unavailable Android physical-device/iOS native gates below
      unchecked, with reproducible steps and the reason they could not run.
- [x] Format changed files, verify completed obligations, check off implemented
      tasks in this plan and record actual evidence. Commit code and this plan
      together. If delivering a development update, preserve configured URLs,
      accounts, data and policies, verify the updated web UI and provide the APK.

## Native validation requiring additional environments

These are verification gates, not deferred implementation scope.

- [ ] On macOS/Xcode, regenerate the iOS project, build the app and embedded share
      extension, and run XCTest in English and German. Verify both bundles include
      the correct resources and localized permission descriptions.
- [ ] On an iOS simulator/device, check app/extension language agreement,
      relaunch/switching, Dynamic Type, VoiceOver, camera and save-provider flows.
- [ ] On supported Android versions, check OS per-app language settings, in-app
      switching/activity recreation, TalkBack and real camera/provider transfer
      continuity. Distinguish emulator coverage from physical-device checks.

## Executed implementation evidence

The production implementation includes bundled English/German resources and a
System/English/Deutsch selector on every client. Web switching is reactive;
Android uses supported per-app locale APIs; iOS and its extension share an App
Group preference. Failure descriptors, scanner stages and stored identities stay
language-neutral. Backend errors add stable codes while preserving HTTP status
and compatibility diagnostics.

| Check                               | Result                                                                                                                                                                                                                                                                                                                                                                                          |
| ----------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Web catalog/source validation       | 1,087 bilingual keys; 49 explicitly reviewed technical exceptions; duplicate, placeholder, plural and raw UI-literal gates passed.                                                                                                                                                                                                                                                              |
| Web unit checks                     | Full suite: 102 passed. After final additions, focused localization/checker tests: 7 passed; the final policy-summary plural regression checks also passed (6 tests).                                                                                                                                                                                                                           |
| Web type check and production build | Passed; 0 type errors or warnings.                                                                                                                                                                                                                                                                                                                                                              |
| Browser regression checks           | 24 cases covered: 19 passed in the combined run; all five affected cases passed after corrections. Includes bilingual login/public/admin/settings/scanner/History, both appearances, narrow/enlarged layouts, selected-file/error continuity, real encrypted receive transfers, cancellation, offline History and receipt retries.                                                              |
| Android resource validation         | 513 strings and 26 plural groups, complete English/German coverage; 102 explicit technical/diagnostic exceptions.                                                                                                                                                                                                                                                                               |
| Android and shared Kotlin           | 146 app unit tests and 163 shared unit tests passed. After the final resource-only plural correction, 4 affected policy tests and both debug APK builds passed.                                                                                                                                                                                                                                 |
| Android emulator                    | 3 instrumentation tests passed: native plural/error rendering; locale switching with retained actual ViewModels, live work, URI/title/limit/secret drafts, session and checkpoints; German 343 dp / 1.3× font layouts in light/dark modes. Screenshots inspected. This emulator run preceded the final resource-only plural correction; that correction received focused unit/build validation. |
| iOS resources and portable checks   | 398 keys and 19 plural messages, both permission bundles; source/configuration checks and 70 portable Swift tests passed. Swift syntax/format checks passed. These are not native app/extension builds.                                                                                                                                                                                         |
| Backend API contract                | `go -C backend test ./internal/api -count=1` passed, including stable-code compatibility and safe metadata/auth failure tests.                                                                                                                                                                                                                                                                  |

Final regression corrections localized three dynamic web headings, removed an
unintentionally visible accessibility-only label, kept mobile QR actions within
reach, and prevented native configuration cleanup from canceling active work.
The bounded closure review identified one remaining policy-summary plural family;
web and Android now select complete messages from the total limit, preserving
the separate used count. Focused English/German checks cover 0-of-1, 1-of-1,
1-of-2 and one/two download allowances.

Existing receive-navigation timing was accommodated in its test fixture, without
changing that production workflow as part of localization.

Reproducible checks and remaining native runtime steps are documented in
[localization verification](../testing/localization.md). Resource locations,
terminology, failure contracts and translation contribution rules are in
[translation contributions](../localization/contributing.md).

The unchecked native gates require macOS/Xcode or physical devices that were not
available here. Portable checks and emulator evidence do not establish iOS
resource packaging, native SwiftUI/framework integration, VoiceOver/TalkBack or
real camera/save-provider behavior.

## Closure and development delivery

The single discovery review found F1: fixed plural wording in the policy-summary
family. One remediation batch and one integrated post-fix review resolved it;
no further material scoped finding remained. The final affected web and Android
checks passed. Native environment checks are deferred as explicitly permitted
by this plan, rather than reported as successful.

The development service was updated at `http://192.168.178.29` after a cold
backup under `backend/data/development-backups/20261005T095342Z`. Existing data
volumes, accounts, URLs and settings were preserved. Public configuration matched
exactly before/after; LAN and loopback shell/CSP/API-code checks passed. A real
LAN browser verified German initial resolution, English override/persistence
across reload, and switching back to German.

The debug APK recorded at localization closure was
`android/app/build/outputs/apk/debug/app-debug.apk` (24,428,503 bytes), SHA-256
`99396bf991f3c3325458614d4a7c818fd584a7e1776f7db807d56e05184f51cd`.
This checksum records that delivery; later fixes may rebuild the APK at the
same path. Code, resources, contributor documentation and this completed plan
were committed together.

## Platform references

The design uses [Android per-app language support](https://developer.android.com/guide/topics/resources/app-languages),
[Android localization resources](https://developer.android.com/guide/topics/resources/localization),
[Apple localization guidance](https://developer.apple.com/documentation/xcode/preparing-your-apps-text-for-translation)
and [JavaScript internationalization](https://developer.mozilla.org/en-US/docs/Web/JavaScript/Guide/Internationalization).
Validate toolchain-specific integration against the project's installed versions
when implementation starts.
