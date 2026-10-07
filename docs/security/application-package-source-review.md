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

## Remaining review

- [x] Inspect this ten-package subset against the actual replayed archive,
      distinguishing original source, generated files and embedded map text.
- [ ] Determine the complete final browser import closure, including transitive
      runtime packages and build inputs needed to reproduce the distributed code.
- [ ] Retain and verify missing exact upstream source/build inputs, including the
      generated icon inputs and TypeScript/build configuration identified above.
- [ ] Review all Go, browser, Caddy and retained Alpine sources, notices, recipes
      and patches against the exact distributed native image pair.
- [ ] Authenticate the complete source offering and verify actual public retrieval
      before publication. This inspection creates no source-completeness gate.
