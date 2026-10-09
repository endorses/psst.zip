# Android lint bidi traversal workaround

The release build uses stable AGP 9.4.1 and its bundled lint 32.4.1. During local
release validation, `lintAnalyzeRelease` spent approximately nine CPU minutes in
`KotlinUFile.getAllCommentsInFile` through `PsiWalkingState` and
`BidirectionalTextDetector.visitFile`. The optimized APK assembly had already
finished. Thread samples establish the stalled comment traversal, but do not
identify its Kotlin file or prove a PSI cycle.

The bundled lint compiler reports `2.4.0-dev-2631-for-lint`, independently of the
project's Kotlin 2.4.20 compiler and SKIE. This is not evidence of a SKIE defect.
No exact public issue or newer stable lint patch was found during the investigation.
The [upstream detector](https://android.googlesource.com/platform/tools/base/+/refs/heads/studio-main/lint/libs/lint-checks/src/main/java/com/android/tools/lint/checks/BidirectionalTextDetector.kt)
requests all comments for each Kotlin UAST file before checking bidi segments.

Only the `BidiSpoofing` lint rule is replaced temporarily. All other release lint
rules remain enabled. Its replacement is
[`tools/check_source_bidi.py`](../../tools/check_source_bidi.py), a standard-library
source check that does not initialize Kotlin PSI or a compiler. Run it before
release lint and in repository security:

```sh
python3 tools/check_source_bidi.py
python3 tools/check_source_bidi.py \
  --generated-directory android/app/build/generated/ksp/release/kotlin
python3 -m unittest discover -s tools -p 'test_source_bidi.py'
```

The generated-directory command requires KSP to have emitted the selected source
directory. It scans that explicitly selected compilation input, not every copied
artifact beneath `build`. Do not pass a broad build directory.

The check covers tracked Kotlin, Java, Gradle and Swift source, including uppercase
file suffixes. It rejects U+202A–U+202E and U+2066–U+2069 formatting controls, whether
literal or written using Unicode escapes. Java's repeated `u` escape and Swift's
braced escape are included. A textual escape in a comment or an escaped-backslash
string also fails deliberately: this conservative policy is stricter than lint's
unterminated-segment check. Ordinary Unicode text and safe Unicode escapes remain
allowed; English/German localization and non-Latin text do not require these
formatting controls.

Source must be valid UTF-8 and a regular file. Symlinks, path traversal, missing
tracked source, invalid inventories and size/count/depth overruns fail closed.
The default limits are 10,000 inventory/files, 8 MiB per file, 64 MiB combined,
8 MiB Git inventory and 32 nested generated directories. Work is linear in the
selected source bytes, with bounded reads. Diagnostics contain only a quoted
relative filename, line number and Unicode codepoint, or a fixed failure reason.
The check neither reads signing material nor prints source contents, credentials,
keys or private values. Untracked source is excluded from the tracked inventory;
release source validation independently rejects untracked mobile source.

Review this workaround when the next stable AGP/lint patch becomes available.
Re-enable `BidiSpoofing` after the same release lint command completes within its
existing execution budget with that patch. Keep the direct source check until the
replacement coverage has been deliberately reviewed. No timeout increase or claim
of a confirmed upstream fix is implied by this workaround.
