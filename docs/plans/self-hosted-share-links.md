# Working share links for self-hosted installations

Each installation has one operator-selected HTTPS origin. The mobile app uses
that origin for API calls and generated links; recipients open those links
without editing them. Keep the existing Caddy routing and make the standard
deployment include its web build. Validate both the API and share pages during
mobile server setup, without creating a transfer or accepting network failures.

- [x] Package the web build into the Caddy image and document the single-origin deployment.
- [x] Add a read-only API identity endpoint and a web identity marker for setup validation.
- [x] Validate URL, API identity, and download/upload pages in the shared client with regression tests.
- [x] Require successful validation before saving Android and iOS server settings; preserve the old settings on failure.
- [x] Show the full receive link with copy feedback and sharing alongside QR codes in both mobile apps; keep long links accessible on small screens.
- [x] Verify deployment routing, shared tests, Android build, and available web/backend checks; document iOS verification limits.
- [x] Format the scoped changes, preserving unrelated branding edits.

## Verification

- Backend `go test ./...` passed, including the read-only health endpoint test.
- Web `npm run check` passed without errors or warnings.
- Both Docker images built from source; the web image excludes host dependencies
  and generated output from its build context.
- All three Playwright tests passed against an isolated Compose deployment on
  loopback port 18480: API/web routing, upload with unchanged generated download
  links, file and ZIP decryption, and drop-slot uploads. This check used HTTP on
  loopback, not public TLS certificate issuance. Test containers, volumes, and
  temporary configuration were removed afterward.
- `:app:assembleDebug :shared:testDebugUnitTest` passed with all 59 shared tests,
  including eight server-validation regression tests. Mock HTTP handlers use the
  coroutine test scheduler so timeout tests exercise the intended virtual clock.
- Kotlin, Swift, XML, YAML, HTML, TypeScript, Go, and Markdown changes were
  formatted; whitespace checks passed.
- iOS validation/save behavior was reviewed and formatted, but native Swift/KMP
  compilation and device testing remain unverified on this Linux host.
- Receive links are selectable and wrap in scrolling mobile screens. Copy and
  share actions use the exact same complete URL as the QR code, retaining its
  encryption-key fragment. Android includes copy feedback in the receive screen
  and history details; iOS uses the same improved link controls for send/receive.
  Clipboard/share-sheet behavior was checked in source; device UI testing remains
  manual.

Operators still choose and configure their own hostname, DNS, and trusted HTTPS.
No installation-specific hostname is built into the application. Existing saved
mobile settings remain unchanged until the user saves a newly validated URL.
