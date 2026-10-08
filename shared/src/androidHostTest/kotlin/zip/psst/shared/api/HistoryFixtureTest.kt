package zip.psst.shared.api

import java.io.File
import kotlin.test.*
import kotlinx.serialization.json.*

class HistoryFixtureTest {
    @Test
    fun frozenBackendFixtureUsesTheSameStrictDecoder() {
        val root =
            Json.parseToJsonElement(
                    File("../docs/testing/fixtures/history-sync-v1.json").readText()
                )
                .jsonObject
        val snapshot = decodeResourcePage(root.getValue("snapshot").jsonObject, null, 50)
        val cursor = requireNotNull(snapshot.syncCursor)
        assertEquals(2, snapshot.transfers.single().revision)
        assertEquals("Urlaubsbilder", snapshot.slots.single().title)
        val empty = decodeHistoryChanges(root.getValue("empty").jsonObject, cursor, 50)
        assertTrue(empty.changes.isEmpty())
        assertEquals(cursor, empty.nextCursor)
        val updated = decodeHistoryChanges(root.getValue("upsert").jsonObject, cursor, 50)
        assertEquals("Renamed files", updated.changes.single().resource!!.transfers.single().title)
        val removed =
            decodeHistoryChanges(root.getValue("remove").jsonObject, updated.nextCursor, 50)
        assertNull(removed.changes.single().resource)
        val unknown =
            decodeHistoryChanges(
                root.getValue("unknown_summary").jsonObject,
                removed.nextCursor,
                50,
            )
        assertFalse(unknown.changes.single().resource!!.slots.single().summary!!.ready)
        assertEquals(snapshot.generation, unknown.generation)
    }
}
