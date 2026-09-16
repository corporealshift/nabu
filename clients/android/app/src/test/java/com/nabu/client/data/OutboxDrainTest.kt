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
import org.junit.Assert.assertFalse
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * The drain is what runs when connectivity returns, with the app closed. Its
 * answer decides whether the system tries again, so each case is pinned.
 */
@RunWith(RobolectricTestRunner::class)
class OutboxDrainTest {

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

    private fun settings() = Settings(host = daemon.server.hostName, port = daemon.server.port, token = "sekrit")

    private suspend fun queue(id: String) {
        db.sessions().upsert(SessionRow(id = "S1", updatedAt = 1))
        repo.queuePrompt("S1", "a prompt", id)
    }

    /** Nothing waiting means nothing to do, and no reason to open a socket. */
    @Test
    fun `an empty outbox does not connect`() = runBlocking {
        var connected = false

        val result = drainOutbox(repo, settings()) { connected = true; error("unreachable") }

        assertEquals(Drain.Nothing, result)
        assertFalse("an empty outbox must not open a connection", connected)
    }

    /** Without somewhere to send, retrying forever would only burn battery. */
    @Test
    fun `an unconfigured daemon is not retried`() = runBlocking {
        queue("c1")

        val result = drainOutbox(repo, Settings()) { error("unreachable") }

        assertEquals(Drain.Nothing, result)
    }

    /** A daemon that cannot be reached is exactly what a retry is for. */
    @Test
    fun `an unreachable daemon asks to be retried`() = runBlocking {
        queue("c1")

        val result = drainOutbox(repo, settings()) { error("no route to host") }

        assertEquals(Drain.Failed, result)
        assertEquals("the prompt must still be waiting", 1, repo.pendingCount())
    }

    /** The whole point: the queued prompt leaves without the app being open. */
    @Test
    fun `a reachable daemon empties the outbox`() = runBlocking {
        queue("c1")
        queue("c2")

        val result = drainOutbox(repo, settings()) { s ->
            DaemonClient(baseUrl = "http://${s.host}:${s.port}/", token = s.token).also { it.connect() }
        }

        assertEquals(Drain.Sent, result)
        assertEquals(0, repo.pendingCount())
    }
}
