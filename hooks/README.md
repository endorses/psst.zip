# Repository checks

Install the checked-in Git hooks before contributing:

```sh
./install-hooks.sh
```

The installer enables `hooks/` for this checkout using Git's local
`core.hooksPath`. It refuses to replace a different hooks configuration or active
commit/push hooks. Installation is local: cloning this repository does not
automatically enable hooks.

Install Python 3, Git, Go (including `gofmt`), and
[Gitleaks](https://github.com/gitleaks/gitleaks). CI uses Gitleaks `v8.30.1`; install
that release from its official distribution, or build it with Go:

```sh
go install github.com/zricethezav/gitleaks/v8@v8.30.1
```

Ensure the installed executable is on `PATH`. For web changes, install the
locked Node dependencies first:

```sh
cd web
npm ci
```

## Checks at commit and push

Pre-commit scans staged changes for secrets, rejects sensitive/generated files
and oversized blobs, checks whitespace/conflicts, and checks changed Go/web files
for formatting. It reads staged file content, so an unstaged correction does not
hide a problem in the actual commit. Checks never format or restage files for you.
Gitleaks and applicable formatters must be installed; missing tools block checks.

Pre-push scans all reachable local Git history, including secrets removed in later
commits. It also checks current and historical file paths and sizes, including
deleted files. For this small repository,
scanning all history avoids missing leaks on a first push or a new branch.

Run the checks manually from the repository root:

```sh
python3 tools/check_repository.py staged
python3 tools/check_repository.py files
python3 tools/check_repository.py history
python3 -m unittest discover -s tools -p 'test_repository_checks.py' -v
```

GitHub CI repeats the tracked-file and full-history checks and runs the checker
regression tests. The existing backend lint/race tests, web tests, Android/shared
checks, and native iOS app/share-extension/XCTest jobs remain separate gates.
Full builds and dependency vulnerability review belong in CI/release review,
rather than every local commit. See the
[deployment security guidance](../docs/security/deployment.md#updates-and-vulnerability-review).

## Findings and exceptions

Scanner output is redacted. Default Gitleaks detectors remain enabled, and inline
`gitleaks:allow` comments do not bypass them. Scan configuration and the explicit
ignore file are taken from the index, so an unstaged local exception cannot hide
staged credentials. CI checks the committed configuration.

`.gitleaksignore` contains seven exact historical findings that were individually
verified as public test fixtures: a malformed cursor, a deterministic TOTP seed,
an HPKE interoperability vector shared by web/Android/iOS, and an ephemeral
browser-test password. It does not exempt test files, directories, credential
types, or future occurrences. Editing those fixtures can therefore trigger a new
finding and needs review.

For a real credential, revoke/rotate it and remove it from the proposed commit.
A credential already in history needs removal from the history that will be
published; simply deleting the current file does not pass the pre-push scan. Do
not put actual credentials into scanner reports, issues, or exception files.

For a verified intentional fixture, explain its purpose and use the narrowest
reviewed exception. Never disable a detector or ignore an entire test directory
to make a scan pass. Exact historical fingerprints include the commit, path,
rule, and line; they are not reusable exemptions for newly staged findings.

The file policy rejects local `.env` files (keeping `.env.example` available),
database state/journals, private credential stores, generated transfer data and
mobile build artifacts, and blobs larger than 5 MiB. Review any needed public
fixture explicitly rather than forcing it through with `git add -f`.

Git hooks can be bypassed or uninstalled; they are an early local check, not the
entire publication boundary. Require the CI secret-scan status before merging,
and keep secrets in operator-local configuration or GitHub environment secrets.
