# psst.zip identity and history retcon

## Objective

Apply psst.zip identities throughout the existing development history, as requested
after GitHub publication. Replace legacy project/package names across Android,
shared Kotlin, iOS including the share extension, backend, web, build tooling and
documentation. Keep authors, dates, topology, AGPL licensing, third-party names,
operator URLs, cryptographic formats and ciphertext fixtures intact.

This explicitly supersedes earlier branding plans' instruction to preserve the
old project identifiers. Use `zip.psst.android`, `zip.psst.shared`, `zip.psst.ios`,
`zip.psst.ios.share-extension`, `group.zip.psst.ios`, and the `Psst` native targets.
Use the real repository path `github.com/endorses/psst.zip/backend` for Go imports.

## Implementation and verification

- [x] Back up all current refs and preserve the uncommitted App Store research.
- [x] Audit coupled package, application, storage, protocol and build identities.
- [x] Rewrite historical paths, text and project names without changing binary assets or legitimate security terms such as spoofing.
- [x] Rename Android/shared packages, source directories and dependency coordinates together.
- [x] Rename iOS targets, directories, imports, storyboard modules, entitlements and CI references together.
- [x] Align backend/web/shared server identity checks, pairing markers, cookies and test fixtures.
- [x] Update current guidance, localization inventories and release research; leave the research uncommitted.
- [x] Make existing production database and volume paths explicitly configurable before the next deployment.
- [x] Remap exact secret-scanner fixture fingerprints to the rewritten history and run repository checks.
- [x] Verify commit metadata, parent topology, asset bytes, absence of legacy identities and rewritten-ref integrity.
- [x] Format changed files and run available backend/web/Android/shared/iOS checks.
- [ ] Validate the renamed native app, share extension and XCTest on a macOS runner.
- [x] Commit verified implementation and plan changes, excluding the App Store research.
- [ ] Apply the verified rewrite to the working repository and publish with an explicit lease against the previously published head.

## Existing installations and production storage

A history retcon does not migrate installed apps, browser storage or the running
VPS. New mobile IDs have separate application/storage namespaces. Old sessions and
pairing QR codes need replacement after the coordinated backend/web/mobile update.
Do not update or delete existing production resources as part of this local work.

Before deploying the renamed Compose defaults, stop the services and take the
documented cold backup. Record the currently mounted backend volume and database
path, and set explicit overrides so the new Compose setup reuses them. Verify the
same administrator, resources and encrypted files after deployment. Changing a
logical volume label or filename alone must never create an unnoticed empty
production database.

## History handoff

The original main head is `a009a9a671ff770c9e99411ce7329c9a44e4b0cb`; the last
published head is `c6b322240edc5f62b416610f5316a8761765870c`. Preserve a private
pre-rewrite bundle, commit mapping and verified rewritten bundle outside tracked
files. Existing GitHub CI run links are evidence for the pre-rewrite source only;
the new head needs its own CI validation. Do not merge the old lineage back into
the rewritten branch or publish backup refs.

The session currently permits working-file edits but has read-only access to the
main repository's Git metadata. Prepare the rewrite in a separate writable
checkout, then document any application/publication steps that remain pending.

## Verified preparation (2026-10-06)

The rewritten checkout transforms all 95 original commits. The audit covered
2,887 file versions and 52 unchanged binary blobs, with matching authors,
committers, timestamps, file modes and parent topology. Additional fixes and
current deployment guidance are committed separately on that rewritten lineage.
The prepared checkout and bundles live under ignored `.retcon-private/` storage;
the repository guard now rejects those artifacts even if force-added.

The unrelated localization regression was traced to Node `spawnSync` returning
`EPERM` under sandbox process restrictions, leaving captured diagnostics empty.
The checker now exports its validation function and keeps a thin CLI wrapper.
Tests call the same function directly, preserving duplicate-key, plural-shape,
placeholder, translation and visible-source checks. CLI success/failure exit
codes, diagnostic output and inventory output were separately verified.

Verification passed:

- [x] Backend build, nine socket-free backend package suites and 18 selected API tests.
- [x] All 104 socket-free web tests, including localization regressions, in 0.54 seconds with in-process Node test isolation.
- [x] Web type check with zero errors/warnings, localization catalog validation and production build.
- [x] Android localization/package consistency and iOS source/catalog/entitlement/storyboard consistency checks.
- [x] Compose configuration resolution with both new defaults and explicit existing-production database/volume overrides.
- [x] All 17 repository guard regressions, including rejection of force-added private artifacts from both index and history.
- [x] Direct Gitleaks scan of all rewritten commits using the remapped policy, with zero leaks.

Remaining checks require capabilities unavailable in this session:

- [ ] Network-dependent backend/API restore, transfer, browser and administrator-lifecycle tests; localhost binding is restricted.
- [ ] Android/shared compilation and unit tests; Gradle's local process coordination requires socket access.
- [ ] Portable Swift Docker tests; Docker socket access is restricted.
- [ ] Native macOS app, embedded share extension, KMP bridge, signing and XCTest validation; Swift/Xcode are unavailable locally.

Go formatting was unchanged by import renaming; post-rewrite JavaScript/TypeScript,
Markdown, YAML and Python changes are formatted. Native lexical renames preserve
existing source formatting; native formatter/build verification remains pending
with the macOS checks. Earlier successful CI links describe the pre-retcon source
and are not evidence that the renamed head passed CI.

Research documents remain outside the implementation commit. The live VPS has
not been changed. Preserve its existing storage through the documented overrides
before the next coordinated deployment.
