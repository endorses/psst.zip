# iOS development

The app and share extension are generated from `project.yml` using XcodeGen.
Use macOS with Xcode, XcodeGen, and a JDK supported by Gradle 9.1 (Java 25 is supported).
Set `JAVA_HOME` for command-line builds and ensure Xcode can find Java. The shared
Gradle build also configures Android, so install the Android SDK and set `ANDROID_HOME`.

From this directory:

```sh
xcodegen generate
xcodebuild -project Psst.xcodeproj -scheme Psst \
  -sdk iphonesimulator -configuration Debug CODE_SIGNING_ALLOWED=NO build
```

The pre-build script invokes the repository wrapper to build and link the shared
Kotlin framework. For a device build, configure your signing team for both targets
and register the `group.zip.psst.ios` App Group (or change it consistently in
`Shared/AppConstants.swift` and both entitlements files).

The generated project is ignored; edit `project.yml` and regenerate it. This setup
follows [Kotlin direct integration](https://kotlinlang.org/docs/multiplatform/multiplatform-direct-integration.html)
and the [XcodeGen project specification](https://github.com/yonaskolb/XcodeGen/blob/master/Docs/ProjectSpec.md).

The receive screen polls for completed child transfers every three seconds. The
main app buffers at most 25 MiB of plaintext per file; the share extension limits
files to 10 MiB. Files are encrypted and uploaded one at a time. These limits are
not a streaming implementation or a measured guarantee of extension memory use.

Native compilation, signing, simulator/device flows, and memory profiling still
require verification on macOS; they cannot be certified by the Linux checks.
