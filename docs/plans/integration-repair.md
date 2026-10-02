# Integration repair

Repair the concrete interoperability, upload validation, and build setup problems identified during project exploration. Preserve existing local Android/shared edits. Native release certification and a new streaming encryption protocol are outside this repair; document and enforce practical limits where needed.

- [x] Repair web API and manifest contracts, slot uploads, and TypeScript errors.
- [x] Harden backend tus URLs, upload lengths, ownership, expiry, and completed-transfer mutation checks.
- [x] Make slot completion notifications and multi-file download limits usable.
- [x] Repair Android/shared receive contracts and filename privacy; run available native checks.
- [x] Upgrade Gradle to 9.1 and align Kotlin/Android/KSP/SKIE plugins for the installed Java 25 runtime; assemble Android and test the shared module.
- [x] Repair iOS receive/serialization and provide reproducible project setup.
- [x] Add and run meaningful backend and web interoperability regression tests.
- [x] Document memory limits and correct unsupported completion claims in the original plan.
- [x] Format and review the final diff.
- [x] Commit the repair and updated plans.

## Verification

- Android debug APK assembled on Java 25 with Gradle 9.1.0; 51 shared tests passed.
- Backend `go test -race ./...` and `go vet ./...` passed.
- Web type check, four encrypted API/crypto tests, two Chromium browser tests, and production build passed.
- Kotlin and Swift formatted; iOS YAML, plists, entitlements, and storyboard syntax checked.
- Native iOS compilation/signing/device tests remain unverified on Linux. The original plan keeps these open, along with streaming and native cross-platform tests.
- Existing unrelated Android branding edits remain outside the repair commit. Existing build-configuration and Downloads-directory fixes are retained as part of the working native integration.
