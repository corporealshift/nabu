package com.nabu.client.ui

import com.nabu.client.data.EventRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The feed folds rows in as they arrive instead of rebuilding from the whole
 * log on every event (issue 82). Whatever order the rows come in, it must show
 * exactly what the whole-log functions do.
 */
class FeedTest {

    private fun row(ordinal: Long, type: String, data: String) = EventRow(
        id = "E%03d".format(ordinal), sessionId = "S1", ordinal = ordinal, type = type,
        raw = """{"id":"E%03d","type":"$type","timestamp":"2026-09-16T00:00:00Z","data":$data}""".format(ordinal),
    )

    private val log = listOf(
        row(1, "session", """{"workspace":"C:/p","workspace_key":"k","context_window":1000,"options":{"model":"m","compaction_enabled":true,"permission_mode":"ask"}}"""),
        row(2, "message", """{"role":"user","content":"fix it"}"""),
        row(3, "tasks", """{"tasks":[{"id":"t1","title":"read","status":"in_progress"}]}"""),
        row(4, "tool_call", """{"call_id":"c1","tool":"read","arguments":{"path":"a.go"}}"""),
        row(5, "tool_result", """{"call_id":"c1","tool":"read","status":"ok","content":"package a"}"""),
        row(6, "message", """{"role":"assistant","content":"done","usage":{"input_tokens":250,"output_tokens":5}}"""),
        row(7, "tasks", """{"tasks":[{"id":"t1","title":"read","status":"done"}]}"""),
    )

    private fun fed(vararg chunks: List<EventRow>): TranscriptView {
        val feed = TranscriptFeed()
        chunks.forEach { feed.add(it) }
        return feed.view()
    }

    @Test
    fun `fed in pieces it shows what the whole log shows`() {
        val whole = fed(log)
        val pieces = fed(log.subList(0, 2), log.subList(2, 5), log.subList(5, 7))

        assertEquals(transcript(log, synced = true), whole.lines)
        assertEquals(whole, pieces)
        assertEquals(tasksOf(log), pieces.tasks)
        assertEquals(contextUsed(log), pieces.contextUsed)
        assertEquals("done", pieces.tasks.single().status)
    }

    /** A catch-up and a refetch can hand over rows the feed already has. */
    @Test
    fun `rows already folded in are not folded in twice`() {
        val once = fed(log)
        val twice = fed(log.subList(0, 5), log)

        assertEquals(once, twice)
    }

    @Test
    fun `a gap is shown in front only while the mirror is behind`() {
        val view = fed(log)

        assertEquals(view.lines, withGap(view.lines, synced = true, fetched = true))
        val behind = withGap(view.lines, synced = false, fetched = true)
        assertTrue(behind.first() is Line.Gap)
        assertFalse((behind.first() as Line.Gap).neverFetched)

        val empty = TranscriptFeed().view()
        assertFalse(empty.fetched)
        assertTrue((withGap(empty.lines, synced = false, fetched = empty.fetched).single() as Line.Gap).neverFetched)
        assertNull(empty.contextUsed)
    }

    @Test
    fun `a reset starts the feed again`() {
        val feed = TranscriptFeed()
        feed.add(log)
        feed.reset()

        assertEquals(0L, feed.lastOrdinal)
        assertEquals(TranscriptView(), feed.view())
    }
}
