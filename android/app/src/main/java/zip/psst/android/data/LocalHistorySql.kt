package zip.psst.android.data

internal const val ACCOUNT_LOCAL_PAGE_SQL =
    "SELECT * FROM transfer_history WHERE accountId = :accountId AND originScope = :originScope AND (createdAt, id) < (:beforeTime, :beforeId) ORDER BY createdAt DESC, id DESC LIMIT 51"
internal const val ACCOUNT_SCOPE_COLUMN_SQL =
    "ALTER TABLE transfer_history ADD COLUMN originScope TEXT NOT NULL DEFAULT ''"
internal const val ACCOUNT_SCOPE_BACKFILL_SQL =
    "UPDATE transfer_history SET originScope = lower(rtrim(trim(serverUrl), '/'))"
internal const val ACCOUNT_PAGE_INDEX_SQL =
    "CREATE INDEX index_transfer_history_accountId_originScope_createdAt_id ON transfer_history(accountId, originScope, createdAt, id)"

internal val GUEST_HISTORY_SCHEMA =
    listOf(
        "CREATE TABLE migration (id INTEGER PRIMARY KEY CHECK(id=1), complete INTEGER NOT NULL DEFAULT 0)",
        "INSERT INTO migration(id) VALUES(1)",
        "CREATE TABLE records (kind TEXT NOT NULL, identity TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT 0, pending INTEGER NOT NULL DEFAULT 0, deleted INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0, payload TEXT, PRIMARY KEY(kind, identity))",
        "CREATE INDEX guest_history_page ON records(kind, deleted, created_at DESC, identity DESC)",
        "CREATE INDEX guest_history_errors ON records(failed, deleted)",
        "CREATE INDEX guest_history_pending ON records(kind, deleted, failed, pending, identity)",
    )
internal const val GUEST_INSERT_FIELDS =
    "INTO records(kind,identity,created_at,pending,deleted,failed,payload) VALUES(?,?,?,?,0,?,?)"
internal const val GUEST_IMPORT_SQL = "INSERT OR IGNORE $GUEST_INSERT_FIELDS"
internal const val GUEST_SAVE_SQL = "INSERT OR REPLACE $GUEST_INSERT_FIELDS"
internal const val GUEST_DELETE_SQL =
    "INSERT OR REPLACE INTO records(kind,identity,deleted) VALUES(?,?,1)"
internal const val GUEST_FIRST_PAGE_SQL =
    "SELECT identity, created_at, payload FROM records WHERE kind='json' AND deleted=0 ORDER BY created_at DESC, identity DESC LIMIT 51"
internal const val GUEST_NEXT_PAGE_SQL =
    "SELECT identity, created_at, payload FROM records WHERE kind='json' AND deleted=0 AND (created_at, identity) < (?, ?) ORDER BY created_at DESC, identity DESC LIMIT 51"
