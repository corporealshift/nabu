package com.nabu.client.data

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.FakeDaemon
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/** Stats across sessions (spec 7.25, 7.26): asked of the daemon, never counted here. */
@RunWith(RobolectricTestRunner::class)
class StatsTest {

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
    fun `the totals ask for a period and a kind and decode what comes back`() = runBlocking {
        daemon.results["nabu.stats"] = """{"days":7,"kind":"runs","sessions":3,"turns":40,"prompts":3,
            "tokens":{"input":9000,"output":800,"cached":0},
            "tools":[{"tool":"web.search","calls":4,"errors":1,"sessions":2}],
            "compactions":{"summarize":1,"clear_results":2},"vetoes":1,"interruptions":0,
            "working_seconds":3600,"per_day":[{"date":"2026-10-07","turns":40,"input":9000,"output":800}],
            "skipped":1}"""

        val w = repo.windowStats(client, 7, "runs")

        val asked = daemon.received.last { it.contains("\"nabu.stats\"") }
        assertTrue("the period must be asked for: $asked", asked.contains("\"days\":7"))
        assertTrue("the kind must be asked for: $asked", asked.contains("\"kind\":\"runs\""))
        assertEquals(3, w.sessions)
        assertEquals(9000L, w.tokens.input)
        assertEquals(listOf(com.nabu.client.protocol.WindowTool("web.search", 4, 1, 2)), w.tools)
        assertEquals(2, w.compactions.clearResults)
        assertEquals("2026-10-07", w.perDay.single().date)
        assertEquals(1, w.skipped)
    }

    @Test
    fun `a tool's calls carry their arguments and whose they were`() = runBlocking {
        daemon.results["nabu.stats.calls"] = """{"calls":[{"session_id":"S1","label":"Room DAOs",
            "at":"2026-10-07T15:45:00Z","arguments":{"query":"createFromFile copies"},
            "status":"ok","kind":"","result":"it copies the file","archived":true}],
            "truncated":true,"skipped":0}"""

        val got = repo.toolCalls(client, "web.search", 7, "all", sessionId = "S1", limit = 50)

        val asked = daemon.received.last { it.contains("nabu.stats.calls") }
        for (part in listOf("\"tool\":\"web.search\"", "\"session_id\":\"S1\"", "\"limit\":50")) {
            assertTrue("the request must carry $part: $asked", asked.contains(part))
        }
        val call = got.calls.single()
        assertEquals("Room DAOs", call.label)
        assertEquals("createFromFile copies", call.arguments!!.jsonObject["query"]!!.jsonPrimitive.content)
        assertTrue(call.archived)
        assertTrue(got.truncated)
    }

    @Test
    fun `without a session the calls are not narrowed to one`() = runBlocking {
        daemon.results["nabu.stats.calls"] = """{"calls":[],"truncated":false,"skipped":0}"""

        val got = repo.toolCalls(client, "claude.ask", 30, "interactive")

        val asked = daemon.received.last { it.contains("nabu.stats.calls") }
        assertFalse("no session was named: $asked", asked.contains("session_id"))
        assertTrue(got.calls.isEmpty())
    }
}
