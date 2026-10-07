# Container dependency vulnerability review

Reviewed on 2026-10-07. The backend source scan found no known vulnerable symbols
reachable from the analyzed code and no vulnerabilities in imported packages.
It reported 21 advisories at module level; their applicability is documented
below. The web dependency audit passed after the source-map-js security patch.
Historical AMD64 image scans found fixable runtime findings. Fresh patched
AMD64 scans below resolve those Alpine findings; remaining binary module
advisories have bounded import-graph dispositions. ARM64 scans and full release
workflow evidence remain pending. This record does not approve a container release.

## Backend source analysis

| Evidence                | Observed value                                                                                                 |
| ----------------------- | -------------------------------------------------------------------------------------------------------------- |
| Source baseline         | `c7e4bacb1cabb1f8d1b0c81b2bc82e8e4122372d`; backend dependency files unchanged during the scan                 |
| Started / completed     | `2026-10-07T05:55:45Z` / `2026-10-07T05:56:12Z`                                                                |
| Scanner                 | Official `golang.org/x/vuln/cmd/govulncheck@v1.8.0`, installed with Go 1.26.8                                  |
| Scanner module checksum | `h1:clG4qBU6zH5VKjti8n5j8BBuYzoSha392xXMkXS351U=`                                                              |
| Analysis toolchain      | `go version go1.26.8 linux/amd64`, matching the release backend's declared Go version                          |
| Database                | `https://vuln.go.dev`, last modified `2026-10-01T20:24:15Z`                                                    |
| Scope                   | Source mode, symbol level, `./...` in `backend/`, default Linux/AMD64 build configuration; test files excluded |
| Coverage                | 11 root packages, 14 modules including the application module, plus the Go 1.26.8 standard library             |
| Result                  | 0 symbol findings; 0 imported-package findings; 21 required-module findings                                    |
| Execution status        | JSON scan exited 0 with empty stderr; conversion of that same report to verbose text exited 0                  |
| `backend/go.mod` SHA256 | `8d847a2ea9341c10954a89ce9c4bbbfd338c495936bf53d18e6dc315b64188e2`                                             |
| `backend/go.sum` SHA256 | `7210ec1c6220409d27a07d94a0e505f18b4a796dab9ba829d737839052fd9332`                                             |

Go source analysis uses the Go executable on `PATH`, so this review used the
actual release Go 1.26.8 executable rather than the host's Go 1.27.1. JSON output
returns success even when vulnerabilities are reported. The text conversion was
therefore checked as well; the JSON exit status alone was not treated as approval.
Scanner errors, incomplete output, and failed downloads must fail the review.
See the official [scanner documentation](https://pkg.go.dev/golang.org/x/vuln@v1.8.0/cmd/govulncheck).

The declared direct dependencies at this baseline are:

| Module                     | Version   |
| -------------------------- | --------- |
| `github.com/go-chi/chi/v5` | `v5.3.0`  |
| `github.com/google/uuid`   | `v1.6.0`  |
| `github.com/pquerna/otp`   | `v1.5.0`  |
| `golang.org/x/crypto`      | `v0.36.0` |
| `golang.org/x/sys`         | `v0.32.0` |
| `modernc.org/sqlite`       | `v1.37.0` |

## Module findings and applicability

Every finding below had a module-only trace: no vulnerable package or symbol was
reported as imported or called. Independently, `go list -deps ./cmd/server` with
Go 1.26.8 listed only `golang.org/x/crypto/bcrypt`,
`golang.org/x/crypto/blowfish`, and `golang.org/x/sys/unix` from these two modules.
The release command's 237-package dependency graph contained no SSH, SSH agent,
knownhosts, OpenPGP, or Windows package.

The decision for the 20 `golang.org/x/crypto@v0.36.0` findings is **not applicable
to this analyzed backend build because the affected packages are absent**. The
`golang.org/x/sys@v0.32.0` finding is **not applicable to this Linux build because
the affected Windows package is absent**. These are bounded source-build
assessments, not permanent suppressions or claims about other applications using
the same modules. The host deployment uses external OpenSSH executables; their
security is outside this Go import-graph assessment.

| Advisory                                             | Affected package within the module     | First fixed module version reported       |
| ---------------------------------------------------- | -------------------------------------- | ----------------------------------------- |
| [GO-2025-4116](https://pkg.go.dev/vuln/GO-2025-4116) | `x/crypto/ssh/agent`                   | `v0.43.0`                                 |
| [GO-2025-4134](https://pkg.go.dev/vuln/GO-2025-4134) | `x/crypto/ssh`                         | `v0.45.0`                                 |
| [GO-2025-4135](https://pkg.go.dev/vuln/GO-2025-4135) | `x/crypto/ssh/agent`                   | `v0.45.0`                                 |
| [GO-2026-5005](https://pkg.go.dev/vuln/GO-2026-5005) | `x/crypto/ssh/agent`                   | `v0.52.0`                                 |
| [GO-2026-5006](https://pkg.go.dev/vuln/GO-2026-5006) | `x/crypto/ssh/agent`                   | `v0.52.0`                                 |
| [GO-2026-5013](https://pkg.go.dev/vuln/GO-2026-5013) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5014](https://pkg.go.dev/vuln/GO-2026-5014) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5015](https://pkg.go.dev/vuln/GO-2026-5015) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5016](https://pkg.go.dev/vuln/GO-2026-5016) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5017](https://pkg.go.dev/vuln/GO-2026-5017) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5018](https://pkg.go.dev/vuln/GO-2026-5018) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5019](https://pkg.go.dev/vuln/GO-2026-5019) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5020](https://pkg.go.dev/vuln/GO-2026-5020) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5021](https://pkg.go.dev/vuln/GO-2026-5021) | `x/crypto/ssh/knownhosts`              | `v0.52.0`                                 |
| [GO-2026-5023](https://pkg.go.dev/vuln/GO-2026-5023) | `x/crypto/ssh`                         | `v0.52.0`                                 |
| [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024) | `x/sys/windows`                        | `v0.44.0`                                 |
| [GO-2026-5033](https://pkg.go.dev/vuln/GO-2026-5033) | `x/crypto/ssh/agent`                   | `v0.52.0`                                 |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | `x/crypto/openpgp` and its subpackages | No fixed version; package is unmaintained |
| [GO-2026-6303](https://pkg.go.dev/vuln/GO-2026-6303) | `x/crypto/ssh`                         | `v0.55.0`                                 |
| [GO-2026-6354](https://pkg.go.dev/vuln/GO-2026-6354) | `x/crypto/ssh`                         | `v0.56.0`                                 |
| [GO-2026-6355](https://pkg.go.dev/vuln/GO-2026-6355) | `x/crypto/ssh`                         | `v0.56.0`                                 |

No Go dependency versions were changed for this review. Reassess these decisions
when imports, build tags, target platforms, toolchain versions, dependencies, or
the vulnerability database change. Static analysis has limitations around
reflection and unsafe code; a zero finding count covers the scanner's analysis,
not all possible vulnerabilities. See the official
[Go vulnerability management overview](https://go.dev/doc/security/vuln/).

## Web dependency review

The implementation owner separately verified the source-map-js patch on
2026-10-07. The lockfile change updates only `source-map-js` from `1.2.1` to
`1.2.2`, resolving `GHSA-68fv-2mgg-jv7q` / `CVE-2026-93749`. The maintainer's
[v1.2.2 release notes](https://github.com/7rulnik/source-map-js/releases/tag/v1.2.2)
describe the malicious indexed-source-map denial-of-service fix.

| Owner-reported check                          | Result                                            |
| --------------------------------------------- | ------------------------------------------------- |
| `npm audit --json`, npm 12.1.0 / Node 26.10.0 | Exit 0; 0 vulnerabilities across 173 dependencies |
| Web unit tests                                | 114 passed in 1.38 seconds                        |
| Svelte checks                                 | 0 errors and 0 warnings                           |
| Production web build                          | Passed in 6.99 seconds                            |

These checks used the owner's local Node 26.10.0 environment. The release image
declares Node 22.23.3; its CI/build validation is a separate pending check.

## Repeating the backend review

From the repository root, use the pinned scanner and exact release Go toolchain.
The following keeps downloads and caches in a disposable directory and checks
the symbol-level text result. Retain the report and toolchain metadata in the
release's evidence before deleting temporary files when approving an actual
release.

```sh
set -eu
psst_review=$(mktemp -d /tmp/psst-dependency-review.XXXXXX)
trap 'chmod -R u+w "$psst_review"; rm -rf "$psst_review"' EXIT
export GOTOOLCHAIN=go1.26.8
export GOPATH="$psst_review/gopath"
export GOMODCACHE="$psst_review/modules"
export GOCACHE="$psst_review/build-cache"
export GOBIN="$psst_review/bin"
export XDG_CACHE_HOME="$psst_review/cache"
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
go version
"$GOBIN/govulncheck" -version
cd backend
"$GOBIN/govulncheck" -json ./... > "$psst_review/backend.json"
"$GOBIN/govulncheck" -mode convert -show verbose \
  < "$psst_review/backend.json" > "$psst_review/backend.txt"
cat "$psst_review/backend.txt"
go list -deps ./cmd/server
sha256sum go.mod go.sum
```

## Remaining release evidence

- [ ] Scan the exact final backend and web container digests for both advertised
      architectures, including installed Alpine packages, Caddy and its embedded
      Go/module dependencies. Record scanner/database versions, digests,
      findings, fixes and applicability. A source scan does not cover this gate.
- [ ] Run the web checks and production build with the declared release Node
      22.23.3 toolchain and verify the resulting image pair.
- [ ] Repeat the Go review for the final release source/toolchain and record any
      ARM64 build-configuration differences; this execution analyzed Linux/AMD64.
- [ ] Review native Android/KMP and iOS dependency vulnerabilities against the
      resolved platform dependency inventories. No native vulnerability scan was
      performed in this review.
- [ ] Retain the final release's raw scanner outputs and resolve scanner failures
      or applicable findings before declaring that release deployment-ready.

## Private final AMD64 image scan

On 2026-10-07, Trivy 0.75.0 scanned the final runtime-source overlays built from
`dffeac44c0913c6dbf0dee4e4156e918f2f4d9c5` with candidate version `v0.0.0`.
These are private local verification images, not published release assets.
The binary archive and checksum list were independently verified using exact
upstream Sigstore workflow identity, tag and commit, mandatory SCT/Rekor checks,
and cosign 2.6.5. Trivy source commit:
`591e9799316a602e703f0b484f6c6d7b234ec8f3`; binary archive SHA256:
`c6e65abddb348e25f10549df887045629cf28cc72453cd1c63acb717316b3f3f`.
See [upstream signature verification](https://github.com/aquasecurity/trivy/blob/v0.75.0/docs/getting-started/signature-verification.md).

The schema-2 database was updated at `2026-10-07T07:38:55.515026687Z`, with
SHA256 `5f4b978a55284b1997dc31e9f2fc3f4f1abae80829f51451ade221d5675a69b9`.
Scanning used vulnerability-only JSON with all packages, offline analysis and
no ignore-unfixed, severity filter or suppression. Trivy requires an unpacked
OCI layout: the already validated archive's regular layout/blob files were
copied unchanged into a private directory. Both scans completed successfully;
exit status alone was not treated as vulnerability approval.

| Subject       | Exact tested configuration                                                | Findings                         |
| ------------- | ------------------------------------------------------------------------- | -------------------------------- |
| Backend AMD64 | `sha256:c99c04aaabbde0110b8af423f58ab3e79a52fc73459266821ce7049d1026c1a8` | 21 Alpine, 21 Go module findings |
| Web AMD64     | `sha256:711a7af6698f3a81d09e20f717fe737ee79c2c0b66dfcc383ed1ad737fe12aac` | 1 Alpine, 1 Go module finding    |

Backend `libcrypto3` and `libssl3` 3.3.7-r1 each have ten findings, fixed in
3.3.7-r2: HIGH `CVE-2026-75804`, `CVE-2026-84782`; MEDIUM
`CVE-2026-54872`, `CVE-2026-54875`, `CVE-2026-72897`, `CVE-2026-75805`,
`CVE-2026-75806`, `CVE-2026-77696`, `CVE-2026-84784`; LOW
`CVE-2026-35189`. Both images retain zlib 1.3.2-r0 with MEDIUM
`CVE-2026-85091`, fixed in 1.3.2-r1. These findings require patched runtime
inputs, fresh builds, source correspondence and rescanning before distribution.
They have not been dismissed as unreachable.

The backend binary repeats the 21 module-level advisories addressed by the
bounded source import-graph review above. Caddy reports `GO-2026-5932` for
x/crypto v0.57.0 with unknown severity and no fixed version. The exact AMD64
Caddy source/binary assessment below finds its affected OpenPGP packages absent;
the raw module finding remains retained. Neither module findings nor the successful
scan exit status authorize publication.

Backend report SHA256:
`c1439946820f6990df3c390f1ed824f0f4afde8864531c4dba7817d29e824b80`;
web report SHA256:
`fbfb59b727499d6c40d53ed03ff1391fa30dce0087d21f0e4df819681f71d4cc`.
The full reports and disposable images remain private. ARM64 and patched final
pair scans are still pending.

## Exact Caddy AMD64 package applicability

On 2026-10-07, the independent review analyzed the complete official Caddy
`v2.11.7` buildable artifact, including its original `caddy` main-module wrapper
and vendored dependency source. It analyzed the wrapper executable target `.`;
it did not substitute the library module's `cmd/caddy` target or infer package
usage from `golang.org/x/crypto`'s module version.

[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) applies to all versions and
all symbols in `golang.org/x/crypto/openpgp` and its `armor`, `clearsign`,
`elgamal`, `errors`, `packet` and `s2k` subpackages. There is no known fixed
version. The bounded disposition is **not applicable to the exact reviewed
Linux/AMD64 Caddy executable because all seven affected packages are absent
from its complete matching source import graph**. This is not a dependency fix,
an advisory suppression, or an approval of the runtime image's other findings.

| Evidence                                   | Observed value                                                                                                                                                                                                                              |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Official source/binary revision            | `72dd0fb067f6d7826c7f79907670ba4a713bfe37`, unmodified Caddy `v2.11.7`                                                                                                                                                                      |
| Final scanned web configuration            | `sha256:711a7af6698f3a81d09e20f717fe737ee79c2c0b66dfcc383ed1ad737fe12aac`                                                                                                                                                                   |
| Final web OCI archive SHA256               | `af2367e523cf399c68447b8b9a6146da49dffa6f90ac163ad9cbd7afd93ea1b6`                                                                                                                                                                          |
| Actual final-image Caddy executable SHA256 | `678ade3bfc088749c81a681adc603333ee0bb023b6a6cfe3c0f58bef8ff854e9`                                                                                                                                                                          |
| Signed buildable archive SHA256            | `b430516910839fbaf35c0a9e9df80d1e2e30aa792530293c39f4a97a1b2c9060`                                                                                                                                                                          |
| Retained binary build-info SHA256          | `dd3f5dd44ea4939e3c12c1f2168798bbcd936761704f2d7e7c78fb4d6483a812`                                                                                                                                                                          |
| Wrapper `main.go` SHA256                   | `ac320c3ac47ad8abed7a0d2595639d78f9ff55cf1dba8494a66cd8851c5d076e`                                                                                                                                                                          |
| Wrapper `go.mod` SHA256                    | `18b279010277f797f78d6bd14036989a185ad7d44396a5ccce9207c13a864293`                                                                                                                                                                          |
| Wrapper `go.sum` SHA256                    | `7e80d3ed4bfe892d1e2ffbd83e99f4ba862a4090a94be941aaf63193aa5d2a60`                                                                                                                                                                          |
| Wrapper `vendor/modules.txt` SHA256        | `145310451e5679ed7309cad898dbc066a2c9f46655a9367a0ba672a924911bc2`                                                                                                                                                                          |
| Toolchain and executable build settings    | Go `1.26.8`, `linux/amd64`, `GOAMD64=v1`, `CGO_ENABLED=0`, tags `nobadger,nomysql,nopgx`                                                                                                                                                    |
| Imported packages                          | 970, with no incomplete-package or dependency errors; tests excluded                                                                                                                                                                        |
| Embedded dependency correspondence         | All 147 source-graph dependency module/version pairs exactly equal the binary's 147 embedded pairs; every embedded module checksum matches the wrapper `go.sum` and its module/version is declared in `vendor/modules.txt`; no replacements |
| Affected imported packages                 | 0 of 7; the full retained vendor archive also contains no OpenPGP source files                                                                                                                                                              |
| Sorted import-path list SHA256             | `6120a24d276bdc383cc7d60f87ab785812b313bb38244bf20b915f0702b4a195`                                                                                                                                                                          |
| Full package-graph JSON SHA256             | `beae636272502cf07210d69eba633ea0a22956f6d683cecb2ad6de274af2332a`                                                                                                                                                                          |
| Official advisory JSON SHA256              | `f277b0400996200a7d5034c676661cd8cf18adeab1fdc5d0dc369e456ddf1fad`                                                                                                                                                                          |

The review independently read the final OCI archive's manifest, configuration and
every layer, verified their recorded sizes/digests, and resolved the final
`/usr/bin/caddy` file without executing it. Its bytes match the official release
executable from the retained checksum-bound archive. Independently reading that
executable with the matching Go toolchain reproduced the retained `GoVersion`,
main-module identity, all 147 dependencies and build settings.

The retained Caddy Sigstore receipt records successful buildable-artifact and
checksum-list verification for the exact release workflow, tag and source revision,
with transparency verification required. This review matched the signed artifact's
bytes and receipt bindings; it did not repeat that network signature ceremony.
The wrapper `main.go` was independently compared byte for byte with the
[original entry point at the recorded revision](https://github.com/caddyserver/caddy/blob/72dd0fb067f6d7826c7f79907670ba4a713bfe37/cmd/caddy/main.go).
The dependency/checksum checks used the repository's existing
`tools/collect_caddy_sources.py::verify_modules` contract, followed by an independent
exact comparison of all graph-selected dependency pairs with the binary metadata.
The signed vendored archive authenticates its source bytes; matching embedded
`h1` values to `go.sum` is a metadata correspondence check, not a claim to have
recomputed full original-module zip checksums from reduced vendored directories.

The actual package graph was produced inside the extracted original wrapper with
isolated caches and these settings:

```sh
export GOTOOLCHAIN=go1.26.8
export GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0
export GOWORK=off GOFLAGS=-mod=vendor
go version
go list -deps -json -buildvcs=false -tags=nobadger,nomysql,nopgx .
```

VCS stamping was disabled only because an extracted signed source archive has no
original Git metadata; the exact original wrapper revision was checked separately.
The Go toolchain download retained its normal checksum-service verification.
The complete graph includes 23 `golang.org/x/crypto` package paths, including SSH,
SSH agent, Argon2, bcrypt, OCSP and the cryptographic primitives used by Caddy.
None is an advisory-listed OpenPGP path. This assessment does not borrow the
backend's different import graph or presume that all packages in `x/crypto` are
absent.

This static source-package proof covers the exact recorded executable and build
configuration. It does not establish absence of unknown vulnerabilities,
call-level safety of other packages, arbitrary custom modules, dynamically loaded
code, different tags/toolchains, or ARM64 applicability. A changed executable or
source graph requires a new review; retain the module finding in raw reports and
carry this disposition only with its matching binary/source evidence. No scanner
ignore entry was added. Patched final-image rescans and ARM64 evidence remain
pending.

## Patched private AMD64 rescan

The actual `750f440d143d6766d83b13e1ce789436471ae029` candidate pair was rebuilt,
fully source-collected, overlaid, smoke-tested and OCI-verified before this
2026-10-07 rescan. Both final installed Alpine graphs have **zero findings**.
The unchanged scanner version, authenticated archive SHA256 and database SHA256
above were reused; the new scanner download matched the exact previously
signature-verified archive and bundle bytes. Extracted Trivy executable SHA256:
`93f9da8e4ba5e0c1c76d8234ed2494cf9afb0a96fd21953e424bb795f3299b8e`.
The database was downloaded at `2026-10-07T18:23:53.492313625Z` and retains the
recorded `2026-10-07T07:38:55.515026687Z` update. Scans used the same bounded,
credential-free offline analysis and no ignored findings.

| Final subject | Exact configuration                                                       | Installed Alpine graph                                   | Remaining binary findings |
| ------------- | ------------------------------------------------------------------------- | -------------------------------------------------------- | ------------------------- |
| Backend AMD64 | `sha256:efd3778408f2d98e8e79ca8aa8744602f3f45811b59146574dcde6de7e721880` | 17 packages; OpenSSL 3.3.7-r2, zlib 1.3.2-r1; 0 findings | 21 module findings        |
| Web AMD64     | `sha256:9909e94d12f5e865363993673c538d206dffc7a2bd1ca32d8a820e7062d55138` | 32 packages; OpenSSL 3.5.8-r0, zlib 1.3.2-r1; 0 findings | 1 module finding          |

Backend OCI archive SHA256:
`ff832ed03451a635cd716ce8415a623ad88bd75380e5269dbc5d6385f5cbf648`;
web OCI archive SHA256:
`a3463107e1180d875b7fb0ac90c08d232e8a9d7264a202a728fe9b3820be42ff`.
Both archives passed full blob/configuration/decompressed-layer validation
against the exact native smoke-tested configurations before scanning.

The backend executable remains SHA256
`03824947e006205dcf592c4cccfa70fae371cb222b5da410a4d83c2f905efd0f`;
the Caddy executable remains the exact SHA256 reviewed above. Between the
backend source-analysis baseline and this candidate, the only changed backend
file is its Dockerfile. Every one of the 21 backend scanner advisory IDs was
independently matched to its current primary
[Go vulnerability database](https://vuln.go.dev/) record and aliases. All affected
packages are SSH/SSH agent/OpenPGP or Windows paths absent from the reviewed
backend import graph. Their bounded disposition remains **not applicable to
this unchanged Linux AMD64 executable**. The correspondence JSON SHA256 is
`3bb0f4cc2ad3cee6c816fa64a8fba65d6c498e694a73c632b79729a5c8129b15`. Caddy's single OpenPGP finding likewise retains the exact
matching-binary/import-graph disposition above. Raw reports keep every finding;
no scanner ignore or blanket module suppression was introduced.

Patched backend report SHA256:
`3303f81d6ba4dc747335bc2b5f4471a873fac58f1a5e758e16e08e22561cb93a`;
patched web report SHA256:
`441e2e2a622f9c91e83397edaa3311c878b44a500a08a2379a9ebc40702c984a`.

The source pack covers all 20 retained backend package versions from 13 origins
and 33 web versions from 21 origins, including superseded vulnerable lower-layer
copies. Those copies are source-accounted, not claimed to be patched or covered
by the zero-finding statement about final installed graphs. The exact new runtime
source archive is 156854687 bytes, SHA256
`4ce28130c2d926558568fdcc58cdd78193da92fefe54873c827fb3454d45bad6`.
Its pack hash is `498820d85333eb2022344396e7b6e7f41a95c9a97faf044441cabb09bbf3810d`.
Native HTTP checks verified exact served notices/source metadata, unchanged
runtime binaries, administrator/session persistence and complete fixture cleanup.

This closes the recorded patched AMD64 image scan review for the exact subjects.
It does not approve public distribution or substitute for ARM64 scans, source
publication, exact-tag CI, authenticated workflow evidence or live release checks.
Private review version `v0.0.0` does not move or replace the existing remote tag.

## Native source scanner measurements

`tools/measure_release_source_scans.py` runs source scanners on an exact
`git archive` snapshot using the immutable official release Go and Node builder
inputs. It accepts a matrix-local `NativeSourceContext`; it does not authenticate
that context, create a version tag, attest outputs, approve findings, or publish.
The release workflow must validate the tag and authenticate the measurements
before combining both native platforms with the complete release binding.

```sh
python3 tools/measure_release_source_scans.py \
  --repository endorses/psst.zip --version v1.2.3 --revision TAGGED_COMMIT \
  --platform linux/amd64 --repository-root "$PWD" \
  --go-image docker.io/library/golang:1.26.8-alpine@sha256:RESOLVED_GO_INDEX \
  --node-image docker.io/library/node:22-alpine@sha256:RESOLVED_NODE_INDEX \
  --output /private/release-review/source-scans-amd64
python3 tools/test_release_source_scans.py
```

Run the equivalent measurement on a native ARM64 runner. The producer checks
host and actual builder architecture; new executions also reject a Docker daemon
that would emulate the requested platform. A successful AMD64 absent-package
assessment does not apply to ARM64 without that platform's actual graph.

Fixtures run unprivileged with a read-only source mount, read-only root
filesystem, dropped capabilities, bounded memory/processes/tmpfs and private
output files. They mount no Docker socket and receive no host credentials.
Owned fixture IDs are recorded so failure/timeout cleanup removes only the
producer's containers. Disposable caches are removed afterward. The scanner's
private tmpfs permits execution because its authenticated Go-built binary runs
there; an initial noexec fixture correctly failed and emitted no successful
measurement.

The Go fixture uses the actual Go 1.26.8 release compiler with automatic toolchain
switching disabled. It installs `govulncheck@v1.8.0` only after the downloaded
module matches `h1:clG4qBU6zH5VKjti8n5j8BBuYzoSha392xXMkXS351U=` through normal
Go proxy/checksum verification. Actual scanner binary/build metadata, database
configuration/date, full streaming JSON, verbose conversion, complete root and
server dependency graphs and resolved module metadata are retained. Repeated
identical advisory messages are valid streaming output; conflicting versions
of one advisory fail validation. Scanner errors, partial JSON, missing SBOM
coverage, module replacements and conversion errors cannot pass.

Every finding stays in the report. A module-only finding can receive a scoped
structural `not-applicable` measurement only when every affected Go package path
from its official advisory is absent from both actual all-root and server import
graphs. Missing advisory paths, imported packages, symbols or uncertain findings
remain unresolved. This measurement is not an authenticated release disposition.
The [official scanner documentation](https://pkg.go.dev/golang.org/x/vuln@v1.8.0/cmd/govulncheck)
explains why JSON exit zero alone cannot establish absence of vulnerabilities.

The web fixture uses Node 22's actual bundled npm, the exact package-lock state,
`npm ls --package-lock-only --all --json` and a lock-only `npm audit --json` with
lifecycle scripts disabled. The full audit response, lock graph, dependency
count, npm/Node versions, timestamps and response hashes are retained. The npm
service exposes no immutable database snapshot version, so the report records
that limitation instead of inventing one. Audit API failures/inconsistent
accounting fail; vulnerability exit one retains all unresolved findings and
cannot pass the final source-scanners gate. See the
[official npm audit documentation](https://docs.npmjs.com/cli/v10/commands/npm-audit/).

The private `750f440d143d6766d83b13e1ce789436471ae029` AMD64 measurement completed
on 2026-10-07 with Go 1.26.8/govulncheck 1.8.0 and Node 22.23.3/npm 10.9.9.
It retained 21 module-only Go findings, 11 root packages, 240 all-root imported
packages, 236 server imported packages and 15 scanner SBOM modules. The Go
vulnerability database's observed last-modified value was
`2026-10-07T14:10:51Z`. All affected paths were structurally absent in this
native configuration; package/symbol findings were zero. The Node audit retained
zero vulnerabilities across 173 locked dependency packages. The unsigned source
measurement SHA256 is
`f2d443e5f77dbc910a17d145ef5df354dad99246dd6a0f0395e31d392fedea3e`.
These exact compiler graphs reflect the release Alpine builder's
`CGO_ENABLED=0`; they do not reuse the earlier host review's graph count.

## Compiler graphs bound to actual runtime executables

The same module exposes `measure_compiler_graph(...)` for individual backend and
web native graph measurements. Inputs are the native source context, externally
bound runtime source asset, actual final OCI archive/config, immutable Go
builder and official advisory IDs. It outputs full compiler graph JSON and
raw advisory records with safe filenames/hashes, actual executable/build
metadata, source/toolchain identities and a typed correspondence proof.
It preserves all findings elsewhere; it does not accept caller-authored
applicability or approval flags.

The existing source-scan CLI is unchanged (`--mode source-scans` is its optional
explicit default). For a compiler graph, use the same native source/builder
arguments with the exact privately retained runtime inputs:

```sh
python3 tools/measure_release_source_scans.py --mode compiler-graph \
  --repository endorses/psst.zip --version v1.2.3 --revision TAGGED_COMMIT \
  --platform linux/amd64 --repository-root "$PWD" \
  --go-image docker.io/library/golang:1.26.8-alpine@sha256:RESOLVED_GO_INDEX \
  --component web --runtime-pack /private/release-review/pack \
  --runtime-source-sha256 sha256:BOUND_RUNTIME_SOURCE_HASH \
  --image-archive /private/release-review/export/web-amd64.oci.tar \
  --tested-config sha256:ACTUAL_SMOKE_TESTED_CONFIG \
  --cosign /private/tools/cosign --advisory-id GO-2026-5932 \
  --output /private/release-review/caddy-compiler-amd64
```

For backend graphs, use `--component backend`, its actual OCI/config inputs,
omit `--cosign`, and repeat `--advisory-id` for the relevant official records.
The graph report and full raw proof files are written to the new output
directory. `--node-image` applies only to source scans. Scanner process errors
fail the producer; a complete source scan with package/symbol or npm findings
retains those findings as unresolved with the release gate pending. Successful
measurement exit status therefore never means those findings were approved.

For the backend, the source snapshot is mounted at `/build` and rebuilt using
the exact release Go builder, `/go/pkg/mod` dependency location and Dockerfile
build configuration. The resulting executable must byte-match the actual final
OCI `/app/server`; all graph-selected module versions and `h1` sums must match
its embedded metadata. The private patched AMD64 executable reproduced exactly
as SHA256 `03824947e006205dcf592c4cccfa70fae371cb222b5da410a4d83c2f905efd0f`.
Its typed correspondence is `reproduced-in-release-builder`.

For Caddy, the actual final OCI executable must match the official executable
archive authenticated by the same freshly verified signed SHA512 list that
binds the full buildable vendor/wrapper source. Exact release workflow identity,
commit/ref and public Sigstore transparency checks are required. The native
compiler resolves that original full wrapper with its actual Go version,
platform/architecture variant, `CGO_ENABLED=0` and
`nobadger,nomysql,nopgx` tags; every selected dependency/module checksum matches
the embedded binary metadata and authenticated vendor declarations.
Unhandled package-selection flags, compiler modes and nonempty `GOEXPERIMENT`
fail instead of silently resolving a graph with different defaults.
The private patched AMD64 graph again selected 970 packages and all 147
embedded dependencies, with no affected OpenPGP package. Its executable remains
SHA256 `678ade3bfc088749c81a681adc603333ee0bb023b6a6cfe3c0f58bef8ff854e9`.
The typed correspondence is `upstream-signed-source-and-binary`, not a claim
that an independently rebuilt upstream binary is byte-identical. The actual
signature report is retained as a hash-bound raw proof file.
The unsigned native Caddy graph measurement SHA256 is
`5d058e895cbcb3d9abba46ac7890e174ec60c395c3e0db7251a68ae87384098f`.

A later authenticated final-image review must read these full compiler/advisory
bytes itself, bind the exact executable and source asset to its native image
scan, and derive absence of all advisory-listed packages. The backend source
graph cannot justify a Caddy finding. These proofs do not collect complete
application dependency corresponding sources: preserving Go/npm dependencies
for redistribution is a separate source-pack gate. They also do not approve
OS findings, unknown imports, other native platforms or arbitrary custom modules.
