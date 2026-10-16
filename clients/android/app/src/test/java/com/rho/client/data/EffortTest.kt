package com.rho.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.rho.client.net.DaemonClient
import com.rho.client.net.FakeDaemon
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** Choosing a session's effort (docs/specs/2026-10-16-session-effort-design.md). */
@RunWith(RobolectricTestRunner::class)
class EffortTest {

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

    @Test
    fun `choosing a level sets the effort option and nothing else`() = runBlocking {
        repo.setEffort(client, "S1", "medium")

        val options = sent("rho.session.set_option")
        assertEquals(1, options.size)
        assertTrue(options[0], options[0].contains("\"key\":\"effort\"") && options[0].contains("\"value\":\"medium\""))
        assertTrue(sent("rho.session.send_prompt").isEmpty())
    }

    @Test
    fun `choosing default clears it`() = runBlocking {
        repo.setEffort(client, "S1", "")

        val options = sent("rho.session.set_option")
        assertEquals(1, options.size)
        assertTrue(options[0], options[0].contains("\"key\":\"effort\"") && options[0].contains("\"value\":\"\""))
    }
}
