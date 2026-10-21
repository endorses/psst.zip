These public-test-only vectors are exact copies of the repository's shared
cross-client fixtures, retained here so the Go submodule's source archive can run
its own cryptographic tests:

- `docs/protocol/chunked-file-vector.json`
- `docs/security/fixtures/hpke-receive-v2.json`
- `docs/security/fixtures/hpke-rfc9180-x25519-aes256.json`

The test loader also checks byte equality against those originals when running
inside a complete checkout. Keep copies in sync; never use these fixture keys or
fixed nonces in production. The HPKE fixtures retain their provider and pinned
RFC-vector provenance.
