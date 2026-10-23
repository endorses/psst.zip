# Container release evaluation

The tagged container workflow runs `psst-release evaluate` once for the exact
repository, version, source commit, current run and attempt. Before reading native
claims, it verifies the workflow attestations for both transfer inventories and
native artifact descriptors. The strict transfer reader then checks every retained
file against those authenticated bytes. Source, browser, compiler and scanner
measurements receive the same exact workflow/source authentication; identical
bytes are verified once per invocation.

The shared assembler reuses the common originals, native module inventories,
actual image files and browser replay for all six source checks. Backend coverage
checks the actual executable's module checksums, retained preferred originals,
normalized upstream legal files and final distributed notices. Browser coverage
uses the independently derived preferred-source relationships; it leaves the
native measurement's `preferred_source_complete` flag false. Recorded Caddy
signature proofs are bound by the authenticated native measurement. Reader-only
evaluation does not rerun Cosign; native collection and compiler measurement
continue to perform the cryptographic checks.

The source finding analyzer replays the raw native Go reports and shared npm
receipt against their exact inputs. Final image analysis checks the official
advisories against actual native compiler graphs. Findings, compiler versions and
the existing expiring exception policy retain their existing scope. No scanner
success flag, generated source relationship or local evaluation authorizes
publication.

`--prepared` must name a new directory. It receives the deployment manifest,
deployment bundle, six source offerings, both OCI index documents and the schema2
`release-inputs.json` completion record. `--output` must also be new; it receives
only `corresponding-source-evidence.json` and `release-result.json`. The result
contains six completed checks directly, plus digests of useful raw inputs. The
workflow attests the result before the recovery stage consumes it. Recovery adds
its two native experiment results to that same result contract; publication uses
the final authenticated result and the single protected approval.

`psst-release evaluate recovery` authenticates that source result before using
the three small retained binding files. The source result binds the exact
`release-inputs.json` bytes through its `artifact-foundation` input digest. Both
native descriptors and completed terminal measurements must match the same
repository, version, commit, run and attempt. The reader checks actual recorded
upgrade/reapply/pause/failure/isolated-restore observations, historical ancestry,
committed fixture inputs, matching native configurations, stopped checkpoints,
isolated volumes and measurement intervals. Its structured flow checks share the
Go updater's full-flow policy.

Recovery writes the extended `release-result.json` directly, retaining digests of
the authenticated source result and both terminal measurements. There is no
intermediate recovery-gate file or repeated source, OCI, Docker or experiment
execution. Explicit false scope flags keep public provenance, independent backup
provider and real browser/mobile validation outside this local fixture evidence.
The remaining Python native experiment controller and its historical input
readers remain in place until their separate cutover.

Focused offline checks cover authentication before native claim consumption,
changed retained bytes, current tagged CI run/attempt selection, measured versus
derived browser facts, result scope and scanner descriptor overrides. These do
not establish a successful hosted attestation, complete final image pair,
publication or production deployment.

One opt-in source-data rehearsal completed in 23.86 seconds using the exact Git
application snapshot, 193 retained common originals, 18 H1-authenticated modules,
an offline-built backend executable and independently normalized dependency
notices. It checked six source offerings and four coverage records, then refused
substituted final AGPL bytes. Its final runtime/image and browser facts were
explicit synthetic boundaries; it did not exercise a final OCI pair or hosted
signatures. The private fixture and temporary harness were removed after the run.
