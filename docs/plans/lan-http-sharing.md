# LAN sharing without domain or certificate setup

Self-hosters can start the complete stack and use `http://<server-ip>` from
their LAN. Native clients and browsers use that same origin for API requests
and complete share links. Public HTTPS remains supported without pinning an
installation-specific host or certificate into the app.

This supersedes the HTTPS-only setup restriction in the earlier share-link plan.
Keep AES-256-GCM, secure random keys/nonces, fragment keys, and the existing wire
format. HTTP does not authenticate delivery of browser code; document that
limitation without blocking trusted-LAN use.

- [x] Add a maintained AES-GCM browser fallback when Web Crypto's secure-context API is unavailable; test interoperability and authentication failures.
- [x] Accept HTTP origins in shared setup validation and native networking policies, with no TLS verification bypass or embedded certificates.
- [x] Make Docker Compose serve HTTP on the LAN by default and document optional HTTPS.
- [x] Verify browser transfers at an actual non-loopback HTTP origin, including receive links, and rebuild the Android APK.
- [x] Start and verify the LAN instance at its current address, record iOS verification limits, and clean temporary test artifacts.
- [x] Format and review the changes while preserving unrelated branding edits.

## Verification

- `@noble/ciphers` is pinned to 2.4.0; Web Crypto remains preferred where available.
  Interoperability tests compare the HTTP fallback with Node's independent Web
  Crypto implementation in both directions, including empty files and tampering.
- Web type checking, all nine unit/integration tests, and the production build
  passed. HTTP copy-link support also works when `navigator.clipboard` is absent.
- Android debug assembly and all 60 shared tests passed. Merged debug and release
  manifests both allow configured HTTP, with no custom certificate policy.
- Caddy's default HTTP configuration validated. Four Playwright tests passed
  against `http://192.168.178.29:18481` in an isolated deployment: the browser
  confirmed `isSecureContext === false` and `crypto.subtle === undefined`, then
  exercised unchanged/copied links, encrypted uploads, file/ZIP downloads, and
  drop-slot uploads.
- The disposable deployment's containers, volumes, and temporary configuration
  were removed. The `psst-local` instance was updated while preserving data
  and left running at `http://192.168.178.29`, with the existing loopback port
  18480 also retained. The final LAN API and download route responded correctly.
  This IP is runtime configuration for the developer's machine, not an app default.
- iOS plist policy was validated and source formatted; native compilation and
  physical-device checks remain unavailable on this Linux host. The containing
  app requests local network access; HTTPS keeps normal certificate validation.

## Transport limits

HTTP works without a domain or certificate, but cannot authenticate browser code
delivery against an active network attacker. HTTPS is optional and recommended
for untrusted networks. LAN URLs are reachable only from that LAN or a network
with a route to it; copying a link does not make a private server Internet-accessible.
