package com.nabu.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.DaemonException
import com.nabu.client.net.FakeDaemon
import com.nabu.client.protocol.RpcCodes
import com.nabu.client.settings.Settings
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * A prompt the daemon will refuse every time used to sit at the head of the
 * queue and stop everything behind it, because the drain marked it failed and
 * returned. Days of prompts went nowhere and the screen only ever said how
 * many were waiting. These pin the behaviour that replaced it.
 */
@RunWith(RobolectricTestRunner::class)
class OutboxBlockingTest {

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
        daemon.stop()
        db.close()
    }

    private fun settings() =
        Settings(host = daemon.server.hostName, port = daemon.server.port, token = "sekrit")

    private suspend fun connect(): DaemonClient {
        val s = settings()
        return DaemonClient(baseUrl = "http://${s.host}:${s.port}/", token = s.token)
            .also { it.connect() }
    }

    private suspend fun queue(session: String, clientId: String, content: String = "a prompt") {
        db.sessions().upsert(SessionRow(id = session, updatedAt = 1))
        repo.queuePrompt(session, content, clientId)
    }

    /** The exact shape of the jam: one dead row, four live ones behind it. */
    @Test
    fun `a permanently refused prompt does not block the ones behind it`() = runBlocking {
        daemon.refuseSession["ENDED"] = RpcCodes.INVALID_TRANSITION
        queue("ENDED", "c1", "into a session that has ended")
        queue("S2", "c2")
        queue("S3", "c3")

        repo.flushOutbox(connect())

        assertEquals("the live prompts must all have gone", 0, repo.pendingCount())
        val blocked = repo.watchBlocked().first()
        assertEquals("the refused one is kept, not dropped", 1, blocked.size)
        assertEquals("c1", blocked.single().clientId)
        assertNotNull("and it says why", blocked.single().lastError)
    }

    /** A blocked prompt is out of the queue but not lost. */
    @Test
    fun `a blocked prompt is kept and can be retried or discarded`() = runBlocking {
        daemon.refuseSession["ENDED"] = RpcCodes.INVALID_TRANSITION
        queue("ENDED", "c1")
        repo.flushOutbox(connect())
        assertEquals(1, repo.watchBlocked().first().size)

        // Whatever made the daemon refuse is fixed, so it goes back in line.
        daemon.refuseSession.clear()
        repo.retryBlocked("c1")
        assertEquals("back in the queue", 1, repo.pendingCount())
        assertTrue("and no longer listed as blocked", repo.watchBlocked().first().isEmpty())

        repo.flushOutbox(connect())
        assertEquals("and it sends", 0, repo.pendingCount())
    }

    @Test
    fun `discarding a blocked prompt removes it`() = runBlocking {
        daemon.refuseSession["ENDED"] = RpcCodes.INVALID_TRANSITION
        queue("ENDED", "c1")
        repo.flushOutbox(connect())

        repo.discardBlocked("c1")

        assertTrue(repo.watchBlocked().first().isEmpty())
        assertEquals(0, repo.pendingCount())
    }

    /**
     * The other half: a failure that is about the moment rather than the
     * request still stops the drain, so prompts keep their order.
     */
    @Test
    fun `a transient failure stops the drain and keeps everything queued`() = runBlocking {
        daemon.refuseSession["FLAKY"] = RpcCodes.INTERNAL_ERROR
        queue("FLAKY", "c1")
        queue("S2", "c2")

        repo.flushOutbox(connect())

        assertEquals("nothing is given up on", 2, repo.pendingCount())
        assertTrue("and nothing is blocked", repo.watchBlocked().first().isEmpty())
    }

    /** A queue that is stuck has to be able to say why. */
    @Test
    fun `a connection failure is recorded on the waiting prompts`() = runBlocking {
        queue("S1", "c1")

        val result = drainOutbox(repo, settings()) { error("no route to host") }

        assertEquals(Drain.Failed, result)
        val reason = db.outbox().pending().firstNotNullOfOrNull { it.lastError }
        assertNotNull("the banner has nothing to show without this", reason)
        assertTrue(reason!!.contains("no route to host"))
    }

    @Test
    fun `error codes are classified by whether a retry could ever work`() {
        assertTrue(DaemonException("x", RpcCodes.INVALID_TRANSITION).isPermanent)
        assertTrue(DaemonException("x", RpcCodes.SESSION_NOT_FOUND).isPermanent)
        assertFalse(DaemonException("x", RpcCodes.INTERNAL_ERROR).isPermanent)
        assertFalse("about the connection, not this prompt",
            DaemonException("x", RpcCodes.UNAUTHORIZED).isPermanent)
        assertFalse("no answer at all is the retryable case",
            DaemonException("dropped").isPermanent)
    }
}
