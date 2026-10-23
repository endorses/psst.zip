# Release scanner evidence

The release tool separates collecting scanner facts from deciding whether a
release may be published. A successful scanner process retains every finding;
its exit code cannot authorize publication.

`psst-release scan image` verifies the pinned native Cosign executable, the
official Trivy archive and its signature bundle before running Trivy. The
signature profile requires the exact official workflow, release tag, source
commit, issuer, Rekor verification and certificate transparency. The extracted
scanner must match its reviewed executable pin where available, native ELF
architecture and release version. Neither subprocess receives ambient GitHub
credentials, proxy settings, scanner plugins or scanner configuration.

The authenticated scanner downloads its own official database into a private
directory. The measurement retains its database hashes, actual metadata and
acquisition times. The shared OCI reader validates the exact smoke-tested native
configuration, compressed blobs and uncompressed layer identities before staging
the runnable image. Trivy scans every severity, lists packages and reports
suppressed findings. Changed scanner, database, image or layout bytes invalidate
the measurement. Completed outputs are `measurement.json` and `scan.json`;
private tool/database snapshots are removed.

`psst-release scan compiler` reads the actual final OCI executable's Go build
information without executing that executable. It uses the verified runtime
source offering and the exact executable's native compiler. The backend must
rebuild byte-for-byte with the selected application compiler. Caddy retains its
original compiler, independently verified upstream signatures, full vendor tree
and original build tags. Unsupported build experiments, module replacements,
partial graphs, test-only packages and differing module checksums fail closed.
Repeat `--advisory-input` to collect Go advisory references from scanner JSON;
those identifiers only request evidence and never suppress a finding.

The evaluator authenticates the exact measurement bytes against the repository,
source, version-tag workflow, hosted runner, run and attempt. `DecodeCompiler`
validates those same bytes and re-derives the graph summary from retained raw
reports. The assembler shares its existing runtime source and OCI replay rather
than repeating that work for each finding.

`AggregateImages` retains the existing final-image scanner policy. A finding must
come from the authoritative Go vulnerability database and match the actual
compiled module version. Every affected import path across all official advisory
ranges must be absent from the exact dependency graph before it can be called
not applicable. Symbol reachability and a scanner's “fixed” status cannot replace
that proof. OS, non-Go and unrelated executable findings require repair and a
complete rescan.

The only alternative is the existing exact committed, expiring Caddy exception.
It must match the official advisory bytes, finding, binary, upstream revision,
original compiler, base index and both native base digests. The publication
boundary rechecks the policy and its expiration before mutations and publication.
An unused expired exception does not block a repaired image with no accepted
findings. This migration neither broadens nor renews that exception.

The `ReadSourceMeasurement` API supplies structural binding for native Go and
shared npm measurements. `AggregateSources` separately replays the raw reports,
module checksums and affected-package evidence against the verified application
snapshot. Both native Go measurements are required; the shared npm receipt must
be identical across consumers. The source and image Python aggregation modules
have been removed with their last workflow consumers.

Focused offline verification covers substituted tools, failed signatures,
uncertain databases, omitted findings, incomplete compiler graphs, unsafe source
archives, changed layouts, affected/absent advisory packages and exception expiry.
A publication-disabled scanner rehearsal injects only external tool and OCI
boundaries; it verifies sequencing, retained outputs and private cleanup. It does
not claim a live Trivy database download, hosted attestation or real release run.
