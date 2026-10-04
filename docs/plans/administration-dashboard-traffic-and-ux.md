# Administration dashboard, traffic accounting and transfer UX

## Status and scope

Implemented across backend, web, shared client, Android and iOS, and deployed to
the development server. The bounded closure review is complete with external
validation deferrals: native iOS builds/XCTest and physical optical/accessibility
checks remain pending. All discovered implementation issues were fixed and their
focused checks passed. Unchecked items below retain those external checks and
the final repository commit step.

This plan covers clearer transfer History, simpler mobile scanning, consistent narrow-screen web navigation, administration-only accounts, confirmed/mandatory password changes, persistent traffic accounting and an admin dashboard. It supersedes the earlier idea of giving admins an Overview page alongside Send, Receive and Scan: **administrators manage the service; regular accounts perform authenticated transfers**.

This builds on [access control](access-control.md), [mobile navigation and scanning](mobile-navigation-scanning-and-branding.md), and [download acknowledgements](download-acknowledgements.md). The scope and Android/iOS parity requirements in this plan take precedence over older platform exclusions.

Preserve the existing psst.zip branding, configurable file limits, chunked encryption, public link capabilities, delivery acknowledgements, local download persistence and account isolation. Android and iOS receive equivalent applicable changes, including account setup and sending in the iOS share extension. Missing Xcode or physical devices limits verification, not implementation scope. This plan does not include APK installer integration or further logo changes.

## Agreed experience

Regular users continue to land on Send. Their web navigation remains Send, Receive, Scan QR code, History and Settings. On narrow screens, all five destinations use an icon above a single-line label: **Send · Receive · Scan · History · Settings**. The Scan page retains its full heading.

Administrators land on Overview and have **Overview, Users, Traffic and Server settings** navigation. Account/password settings, session management and Sign out remain reachable from the account area. Admins have no transfer creation, scanner, receive-link creation or personal transfer History pages. The same person can create a separate regular account for their own transfers; creating the first regular user is the dashboard's initial next action.

Public download and receive-upload links remain accessible to everyone, including someone whose browser happens to have an admin or restricted session. Possession of the link grants those capabilities independently of account privileges. Web scanning itself remains restricted to fully enabled regular-user sessions; do not add a guest scanner. Native guest scanning/downloads remain available without account configuration.

History uses visible type labels, meaningful titles, dates, file counts and status instead of foregrounding UUIDs. Mobile scanning automatically uses an available rear camera, keeps a supported torch and camera-free alternatives, and has no camera switch button. Browser webcam selection remains available.

Newly created non-admin users must replace their temporary password before authenticated work. Admin resets of non-admin passwords reinstate that requirement. Admin accounts, including the initial administrator, are exempt from mandatory first-login password replacement. Every user-initiated new-password form requires confirmation.

## Existing implementation entry points

| Area                                        | Relevant source                                                                                                                                                                                                                                                            |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Web shell, History, account forms and users | `web/src/routes/+page.svelte`, `web/src/lib/account.ts`, `web/src/lib/api.ts`, `web/src/lib/components/ScanPanel.svelte`, `ServerSettings.svelte`                                                                                                                          |
| Android History and scanner                 | `android/app/src/main/java/zip/psst/android/data/HistoryDatabase.kt`, `AccountHistory.kt`, `UnifiedHistory.kt`, `ui/screens/HistoryScreen.kt`, `ui/components/EmbeddedScanner.kt`, `ui/screens/ScanScreen.kt`                                                               |
| Android account flow                        | `ui/navigation/NavGraph.kt`, `ui/screens/ServerConfigScreen.kt`, `viewmodel/ServerConfigViewModel.kt`, `data/SessionStorage.kt` under the same Android package                                                                                                             |
| iOS History, camera and account flows       | `ios/Shared/TransferHistoryStore.swift`, `PairingScanner.swift`, `ServerConfigManager.swift`, `SessionStore.swift`, `LoginFields.swift`, `ios/Psst/Services/UnifiedHistory.swift`, `Views/HistoryView.swift`, `Views/ScanReceiveView.swift`, and `ios/PsstShareExtension/` |
| Shared auth and error contract              | `shared/src/commonMain/kotlin/zip/psst/shared/api/AuthApi.kt`, `AuthenticatedWrite.kt`, `AuthResources.kt`                                                                                                                                                                  |
| Backend authorization and resources         | `backend/internal/api/auth.go`, `server.go`, `handlers.go`, `settings.go` and the tus adapter                                                                                                                                                                              |
| Persistence and transfer IO                 | `backend/internal/database/auth.go`, `migrations.go`, `settings.go`, resource queries, `backend/internal/tus/handler.go`, and file-store reads/writes                                                                                                                      |

Existing title fields and local web labels should be extended rather than replaced with a parallel history store. Current password changes and admin resets share database update behavior; distinguish their intent when adding the mandatory-change flag. Current shared clients collapse authentication failures into a common error; required-password and role restrictions need explicit handling.

## 1. Recognizable History entries

For example, a sent row can read **Sent — holiday.jpg + 3 files**, followed by its creation time, size and delivery status. A receive-link row can read **Receive link — Wedding photos**, followed by its creation time, expiry and number of files received. Type must remain explicit even when the user supplies a custom title.

For this iteration, custom titles are **local to the browser/device**, using the existing account-scoped local stores. This fulfills the agreed encrypted-or-local requirement without introducing a new server-visible naming field or key-synchronization protocol. Explain that a rename applies on this device. A device without the encryption key or local title uses a useful type/date/count fallback; do not invent access to decrypted filenames.

- [x] Add consistent Sent, Receive link and Downloaded type labels/icons across web and mobile History where those record types exist. Keep existing filters, stable ordering and type-specific actions.
- [x] Derive sent titles from known/decrypted filenames, with a compact additional-file count. Preserve the file extension when truncating long names; expose the full title accessibly.
- [x] Offer an optional local name when creating a receive link and a Rename action for owned sent/receive entries. Support edit, cancel and clearing a custom name to restore the automatic title. Do not require naming before a transfer can proceed.
- [x] Display creation date/time, file count, size where known, expiry and meaningful status. Keep full UUIDs and other technical identifiers in expanded details rather than the primary row.
- [x] Scope persisted labels by server, account, resource type and ID; keep downloaded records scoped to their existing local identity. Ensure automatic refresh does not overwrite a custom title and account changes do not expose another account's labels.
- [x] Preserve existing local titles and history through migration. No plaintext custom title or filename may be added to server metadata, traffic records, logs or administrator responses.
- [x] Preserve key-missing/cross-device explanations, local-only removal, remote revocation, partial-save resume, delivery receipt retry and offline downloaded-file access.
- [ ] Verify mixed and large histories, identical default titles, long/Unicode names, logout/account switching, reload/relaunch, and correct empty/filter states on all clients.

## 2. Stable mobile scanning without camera switching

The reported Android symptom is that Switch camera removes the torch control and freezes the preview. Do not assert a root cause until tracing the current camera lifecycle. Removing the unnecessary selector should also leave a reliable preview lifecycle and recovery path.

- [x] Reproduce or trace the reported switch/freeze path in `EmbeddedScanner.kt`; record the cause and remove the mobile Switch camera control and its switching-only state.
- [x] Select an available rear camera automatically on Android and iOS; fall back to another available camera when no rear camera exists. Handle devices with no usable camera explicitly.
- [x] Apply the same selection behavior to main-app scanning, account pairing and applicable iOS share-extension pairing. Keep browser webcam selection for multiple-camera setups.
- [x] Show the torch only when supported by the selected camera. Keep torch state accurate and turn it off when capture stops.
- [x] Preserve permission denial/re-entry behavior, immediate preview after grant, foreground/visibility ownership, single active decoder and release after detection, navigation or backgrounding.
- [x] Provide a visible camera error and retry/reopen action if initialization fails; retain Paste link and Choose QR image. Retrying must not create duplicate camera sessions or transfers.
- [ ] Verify rear-only, front-only and unavailable-camera cases, repeated entry/exit, rotation, background/foreground, permission recovery and scanning for download/upload/pairing flows. Record physical optical checks separately from emulator and image-decoder checks.

## 3. Consistent mobile web navigation

- [x] At narrow widths, use five equal-width navigation items, with a fixed icon row above a single-line label. Use Send, Receive, Scan, History and Settings; retain descriptive accessible names and the full Scan QR code page title.
- [x] Keep icon sizing, icon/text gaps, vertical alignment and active indicators consistent. Prevent individual items from independently switching between horizontal and stacked layouts.
- [x] Preserve the comfortable horizontal icon-and-label layout where space allows and the existing wide-screen navigation structure.
- [x] Preserve keyboard focus, current-location semantics and useful touch targets. Verify no clipped labels or horizontal overflow at narrow phone widths, enlarged text/zoom and both appearances.
- [x] Apply equivalent spacing discipline to the separate four-destination admin navigation; do not insert admin destinations into the regular user's five-item row.

## 4. Administration-only role boundary

| Operation                                                                    | Fully enabled regular user        | Administrator                     | Public capability holder            |
| ---------------------------------------------------------------------------- | --------------------------------- | --------------------------------- | ----------------------------------- |
| Create standalone transfer or receive link                                   | Allowed                           | Denied                            | Denied                              |
| Account-backed upload/finalize                                               | Own authorized resources          | Denied                            | No account privilege                |
| Download via public link                                                     | Allowed                           | Allowed through link              | Allowed                             |
| Upload through an existing public receive link                               | Allowed through scoped capability | Allowed through scoped capability | Allowed through scoped capability   |
| Web scanner                                                                  | Allowed                           | No scanner page                   | No guest scanner                    |
| Personal transfer History                                                    | Own resources                     | No personal transfer page         | Native local downloads remain local |
| Users, traffic, server settings, server-wide operational metadata/revocation | Denied                            | Allowed                           | Denied                              |
| Account/password and own session management                                  | Allowed                           | Allowed                           | Not applicable                      |

Users awaiting a password change have only the restricted authentication actions described in section 5; public capability access remains independent.

- [x] Introduce an explicit regular-user creation/write guard rather than treating any authenticated session as upload authorization. Check current server-side role/state for cookie and bearer sessions, including sessions issued before deployment.
- [x] Enforce the boundary on standalone transfer creation, receive-link creation and all account-authorized continuation paths: file allocation/PATCH, manifest upload and finalization. An admin must not bypass the rule by calling an API directly or using an old client.
- [x] Retain admin operational inspection and revocation without granting plaintext/key access. Reuse existing authorized resource management; present operational metadata under Overview rather than a personal transfer History page.
- [x] Keep anonymous public download, receipt and slot-scoped upload capabilities working. A browser's ambient admin/restricted cookie must neither grant account upload rights nor invalidate an otherwise valid public capability.
- [x] Prevent admin mobile-pairing issuance and redemption, including pending grants created before the role restriction. Retain admin session listing/revocation for administrative sessions.
- [x] Show mobile users a clear administration-only account message and a route to use a regular account. Cover username/password login, pairing and already-stored admin sessions on Android, iOS and the share extension; avoid redirect/login loops and preserve guest access.
- [x] Existing admin-owned resources are not silently deleted, reassigned or given new keys. Completed public links and already-issued public receive links remain valid until normal expiry/revocation; admins can revoke them. Block continuation using admin account privileges. Do not create a new personal-history exception for old admin records.
- [x] Keep last-enabled-admin protection, administrator bootstrap, regular-user resource isolation and configured transport restrictions intact. Preserve local copies independently of account role changes.

## 5. Password confirmation and mandatory first change

Add an explicit persistent `must_change_password` flag (or equivalent), returned by login/current-user responses. New regular accounts and administrator resets of regular accounts set it. Successful self-service password replacement clears it atomically. Existing accounts migrate without a forced reset; the rule applies to accounts created or reset after this change. Admins remain exempt from mandatory replacement, including after an admin reset.

- [x] Add New password and Confirm password to web self-service password changes, with an associated mismatch message and submit validation. Support password managers, paste, visibility controls and `new-password` autocomplete; never log password fields.
- [x] Treat administrator-created/reset non-admin passwords as temporary and explain the requirement in the Users UI. Require the replacement to differ from the temporary/current password, using the existing password hashing and strength rules.
- [x] Make schema changes additive and restart-safe within the existing positional migration system; preserve users, sessions, resources and existing server settings.
- [x] Persist the flag and include it in web/shared auth models. Add shared current-user and password-change API methods where absent. Distinguish administrator reset from user self-change in database/service APIs so one path cannot accidentally clear or reinstate the other's requirement.
- [x] Permit a restricted session to inspect its own authentication state, replace its password and sign out. Deny ordinary authenticated resources, transfer creation/writes, pairing issuance/redemption and administrative APIs until the change completes. Preserve independent public-link access.
- [x] Return stable machine-readable errors for password-change-required and admin-transfer-forbidden states. Update shared clients and web handling so these are not flattened into generic credential failures or endless login prompts.
- [x] Route the first web login to a focused mandatory-change form before normal navigation. Direct URLs, reloads, multiple tabs and expired/revoked sessions must not bypass the restriction.
- [x] Provide the equivalent mandatory-change flow on Android and iOS, including share-extension account setup. Preserve pending same-account file selections where permitted; changing accounts must not carry private work to a different account. Do not offer mobile pairing before completing the change.
- [x] Keep existing session/pairing revocation on password change/reset. After successful replacement, clearly request sign-in with the new password and return to the intended same-account screen without replaying an upload automatically.
- [x] Verify wrong current password, mismatch, unchanged replacement, weak password, cancellation, concurrent/reset races, persistence across restart, old sessions, regular-user restrictions and administrator exemptions. Keep password confirmation a client UX check and password policy/state enforcement server-side.

## 6. Persistent traffic accounting

Measure traffic from the server's perspective: **uploaded** is inbound transfer payload, **downloaded** is outbound transfer payload, and **combined** is their sum. Count encrypted file and encrypted manifest body bytes actually read/written by the application, including repeat downloads, retries and partial/failed requests that moved bytes. Do not count declared file sizes, reserved capacity or delivery acknowledgements as bytes transferred. Define this as application transfer traffic, not all network-interface traffic.

Exclude HTML/assets, account/control API bodies, HTTP/TLS overhead, proxy buffering/discarded bytes, backups and unrelated services. A response write measures what the application handed to its HTTP stack, not proof that a recipient saved it. Provider accounting can therefore differ. Keep that explanation visible near the monitor and in operator documentation.

- [x] Add persistent aggregate counters and UTC time buckets for inbound/outbound bytes, with a recording-start timestamp. Do not fabricate historical traffic from stored file sizes; label totals and partial initial billing periods as measured since recording began.
- [x] Instrument the actual transfer request/response IO boundaries once, including chunked tus PATCH and manifest/blob reads/writes. Avoid double-counting between generic middleware, handlers and storage code. Preserve streaming and cancellation rather than buffering files for measurement.
- [x] Include successful bytes before failures, interrupted transfers and retries; HEAD/OPTIONS and zero-byte bodies contribute no payload bytes. Do not decrement traffic when transfers expire, files are revoked or storage is cleaned.
- [x] Persist deltas safely under concurrent requests. Specify and implement a bounded flush strategy for long-running requests, graceful shutdown and restart recovery; document possible unflushed measurement loss on abrupt process termination. Do not claim crash-perfect or provider-billing-grade precision.
- [x] Make failed/stale accounting visible to admins instead of silently showing healthy zero totals. Keep counters nonnegative and use integer byte storage with overflow checks.
- [x] Add admin-only aggregate read endpoints for today, calendar month, current billing cycle, selectable date ranges and measured lifetime totals. Return upload/download/combined series and the recording start/measurement status without filenames, keys, URLs or per-request secrets.
- [x] Add persistent admin settings for an optional traffic allowance, billing-cycle start day and allowance basis: outbound only or both directions. Use UTC consistently and show the timezone; clamp a cycle start beyond a month's last day to that month's final day. Changing allowance settings must not rewrite raw counters.
- [x] Display used/remaining allowance, period boundaries and a simple inbound/outbound chart with an accessible numeric/table equivalent. Treat allowance as monitoring only; do not introduce automatic transfer blocking, paid-provider integration or notification delivery in this iteration.
- [x] Test exact measured bodies, chunk/retry/partial/error cases, concurrent updates, restart persistence, midnight/cycle boundaries including short months, absent history and deletion/cleanup independence.

## 7. Admin Overview and role-specific web shell

The dashboard uses non-secret operational metadata. Traffic and file-delivery counters describe different events: bytes can be transmitted repeatedly, while a delivery confirmation means the existing client-reported save acknowledgement was accepted.

- [x] Make Overview the administrator's default landing page; retain Send as the regular user's default. Enforce role-aware routing on direct URLs, reload, login/logout and session changes rather than only hiding navigation buttons.
- [x] Build the four-destination admin shell: Overview, Users, Traffic and Server settings. Keep account/password and own-session management accessible without presenting mobile pairing as an administrator feature.
- [x] Show an initial Create your first user action when there is no enabled regular account, with a brief explanation of administration-only accounts. Do not silently create a regular account or reuse the admin password.
- [x] Add cards for enabled regular users, active transfers, active receive links, stored encrypted bytes, files uploaded and files with confirmed delivery. Provide clear labels/tooltips so a server-received upload and a recipient-confirmed delivery are not confused.
- [x] Define file counts explicitly: count files once when their parent transfer finalizes; separate standalone sends from files uploaded into receive links where useful. Count confirmed files once on the first valid completed-transfer receipt, without inflating counts on repeated receipt retries.
- [x] Persist historical event aggregates independently of expiring/deleted resources. Keep current gauges separate from period totals and state when historical counting began; do not imply unavailable past activity was recorded.
- [x] Include a compact current-cycle traffic summary linked to Traffic, plus user/server-management shortcuts. Keep detailed charts and allowance settings on Traffic rather than crowding Overview.
- [x] Provide an operational resource view/revocation path from Overview using server-known type, creation/expiry, owner, count/size and status. Keep IDs in details and never claim admins can inspect decrypted names or content. Existing revocation confirmation and authority checks remain in force.
- [x] Handle loading, empty, unavailable and stale metric states, both appearances, small screens, keyboard navigation and screen-reader labels. Normal users must not receive admin statistics or resources from these endpoints.

## Delivery order and validation

- [x] Implement the History/scanner/mobile-navigation improvements as independent UX work, preserving current transfer and account behavior.
- [x] Implement auth model/migrations, server guards and password-state contracts, then their web/shared/Android/iOS flows. Ship compatible clients with the enforcement change; document older-client errors and existing-admin-resource handling.
- [x] Implement traffic and event-counter definitions/persistence before wiring dashboard cards and charts. Build the administration-only shell against those APIs and complete the first-user setup journey.
- [x] Add focused backend authorization tests for anonymous, unrestricted regular, restricted regular and admin sessions across both cookies and bearer tokens. Include old sessions/pairings, direct calls, public capabilities and admin revocation without plaintext access.
- [x] Run affected Go tests/race checks, shared and Android tests/build, web type-check/build and focused browser checks. Verify the initial-password journey through the actual API and role-specific landing/navigation behavior; mock-only UI checks are insufficient for authorization.
- [ ] Exercise Android camera selection/recovery and password flows on an emulator where available. Build/test the iOS app and share extension on macOS using `ios/README.md`; run available source/configuration checks here and explicitly retain unrun Xcode/device checks.
- [ ] Verify actual camera scanning and small-screen/enlarged-text accessibility on physical Android/iOS devices when available. Record unavailable optical, screen-reader and native checks separately from implemented behavior.
- [ ] Update user/admin documentation and this plan with evidence, format changed files, remove task-owned temporary fixtures/caches, and commit implementation together with verified completion checkboxes. Do not mark external checks passed without running them.
- [x] Follow the session's deployment authorization when implementation is ready. Preserve configured origins, ports, credentials and data; verify migration, role landing pages, public links and persisted counters after deployment.

## Completion criteria

- [x] Users can distinguish and locally rename transfer/receive History entries without exposing names to the server or another account.
- [x] Both mobile platforms scan using automatic camera selection, no switcher and a recoverable preview lifecycle; browser webcam selection remains usable.
- [x] Narrow-screen navigation has consistent icons above readable single-line labels and no scrambled wrapping.
- [x] Admin sessions are restricted to administration on the server and in clients; regular users retain immediate transfer access and public links retain their independent capabilities.
- [x] New/reset non-admin accounts cannot perform authenticated work until replacing their temporary password; confirmation, administrator exemptions and session invalidation work across applicable clients.
- [x] Admin traffic and activity metrics have defined units/events, persistent aggregates, honest measurement coverage and no access to encrypted content.
- [x] Applicable verification passes; any unavailable native/device validation is listed explicitly rather than hidden behind completed implementation tasks.

## Verification notes

Backend authorization is enforced for both cookies and bearer tokens, including
sessions issued before the role/password policy changes. Real API tests cover
public capability access with ambient restricted/admin cookies, password reset
and replacement races, pairing restrictions, restart migration, and administrator
exemption. Full backend race checks pass.

Traffic is counted at manifest/file request and response IO boundaries. Focused
tests cover actual bytes, rejected/unread bodies, partial IO failures, repeats,
one-second/1 MiB flushes, degraded accounting, concurrent persistence, overflow,
UTC boundaries, short billing months, and idempotent file events surviving
resource deletion. The storage gauge uses actual tracked blob sizes plus
encrypted manifests. Operational resource sizes describe declared encrypted file
bytes, including pending reservations.

Shared authentication tests pass (103 cases). Android tests and APK builds pass;
emulator checks have exercised temporary-password validation/re-login,
administration-only account rejection, local rename/cancel/clear/persistence, and
camera permission/preview/release. The final same-account reset → temporary login → password replacement → sign-in
sequence preserves the selected file and does not create a transfer automatically;
switching accounts clears the selection. Android now passes 71 unit tests.

Web checks include real API role/password journeys, local naming, responsive
navigation in both appearances, operational revocation, allowance persistence,
degraded/stale states, and an encrypted 101 MiB transfer saved with matching
plaintext bytes. Actual API regressions now verify both reset-with-selected-files recovery and a
367-day chart at 320px and desktop widths, with its full numeric table accessible
by keyboard. Both targeted fixes passed the integrated review.

The camera-switch freeze was traced to replacing the remembered Android
`BarcodeView` while the factory-only `AndroidView` still owned the old view.
Automatic selection removes the switching path, and explicit retry keys creation
of the attached view.

iOS code, XCTest cases and share-extension changes are included. SwiftFormat,
Ruff and `python3 ios/scripts/check_sources.py` pass. These are not a Swift build.
The macOS build/XCTest commands and remaining device checks are recorded in
[ios/README.md](../../ios/README.md). Physical optical scanning, VoiceOver/TalkBack
and native iOS runtime validation have not been claimed as passed.

### Delivery evidence

The backend and web Docker images were rebuilt and deployed to the existing
`psst-local` service at `http://192.168.178.29`. The original LAN/loopback
port bindings, credentials, origins and volume mounts were preserved. Deployment
checks confirmed the existing administrator session and active resources survived,
admin transfer/receive creation is rejected, anonymous administration metrics are
denied, and existing public transfer/receive metadata still resolves. A backend
restart preserved the recording-start timestamp, traffic preferences and
monotonic totals. No user data or accounts were reset.

The final Android APK is
`android/app/build/outputs/apk/debug/app-debug.apk`. iOS source and tests are
included in the same delivery; native validation still requires the commands
and devices listed in `ios/README.md`.

The review's two findings were same-account selection loss through temporary
password replacement, and fixed chart gaps overflowing long traffic ranges.
Both were repaired in one batch and accepted by the integrated post-fix review.
No further review rounds or unresolved implementation findings remain.

Android emulator validation additionally passed background release, foreground and
rotation recovery, front-only selection, no-camera alternatives, startup rejection
of a stored administrator session, and a real Room v5→v6 history migration.
Initialization-error retry is implemented and source-reviewed, but a camera
initialization failure was not successfully fault-injected; verify that specific
hardware error/retry path with the remaining device checks. Disposable emulator,
API/web services and fixtures were stopped and cleaned after verification.
