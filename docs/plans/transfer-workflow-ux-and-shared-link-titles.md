# Transfer workflow UX and shared link titles

## Status and scope

Planned, 2026-10-05. This document records the agreed direction following the
reported History, receive-link, scanner, Settings and web download regressions.
Writing this plan does not start implementation. All implementation and validation
items below remain pending.

Make choosing, sharing and saving files the main actions. Enforce storage,
traffic, authorization and download safeguards automatically; present details
when they help someone make a decision or resolve a problem.

Include web, backend, shared clients, Android and iOS. Include the iOS share
extension where sending and shared titles apply. Preserve psst.zip branding,
operator-configured URLs, chunked encryption, public-key receive invitations,
private owner inboxes, account isolation and administration-only accounts. Web
scanning remains available to enabled regular users; public link pages and native
guest scanning retain their existing access rules.

This builds on [administration and transfer UX](administration-dashboard-traffic-and-ux.md),
[security and link limits](security-abuse-prevention-and-link-limits.md),
[layout refinements](psst-zip-layout-refinements.md) and
[download acknowledgements](download-acknowledgements.md). It supersedes the
local-only custom-title decision and its prohibition on server-stored custom
titles in the administration plan. It does not permit plaintext filenames,
file contents or encryption keys in server metadata.

Implementation and verification are separate. Missing macOS/Xcode or device
access does not exclude iOS work or require new CI infrastructure. Perform the
checks available locally, record unavailable checks as pending, and provide
reproducible commands and manual steps without claiming unrun checks passed.

## 1. History: one visible list and relevant controls

Android currently exposes both local account and download pagers under Downloaded.
iOS hides the account pager there but still exposes two pagers under All. Both
can display controls when no additional page exists. Filters applied only after
loading a page can also hide matching records elsewhere in the history.

- [ ] Present one chronological list and one Load more control under All in
      on-device History. Merge retained account records and local downloads with
      stable ordering by timestamp and a deterministic identity tie-breaker.
- [ ] Keep each source's cursors internal. Use bounded indexed reads, bounded
      merge buffers and a bounded visible window or equivalent paging strategy;
      never load an entire growing history to merge, sort or filter it.
- [ ] Under Downloaded, load and show only downloads and their navigation.
      Under Sent or Receive links, load and show only the matching account
      records. Server History continues to show account-backed records; choosing
      Downloaded selects the on-device source consistently on both apps.
- [ ] Apply filters to the underlying paged query/loader, including server
      History, instead of filtering only the current page. Reset the appropriate
      cursor on source, filter, server or account changes.
- [ ] Hide pagination when neither an earlier nor a later page exists. Place
      necessary controls after the entries, including server History and inbox
      listings. Do not show disabled First/Previous/Next rows for a single page.
- [ ] Keep all filters discoverable and accessible at narrow widths and enlarged
      text sizes. Use platform-appropriate wrapping, a selector or an obviously
      scrollable row rather than a clipped final filter.
- [ ] Simplify cards to type, meaningful title, one compact date/count summary,
      status and relevant actions. Remove duplicate dates/counts and permanent
      unlimited-policy descriptions; put technical identifiers, allowance
      accounting and key availability details behind Details.
- [ ] Preserve saved-file Open, partial-save resume, receipt retry, rename,
      remote revocation, local-only removal and offline access. Keep actionable
      missing-key or unavailable-file states visible without repeated paragraphs.

## 2. Receive links: creation and the active inbox are separate states

The current web screen keeps the new-link form above received files after a link
exists. Mobile puts the QR and accounting controls above the optional name and
file actions. Separate these states instead of continually appending controls.

- [ ] Before creation, show the optional shared link title, collapsed Link limits
      and a primary Create receive link action. Keep naming visible without
      scrolling at ordinary phone sizes; naming remains optional.
- [ ] After creation/opening an existing link, replace the creation form with an
      active-inbox view. Show its title, concise state and received-file actions.
      Do not leave a second title/limits/create-another form above the inbox.
- [ ] While no files have arrived, show the QR prominently with Copy link and
      Share. Once files arrive, prioritize the received-file list and Save actions;
      keep a clear Show QR / Share link action opening an accessible sheet,
      dialog or expandable panel. Preserve the approved QR size and quiet zone.
- [ ] Make arrivals obvious through a visible received count and the existing
      automatic refresh. Keep one manual Refresh action near the file list;
      remove duplicate refresh buttons and internal allocation prose.
- [ ] Offer Rename in the inbox header/menu. Create another link opens a fresh
      creation form through a secondary action, without discarding access to the
      previous inbox or accidentally submitting twice.
- [ ] Show only relevant remaining capacity, such as 3 files remaining, when a
      limit is enabled. Keep allocation-versus-completion mechanics in Details
      and documentation; retain accurate enforcement and actionable full/paused
      states. Allow already admitted uploads to finish under the existing policy.
- [ ] Correct the web optional-limit checkbox to a left-aligned inline
      checkbox-and-label control with a comfortable hit target and visible focus.
      Scope the styling so other form fields retain their intended layout.
- [ ] Hide single-page inbox navigation on web and both apps. Preserve bounded
      inbox reads, live refresh, event/poll cancellation and cursor correctness
      after arrivals or deletions.

## 3. Scanned receive links: put sending first

- [ ] After recognizing a valid receive invitation, stop/release the scanner and
      transition to a dedicated Send files state. Remove camera, paste, QR-image
      and unrelated history/report controls from above the upload task.
- [ ] Show the shared title and destination server, a file picker/list using the
      available height, and prominent Add files / Send files actions anchored
      within reach. Ensure the keyboard, safe areas and enlarged text do not
      conceal the actions or file list.
- [ ] Reuse the main sending layout and behavior where appropriate, while
      retaining the receive link's scoped submission capability and guest access.
      Do not require configuring an account to submit through a valid public link.
- [ ] Present a short applicable size/count summary. Keep capacity checks,
      filename safety, encryption and cleanup automatic; display failures beside
      the action needed to resolve them.
- [ ] Place Scan again, transfer details, Report and existing History access in
      secondary navigation or an overflow menu. Keep reporting discoverable and
      receipt failures recoverable without permanent diagnostic blocks.
- [ ] Preserve drafts and active transfer continuity across navigation. Back must
      leave the result/upload screen predictably, without restarting recognition
      or returning through a transient In progress screen.

## 4. Settings and explanations

- [ ] Give Android a dedicated Usage/Traffic destination equivalent to iOS.
      The main Settings screen shows a compact row, optionally with a relevant
      remaining allowance, rather than expanded accounting diagnostics.
- [ ] Group Settings into concise appearance, server/account, usage and help
      destinations. Keep administrative traffic dashboards separate from regular
      users' account usage and preserve role-based access.
- [ ] Remove the permanent browser-download free-space advisory. Retain storage
      preflight, bounded staging, cleanup and useful no-space/unsupported-save
      errors. Show large-transfer confirmation or fallback choices only when the
      current transfer and platform actually require a decision.
- [ ] Review the affected transfer, History and Settings copy for repeated
      technical/security explanations. Move accounting definitions and operating
      details into expandable Details, the Usage screen or documentation.
- [ ] Keep concise notices for real HTTP exposure, unknown destinations,
      exhausted budgets, missing keys and destructive actions where relevant.
      Simplify presentation without suppressing a current failure, necessary
      consent or important connection condition. Ordinary successful flows
      should not accumulate warning paragraphs.

## 5. Owner downloads retain their workspace context

Received submissions currently open the standalone download layout. Owner
authentication still exists, but the missing account/navigation context makes
the page look signed out.

- [ ] Retain the authenticated regular-user workspace and a clear Back to
      received files action when saving an owner inbox submission. Restore the
      originating inbox, filter/page and useful scroll position on return.
- [ ] Share the file-saving implementation between contextual owner pages and
      lightweight public download pages. Avoid duplicate decryption or receipt
      logic; opening an owner submission must not sign the user out.
- [ ] Preserve owner authorization, inbox membership checks and local private-key
      requirements. Logged-in appearance must not grant access to someone else's
      inbox, synchronize keys implicitly or expose other submissions to guests.
- [ ] Keep direct public send links usable without an account. Use appropriate
      role-aware account navigation when present; do not give administrators
      regular-user transfer pages through the contextual shell.
- [ ] Use Save file for single-file headings/actions where applicable and Save
      files for multiple files. Keep Save again and truthful saved/receipt status.

## 6. Shared link titles across clients and link pages

The optional user-entered title becomes shared, server-readable descriptive
metadata. It is not an encrypted filename and must not be populated automatically
from private filenames. Files, manifests containing filenames, and private keys
retain their existing encryption and ownership rules.

- [ ] Add an optional bounded title to persisted standalone transfers and receive
      links, creation/read models and an owner-authorized rename/clear operation.
      Define one validation contract across web, shared clients, Android and iOS:
      trim surrounding whitespace, treat blank as absent, support Unicode, reject
      control characters and enforce a documented limit of 200 Unicode scalar
      values plus a bounded UTF-8/request size.
- [ ] Enforce rename ownership server-side for cookie and bearer sessions;
      public link holders cannot rename resources. Preserve account state/role
      restrictions and administrative operations without extending public rights.
- [ ] Display the title consistently in owner History, active inboxes, send
      results, public download/receive-upload pages and scanned transfer screens.
      Preserve explicit Sent / Receive link / Downloaded types and useful
      fallbacks when no shared title exists.
- [ ] Use safe text rendering and accessible truncation; never interpret titles
      as HTML, filesystem paths or authorization identifiers. Keep stable opaque
      URLs and key fragments; a rename must not invalidate a link or QR code.
- [ ] Let newly downloaded local records retain a snapshot of the shared title
      for offline History. Keep their downloaded filenames and local record
      identity distinct from the editable source-link title.
- [ ] Keep receive-submission titles contextual to the parent inbox only where
      that invitation or owner authorization already permits access. Do not
      expose child manifests, sender filenames or unrelated submissions through
      a new title endpoint.
- [ ] Preserve existing local labels without automatically publishing them.
      Prefer a server title when available; retain legacy local labels as local
      fallbacks until their owner explicitly saves a shared title. Use concise
      input copy such as Shown to people using this link and document the revised
      metadata privacy boundary.
- [ ] Include optional shared titles in applicable main-app and iOS extension
      sending flows without making naming mandatory or expanding the primary
      file-selection task into a busy form.
- [ ] Keep custom titles out of routine traffic/audit logs and aggregates. Never
      add plaintext filenames, contents or key material as part of this change.

## 7. Exhausted download links close automatically

The server already atomically spends per-file download allowances before
streaming and blocks further attempts. Cleanup removes fully exhausted payloads,
but the remaining transfer metadata still resembles an active completed link.
Automatic closure must differ from manual revocation, which can cancel streams.

- [ ] Expose a consistent inactive state and Download limit reached reason once
      every file in a finalized, non-empty limited transfer has exhausted its
      allowance. Zero/unset limits remain unlimited; exhausting one file leaves
      other files available until their own allowances are exhausted.
- [ ] Make the transition atomic with allowance admission or derive it from an
      authoritative consistent snapshot. Keep it correct across concurrent last
      attempts, restarts, expiry, manual revocation and cleanup retries.
- [ ] Deny fresh payload access immediately after exhaustion while allowing the
      already admitted final responses to finish. Reuse reader-aware cleanup;
      do not call the ordinary cancel-all-streams revocation path at admission.
- [ ] Remove exhausted payloads after final readers drain. Retain only the
      bounded metadata/tombstones needed for owner History and legitimate final
      receipt processing; preserve existing retention and cleanup policies.
- [ ] Show the closed state across web and both apps. Public reopening gives a
      concise unavailable-link message; History preserves the reason and offers
      creating a replacement rather than misleading active Share/Download actions.
- [ ] Keep delivery acknowledgements independent. An interrupted final attempt
      still consumes its allowance but must not be labeled successfully saved.
      Account/traffic totals remain truthful and are not refunded by cleanup.

## Implementation order and verification

Use the existing History stores, page loaders, shared transfer models and save
flows rather than adding parallel stores. Primary entry points include web
`+page.svelte`, `d/[transferId]/+page.svelte`, `OptionalLimit.svelte` and the shared
layout; mobile History, Receive, Scan and Settings views/view models; shared API
models; and backend transfer/slot persistence, admission and cleanup.

- [ ] Implement title persistence/API and the exhausted-state contract first,
      with compatible reads for older records lacking titles and nullable policy
      metadata. Keep the existing cryptographic protocol unchanged.
- [ ] Implement the bounded/filter-aware History presentation, receive states,
      scanned upload layout, settings destinations and contextual downloads
      across web, Android and iOS.
- [ ] Add focused title tests for owner/public/other-user boundaries, safe Unicode
      rendering, validation, clearing/renaming, cross-device reads and deliberate
      publication of an existing local label.
- [ ] Add focused pagination tests with interleaved sources, identical timestamps,
      matching records beyond the first page, single-page/empty collections,
      arrivals/deletions, account switching and bounded loading/window behavior.
- [ ] Add last-download lifecycle tests covering multi-file partial exhaustion,
      concurrent last attempts, a slow authorized final reader, interruption,
      restart, cleanup failure/retry and receipts after exhaustion.
- [ ] Verify web phone/desktop layouts in both appearances, keyboard/focus,
      narrow/enlarged-text navigation, checkbox alignment, received-file
      discoverability, retained login context and return-to-inbox behavior.
- [ ] Verify Android empty/populated History, receive creation/arrivals, scanned
      guest upload and Settings at ordinary and enlarged text sizes. Run affected
      shared/Android tests and build the updated APK.
- [ ] Implement iOS parity and run available formatting/source checks. Record
      native app/share-extension build, XCTest and device checks separately;
      leave them pending if macOS or a relevant device is unavailable.
- [ ] Run appropriate backend, web type/build and focused browser checks. Record
      commands/results against the final changes without repeating unrelated
      security-plan audits or treating source inspection as runtime validation.
- [ ] Format changed files, verify each completion claim, update this plan's
      checkboxes/evidence and commit the implementation and plan together.
      When delivering a development update, preserve configured URLs, data,
      accounts and policies; verify the real transfer paths and provide the APK.
