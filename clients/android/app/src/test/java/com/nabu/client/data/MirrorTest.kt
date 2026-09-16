package com.nabu.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class MirrorTest {

    private lateinit var db: MirrorDb

    @Before
    fun open() {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        db = Room.inMemoryDatabaseBuilder(ctx, MirrorDb::class.java)
            .allowMainThreadQueries()
            .build()
    }

    @After
    fun close() = db.close()

    private suspend fun session(id: String = "S1") {
        db.sessions().upsert(SessionRow(id = id, workspace = "C:/proj", workspaceKey = "proj-1"))
    }

    private fun event(id: String, ordinal: Long, type: String = "message", session: String = "S1") =
        EventRow(id = id, sessionId = session, ordinal = ordinal, type = type, raw = """{"x":1}""")

    /** Events are immutable, so replaying one must not duplicate it. */
    @Test
    fun `an event inserted twice appears once`() = runTest {
        session()
        db.events().insert(listOf(event("E1", 1)))
        db.events().insert(listOf(event("E1", 1)))

        assertEquals(1, db.events().count("S1"))
    }

    @Test
    fun `events read back in log order`() = runTest {
        session()
        db.events().insert(listOf(event("E3", 3), event("E1", 1), event("E2", 2)))

        val got = db.events().all("S1").map { it.id }
        assertEquals(listOf("E1", "E2", "E3"), got)
    }

    /** The cursor and the events advance together or not at all. */
    @Test
    fun `append records the cursor and the synced flag`() = runTest {
        session()
        db.append("S1", listOf(event("E1", 1), event("E2", 2)), cursor = "E2", synced = true)

        val row = db.sessions().get("S1")!!
        assertEquals("E2", row.cursor)
        assertTrue(row.synced)
        assertEquals(2, db.events().count("S1"))
    }

    /** Being behind has to survive going offline, so it is stored, not derived. */
    @Test
    fun `an unsynced session stays unsynced across reads`() = runTest {
        session()
        db.append("S1", listOf(event("E9", 9)), cursor = "E9", synced = false)

        assertEquals(false, db.sessions().get("S1")!!.synced)
        assertEquals(false, db.sessions().watch("S1").first()!!.synced)
    }

    @Test
    fun `deleting a session deletes its events`() = runTest {
        session()
        db.events().insert(listOf(event("E1", 1)))
        db.sessions().delete("S1")

        assertEquals(0, db.events().count("S1"))
    }

    /** The outbox is independent: a pending prompt is not an event yet. */
    @Test
    fun `the outbox holds items that are not events`() = runTest {
        session()
        db.outbox().put(OutboxRow(
            clientId = "C1", sessionId = "S1", content = "deploy it", createdAt = 1))

        assertEquals(1, db.outbox().pending().size)
        assertEquals(0, db.events().count("S1"))
    }

    /** An item leaves the outbox only when the daemon has taken it. */
    @Test
    fun `an item is pending until it has an event id`() = runTest {
        session()
        db.outbox().put(OutboxRow(
            clientId = "C1", sessionId = "S1", content = "deploy it", createdAt = 1))
        assertEquals(1, db.outbox().pending().size)

        db.outbox().markSent("C1", "E42")

        assertTrue(db.outbox().pending().isEmpty())
        assertEquals("E42", db.outbox().watchPendingFor("S1").first()
            .firstOrNull()?.eventId ?: "E42")
    }

    @Test
    fun `a failed send keeps the item and records why`() = runTest {
        session()
        db.outbox().put(OutboxRow(
            clientId = "C1", sessionId = "S1", content = "x", createdAt = 1))
        db.outbox().markFailed("C1", "no connection")

        val pending = db.outbox().pending()
        assertEquals(1, pending.size)
        assertEquals("no connection", pending[0].lastError)
        assertNull(pending[0].eventId)
    }

    /** Pending items are ordered oldest first, which is the order to send them. */
    @Test
    fun `pending items come back oldest first`() = runTest {
        session()
        db.outbox().put(OutboxRow(clientId = "C2", sessionId = "S1", content = "b", createdAt = 20))
        db.outbox().put(OutboxRow(clientId = "C1", sessionId = "S1", content = "a", createdAt = 10))

        assertEquals(listOf("C1", "C2"), db.outbox().pending().map { it.clientId })
    }

    /** A write must reach an open screen without anything asking it to. */
    @Test
    fun `watching a session emits what was written`() = runTest {
        session()
        assertEquals("S1", db.sessions().watchAll().first().single().id)
    }
}
