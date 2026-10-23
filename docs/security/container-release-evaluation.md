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
