package com.nabu.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.FakeDaemon
import com.nabu.client.net.SessionSummary
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Starting and placing orchestrated runs from the phone
 * (docs/specs/2026-09-30-orchestrated-runs-design.md).
 */
@RunWith(RobolectricTestRunner::class)
class RunTest {

    private lateinit var db: MirrorDb
    private lateinit var repo: SessionRepository
    private lateinit var daemon: FakeDaemon
    private lateinit var client: DaemonClient

    @Before
    fun open() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        db = Room.inMemoryDatabaseBuilder(ctx, MirrorDb::class.java)
            .allowMainThreadQueries().build()
        repo = SessionRepository(db)
        daemon = FakeDaemon()
        daemon.start()
        client = DaemonClient(daemon.url(), "sekrit")
        client.connect()
        Unit
    }

    @After
    fun close() {
        client.close()
        daemon.stop()
        db.close()
    }

    private fun sent(method: String) = daemon.received.filter { it.contains("\"$method\"") }

    // The brief is the description, never the goal: a goal starts the session
    // working on it in the owner's checkout, beside the run (seen live).
    @Test
    fun `a run with a brief sets the description, then the labels, and never a goal`() = runBlocking {
        repo.startRun(client, "S1", "Add a Median function", listOf("mine", "run:requested"))

        val options = sent("nabu.session.set_option")
        assertEquals(2, options.size)
        assertTrue(options[0], options[0].contains("\"description\"") && options[0].contains("Add a Median function"))
        assertTrue(options[1], options[1].contains("\"labels\"") && options[1].contains("[\"mine\",\"run:requested\"]"))
        assertTrue(sent("nabu.session.set_goal").isEmpty())
        assertTrue(sent("nabu.session.send_prompt").isEmpty())
    }

    @Test
    fun `a run without a brief only labels the session`() = runBlocking {
        repo.startRun(client, "S1", "  ", listOf("run:requested"))

        val options = sent("nabu.session.set_option")
        assertEquals(1, options.size)
        assertFalse(options[0].contains("\"description\""))
    }

    @Test
    fun `a session's parent and labels come from its mirrored log`() = runBlocking {
        repo.recordSessions(listOf(SessionSummary("S2", "C:/proj", "idle")))
        db.append(
            "S2",
            listOf(
                EventRow(
                    "E1", "S2", 1, "session",
                    """{"id":"E1","type":"session","timestamp":"2026-10-01T00:00:00Z","data":{"workspace":"w","workspace_key":"k",""" +
                        """"options":{"model":"m","compaction_enabled":true,"permission_mode":"auto","parent":"HOME","labels":["guard:no-push"]}}}""",
                ),
                EventRow("E2", "S2", 2, "message", """{"id":"E2","type":"message","timestamp":"2026-10-01T00:00:01Z","data":{"role":"user","content":"x"}}"""),
                EventRow(
                    "E3", "S2", 3, "options_change",
                    """{"id":"E3","type":"options_change","timestamp":"2026-10-01T00:00:02Z","data":{"key":"labels","from":["guard:no-push"],"to":["run:fix","run:attempt:2/10"],"source":"client"}}""",
                ),
            ),
            "E3", true,
        )

        val options = repo.latestOptions("S2")
        assertEquals("HOME", options.parent)
        assertEquals(listOf("run:fix", "run:attempt:2/10"), options.labels)
        assertEquals("", repo.latestOptions("nothing-mirrored").parent)
    }
}
