# Web appearance, login, icons, and configuration names

## Scope

Add explicit appearance control to all web pages, refine the login form, use a consistent freely licensed icon set, and align branded deployment/test environment variables with psst.zip. Preserve authentication, operator-configured URLs and existing protocol/storage identities. These changes concern web and deployment; native app appearance controls are outside this request.

- [x] Add a visible Light / Dark / System choice on login, authenticated and public pages. Persist it locally, apply it before first paint, honor system changes in System mode, and handle unavailable storage safely.
- [x] Refine login hierarchy and put a Show/Hide password eye button inside the right edge of the input. Preserve accessible names, password-manager autocomplete, keyboard submission and field contents.
- [x] Adopt a consistent, freely licensed icon library for navigation and common actions. Retain license notices in source and distributed static assets; keep text labels where icons alone would be unclear.
- [x] Make `PSST_DOMAIN` and `PSST_TEST_*` / `PSST_EXPECT_INSECURE_CONTEXT` canonical in deployment examples and browser tooling, with documented legacy compatibility and precedence. Preserve existing deployment URLs and secrets.
- [x] Verify appearance persistence/overrides/system behavior, eye-button interaction, login and existing transfer workflows; inspect login/header at desktop/phone widths in both themes and enlarged text. Check configuration variants, format, build, deploy, commit and clean task-owned temporary artifacts.

## Evidence

Svelte checks passed with zero errors/warnings, and the production build passed. The 34-case browser run passed 29 cases; five download cases failed only because their raw text assertion included whitespace beside the new decorative icon. Their helper now checks the exact accessible button name. All eight download cases passed on the focused rerun, covering all 34 browser scenarios across the two runs.

New browser cases verify explicit light/dark overrides, reload persistence on public links, cross-tab preference synchronization, automatic system changes, blocked storage, eye-button geometry and keyboard interaction, and Enter-to-sign-in. Existing account isolation, pairing, revocation modal, upload continuity and download receipt checks also ran.

Inspected the login/header at 1440×1000 and 390×844 in both themes and at 200% text. The header wraps when needed; no horizontal overflow was observed. The saved appearance is applied in the document head before styles render. The full Lucide license was verified at `/licenses/lucide.txt`; icons are bundled locally.

Configuration verification covered five Compose cases (default, legacy, canonical, both, empty canonical), default-LAN and named-host Caddy adaptation, and four browser-config precedence cases. Compose keeps the legacy domain alias; bare Caddy users must use `PSST_DOMAIN`, as documented. The private development `.env` key was migrated to `PSST_DOMAIN` without changing its value or other settings.

Deployed to **http://192.168.178.29** using the canonical domain variable, preserving existing data and addresses. Live smoke checks passed API health, the saved appearance after reload, the password eye, the public-page selector and the license endpoint. The disposable test deployment and task-owned temporary files were removed.
