package com.nabu.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.DaemonException
import com.nabu.client.net.FakeDaemon
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Forcing a compaction from the phone (spec 7.15).
 *
 * The rule that matters is the daemon's: a running session is refused and the
 * caller is told to interrupt first. The client's job is to ask, to report the
 * mode that actually ran, and to pass the refusal through intact rather than
 * flattening it into "something went wrong".
 */
@RunWith(RobolectricTestRunner::class)
class CompactTest {

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

    @Test
    fun `compacting asks the daemon for this session`() = runBlocking {
        repo.compactSession(client, "S1")

        val asked = daemon.received.last { it.contains("nabu.session.compact") }
        assertTrue("the request must name the session: $asked", asked.contains("\"S1\""))
    }

    @Test
    fun `the mode the daemon reports is the mode returned`() = runBlocking {
        assertEquals("summarize", repo.compactSession(client, "S1"))
    }

    // A failed summary falls back to stubbing tool results. Reporting the
    // summary that was asked for instead of the one that ran would be a lie the
    // user has no way to catch.
    @Test
    fun `a fallback is reported as itself`() = runBlocking {
        daemon.compactMode = "clear_results"

        assertEquals("clear_results", repo.compactSession(client, "S1"))
    }

    @Test
    fun `a refusal reaches the caller with the daemon's reason`() = runBlocking {
        daemon.refuseMethod["nabu.session.compact"] =
            -32002 to "session is running; interrupt it before compacting"

        val thrown = runCatching { repo.compactSession(client, "S1") }.exceptionOrNull()

        assertTrue("want a DaemonException, got $thrown", thrown is DaemonException)
        val e = thrown as DaemonException
        assertEquals(-32002, e.code)
        assertTrue(
            "the reason is the point of the refusal: ${e.message}",
            e.message.orEmpty().contains("interrupt"),
        )
    }

    /**
     * The daemon keeps summarising when the phone drops, so a lost connection
     * is reported as that, not as a failed compaction (issue 87).
     */
    @Test
    fun `a dropped connection is not reported as a failed compaction`() {
        val dropped = com.nabu.client.ui.compactFailure(DaemonException("connection reset"))
        assertTrue(dropped, dropped.contains("the daemon carries on"))

        val refused = com.nabu.client.ui.compactFailure(
            DaemonException("nabu.session.compact: session is running; interrupt it before compacting", -32002)
        )
        assertEquals("nabu.session.compact: session is running; interrupt it before compacting", refused)
    }
}
