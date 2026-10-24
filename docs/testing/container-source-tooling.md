# Container source tooling verification

A publication-disabled common-source rehearsal at `6ba7e30` retained 193 originals
(173 npm distributions and 20 upstream archives), totaling 412,351,580 bytes, in
2m35s. An exact-source native Node builder completed in 22.5 seconds. Its real
capture plus common-source replay and preferred-source checks passed in 8.42
seconds: 4,337 inputs, 34 generated outputs, 168 rendered preferred package inputs,
276 generator inputs, 23 virtual and 11 generated associations. This check used
the official immutable Node 26.12.1 image, retained originals and actual container
bytes. It did not test final Caddy/backend images or authorize publication.

The experiment caught Docker's inability to retrieve the capture through its
copy API from the container's temporary mount. The collector now keeps its
isolated container running while it executes the capture and streams the bounded
archive through that container's mount namespace, then removes the owned
container. The exact-source Docker context includes both capture catalogs and
the capture script. The temporary rehearsal test was removed after verification;
normal tests remain offline and fast.

A native AMD64 source-scanner rehearsal at the same commit completed with Go
1.27.3 in 75.754 seconds, including a cold installation of the pinned
`govulncheck` tool and both actual import graphs. One fresh common npm audit took
3.204 seconds and was reused without another npm invocation. Independent replay
matched all scanner, database, graph and finding facts: 21 module-only Go findings
had every officially affected package absent from both graphs; three npm findings
remained unresolved and keep publication closed. This was one native architecture,
not a complete release gate. Its temporary replay harness was removed afterward.

The maintained release tool is built with `sh tools/release/build.sh`. Its source collectors retain complete upstream originals and native Go module ZIPs; source replay reads those retained bytes without running upstream generators. The source inventory uses one versioned document, with identical bytes inside and beside each offering. The final release manifest binds the exact archive bytes.

Run the focused checks with the repository's Go toolchain:

```sh
go -C tools/release test ./internal/source ./internal/oci ./internal/browser
```

These checks exercise original archive bounds and link metadata, npm SRI and content deduplication, independent Go H1 verification, APK source checksums and recipe bytes, original legal texts, image layer/whiteout replay, unchanged runtime configuration, exact Git source archives, SQLite syntax correspondence, and final notice substitution. They use small disposable inputs and do not build application images.

On 2026-10-24, a separate retained-original check downloaded and authenticated the five backend archives from `tools/upstream-application-sources.json`. It independently verified the actual modernc.org SQLite, libc, cc, ccgo and fileutil proxy inputs against committed Go checksums. The new backend preferred-source checker matched 5,476 proxy members to full project originals, compared both actual Linux SQLite translations using Go syntax, and retained 622 libc recipe, overlay and header members plus all three generator tool sources. This run passed in 7.96 seconds. Its temporary harness was removed; normal test runs do not download these archives or parse the large generated SQLite files.

`tools/backend-source-recipes.json` records reviewed generator relationships and required source coverage. It replaces fragile source snippet matching. It does not claim that generators were rerun or that the C translation was reproduced. SQLite's supported vendoring transformation still receives a substantive syntax comparison, including build constraints, aliases, literals, declarations and call semantics.

The Go unit checks and retained-original backend run do not establish that a complete native release candidate passed. A full candidate still needs both native Linux architectures, original runtime source acquisition, current official Caddy signature verification, final container smoke checks, unchanged scan policy and the protected publication approval. Those hosted/container operations were not performed by the retained-original check above.
