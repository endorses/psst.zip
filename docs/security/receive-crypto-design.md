# Private receive encryption: cross-platform implementation proposal

Status: implementation and validation record, 2026-10-04. Browser cryptographic foundations, private receive flows and pinned dependencies are implemented. Android Tink and Swift provider adapters have interoperated with browser fixtures; native platform validation remains pending. This supports section 1 of [the security implementation plan](../plans/security-abuse-prevention-and-link-limits.md). Linux/Node checks do not establish native iOS or real LAN device validation.

## Decision

Use HPKE Base mode with DHKEM(X25519, HKDF-SHA256), HKDF-SHA256, and AES-256-GCM: RFC 9180 identifiers `mode=0`, `kem=0x0020`, `kdf=0x0001`, `aead=0x0002`. Wrap exactly one fresh random 32-byte submission key. Continue using that submission key with the existing encrypted manifest and chunked file format. Each submission creates a fresh HPKE sender context and seals one message at sequence zero. Do not implement ECDH, HPKE's labeled KDF/key schedule, or HPKE nonce handling in application code. Google's [hybrid encryption guidance](https://developers.google.com/tink/hybrid) recommends this exact suite and explains that Base mode does not authenticate a sender.

The important interoperability choice is **all application binding goes in HPKE `info`; AEAD AAD is empty**. Tink's public `HybridEncrypt.encrypt(plaintext, contextInfo)` maps `contextInfo` to HPKE `info` and seals with empty AAD. Its ciphertext is `outputPrefix || enc || ciphertext`. Use `NO_PREFIX`, never the default five-byte Tink wire prefix. This behavior is explicit in the [Tink 1.23.0 implementation](https://raw.githubusercontent.com/tink-crypto/tink-java/v1.23.0/src/main/java/com/google/crypto/tink/hybrid/internal/HpkeEncrypt.java). Reading internal source establishes the wire mapping; application code should use public Tink APIs.

## Protocol boundary

The receive link must carry the complete raw recipient public key in its versioned fragment, or a collision-resistant fingerprint checked before use. Prefer the raw 32-byte key to eliminate a server key lookup/substitution opportunity. The private key remains in owner device storage, scoped to server/account/slot, and must never appear in the upload link. The public server-side recipient key must equal the link-bound key. An attacker can replace an entire invitation; this scheme cannot establish authenticity of the communication channel through which that invitation was shared.

Context bytes are fixed, unambiguous binary concatenation:

```text
info = ASCII("psst.zip/receive-key/v2\0")
    || uint16be(0x0020) || uint16be(0x0001) || uint16be(0x0002)
    || slotUUID[16] || submissionUUID[16] || recipientPublicKey[32]
AAD = empty
plaintext = randomSubmissionKey[32]
wrapped = enc[32] || ciphertextAndTag[48]
```

Validate canonical UUIDs before decoding, exact lengths, suite/version, and recipient-key equality. Construct `info` from the expected slot and submission identity, not untrusted envelope values accepted without comparison. A wrapped key is exactly 80 bytes; it does not include an AES nonce, a Tink key ID, a protobuf keyset, or a CryptoKit `AES.GCM.SealedBox.combined` structure. HPKE manages its nonce internally. The RFC-defined KEM output sizes and suite identifiers are specified in [RFC 9180](https://www.rfc-editor.org/rfc/rfc9180.html).

The frozen manifest envelope is `ASCII("PSSTRCV2")[8] || wrapped[80] || encryptedManifest`. Its total size is at least 116 bytes (including the existing 12-byte nonce and 16-byte AES-GCM tag) and at most 1,048,576 bytes, including framing. A parser validates framing and bounds; successful HPKE opening and AES manifest authentication are still required before trusting contents. Appended data fails AES authentication. The forthcoming public URL is `/u/{slotUUID}#v2.{base64urlRecipientPublicKey32}`; standalone send URLs retain their existing format. Seal only once per submission; a resumable retry reuses its persisted envelope and encrypted resources. For a replacement submission, generate a new submission key and HPKE context. Bind transfer/submission identity to prevent copying a valid envelope to another transfer.

This removes shared decryption material from upload invitations. It must accompany owner-only inbox listing, events, child content and delivery operations. Encrypting separate submission keys cannot fix authorization by itself. HPKE Base permits anonymous senders and does not prove content is safe. Replay to the same identity still requires application immutability/idempotency rules. No private key synchronization is implied by pairing.

## Platform adapters

### Android

Candidate dependency: `com.google.crypto.tink:tink-android:1.23.0` in `shared`'s `androidMain`, pinned exactly. [Google's setup guide](https://developers.google.com/tink/setup/java) lists this release and supports Android API 24+. The project minimum is API 26 and JVM target 17 (`shared/gradle/libs.versions.toml`, `shared/build.gradle.kts`), so these declared requirements fit. Artifact resolution and the Android provider JVM tests have passed. Device validation remains pending.

Build `HpkeParameters` with `KemId.DHKEM_X25519_HKDF_SHA256`, `KdfId.HKDF_SHA256`, `AeadId.AES_256_GCM`, and `Variant.NO_PREFIX`. The [public parameter definitions](https://raw.githubusercontent.com/tink-crypto/tink-java/main/src/main/java/com/google/crypto/tink/hybrid/HpkeParameters.java) expose this exact suite and empty-prefix variant.

Use public `HybridConfig.register()`, `KeysetHandle.generateNew(parameters)`, `getPublicKeysetHandle()`, and `getPrimitive(HybridEncrypt.class/HybridDecrypt.class)`. For a link's raw key, `HpkePublicKey.create(parameters, Bytes.copyFrom(raw32), null)` imports the RFC public encoding. Put it in a single-entry `KeysetHandle` with `importKey(...).withRandomId().makePrimary()`; the local key ID does not appear on the wire with `NO_PREFIX`. Explicitly annotate the narrow raw-key adapter with `@AccessesPartialKey`, as required by the [public key API](https://raw.githubusercontent.com/tink-crypto/tink-java/main/src/main/java/com/google/crypto/tink/hybrid/HpkePublicKey.java). Do not use `hybrid.internal` or hand-build keyset protobufs.

Persist an encrypted private keyset, or the minimal private/public material inside the app's existing protected key storage. Fixture import can use `HpkePrivateKey.create(publicKey, SecretBytes.copyFrom(rawPrivate, InsecureSecretKeyAccess.get()))` inside the same narrowly reviewed adapter. The [private key API](https://raw.githubusercontent.com/tink-crypto/tink-java/main/src/main/java/com/google/crypto/tink/hybrid/HpkePrivateKey.java) validates the key pair. `InsecureSecretKeyAccess` is an explicit secret-extraction capability; it does not justify plaintext disk persistence or logging.

### iOS and share extension

`ios/project.yml` already targets iOS 17.0 for the project. Use the system CryptoKit HPKE API, available at that target, in a Swift service shared by the app and share extension. No additional Swift crypto package or deployment-target increase is proposed. Do not substitute raw `Curve25519.sharedSecretFromKeyAgreement` plus application-written KDF logic.

Construct `HPKE.Ciphersuite(kem: .Curve25519_HKDF_SHA256, kdf: .HKDF_SHA256, aead: .AES_GCM_256)`. The named `Curve25519_SHA256_ChachaPoly` preset is a different AEAD and must not be used. Apple exposes a [custom suite initializer](<https://developer.apple.com/documentation/cryptokit/hpke/ciphersuite/init(kem:kdf:aead:)>); the [KEM source](https://github.com/apple/swift-crypto/blob/main/Sources/Crypto/HPKE/Ciphersuite/KEM/HPKE-KEM.swift) maps Curve25519 to `0x0020` and a 32-byte encapsulation.

Generate/import `Curve25519.KeyAgreement.PrivateKey` and `.PublicKey(rawRepresentation:)`. Use `HPKE.Sender(recipientKey:ciphersuite:info:)`, `sender.encapsulatedKey`, and `sender.seal(submissionKey)`; use `HPKE.Recipient(privateKey:ciphersuite:info:encapsulatedKey:)` and `recipient.open(ciphertext)` for decryption. The overloads without `authenticating:` use empty AAD. Apple's [HPKE implementation/API source](https://raw.githubusercontent.com/apple/swift-crypto/main/Sources/Crypto/HPKE/HPKE.swift) distinguishes the raw encapsulation and ciphertext and provides these Base-mode APIs. Assemble/split the common 80-byte wrapper at byte 32; never use an AES-GCM combined-box API for HPKE.

The existing Kotlin/Native client has no Swift CryptoKit bridge. Keep this operation in the shared Swift source target and pass only the recovered submission key/encrypted envelope to the existing shared transfer logic, or introduce an explicit injected crypto-provider boundary. Avoid pretending a Kotlin `actual` can directly call arbitrary Swift-only CryptoKit generics. Apple SDK compilation and the iOS 17 minimum-target check remain required on macOS; this Linux review has not run them.

During negative testing, Apple Swift Crypto 3.12.3 accepted an all-zero recipient public input in HPKE seal. The Swift adapter therefore validates public inputs with the provider’s standard X25519 key-agreement operation and rejects all-zero shared secrets before entering HPKE. The guard introduces no custom KDF or encryption construction. Four Linux Swift adapter tests passed, including 14 low-order encodings, context and ciphertext mutations, and browser/Tink fixture opens. The guard must also pass against native CryptoKit on macOS before that platform is considered validated.

### Browser, including LAN HTTP

Installed pins are `hpke@1.1.7` and `@panva/hpke-noble@1.1.7`, with lockfile integrity for transitive dependencies. The [maintainer's 1.1.7 release](https://github.com/panva/hpke/releases/tag/v1.1.7) includes key-cache/import fixes. The published pair resolved together with lifecycle scripts disabled.

Construct `CipherSuite` from `hpke` with **all three** noble factories: `KEM_DHKEM_X25519_HKDF_SHA256`, `KDF_HKDF_SHA256`, `AEAD_AES_256_GCM` from `@panva/hpke-noble`. The [maintainer's noble-suite documentation](https://github.com/panva/hpke/tree/main/examples/noble-suite) explicitly supplies each factory and requires secure randomness through `crypto.getRandomValues`. This is the implemented LAN-compatible provider when `crypto.subtle` is absent; replacing only X25519 would leave WebCrypto-dependent HKDF/AES operations.

Use `GenerateKeyPair`, `SerializePublicKey`, `DeserializePublicKey`, `SerializePrivateKey`/`DeserializePrivateKey`, `Seal(publicKey, key, { info })`, and `Open(privateKey, enc, ciphertext, { info })`. AAD defaults to empty. These are [public CipherSuite APIs](https://github.com/panva/hpke/blob/main/docs/classes/CipherSuite.md); persist private bytes with existing owner-scoped key storage, never in share-link storage. Keep standalone AES/chunk APIs unchanged.

This library tracks the evolving HPKE draft, not a frozen RFC-only API, and its [security policy](https://github.com/panva/hpke/security) explicitly documents that distinction. Adoption is conditional on passing RFC 9180 **Base-mode exact-suite** vectors and cross-library ciphertext tests. Pin versions and treat future upgrades as protocol-relevant. No independent audit of this HPKE composition is established by this review; audited noble primitives alone do not establish one. If these gates fail, select another maintained full HPKE implementation; do not fill the gap with custom ECDH/KDF code or drop LAN/native parity.

`web/src/lib/receive-crypto.ts` implements raw key generation, one-shot submission-key sealing/opening, context construction and bounded envelope encoding/decoding. `web/tests/receive-crypto.test.ts` verifies the pinned [CFRG exact-suite first-message vector](fixtures/hpke-rfc9180-x25519-aes256.json), whose nonempty AAD exercises the provider separately from the production wrapper. The same vector passes noble with and without `crypto.subtle`, and WebCrypto-backed primitives. The [public test wrapper fixture](fixtures/hpke-receive-v2.json) uses the production empty-AAD context. Tests cover randomized wrapping, authenticated manifest roundtrip, wrong identities/keys, tampering, malformed framing and size bounds, absent secure randomness, and no-subtle operation. The browser opens both the independent Tink-generated and Swift-generated empty-AAD wrappers in the shared fixture, and both native provider adapters have opened the browser fixture. Swift tests here use Apple Swift Crypto on Linux, not native CryptoKit. Real non-loopback LAN browser and native device validation remain pending.

## Browser application integration

New receive links contain only the public key (`#v2.<public32>`). The private pair is persisted under origin-, owner-, and inbox-scoped local storage before the invitation is displayed; persistence failure revokes the newly created inbox. Pairing does not synchronize this private material. An owner without the local key can inspect counters/history and revoke the inbox, but receives an explicit missing-key message instead of save controls.

Anonymous upload pages fetch the limited availability response and compare its immutable public key with the link before displaying upload controls and again before creating a submission. Each upload creates a fresh AES key and HPKE wrapper. Guest requests omit ambient cookies and use only their issued write capability; completion recovery uses the dedicated upload-status endpoint. Owner download routes retrieve the envelope with the owner session, verify inbox membership/key identity, and recover the AES key locally. Legacy inboxes remain owner-readable and reject new uploads.

Opening an individual browser submission uses the owner-only exact lookup
`GET /api/v1/slots/{slotID}/transfers/{transferID}/membership`. Its response contains
only `slot_id`, `transfer_id`, `receive_protocol` and `recipient_public_key`.
The browser verifies both identities, version 2 and its locally saved public key
before fetching the encrypted manifest. This lookup does not enumerate sibling
submissions or return counters, private keys or file contents. Missing membership
is rejected; expired/revoked resources are unavailable. Records associated with
multiple inboxes are rejected rather than choosing a parent for authorization or
traffic attribution. The route uses the existing owner session and is not an
additional public receive capability.

Transfer metadata supports at most 100 files, matching the client manifest
ceiling. An indexed 101-row probe rejects oversized historical/restored records
with HTTP 409 `transfer_file_limit_exceeded`, without returning a partial list.
New file allocations enforce the same ceiling atomically before consuming receive
allowances. `MAX_FILES_PER_TRANSFER` may lower the limit; values above 100 or
nonpositive values use 100. Concurrent allocations cannot exceed the effective
count. This guard does not delete existing payloads or change individual file-size
limits. Inbox pagination and whole-inbox summary bounds are separate work; the
exact membership route alone does not make the inbox listing bounded.

Creation controls expose fixed optional per-file download attempts and cumulative receive-file allocations. UI counters come from server responses, including reservations; interrupted download attempts consume allowance, and abandoned file allocations do not restore receive capacity. Regression coverage includes authenticated inbox retrieval, guest denial of child reads, file-limit exhaustion, scanner/version rejection, receive history reopening, and individual/ZIP saving.

Before publishing a new receive invitation, the browser reads owner metadata and verifies the accepted protocol, exact public key and selected file limit. Before uploading standalone files with a selected download limit, it verifies the server accepted that value. A server that silently ignores new fields is rejected and the unused allocation is removed where possible. A metadata transport failure retains the owner allocation/private key for inspection or revocation instead of silently losing management state or publishing an unverified link.

Chromium browser regression tests exercise the complete receive creation, anonymous upload, owner history/decryption and interrupted-save retry flow with `crypto.subtle` removed. This verifies fallback integration but does not replace the pending real LAN HTTP deployment check. Additional browser tests cover missing private keys, substituted public keys, exhausted capacity, compatibility rejection, retained offline management records, and authoritative per-file download counters. The web footer explains the serving-origin/client trust boundary, and HTTP pages visibly recommend trusted HTTPS instead of claiming that encryption makes the connection or files inherently safe.

Available checks passed: `npm run check` (zero errors/warnings), `npm run build`, all 34 Node unit/integration tests (including eight crypto/storage cases), the initial 17 focused Playwright regressions, and seven final compatibility/limit/private-inbox flows. The latter includes the Chromium no-SubtleCrypto workflow. Browser runs use `PSST_TEST_BACKEND_PORT=18789 PSST_TEST_BACKEND_URL=http://127.0.0.1:18789 npm run test:browser` with disposable test servers. Native CryptoKit/device and real LAN checks below remain explicitly pending.

## Required validation before protocol completion

- [ ] Freeze context/envelope bytes and dependency versions with backend, Android, iOS and web implementations; review malformed/unknown-version behavior and legacy read-only migration.
- [ ] Import the [CFRG HPKE test vectors](https://github.com/cfrg/draft-irtf-cfrg-hpke/blob/master/test-vectors.json), pinned to a commit. Select `mode=0`, `kem_id=32`, `kdf_id=1`, `aead_id=2`. Run supported vectors at each provider boundary; Tink's public hybrid API cannot consume vectors with nonempty AAD, so do not falsely claim those vectors tested through it.
- [x] Generate checked-in **public-test-only** fixed-recipient, fixed-context, empty-AAD one-shot fixtures. Use independently maintained implementations to validate them. Where deterministic ephemeral injection is unavailable in a public API, test decryption of known fixtures plus randomized cross-provider roundtrips; do not patch production RNG for determinism.
- [ ] Complete all sender/recipient pairs among Android Tink, iOS CryptoKit, browser noble, and an independent WebCrypto-backed HPKE path. Verify exact 32-byte public key, 32-byte encapsulation, 48-byte sealed key and 80-byte wrapper lengths, and explicit absence of Tink's prefix.
- [ ] Repeat browser tests with `crypto.subtle` removed while retaining `getRandomValues`; test a real non-loopback LAN HTTP browser. Verify missing CSPRNG fails closed. HTTP still cannot authenticate delivered JavaScript.
- [ ] Reject modified `enc`, ciphertext/tag, slot ID, submission ID, public key, suite/version, lengths and trailing data. Reject wrong recipient private key and invalid/low-order X25519 inputs. Test two independent uploaders, envelope swapping and wrong-context replay.
- [ ] Run Android JVM tests and API-26-compatible device/emulator tests; run iOS app and share-extension builds plus XCTest on macOS, including minimum iOS 17 deployment. Linux source review or a Swift Crypto build does not establish that CryptoKit/extension validation passed.
- [ ] Verify key persistence/restore, cancellation and resumed upload envelope reuse, account switching, owner device without private keys, legacy inbox reads and no credentials/private-key/fragment disclosure in network requests or logs.
- [ ] Obtain a bounded independent review of protocol binding, wrappers, key lifecycle and source-level dependency use before declaring section 1 complete.
