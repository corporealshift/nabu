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
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** Issue 56: sessions put away on the daemon leave the phone too. */
@RunWith(RobolectricTestRunner::class)
class ArchiveTest {

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

    private suspend fun mirrored(id: String) {
        repo.recordSessions(listOf(SessionSummary(id, "C:/p", "idle")))
        db.append(id, listOf(EventRow("$id-E1", id, 1, "message", "{}")), "$id-E1", true)
    }

    // Archived by hand elsewhere, or for sitting idle: the daemon stops
    // listing it, and the phone has to notice by what is missing.
    @Test
    fun `a session the daemon no longer lists is forgotten with its events`() = runBlocking {
        mirrored("KEEP")
        mirrored("GONE")

        repo.forgetUnlisted(listOf(SessionSummary("KEEP", "C:/p", "idle")))

        assertNotNull(db.sessions().get("KEEP"))
        assertEquals(1, db.events().count("KEEP"))
        assertNull(db.sessions().get("GONE"))
        assertEquals(0, db.events().count("GONE"))
    }

    @Test
    fun `archiving asks the daemon and drops the local copy`() = runBlocking {
        mirrored("S1")

        repo.archiveSession(client, "S1")

        val asked = daemon.received.last { it.contains("nabu.session.archive") }
        assertTrue("the request must name the session: $asked", asked.contains("\"S1\""))
        assertNull(db.sessions().get("S1"))
    }

    @Test
    fun `the archive is asked for, not the sessions in use`() = runBlocking {
        repo.listArchived(client)

        val asked = daemon.received.last { it.contains("nabu.session.list") }
        assertTrue("the listing must ask for the archive: $asked", asked.contains("\"archived\":true"))
    }
}
