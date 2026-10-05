# Translation contributions

psst.zip bundles translations with its web UI, Android app, iOS app and iOS share
extension. No translation service is required at runtime. English is the source
language; German uses informal **du**.

## Language behavior

The preference is System, English or Deutsch. System selects the first supported
language from the browser/device preference list, including regional variants.
Unsupported preferences fall back to English. Explicit preferences are stored on
the device, independently of account and server. Android synchronizes with its
supported per-app language APIs; iOS shares the override with the extension using
the existing App Group.

Keep user titles, filenames, usernames and configured URLs unchanged. Do not put
locale settings in transfer links, QR codes, API numbers or encrypted manifests.
A language switch updates presentation; it must not submit forms, restart
transfers, consume another download allowance or discard selected files.

## Terminology

| English                | German                    | Meaning                                               |
| ---------------------- | ------------------------- | ----------------------------------------------------- |
| Send files             | Dateien senden            | Create a download link for other people.              |
| Receive link           | Empfangslink              | Let other people send files to the creator.           |
| History                | Verlauf                   | Account transfers and saved device records.           |
| Save file              | Datei speichern           | Save one file on the receiving device.                |
| Save files             | Dateien speichern         | Save multiple files.                                  |
| Revoke link            | Link widerrufen           | Disable the server link.                              |
| Remove local record    | Lokalen Eintrag entfernen | Remove a device record; not a server revocation.      |
| Settings               | Einstellungen             | Device or account settings, according to context.     |
| Sign in / Sign out     | Anmelden / Abmelden       | Account access.                                       |
| Download limit reached | Download-Limit erreicht   | An inactive link; do not display an expiry countdown. |

For network accounting, use **Datenverkehr** rather than standalone Verkehr.
Use **Traffic-Budget** and **Traffic-Limits** for the corresponding administrator
controls, **Verbrauch** for measured usage and **Datenkontingent/Kontingent** for
an allowance. Avoid Verkehrsbudget, Verkehrsgrenzen and Verkehrsregeln, which
suggest road traffic. Keep terminology consistent across the web UI and apps.

Language names remain English and Deutsch rather than translated country names
or flags. The product name remains **psst.zip**.

## Resource rules

Translate complete messages rather than assembling sentences from fragments.
Keep every interpolation argument and its type, but reorder arguments where the
native format permits. Add comments when a short label could have several
meanings. Use plurals for counts and durations, including zero, one and multiple
items. Never translate protocol identifiers or turn user content into a resource
key.

Android uses `android/app/src/main/res/values/strings.xml`, corresponding
`values-de` resources and native plural resources. Supported app locales are in
`res/xml/locales_config.xml`.

The iOS app and extension share `ios/Shared/en.lproj` and `de.lproj` resources,
including plural `.stringsdict` resources. Target-specific `InfoPlist.strings`
provide OS permission descriptions. Resource inclusion is declared in
`ios/project.yml`. Permission prompts use the OS's supported language resolution;
changing a SwiftUI environment alone does not change system dialogs.

Web resources and locale helpers live in `web/src/lib/i18n`. Keep keys typed,
arguments named and text escaped. Catalogs are part of the static build.
`web/static/language.js` initializes the document language under the existing CSP
before rendering; locale changes must remain reactive rather than reload pages.

## Errors and state

Operational states, failure codes and saved record identities remain language
neutral. Shared Kotlin exposes `ClientFailure` and `FailureDescriptions`; native
presenters translate known codes with bounded arguments. Backend error responses
include a stable `code` and `X-Psst-Error-Code` as well as a compatibility English
`error` diagnostic. The backend does not negotiate UI translations.

Never classify failures by English prose. Never display arbitrary response
bodies, HTML, authentication secrets or key fragments. Unknown errors use a safe
localized fallback. Persist a semantic failure descriptor rather than its rendered
translation so an already visible error can change language.

## Formatting and review

Use the effective locale for display dates, counts, percentages, decimal
separators, sizes, rates and relative durations. Keep the original byte values
and distinguish decimal MB from binary MiB. Retain explicit UTC for accounting
cycles where required. API serialization and whole-number limits remain locale
independent.

Before a contribution is complete:

- [ ] Add the locale to supported-language registries and selectors on every client.
- [ ] Supply complete catalogs/resources, including plurals, accessible names,
      model errors, report templates and native permission descriptions.
- [ ] Run each platform's resource checks; fix missing/duplicate keys and
      placeholder/plural mismatches rather than relying on fallback.
- [ ] Run formatting, affected tests and builds.
- [ ] Check narrow screens, long titles, enlarged text and both appearances.
- [ ] Switch languages with selected files, visible errors and unsaved forms;
      check that drafts, authentication and transfer state survive.
- [ ] Check locale-aware display and locale-independent request serialization.
- [ ] Record unrun Xcode/device checks separately from source or portable checks.

The implementation plan records the executed verification commands and remaining
native validation gates: [English and German localization](../plans/english-german-localization.md).
