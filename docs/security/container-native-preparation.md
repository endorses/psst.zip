# Native release preparation

Native preparation retains the original runtime sources and checks the actual final backend/web pair on each supported Linux architecture. It does not publish images or authorize distribution. The protected publication approval and final release provenance remain separate.

Build the maintained development tool and collect the common originals once:

```sh
release_tool=$(sh tools/release/build.sh)
"$release_tool" --root . sources common collect \
  --repository endorses/psst.zip --version "$VERSION" --commit "$SOURCE_COMMIT" \
  --output "$NEW_COMMON_DIRECTORY"
```

This collection retains full pinned upstream archives, including preferred build inputs, original licenses and every integrity-bound npm archive in the exact lock. Development and optional platform packages remain included. npm originals are shared by both native jobs; the exact-lock npm audit is also measured once.

Collect the native Go inputs using the immutable official builder selected for that release:

```sh
"$release_tool" --root . sources modules collect \
  --repository endorses/psst.zip --version "$VERSION" --commit "$SOURCE_COMMIT" \
  --platform linux/amd64 --go-image "$RESOLVED_GO_INDEX" \
  --output "$NEW_MODULE_DIRECTORY"
```

The collector retains original module ZIP, module and version files plus the selected native graph and authenticated public Go checksum inputs. It independently verifies ZIP and module H1 sums. Committed sums remain unchanged; additional authenticated sums are retained separately.

Prepare the final native pair from genuine build metadata and its original Docker save:

```sh
python3 tools/prepare_native_release.py \
  --repository endorses/psst.zip --version "$VERSION" --commit "$SOURCE_COMMIT" \
  --platform linux/amd64 \
  --build-record "$BUILD_RECORD" --original-archive "$ORIGINAL_SAVE" \
  --helper-config "$SOURCE_HELPER_CONFIG" --cosign "$PINNED_COSIGN" \
  --web-builder-config "$WEB_BUILDER_CONFIG" --source-root "$SOURCE_CHECKOUT" \
  --common-collection "$NEW_COMMON_DIRECTORY" \
  --output "$NEW_OUTPUT_DIRECTORY"
```

Build the unprivileged source helper from `tools/runtime-sources.Dockerfile` using the shared resolved Alpine index. Pass its actual configuration digest. The pinned legacy Cosign identity lives in `tools/release/internal/source/caddy.go`; it is required for the upstream Caddy signature format. The source collector verifies the executable's exact version, commit, platform and hash before using it. Native host and Docker daemon architecture must match; ARM64 uses a native ARM64 runner. The local `default` Buildx builder must use the Docker driver.

The Go source collector reads the original paired save once. It retains every relevant lower-layer APK origin, the exact Aports recipe and helpers, complete original source packages and legal texts. It binds the official Caddy executable to its signed buildable artifact, binary checksums and pinned image recipe. It retains a full Go source tree for each compiler actually used; the backend and Caddy can have different compiler versions.

Each source offering contains `source-inventory.json`, with identical bytes beside the archive. The final release manifest binds the archive digest. Runtime overlays contain the original notices and a reference to this exact offering. There is no recursive archive self-digest or separately signed collection wrapper.

Native preparation overlays notices, runs the application smoke checks, exports the exact tested configuration/layer bytes and captures browser builder observations. The final runtime and browser replay shares one verification of the retained sources and final OCI pair. The completed smoke observation and externally bound runtime source digest must match before the raw runtime replay report can be retained.

Browser capture runs the exact committed selective Node script in a native, unprivileged container without network access, host mounts or a Docker socket. Replay checks Git inputs, npm original member hashes, reviewed generator associations and actual final static bytes. Unknown generated origins remain unresolved. These observations do not claim that upstream generators were rerun or that complete browser source reproduction occurred.

The output retains the original build record/save, canonical runtime source offering, final save, two OCI archives, actual smoke observation, native measurement, raw runtime replay, browser capture and browser verification. `native-artifacts.json` is written only after all checks succeed. Interrupted output is diagnostic evidence; it cannot serve as a completed descriptor. Cleanup removes owned temporary aliases and directories without pruning shared Docker caches.

Post-matrix assembly authenticates the native observations and source inventories, independently replays retained inputs and binds all final image/source/deployment assets. The installed deployment manifest and bundle remain version 1. Go source-tooling validation and the limits of the local retained-original check are recorded in [container source tooling verification](../testing/container-source-tooling.md). Historical successful candidates do not establish validation of the current implementation; both native architectures and current scan policy remain required.
