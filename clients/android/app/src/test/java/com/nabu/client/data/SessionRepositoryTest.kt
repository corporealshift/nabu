package com.nabu.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.nabu.client.net.SessionSummary
import com.nabu.client.protocol.Event
import com.nabu.client.protocol.NabuJson
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class SessionRepositoryTest {

    private lateinit var db: MirrorDb
    private lateinit var repo: SessionRepository
    private var clock = 1000L

    @Before
    fun open() {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        db = Room.inMemoryDatabaseBuilder(ctx, MirrorDb::class.java)
            .allowMainThreadQueries().build()
        repo = SessionRepository(db) { clock++ }
    }

    @After
    fun close() = db.close()

    private fun event(id: String, type: String = "message", body: String = """{"role":"user","content":"hi"}""") =
        NabuJson.decodeFromString(
            Event.serializer(),
            """{"id":"$id","type":"$type","timestamp":"2026-09-16T00:00:00Z","data":$body}""",
        )

    /** Empty and missing must not look alike (spec 15). */
    @Test
    fun `a listed but unfetched session is recorded as unsynced`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))

        val row = db.sessions().get("S1")!!
        assertFalse("a never-fetched session must not claim to be synced", row.synced)
        assertEquals("", row.cursor)
        assertEquals(0, db.events().count("S1"))
    }

    /** Listing again must not throw away a mirror already fetched. */
    @Test
    fun `relisting does not reset an existing cursor`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))
        db.append("S1", listOf(EventRow("E1", "S1", 1, "message", "{}")), "E1", true)

        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "running")))

        val row = db.sessions().get("S1")!!
        assertEquals("E1", row.cursor)
        assertTrue(row.synced)
        assertEquals("running", row.state)
    }

    /** Ordinals continue, so a later batch cannot sort above an earlier one. */
    @Test
    fun `events keep log order across separate batches`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))
        repo.apply("S1", listOf(event("E1"), event("E2")), synced = false)
        repo.apply("S1", listOf(event("E3")), synced = true)

        assertEquals(listOf("E1", "E2", "E3"), db.events().all("S1").map { it.id })
    }

    /** Being behind has to survive going offline. */
    @Test
    fun `an unsynced flag is stored and readable with no connection`() = runBlocking {
        repo.apply("S1", listOf(event("E1")), synced = false)

        assertFalse(db.sessions().get("S1")!!.synced)
        assertFalse(repo.watchSession("S1").first()!!.synced)
    }

    @Test
    fun `a state change updates the session row`() = runBlocking {
        repo.apply("S1", listOf(
            event("E1", "state_change", """{"to":"running","reason":"prompt"}"""),
        ), synced = true)

        assertEquals("running", db.sessions().get("S1")!!.state)
    }

    /** Replaying an event the mirror already has must not duplicate it. */
    @Test
    fun `replayed events do not duplicate`() = runBlocking {
        repo.apply("S1", listOf(event("E1"), event("E2")), synced = true)
        repo.apply("S1", listOf(event("E1"), event("E2")), synced = true)

        assertEquals(2, db.events().count("S1"))
    }

    /** A prompt exists locally before any send, which is the outbox's point. */
    @Test
    fun `queueing a prompt writes it before sending`() = runBlocking {
        repo.queuePrompt("S1", "deploy it", "C1")

        val pending = db.outbox().pending()
        assertEquals(1, pending.size)
        assertEquals("deploy it", pending[0].content)
        assertEquals("C1", pending[0].clientId)
        assertEquals(0, db.events().count("S1"))
    }

    @Test
    fun `pending prompts are visible per session`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/a", "idle"),
            SessionSummary("S2", "C:/b", "idle")))
        repo.queuePrompt("S1", "one", "C1")
        repo.queuePrompt("S2", "two", "C2")

        assertEquals(listOf("one"), repo.watchPending("S1").first().map { it.content })
    }
}
