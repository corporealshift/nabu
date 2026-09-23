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

    private fun event(
        id: String,
        type: String = "message",
        body: String = """{"role":"user","content":"hi"}""",
        at: String = "2026-09-16T00:00:00Z",
    ) =
        NabuJson.decodeFromString(
            Event.serializer(),
            """{"id":"$id","type":"$type","timestamp":"$at","data":$body}""",
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

    /** Relisting on every reconnect must not empty the mirror it just filled. */
    @Test
    fun `relisting keeps the events already mirrored`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))
        db.append("S1", listOf(EventRow("E1", "S1", 1, "message", "{}")), "E1", true)

        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))

        assertEquals("relisting must not delete mirrored events", 1, db.events().count("S1"))
    }

    /** A cursor with nothing behind it would ask for events after the end. */
    @Test
    fun `a cursor with an empty mirror is discarded`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))
        db.sessions().markSynced("S1", "E9", true, clock++)

        assertEquals("", repo.cursorFor("S1"))
    }

    /** Two sessions in one workspace are told apart by what was last asked. */
    @Test
    fun `a session summarises the last thing the user asked`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))
        repo.apply(
            "S1",
            listOf(
                event("E1", body = """{"role":"user","content":"first question"}"""),
                event("E2", body = """{"role":"assistant","content":"an answer"}"""),
                event("E3", body = """{"role":"user","content":"second question"}"""),
                event("E4", body = """{"role":"assistant","content":"another answer"}"""),
            ),
            synced = true,
        )

        assertEquals("second question", repo.latestPrompt("S1"))
    }

    /** One prompt can be followed by a long agent run of assistant turns. */
    @Test
    fun `a prompt buried under a long run is still found`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))
        val turns = mutableListOf(event("E0", body = """{"role":"user","content":"the only prompt"}"""))
        repeat(40) { turns += event("E$it-a", body = """{"role":"assistant","content":"turn $it"}""") }
        repo.apply("S1", turns, synced = true)

        assertEquals("the only prompt", repo.latestPrompt("S1"))
    }

    /** A session with nothing asked yet has nothing to show, not a stale line. */
    @Test
    fun `a session with no prompt summarises as empty`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S1", "C:/proj", "idle")))

        assertEquals("", repo.latestPrompt("S1"))
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

    /**
     * The daemon subscribes before it answers a catch-up, so an event can come
     * both ways. The pushed copy must not land a second time, out of order.
     */
    @Test
    fun `a pushed event the catch-up already brought is not mirrored again`() = runBlocking {
        repo.apply("S1", listOf(event("E1"), event("E2")), synced = true)

        repo.record(com.nabu.client.net.Incoming.Event(com.nabu.client.net.SessionEvent("S1", event("E2"))))
        repo.record(com.nabu.client.net.Incoming.Event(com.nabu.client.net.SessionEvent("S1", event("E3"))))

        val rows = db.events().all("S1")
        assertEquals(listOf("E1", "E2", "E3"), rows.map { it.id })
        assertEquals(listOf(1L, 2L, 3L), rows.map { it.ordinal })
    }

    /** A screen reads what follows the last row it has, not the whole log again. */
    @Test
    fun `rows after an ordinal are the ones that follow it`() = runBlocking {
        repo.apply("S1", listOf(event("E1"), event("E2"), event("E3")), synced = true)

        assertEquals(listOf("E2", "E3"), repo.rowsAfter("S1", 1).map { it.id })
        assertEquals(3L, db.events().lastOrdinal("S1"))
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

    /**
     * Issue 50: the list is ordered by when each session was last worked on.
     * The daemon lists newest-created first and stamping the local clock per
     * row turned that into exactly the wrong order — the oldest session on top.
     */
    @Test
    fun `sessions are ordered by the daemon's last interaction`() = runBlocking {
        repo.recordSessions(listOf(
            SessionSummary("S-old", "C:/a", "idle", updatedAt = "2026-09-17T09:00:00Z"),
            SessionSummary("S-new", "C:/b", "idle", updatedAt = "2026-09-17T18:30:00Z"),
            SessionSummary("S-mid", "C:/c", "idle", updatedAt = "2026-09-17T12:00:00Z"),
        ))

        assertEquals(
            listOf("S-new", "S-mid", "S-old"),
            repo.watchSessions().first().map { it.id },
        )
    }

    /**
     * Catching up on an old session must not push it to the top. Every session
     * is synced on connect, so stamping the clock as each batch landed put the
     * list back in the daemon's order — the bug the timestamps above fix.
     */
    @Test
    fun `mirroring old events does not make a session look recent`() = runBlocking {
        repo.recordSessions(listOf(
            SessionSummary("S-old", "C:/a", "idle", updatedAt = "2026-09-17T09:00:00Z"),
            SessionSummary("S-new", "C:/b", "idle", updatedAt = "2026-09-17T18:30:00Z"),
        ))

        repo.apply("S-old", listOf(event("E1", at = "2026-09-17T09:00:00Z")), synced = true)

        assertEquals(
            listOf("S-new", "S-old"),
            repo.watchSessions().first().map { it.id },
        )
    }

    /** A prompt that just arrived does move its session to the top. */
    @Test
    fun `a new event moves its session up`() = runBlocking {
        repo.recordSessions(listOf(
            SessionSummary("S-old", "C:/a", "idle", updatedAt = "2026-09-17T09:00:00Z"),
            SessionSummary("S-new", "C:/b", "idle", updatedAt = "2026-09-17T18:30:00Z"),
        ))

        repo.apply("S-old", listOf(event("E1", at = "2026-09-17T21:00:00Z")), synced = true)

        assertEquals(
            listOf("S-old", "S-new"),
            repo.watchSessions().first().map { it.id },
        )
    }

    /** The daemon writes a time zone offset, not always Z. */
    @Test
    fun `an offset timestamp is read as the same instant as its UTC form`() {
        assertEquals(
            interactionTime("2026-09-17T12:00:00Z", existing = 0, fallback = 1),
            interactionTime("2026-09-17T07:00:00-05:00", existing = 0, fallback = 1),
        )
    }

    /** Go writes nanoseconds; Instant.parse must not be handed them raw. */
    @Test
    fun `sub-second precision parses`() {
        assertTrue(interactionTime("2026-09-17T12:00:00.123456789Z", existing = 0, fallback = 1) > 1)
    }

    /**
     * The daemon knows every event, so its word wins even when it is older
     * than what this device recorded. Without that, a mirror carrying the
     * times this bug wrote would keep them forever and never re-sort.
     */
    @Test
    fun `the daemon's time replaces one this device guessed`() {
        val guessed = 4_000_000_000_000L // a local clock, far ahead of the event
        val real = interactionTime("2026-09-17T09:00:00Z", existing = guessed, fallback = 1)

        assertTrue("the guessed time survived: $real", real < guessed)
    }

    /** Mirroring an old batch is not news, so it cannot pull a session down. */
    @Test
    fun `catching up never moves a session backwards`() {
        val known = interactionTime("2026-09-17T18:00:00Z", existing = 0, fallback = 1)
        val after = interactionAfter(
            listOf(event("E1", at = "2026-09-17T09:00:00Z")), existing = known, fallback = 1,
        )

        assertEquals(known, after)
    }

    /** An unreadable timestamp is no reason to reshuffle the list. */
    @Test
    fun `an unreadable timestamp keeps what is already known`() {
        assertEquals(500L, interactionTime("", existing = 500, fallback = 999))
        assertEquals(500L, interactionTime("whenever", existing = 500, fallback = 999))
    }

    /** With nothing known, a new session belongs at the top, not the bottom. */
    @Test
    fun `an unreadable timestamp on a new session falls back to now`() {
        assertEquals(999L, interactionTime("", existing = 0, fallback = 999))
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
