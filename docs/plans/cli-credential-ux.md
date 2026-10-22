# CLI credential storage and sign-in UX

Created: 2026-10-21. Status: complete on `feat/cli`, with the external validation limits below.

## User outcome

Sign in once and reuse the saved session and local encryption keys on subsequent
commands. Remember the selected storage mode, integrate with the available desktop
wallet, and explain setup failures without requiring users to understand the vault
implementation. Keep the existing 30-day server-session expiry and explicit renewal.

This is a focused follow-up to the completed CLI implementation. It changes no
backend API, mobile source, encryption format or account permissions. Android/iOS
wire compatibility and their previously pending native checks remain unchanged.
It does not add plaintext credential fallback, a background daemon, automatic
session renewal, releases or a GitHub push.

## Implementation

- [x] Default CLI storage selection to `auto`: reuse the existing encrypted vault's
      recorded mode; prefer an OS wallet for a new vault. Explicit mode overrides
      remain available and mismatches produce a useful diagnostic without modifying
      existing state.
- [x] Activate the installed KWallet desktop service and use its Secret Service
      interface. Select and retain the provider consistently, read/write only this
      CLI's master-key entry, and never replace a missing or inaccessible existing master with a new key.
- [x] Allow desktop wallet activation/unlocking for interactive commands. JSON and
      noninteractive execution reuse only already available/unlocked providers and
      never start a wallet or display a prompt. Bound calls by command deadlines.
- [x] Remember the successful provider in vault metadata; retain compatibility with
      earlier vaults that have no provider hint. Keep ciphertext, private keys,
      session tokens and encrypted backups protected throughout failure/retry paths.
- [x] Reuse a remembered passphrase mode without repeating `--vault-mode` on every
      command. Explicitly chosen passphrase storage still requires its passphrase;
      it is an alternative for systems without a desktop wallet.
- [x] Improve help and account/storage diagnostics so users can see the storage
      choice and distinguish initial wallet unlocking from server-session renewal.
      Preserve the project URL in help and keep secrets out of diagnostics.
- [x] Reuse the remembered origin and username when `auth login` renews an expired
      or signed-out profile. Require explicit login details for a different server.

## Verification and delivery

- [x] Add small meaningful regressions for remembered modes, explicit-mode conflicts,
      provider selection/pinning, locked or missing masters, cancellation, and
      KWallet calls that preserve existing keys. Test JSON/headless prompt refusal.
- [x] Run focused Go vet/race checks and compile affected macOS/Windows adapters;
      keep native execution explicitly pending when not available. No unrelated
      application suites or long-running performance campaign is required.
- [x] Check actual desktop service availability using metadata only. Do not read
      unrelated credentials or create real wallet entries during automated tests.
      Record any first-use wallet interaction the user must complete privately.
- [x] Update `docs/cli.md`, format changes, run repository security hooks, commit
      locally and rebuild `client/psst`. Preserve the untracked research drafts and
      keep the binary out of commits. Clean task-owned temporary files.

## Evidence and limitations

The current machine has KWallet 6 installed. No Secret Service or KWallet daemon
was running during the initial availability check; Secret Service was not
activatable. No CLI vault existed, and no credential values were inspected.

Activating `org.kde.kwalletd6` exposed `org.freedesktop.secrets`; its default
collection exists and was locked at the initial check. Automated checks did not
read or create any real wallet entry. The user subsequently completed a real
interactive account login: the CLI reported authenticated keyring storage through
`secret-service`, and later resource commands successfully reused that saved
session. Real Linux credential persistence/reuse is therefore user-verified; the
specific desktop unlock-dialog interaction was not observed by the agent. The
wallet's lock policy still applies after login.

Go vet and the complete affected state/CLI race suites passed (1.285s and 1.077s).
The Unix adapter tests use disposable DBus services, including early prompt
completion, cancellation dismissal, owner replacement and KWallet activation;
they do not depend on the user's wallet. Remembered-mode/provider and CLI tests
cover session reuse, expired-session key retention, renewal defaults, origin
isolation and noninteractive passphrase guidance.

The compact real backend/production-TypeScript interoperability fixture passed in
7.8s using automatic storage selection and private passphrase descriptors. It
covers bidirectional Send/Receive encryption, multiframe and empty files, encrypted
Receive-key recovery, receipt retry, expiry, revocation and allowances. No network
service outside the disposable loopback fixture was used.

macOS arm64 and Windows amd64 state tests and CLI binaries cross-compiled. Native
Keychain/Credential Manager execution remains pending on those operating systems;
cross-compilation is not native execution. Existing session expiry and all server
and mobile wire formats remain unchanged.

One independent bounded review found no material issues in mode/TTY selection,
origin-bound renewal, existing-master preservation, authenticated metadata
migration, prompt lifecycle and cancellation. Outcome: **CLOSED_WITH_DEFERRALS**
for the remaining native/graphical checks; no further discovery or test
campaign is needed for this scope. The repository's staged security/format checks
passed at local commit. `client/psst` was rebuilt and its version/help checked;
the binary is ignored by Git. Task-owned temporary files were removed, and both
untracked store-release research drafts were preserved. Nothing was pushed.
