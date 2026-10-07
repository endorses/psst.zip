# Container dependency vulnerability review

Reviewed on 2026-10-07. The backend source scan found no known vulnerable symbols
reachable from the analyzed code and no vulnerabilities in imported packages.
It reported 21 advisories at module level; their applicability is documented
below. The web dependency audit passed after the source-map-js security patch.
Final AMD64 image scans found unresolved fixable runtime findings below.
ARM64 scans, release-toolchain web validation and native dependency scans remain
pending. This record does not approve a container release.

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
x/crypto v0.57.0 with unknown severity and no fixed version; its actual affected
OpenPGP package usage needs separate review. Neither module findings nor the
successful scan exit status authorize publication.

Backend report SHA256:
`c1439946820f6990df3c390f1ed824f0f4afde8864531c4dba7817d29e824b80`;
web report SHA256:
`fbfb59b727499d6c40d53ed03ff1391fa30dce0087d21f0e4df819681f71d4cc`.
The full reports and disposable images remain private. ARM64 and patched final
pair scans are still pending.
