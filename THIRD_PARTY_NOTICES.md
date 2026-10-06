# Third-party notices

## Container dependency inventories

The backend ships version-matched Go module license and notice snapshots under
`/app/licenses/dependencies`, with its inventory and consolidated notices in
[`backend/licenses/`](backend/licenses/). The Go standard library license is
copied from the actual image builder into `/app/licenses/go/LICENSE`.

The web image serves its locked npm inventory and consolidated upstream notices
under `/licenses/`, including runtime code declared as development dependencies
in the build project. Regenerate the committed notices after dependency changes:

```sh
cd backend
go mod download
python3 scripts/generate-third-party-notices.py
cd ../web
npm ci
node scripts/generate-third-party-notices.mjs
```

CI checks that inventories and notice texts match the committed locks. These
generated snapshots normalize line endings and trailing whitespace while
preserving the legal text. Inventories record both the original upstream hash
and the hash of the distributed text. These
inventories cover application dependencies. The selected Alpine/Caddy runtime
distribution and native applications require separate release licensing review;
these snapshots do not establish that all distribution obligations are complete.

## Administrator authenticator codes

The backend uses [`pquerna/otp` v1.5.0](https://github.com/pquerna/otp) under the
Apache License 2.0. Its full [license](backend/licenses/pquerna-otp/LICENSE)
and [notice](backend/licenses/pquerna-otp/NOTICE) are included in source and in the
backend image under `/app/licenses/pquerna-otp`. Its barcode dependency's MIT
[license](backend/licenses/boombuler-barcode/LICENSE) is also retained and shipped
under `/app/licenses/boombuler-barcode`. Include these notices when distributing
standalone backend binaries.

## Lucide icons

The web interface uses [Lucide](https://lucide.dev) through the `@lucide/svelte` package. Lucide is freely usable under the ISC license, with specified icons derived from Feather under the MIT license.

The package’s complete copyright notices and license terms are preserved in [web/static/licenses/lucide.txt](web/static/licenses/lucide.txt). That file is distributed with the web application at `/licenses/lucide.txt`.

## QR Scanner

The authenticated web scanner uses [Nimiq QR Scanner](https://github.com/nimiq/qr-scanner) under the MIT license. It detects QR codes locally, using native BarcodeDetector when available and a bundled worker fallback otherwise. Its copyright and license are distributed at [web/static/licenses/qr-scanner.txt](web/static/licenses/qr-scanner.txt).

## Nunito wordmark

The psst.zip wordmark uses [Nunito](https://github.com/google/fonts/tree/main/ofl/nunito), licensed under the SIL Open Font License 1.1. The unmodified source font and full copyright/license terms are preserved in [assets/brand/fonts/](assets/brand/fonts/). Production logo exports contain outlined glyphs; the editable sources and reproducible exporter are in [assets/brand/](assets/brand/).
