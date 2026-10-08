package com.nabu.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.FakeDaemon
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** Choosing a session's context size (docs/specs/2026-10-08-context-size-design.md). */
@RunWith(RobolectricTestRunner::class)
class ContextSizeTest {

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
    fun `choosing large sets the context option and nothing else`() = runBlocking {
        repo.setContext(client, "S1", "large")

        val options = sent("nabu.session.set_option")
        assertEquals(1, options.size)
        assertTrue(options[0], options[0].contains("\"key\":\"context\"") && options[0].contains("\"value\":\"large\""))
        assertTrue(sent("nabu.session.send_prompt").isEmpty())
    }
}
