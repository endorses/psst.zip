# Mobile appearance settings

Add a persistent System / Light / Dark appearance choice to Android and iOS settings. System remains the default. The choice applies to the entire app, including authentication and the iOS share extension, independently of server/account settings. Remove the duplicate History toolbar shortcut from Home while preserving its main History action.

- [x] Add Android appearance preference, reactive theme selection and accessible settings control.
- [x] Add equivalent iOS preference/control and share-extension appearance using shared preferences.
- [x] Remove redundant Home history toolbar shortcuts on both platforms where present.
- [x] Verify Android build and appearance changes, persistence, system fallback and Home navigation.
- [x] Format and run available iOS source checks; document pending macOS build/device validation.
- [x] Update user documentation, review the changes, clean task-owned temporary artifacts and commit.

## Validation

Android `:app:assembleDebug :app:testDebugUnitTest` passed; all 39 existing unit tests passed. The final formatted debug APK was rebuilt and installed in the Android API 36.1 emulator. Verified System as the initial default, immediate Light/Dark changes, Dark persistence after force-stop/relaunch, explicit Light while Android uses dark mode, and live System responses to both OS appearances. Status/navigation bar icons maintain appropriate contrast. The selector and all three options remain visible and usable at 150% font scale. Home has only Settings in its toolbar; its History card still navigates correctly.

Kotlin was formatted with ktfmt 0.54 and Swift with SwiftFormat 0.62.1. Markdown was formatted with Prettier. iOS source/configuration checks and `git diff --check` passed. Reviewed the shared App Group preference and appearance modifier on the app, Settings presentation and share extension. iOS has no duplicate Home history toolbar action.

- [ ] On macOS, build the iOS app and share extension and verify immediate switching, relaunch/sign-out persistence, System behavior, and extension appearance using the commands/checklist in `ios/README.md`. These native checks were not run on Linux.

Task-owned temporary screenshots, UI dumps, helper scripts and formatter downloads were removed; the test emulator was stopped. The updated Android APK is at `android/app/build/outputs/apk/debug/app-debug.apk`.
