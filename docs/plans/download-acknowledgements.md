# Download completion acknowledgements

- [x] Add an idempotent server acknowledgement and expose its timestamp without changing download-attempt quotas.
- [x] Preserve acknowledgement metadata until expiry when quota cleanup removes encrypted payloads.
- [x] Acknowledge completed, authenticated downloads in the web and native receiving clients; retry acknowledgement failures without downloading files again.
- [x] Show Downloaded in Android history and details only after acknowledgement.
- [x] Verify server lifecycle, client failure/partial-download behavior, and Android builds; document the status semantics.
- [x] Rebuild the local self-hosted deployment and APK and verify the deployed flow.

Verification: backend `go test -race ./...` and `go vet ./...` passed; 15 Android
and 63 shared tests passed, and the debug APK built. All 11 web integration/crypto
tests, Svelte checks, and 12 browser tests passed. Browser tests used a disposable
LAN HTTP deployment with Web Crypto unavailable. An Android emulator saved two
encrypted files with a one-download quota, the server recorded `downloaded_at`,
and history showed sender Downloaded and receiver Saved after restart. The live
LAN deployment was rebuilt with existing data volumes and its health and new
endpoint checked.

The iOS receiver and durable retry queue were reviewed for API compatibility but
cannot be compiled on this Linux host; Xcode verification is still required for
an iOS release. Browser confirmation means decrypted files were handed to the
browser download mechanism, not independently verified filesystem persistence.

Commit the implementation and this completed plan together.
