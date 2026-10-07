# Application package source input inspection

This inspection records actual retained npm package contents before the
corresponding-source review. It covers eight direct web dependencies and the
Svelte/SvelteKit inputs selected by the committed web locks; it does not establish
the complete final browser import closure or approve distribution.

The independently replayed AMD64 dependency archive for source
`c73a5da9bbbec4fb5de63586eb498879ec73a28c` and planned version `v0.1.0` is
`psst.zip-dependency-inputs-v0.1.0-amd64.tar.gz`, SHA256
`64185cd10474cf2ed71cf9f437d1474b0429d97c1f2b261d4d4e18280cd7b583`.
The inspection rechecked that archive's recorded hash and each selected inner
npm archive's recorded hash, then read archive members and package metadata
without extraction or package-script execution. Declaration files are excluded
from the TypeScript source count below. Source-directory counts refer to paths
containing `/src/` or `/source/`; they are observations, not completeness tests.

| Locked package    | Version | Regular files | TypeScript source files | Source-directory files |
| ----------------- | ------- | ------------: | ----------------------: | ---------------------: |
| @lucide/svelte    | 1.51.0  |         8,024 |                       0 |                      0 |
| @noble/ciphers    | 2.4.0   |            33 |                      10 |                     10 |
| @panva/hpke-noble | 1.1.7   |             5 |                       0 |                      0 |
| @sveltejs/kit     | 2.70.3  |           194 |                       0 |                    188 |
| fflate            | 0.8.3   |            17 |                       0 |                      0 |
| hpke              | 1.1.7   |             7 |                       1 |                      0 |
| qr-scanner        | 1.4.2   |            12 |                       0 |                      0 |
| qrcode            | 1.5.4   |            41 |                       0 |                      0 |
| svelte            | 5.57.1  |           388 |                       0 |                    368 |
| tus-js-client     | 4.3.1   |            82 |                       1 |                      0 |

## Concrete follow-up inputs

`@lucide/svelte` retains generated `dist/icons/*.js` files and declares build
commands using a generator, `scripts/exportTemplate.mts`, `.svelte` inputs and
`scripts/appendBlockComments.mts`. The archive has no source directory or source
maps. Retain the matching upstream generator/icon inputs and package build
configuration, and establish their relationship to the exact locked package.

`fflate` declares TypeScript compilation, `src/index.ts`, `tsconfig.esm.json`,
rewrite and UMD build steps. Its archive contains compiled `lib/`, `esm/` and
`umd/` files without original TypeScript or source maps. The matching upstream
source and build inputs remain to be retained and checked.

`hpke` retains `index.ts`, but its declared build command is `node build.cjs` and
that build script is absent from the package archive. `@panva/hpke-noble` retains
`index.js`, declarations, license and README, with no build commands in its
package metadata. Both identify the same upstream repository; inspect the exact
version's full build inputs rather than infer completeness from package names.

`qr-scanner` retains four maps. Its worker map embeds all thirteen named source
texts, including the bundled decoder's TypeScript; its standard and UMD maps each
embed `src/qr-scanner.ts`. The legacy map contains no source entries. Verify these
embedded inputs against the exact upstream release and retain its Rollup/build
configuration and decoder notices. A source map's presence alone does not prove
that all build inputs or license obligations are covered.

`tus-js-client` retains `lib/`, `lib.esm/`, `lib.es5/` and bundled outputs. Its
unminified bundle map embeds all twenty-four named source texts; the minified map
lists twenty-four without embedded text. Its declared commands compile from
`lib/` using Babel and Browserify. Review the original `lib/` inputs and the
matching upstream build configuration; absence of a directory named `src` is not
evidence that original JavaScript source is missing.

No inspected package archive manifest provided a `gitHead`. Repository metadata
and a version string therefore cannot independently identify the exact upstream
source commit. Public registry metadata is a separate input to be compared with
upstream tags and retained source bytes. Preserve immutable upstream source references and hashes when adding
missing inputs, and bind the offering to the final native image subjects.

## Retained immutable upstream inputs

Read-only upstream tag resolution identified the following commits. Archives were
fetched by full commit from GitHub's official codeload endpoint, hashed, and read
without extraction or script execution. They are retained privately for review;
they are not yet attached to assembled release inputs or publicly offered.

| Upstream                                                                                                    | Observed tag | Full source commit                         | Archive SHA256                                                     |     Bytes |
| ----------------------------------------------------------------------------------------------------------- | ------------ | ------------------------------------------ | ------------------------------------------------------------------ | --------: |
| [lucide-icons/lucide](https://github.com/lucide-icons/lucide/tree/45b0e148db4ee4d748340d0f99982aa1c2159d52) | 1.51.0       | `45b0e148db4ee4d748340d0f99982aa1c2159d52` | `8d74567fb686aef7d6a7d2e773fc8dec223b7719360d7043cbd84568eadad691` | 5,540,149 |
| [101arrowz/fflate](https://github.com/101arrowz/fflate/tree/dcb3714a6c25db3a2748641019c5277413d09714)       | v0.8.3       | `dcb3714a6c25db3a2748641019c5277413d09714` | `24d3150cf1b94ced292df0888d7a169473ab6aeead44518718dd2bd96893e981` |   120,872 |
| [panva/hpke](https://github.com/panva/hpke/tree/24456ca764f094228b5b292508236bc26d76eeed)                   | v1.1.7       | `24456ca764f094228b5b292508236bc26d76eeed` | `d2e5df0f2c01304a575bd5da0c0c734c60bee0dc994886aa964d5387a5e4dcbb` | 1,995,407 |

The fflate source archive contains the missing original `src/index.ts`, both
TypeScript configurations and a package manifest identifying `fflate@0.8.3`.
The hpke archive contains the missing `build.cjs`, package lock, original
`index.ts` and noble adapter example/build inputs. Its original `index.ts` is
byte-identical to the retained npm source, SHA256
`532fa3b5979e1b340d0a34c27c716e0cf2db1d58efcf4d3a6f0ca430288d3d30`.
The full generated npm outputs have not been reproduced.

The Lucide archive retains both identified `.mts` scripts, the Svelte package
configuration, monorepo package lock and 1,866 original SVG icon files. Its
`packages/svelte/package.json` declares template version `0.1.0`. The exact
retained `.github/workflows/release.yml` (5,690 bytes, SHA256
`b16738051dcfd29bb63925ebe8c5f3839eaea6c132c98c289db9b45eb46cf3b9`)
rewrites the selected package version with `pnpm version`, then builds and
publishes that package. This explains the difference from the tagged and locked
`1.51.0`; it does not prove complete package reproduction. The measured generated
icon data matched all 1,866 original SVG child tag/attribute arrays and sizes,
and the package license matched the original. Full build correspondence remains
pending.

## Integrated source retention verification

`tools/package_upstream_application_sources.py` and mandatory release-input
assembly now retain all three pinned originals in a common source offering. A
fresh official HTTPS collection and independent replay for application source
`2a6d2a5d65ad50b2565aa09624e5681c0976c262` and planned `v0.1.0` passed in 4.3 seconds.
The exact committed npm lock and catalog were read from Git; all 6,238 source
archive members were validated without extraction or upstream script execution.

The retained `psst.zip-upstream-inputs-v0.1.0.tar.gz` is 7,910,216 bytes, SHA256
`eb446636422a60dda1fd25b1efba2c0cff2ac0665cab61bc0f3ba4a84f17f7fd`.
Collection-record SHA256 is
`39dc1699752af1e16c7962d623c3612545c88ffee1fbef45e9d1ef5ffe41648a`;
independent replay SHA256 is
`d36478694b660161c921a14b9cb39fa301a2cd92087fb8c8fc9e629a0746f253`.
The offering includes untouched archives, complete measured inventories and the
exact catalog/lock inputs. Every association, complete-output correspondence,
reproduction, source-completeness and publication approval flag remains false.

Twelve collector/replay tests and twelve assembly tests passed, including
substitution after verification. The signing fixture covers the additional asset;
all 272 release-tooling tests passed in 19.9 seconds. These results verify
retention and integrity for their recorded source, rather than completion of the
full release source/distribution gates. New selected source commits require fresh
collection and measurements.

## Additional source references to resolve

Public registry metadata for
[tus-js-client 4.3.1](https://registry.npmjs.org/tus-js-client/4.3.1)
records `gitHead` `d4aa3dee249b8a2e9fb1dcb9c2b829937bc2b108`, while the upstream
`v4.3.1` tag resolves to `bf3337ce4921ed9103a085d258afc39cff2b4e7a`. Both source
manifests identify version 4.3.1 and share the observed Babel configuration, but
that does not prove equality of the 26 original `lib/` files shipped in npm.
Compare both commits before selecting and retaining corresponding source.

[qr-scanner 1.4.2](https://registry.npmjs.org/qr-scanner/1.4.2) has no registry
`gitHead`; neither `1.4.2` nor `v1.4.2` exists as an upstream tag. Source history
identifies a version-bump candidate `abcfe1bce2703721408d8ce7ebde94a359998506`.
Its Rollup configuration builds the worker before the scanner using TypeScript,
source maps and Closure Compiler, with a separate legacy inline-worker build.
Its full original archive is now retained and pinned. All twelve regular npm
members, including four scanner/worker JavaScript outputs and their maps, match
the original archive bytes. The scanner and worker's own embedded TypeScript
texts match their originals. This verifies those file comparisons; the upstream
build itself has not been executed.

The worker embeds twelve implementation texts from `jsqr-es6@1.4.0-1`, selected
by the original scanner yarn lock. Its original npm archive matches that lock's
SHA1 and SHA512 integrity. All twelve texts match the full original `danimoh/jsQR`
source at `42dbfd55f35db625119e8f33247b6e9d073902ec`. The original source also
retains `Point.ts`, absent from the maps, build configuration, yarn lock and tests.
The decoder's Apache 2.0 LICENSE was absent from the scanner npm package and our
previous web notices. The web generator now includes its full original license
and factual upstream attribution, recording upstream/distributed hashes. Exact
scanner lock, four compiled file hashes and the README attribution input are
checked before writing. The embedded component is separately inventoried; it is
not presented as a direct entry in the application's npm lock.

## Additional rendered-package source inputs

All fourteen observed rendered-package manifests match their actual retained
npm inputs, including lock version/SRI and archive size/hash. Four additional
version tags resolve to the same full commits as published npm metadata.
Twenty-three noble-curves TypeScript files, nineteen noble-hashes TypeScript
files and thirty-seven qrcode implementation JavaScript files match their retained
npm originals. The clsx source tree recovers its original index/lite modules and
build script, omitted from npm.

The expanded catalog pins original noble-curves, noble-hashes, clsx, node-qrcode,
qr-scanner and its embedded jsQR decoder source, alongside the initial three
upstreams. All nine originals total 83,412,855 bytes and contain 7,513 measured
members. Three noble test symlinks are inventoried without extraction or following
filesystem links; each resolves to an existing directory in its own source root.
Escaping, dangling, chained, recursive and hard links remain rejected. The decoder
has an explicit embedded relationship to the exact locked scanner, with one
primary upstream association. Exact commits, archive hashes and sizes are in
`tools/upstream-application-sources.json`.

Fresh official HTTPS collection against committed source
`224d95042349e5cd37421a992810eef1c58ec14d` and planned `v0.1.0` passed in
31.2 seconds; independent replay of all nine originals passed in 2.4 seconds.
The 83,381,770-byte offering has SHA256
`927428c006c88aadbef21622c2f9a43226f72a2cd59a3f6ebb8c036886786f87`.
Private collection and replay evidence is retained under
`/tmp/psst-upstream-source-offering-review-224d95042349/`; this is evidence,
not a reusable download cache. The replay record has SHA256
`efd2f247b2b71896b6857ca4e213423fdc9aa29556d946e914cb3ee73de7e106`.
Source completeness, upstream build reproduction, authenticated publication,
final hosted image correspondence and public retrieval remain unverified.

Upstream build reproduction remains unrun. Noble declares jsbt 0.7.1 and
TypeScript 6.0.3 inputs distinct from the application's retained compiler; clsx
declares terser 4.8.0. qrcode's original Rollup/Babel recipe uses tooling distinct
from the app build, which consumes its original browser implementation.
The dijkstrajs package retains original JavaScript, author/disclaimer and an MIT
reference, but its abbreviated LICENSE omits the full permission text. These are
specific outstanding review items, rather than source-completeness approvals.
The esm-env package retains its original small JavaScript modules, export
configuration and full MIT text, with no declared build step.

## Original framework and cipher recipes

Three further official full-commit archives recover concrete omitted recipes.
The noble-ciphers version tag resolves to the registry's `gitHead`; the SvelteKit
and Svelte annotated version tags resolve to the commits below. Their original
package manifests identify the exact locked versions. All ten noble-ciphers
TypeScript originals, 163 SvelteKit source files and 368 Svelte source files match
the actual retained npm bytes, with no missing or different compared files.

| Package              | Source commit                                                                                                                        | Archive SHA256                                                     |     Bytes |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------ | --------: |
| @noble/ciphers 2.4.0 | [d9e8a6a599e7ed729d9be03854c46a3c73bd9a79](https://github.com/paulmillr/noble-ciphers/tree/d9e8a6a599e7ed729d9be03854c46a3c73bd9a79) | `3bcf1bc60f00480615d370001efbcd000f10b542c7f15b3ab00acdb97cf6a41d` | 1,289,689 |
| @sveltejs/kit 2.70.3 | [39e8e1fbd4feba7f22dd46bfdf7335362c38de16](https://github.com/sveltejs/kit/tree/39e8e1fbd4feba7f22dd46bfdf7335362c38de16)            | `6d19fda7fb03101a40a0ccb45a89338303a442e33dbc2b7a5e983686a18dc46b` | 4,350,585 |
| svelte 5.57.1        | [636eaaaa6f064b55072e7d192bb76dc9d8c4516e](https://github.com/sveltejs/svelte/tree/636eaaaa6f064b55072e7d192bb76dc9d8c4516e)         | `951e36068331f3f7e206bdc948e25824375f1c3a0356c34c462b0f3e8e1148c7` | 1,938,208 |

The cipher archive retains its missing TypeScript configuration and exact jsbt
0.7.1 / TypeScript 6.0.3 requirements. The framework originals retain monorepo
locks, version/type generators, Svelte's Rollup/browser-support recipes, original
message Markdown and message templates. The two original version generators
were run in separate disposable directories using only their original manifests
and scripts; their output matched the retained originals byte-for-byte in 26 ms
and 19 ms. The directories were removed. This is not a complete upstream build
reproduction or message-generator execution.

The catalog now retains twelve original archives totaling 90,991,337 bytes and
24,853 members. Eight internal links remain metadata only; all twelve originals
passed the collector's existing bounded inspection without changing its limits.
Private archive, member, input-comparison and generator evidence is retained in
`/tmp/psst-browser-recipe-review-edc6610/`, not used as a download cache.

For the five packages outside the preceding nine-upstream catalog, every observed
rendered module's input hash also matches its retained npm member: three cipher,
23 SvelteKit, 59 Svelte, one dijkstrajs and one esm-env files. esm-env's five small
original modules, conditional exports and full MIT text require no additional
upstream compilation recipe. tus-js-client contributes neither rendered nor
excluded modules in this measured build; its recorded source-reference mismatch
remains a separate review item, not evidence of a missing rendered module.

dijkstrajs's original Wyatt Baldwin notice remains unchanged. The web generator
now adds explicitly supplementary full MIT template terms from the upstream's
referenced [Open Source Initiative page](https://opensource.org/license/mit),
retrieved on 2026-10-08. The supplement preserves the source template placeholders
and does not invent an owner or year. Its checked-in catalog binds the exact
1.0.3 lock integrity, original notice and supplementary text hashes. Three small
fixtures verify preservation, changed inputs before output writes, and unsafe
paths. This fixes the missing permission text without claiming it originally
appeared in the npm notice or authorizing the complete distribution.

Fresh official collection of all twelve originals against source
`f62d35364fdc2cbb3540176d39899448d5ec4835` and planned `v0.1.0` passed in
37.2 seconds; independent replay passed in 3.7 seconds. The 91,545,944-byte
offering has SHA256
`c5ac96bdb47785183e4c10e8ea6c133a25c4c52ea20c2420c97283af0a6531cf`.
The private collection/replay receipt is retained under
`/tmp/psst-upstream-source-offering-review-f62d35364fdc/`; replay SHA256 is
`b6af18d3509ea34f93ecea0930fa9ae743de238d0b321ca83d4ff1d3e7d1843e`.
This verifies retention and integrity for this committed policy. Complete final
source/distribution review, hosted image binding and public delivery remain pending.

## Browser build module observations

The client build now emits `licenses/browser-module-inventory.json` using
Rollup's rendered module records. It hashes original files, transformation
inputs, parsed module inputs, installed package manifests, the npm lock and
generated output bytes. SSR modules are excluded from this client inventory;
tree-shaken and zero-rendered modules remain separately recorded. Exact Vite
manifest files are build metadata, since the static adapter omits them from the
served website.

A local working-tree production build using `v0.1.0` metadata observed 287
rendered modules, 4,093 excluded modules, and these fourteen packages contributing
rendered JavaScript:

| Package           | Version |
| ----------------- | ------- |
| @lucide/svelte    | 1.51.0  |
| @noble/ciphers    | 2.4.0   |
| @noble/curves     | 2.4.0   |
| @noble/hashes     | 2.4.0   |
| @panva/hpke-noble | 1.1.7   |
| @sveltejs/kit     | 2.70.3  |
| clsx              | 2.1.1   |
| dijkstrajs        | 1.0.3   |
| esm-env           | 1.2.2   |
| fflate            | 0.8.3   |
| hpke              | 1.1.7   |
| qr-scanner        | 1.4.2   |
| qrcode            | 1.5.4   |
| svelte            | 5.57.1  |

`tools/measure_browser_source_inventory.py` independently checks these recorded
source and installed-manifest hashes, lock associations and final static output
bytes. CI runs it after the production build. The copied `appearance.js` and
`language.js` scripts are explicitly reported outside the chunk module graph.
Worker sub-builds, CSS attribution, generated adapter
files and retained npm archive correspondence still require review. These local
build observations do not independently bind the supplied revision to a clean
Git checkout, verify an OCI image, reproduce upstream packages or establish
complete preferred-form source coverage. Git binding, source reproduction,
complete closure and publication approval flags remain false.

## Remaining review

- [x] Match all fourteen observed rendered-package manifests to locked retained
      npm inputs and retain six additional immutable original source trees,
      including the scanner's separately licensed embedded decoder.
- [x] Include the decoder's full Apache license and upstream attribution in web
      notices, rejecting stale compiled, license and parent inputs.

- [x] Inspect this ten-package subset against the actual replayed archive,
      distinguishing original source, generated files and embedded map text.
- [x] Retain the three immutable upstream source archives identified above and
      check the hpke original source against its locked npm input.
- [x] Record the actual rendered client module graph and independently compare
      recorded source and final static output bytes, with separate build metadata
      and explicit copied-script/worker coverage limits.
- [ ] Determine the complete final browser import closure, including transitive
      runtime packages and build inputs needed to reproduce the distributed code.
- [ ] Retain and verify missing exact upstream source/build inputs, including the
      generated icon inputs and TypeScript/build configuration identified above.
- [ ] Review all Go, browser, Caddy and retained Alpine sources, notices, recipes
      and patches against the exact distributed native image pair.
- [ ] Authenticate the complete source offering and verify actual public retrieval
      before publication. This inspection creates no source-completeness gate.
