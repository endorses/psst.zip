# Test suite performance

## Objective

Make the whole psst.zip test suite substantially more efficient by eliminating
repeated fixture setup, oversized setup workloads and unnecessary real-time
waits. Preserve security, protocol, persistence and mobile compatibility coverage. Keep all new fixes and performance changes local until the outstanding
failures are resolved; no additional push is authorized yet.

## Baseline

GitHub run `37423344109` took about 18 minutes for the backend job. Its race-test
packages took 611 seconds for API, 950 seconds for database and 141 seconds for
reconciliation. The web job was still running after 24 minutes in the supplied
screenshot and ultimately failed after 30 minutes 30 seconds. Local browser
discovery spent 15.3 minutes on 25 passing tests and five failures; three stale
fixture/selector cases alone spent nine minutes waiting for the whole-test timeout.

The backend repeatedly applies the full SQLite schema to empty behavior-test
fixtures. The browser suite also has 48 UI sign-in calls, which compete for the
real account authentication bucket's thirty-second refill. Explicit rate-limit
and login-flow tests must continue exercising those production controls.

## Implementation and verification

- [x] Cache a fully migrated, empty SQLite template once per test binary and
      clone it into independent private fixture files. Checkpoint and close the
      template before copying; retain production open/version checks and fresh
      history generation initialization for each clone.
- [x] Preserve existing-file reopen behavior and never replace a populated
      fixture. Keep explicit legacy migration, restart, restore and permissions
      tests on their original production setup paths.
- [x] Verify fixture isolation, parallel clone behavior, file permissions and
      template lifecycle cleanup. Remove every process-owned temporary template
      and benchmark artifact.
- [x] Convert ordinary behavior-fixture setup in database, API, reconciliation
      and cleanup tests where applicable, without changing production schema,
      retention thresholds or resource limits.
- [x] Reuse genuine worker browser sessions for ordinary signed-in navigation
      tests, with fresh browser contexts and local storage. Refresh revoked or
      invalid sessions through real authentication; keep explicit sign-in,
      password, logout, recovery and authentication lifecycle tests unchanged.
- [x] Set a bounded browser action timeout so obsolete controls fail promptly,
      while retaining explicit longer waits for payload and network operations.
- [x] Repair obsolete nested-panel/scanner interactions and legacy history mocks
      so they exercise their intended contracts. Keep independent history-sync
      tests and real backend integration coverage.
- [x] Review redundant or obsolete tests and remove only cases with a concrete
      explanation of equivalent retained coverage. Do not remove failing tests
      merely to improve runtime.
- [x] Profile slow tests across backend, browser and mobile suites and review
      their fixture, authentication, compilation and wait costs.
- [x] Remove the duplicate native iOS CI build by compiling app, extension and
      XCTest products with `build-for-testing` and executing those same products
      with `test-without-building`. Workflow implementation is complete; native
      execution and timing verification remain pending without macOS/Xcode.
- [x] Bulk-seed large historical retention fixtures while preserving real
      production thresholds, boundary mutations, pruning, counters and security
      assertions. Benchmark affected cases rather than reducing their limits.
- [x] Remove the blanket browser delay using supported higher global/creation
      limits only in the disposable functional-test backend. Keep authentication
      limits unchanged and retain dedicated rate-limit coverage.
- [x] Advance client time for automatic history refresh tests after real server
      mutations, preserving actual requests, refresh scheduling and assertions.
      Keep server-clock authentication and payload tests on their required clocks.
- [x] Compare representative backend race workloads before and after fixture
      changes, then run the complete backend race suite and lint.
- [x] Run the complete browser suite and the opt-in administrator lifecycle;
      report test counts, failures and wall time. Repeat web type checks and
      production build after source changes.
- [x] Finish portable iOS harness repairs and regression checks. Retain Android
      assembly/shared crypto tests and the native iOS app/share-extension/XCTest
      gate. Native Apple compilation remains pending without macOS/Xcode.
- [x] Record verified results and pending checks in both plan files.
- [x] Stage and commit the complete local change set after final verification.
      The user approved expanded execution for localhost tests and local Git
      writes. Hold publication until the user's no-push condition is met.

## Validation limits

Local performance measurements use the available Linux machine and do not prove
GitHub runner timings. Report machine/toolchain differences and use the same
race flags when comparing backend workloads. Native iOS tests cannot run in this
environment; portable Swift harnesses and parsing do not replace an Xcode build,
embedded share-extension check or native XCTest execution.

## Verified mobile and repository checks

All 171 portable Swift tests across 12 harnesses passed, along with source and
localization gates and compiler syntax parsing. The crypto harness uses official
Swift 6.2.4 to resolve a Swift 6.0 runtime linker failure; other harnesses keep
their existing image. Android assembly and 175 shared tests passed, including
four new AES-GCM compatibility tests. Native iOS app, extension and XCTest checks
remain unrun. All 16 repository security regression tests also passed.

## Intermediate profile and audit

Before the second set of fixture optimizations, the complete local Go 1.26.8
race suite passed in 371.634 seconds (6 minutes 12 seconds), with API 157.225,
database 350.531 and reconciliation 41.030 seconds. The slowest database cases
included account history retention (70.89 seconds), global history retention
(36.03), audit retention (40.05) and historical file-list bounds (43.85). Final
verification must run again after all bulk fixture changes.

The browser audit retained distinct navigation, responsive layout, authorization,
authentication and history contracts. Obsolete labels, message expectations and
legacy mocks are repaired rather than deleting their cases. Restoring an empty
incremental feed exposed a stale offline error: Android and iOS already clear
that state after successful refresh, including empty feeds; the web controller
needs the corresponding recovery notification.

The 175 shared mobile test cases total 3.962 seconds on the local JDK. The longest
case is the 2.537-second test proving streaming above 100 MiB; its payload size is
necessary to exercise that boundary. Gradle caching is already configured in CI.
The native iOS workflow now reuses built test products, but native runtime and
build timing remain unmeasured without macOS.

## Optimization measurements and pending validation

The production-sized fixtures retain their thresholds and boundary assertions.
Go 1.26.8 race measurements on the same Linux machine:

| Case                              |  Before |   After |
| --------------------------------- | ------: | ------: |
| Account history retention         | 70.89 s |  1.49 s |
| Global history retention          | 36.03 s | 19.93 s |
| Audit retention buckets           | 40.05 s |  9.58 s |
| Historical file-list bounds       | 43.85 s |  2.67 s |
| Traffic retention and late leases |  6.36 s |  1.70 s |
| Billing-day retention             |  0.78 s |  0.61 s |

The complete Go 1.26.8 race suite passed after all fixture changes:
698 cases (443 top-level tests), zero failures, 216.919 seconds wall time
(3 minutes 37 seconds). With the same toolchain and `-race -count=1 -p 2
-timeout 30m` flags, the earlier full run took 371.634 seconds: a 41.6%
reduction. Database improved from 350.531 to 186.463 seconds (46.8%);
API took 160.848 and reconciliation 45.043 seconds. Lint reported zero
issues and the production backend build passed. Local lint used v2.13.2;
CI pins v2.13.0.

Empty schema migration is cached; fixture databases remain independent and
production reopen behavior stays intact. Large history fixtures seed consistent
rows and counters before exercising real boundary writes with the exact
production triggers restored. Historical file-list setup bypasses only unrelated
allocation admission inside its fixture transaction; derived counters stay
active and real allocation-ceiling tests retain that admission trigger.

The first expanded complete browser run finished in 242.471 seconds: 187
passed, five existing opt-in cases skipped and one intermittent page-loading
failure. Eighty targeted download acknowledgment repeats subsequently passed.
A retained trace from the complete diagnostic run captured numerous local CSS
and JavaScript requests cancelled by Chromium with `ERR_NETWORK_CHANGED`, then
a failed route-module import and SvelteKit's generic 500 screen. There were no
HTTP 500 responses in that trace. This is host network-change interference;
no application error handling, assertions or Chromium security settings were
weakened to hide it. Final browser and administrator lifecycle validation uses
a temporary Linux network namespace with only loopback enabled.

The diagnostic is consistent with Chromium's
[network-change notifier](https://chromium.googlesource.com/chromium/src/+/refs/heads/main/net/base/network_change_notifier.h),
which resets connections after host address or route changes. Namespace
isolation is a local verification measure, not a change to CI or host networking.

All 110 Node integration tests passed after the final source changes in
3.449 seconds. The new mocked polling-clock cases passed, including recovery
code survival (714 ms), held unauthorized identity polling (1.2 seconds) and
legacy history refresh (1.1 seconds). Actual history recovery passed in 775 ms.
No tests or security assertions were removed.

The complete isolated browser suite passed: 188 tests, zero failures, five
existing opt-in skips, 239.356 seconds wall time (4 minutes). This run retained
failure traces; timing includes frontend/backend startup and the complete browser
suite. The skipped cases are three administrator lifecycle tests run separately,
one real 101-submission capacity opt-in and one non-loopback LAN context opt-in.
The separate real administrator lifecycle passed all three tests in 34.131
seconds wall time. It retains real server-clock authentication limits, second
factor enrollment, recovery-code rotation, session revocation and explicit
recent-proof refresh. The final web type check reported zero errors and
warnings, and the production static build passed.

The earlier sandbox listener and read-only Git restrictions were resolved by
the user's approval for expanded test execution and local commits. Native iOS
app, extension and XCTest validation remains pending without macOS/Xcode;
no further push is authorized.

A default-detector Gitleaks scan of all changed additions and new files reported
zero findings. All 16 repository security regression tests passed. The staged
security and formatting checks and installed commit hook passed. Complete-history
scanning is also required before publication. The verified changes are committed
locally; no push was made. Task-owned temporary logs, traces and measurement
scripts were removed after recording results; fixture databases and build
artifacts are not committed.

## GitHub verification and follow-up CI faults

Run [`37433228153`](https://github.com/endorses/psst.zip/actions/runs/37433228153)
on `3a05f71` confirms the performance changes on GitHub: backend completed in
8 minutes 43 seconds (its race step took 6 minutes 10 seconds), versus the
previous 18-minute backend job. Its initial job setup consumed about two minutes.
The complete main browser suite passed in a 5-minute 41-second step; type checks
and Node integration tests passed. Android/shared completed in 2 minutes
39 seconds, and repository security passed.

The web job's failure was the separate administrator lifecycle startup: its
state marker was placed under `runner.temp`, whereas Node's `os.tmpdir()` used
its default root. The fix sets `TMPDIR` to `runner.temp` for that step and keeps
all disposable-database safety restrictions. Native iOS failed in `ibtool`
because the share-extension storyboard declared `targetRuntime="AppleSDK"`.
The native build and XCTest suite remain unverified until a successful Xcode run.

- [x] Identify both failed CI steps and distinguish them from the passing suites.
- [x] Align the administrator fixture's Node temporary root with the CI state path.
- [x] Verify the administrator lifecycle with the repaired workflow environment.
      All three tests passed in 33.748 seconds; the temporary marker/database
      cleanup was verified. The safety checks and authentication limits remain.
- [x] Repair the iOS storyboard and check its configuration on Linux. XML parsing
      and iOS source gates passed; the source gate now checks the target runtime
      and initial share-controller wiring.
- [ ] Verify the native app, embedded extension and XCTest suite on macOS/Xcode.
- [x] Commit these follow-up fixes locally; installed security/format hooks
      passed. Task-owned temporary logs and validation files were removed.
      Keep publication under user control; no new push was made.

## Native shared-exception bridge

Run [`37435387491`](https://github.com/endorses/psst.zip/actions/runs/37435387491)
on `eb635d3` passed all Linux jobs, including the previously failing real
administrator lifecycle. The repaired iOS storyboard compiled and Kotlin
framework generation completed. Native Swift compilation then exposed an
unchecked `Any` value from `NSError.kotlinException` being passed to the shared
classifier's required `KotlinThrowable` parameter.

The existing portable localization harness compiled with `canImport(Shared)`
false, excluding that bridge. Its improvement must exercise the actual presenter
against the observed bridge types while clearly separating those boundary types
from generated Kotlin/Native and native Xcode verification.

- [x] Diagnose the precise native error and confirm all other jobs passed.
- [x] Safely cast the Kotlin exception and retain unknown-error fallback/privacy.
- [x] Verify the actual presenter with the shared-bridge branch compiled. All
      18 localization/presenter tests passed, including four typed bridge tests.
- [x] Run the source/localization gates and format the repair. Both gates passed,
      along with Swift/Python/Markdown formatting and whitespace checks.
- [ ] Verify native app, embedded extension and XCTest on macOS/Xcode.
- [x] Commit verified bridge changes locally. Installed security/format hooks
      passed. Remove task-owned temporary artifacts and keep the commit unpushed.
