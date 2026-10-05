# Shared link titles

An optional title deliberately entered by a link's creator is server-readable
metadata. It appears in account History, invitations, link pages and mobile
scanning flows. The server stores the title alongside the transfer or receive
link, so it is also readable in database backups. Routine traffic and security
activity records do not include the title.

Titles do not replace filenames. Filenames remain inside encrypted manifests;
clients never automatically copy a private filename into shared metadata. Existing
local names are preserved as local fallbacks and are not automatically uploaded.
Explicitly saving a name through Rename publishes it as a shared title. Clearing
the title removes it from current server responses, but cannot erase previously
downloaded snapshots or backups.

Only an authorized owner can create, rename or clear a resource's title. Public
link holders can read the corresponding title but cannot change it. A receive
submission inherits its parent title only on already-authorized owner reads;
invitation holders gain no access to other submissions, manifests or private keys.
Titles are rendered as text and never used as filenames, paths or authorization
identifiers. URLs and key fragments remain stable after a rename.

The validation contract rejects Unicode control characters and unpaired
surrogates before trimming Unicode whitespace. Blank titles become absent.
Nonblank titles permit at most 200 Unicode scalar values and 800 UTF-8 bytes.
Web, shared mobile clients and backend enforce the same contract. Public pages
use a generic fallback when there is no title; saved mobile downloads retain a
snapshot independently of their saved filenames and paths.

Receiving links continue to expose only their public encryption key in the URL
fragment. Adding shared titles does not change the
[receive encryption protocol](receive-crypto-design.md).
