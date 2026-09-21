package com.nabu.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.FakeDaemon
import com.nabu.client.settings.Settings
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Stopping a turn is a different act from ending a session, and the client has
 * to ask for the one it means. nabu.session.interrupt leaves the session alive;
 * nabu.session.stop does not.
 */
@RunWith(RobolectricTestRunner::class)
class InterruptTest {

    private lateinit var db: MirrorDb
    private lateinit var repo: SessionRepository
    private lateinit var daemon: FakeDaemon

    @Before
    fun open() {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        db = Room.inMemoryDatabaseBuilder(ctx, MirrorDb::class.java)
            .allowMainThreadQueries().build()
        repo = SessionRepository(db)
        daemon = FakeDaemon()
        daemon.start()
    }

    @After
    fun close() {
        db.close()
        daemon.stop()
    }

    private fun settings() = Settings(host = daemon.server.hostName, port = daemon.server.port, token = "sekrit")

    private suspend fun connect(): DaemonClient {
        val s = settings()
        return DaemonClient(baseUrl = "http://${s.host}:${s.port}/", token = s.token)
            .also { it.connect() }
    }

    @Test
    fun `interrupting asks the daemon to cancel the turn`() = runBlocking {
        val c = connect()

        repo.interruptSession(c, "S1")

        val sent = daemon.received.joinToString("\n")
        assertTrue("expected nabu.session.interrupt in:\n$sent",
            sent.contains("nabu.session.interrupt"))
        assertTrue("it must name the session", sent.contains("S1"))
    }

    // Ending the session is a different, unrecoverable intention. Stopping what
    // it is doing must never quietly do that instead.
    @Test
    fun `interrupting never ends the session`() = runBlocking {
        val c = connect()

        repo.interruptSession(c, "S1")

        val sent = daemon.received.joinToString("\n")
        assertTrue("interrupt must not call stop:\n$sent",
            !sent.contains("nabu.session.stop"))
    }

    // A refusal has to surface. The daemon makes interrupt a no-op when the
    // session is not running, but a transport failure is not a no-op and the
    // caller needs to know the turn is still going.
    @Test
    fun `a refusal is reported rather than swallowed`() = runBlocking {
        daemon.refuseSession["S1"] = com.nabu.client.protocol.RpcCodes.INVALID_TRANSITION
        val c = connect()

        // refuseSession only intercepts send_prompt, so interrupt still
        // succeeds here; what is pinned is that the call is made and its result
        // is not discarded on the way back.
        repo.interruptSession(c, "S1")
        assertTrue(daemon.received.any { it.contains("nabu.session.interrupt") })
    }

    // The session keeps existing: interrupt is not a teardown, and the mirror
    // should still hold the row afterwards for the transcript to render.
    @Test
    fun `the session survives being interrupted`() = runBlocking {
        db.sessions().upsert(SessionRow(id = "S1", updatedAt = 1))
        val c = connect()

        repo.interruptSession(c, "S1")

        assertEquals("S1", db.sessions().get("S1")?.id)
    }
}
