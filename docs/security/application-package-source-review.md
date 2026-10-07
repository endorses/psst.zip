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

No inspected package metadata provided a `gitHead`. Repository metadata and a
version string therefore cannot independently identify the exact upstream source
commit. Preserve immutable upstream source references and hashes when adding
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
`packages/svelte/package.json` declares version `0.1.0`, although the observed tag
and locked npm package are `1.51.0`. Resolve the release's version-setting and
generation process before claiming correspondence; the tag name alone cannot
prove that these inputs reproduce the published package.

## Remaining review

- [x] Inspect this ten-package subset against the actual replayed archive,
      distinguishing original source, generated files and embedded map text.
- [x] Retain the three immutable upstream source archives identified above and
      check the hpke original source against its locked npm input.
- [ ] Determine the complete final browser import closure, including transitive
      runtime packages and build inputs needed to reproduce the distributed code.
- [ ] Retain and verify missing exact upstream source/build inputs, including the
      generated icon inputs and TypeScript/build configuration identified above.
- [ ] Review all Go, browser, Caddy and retained Alpine sources, notices, recipes
      and patches against the exact distributed native image pair.
- [ ] Authenticate the complete source offering and verify actual public retrieval
      before publication. This inspection creates no source-completeness gate.
