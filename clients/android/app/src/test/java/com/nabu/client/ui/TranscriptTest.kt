package com.nabu.client.ui

import com.nabu.client.data.EventRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TranscriptTest {

    private fun row(id: String, type: String, data: String, ordinal: Long = 1) =
        EventRow(
            id = id, sessionId = "S1", ordinal = ordinal, type = type,
            raw = """{"id":"$id","type":"$type","timestamp":"2026-09-16T00:00:00Z","data":$data}""",
        )

    @Test
    fun `messages render by role`() {
        val lines = transcript(
            listOf(
                row("E1", "message", """{"role":"user","content":"do the thing"}"""),
                row("E2", "message", """{"role":"assistant","content":"doing it"}""", 2),
            ),
            synced = true,
        )

        assertEquals(2, lines.size)
        assertTrue(lines[0] is Line.UserSaid)
        assertTrue(lines[1] is Line.AgentSaid)
        assertEquals("do the thing", (lines[0] as Line.UserSaid).text)
    }

    /** An assistant message with no text is the model calling a tool. */
    @Test
    fun `an empty message is not a line`() {
        val lines = transcript(
            listOf(row("E1", "message", """{"role":"assistant","content":""}""")),
            synced = true,
        )
        assertTrue(lines.isEmpty())
    }

    @Test
    fun `a tool call shows what it did rather than its payload`() {
        val lines = transcript(
            listOf(
                row(
                    "E1", "tool_call",
                    """{"call_id":"c1","tool":"bash","source":"model","arguments":{"command":"go test ./..."}}""",
                )
            ),
            synced = true,
        )

        val line = lines.single() as Line.ToolRan
        assertEquals("bash", line.tool)
        assertEquals("go test ./...", line.summary)
        assertEquals("go test ./...", line.full)
    }

    // The line has room for about 120 characters. The command it stands for can
    // be any length, and copy needs the whole one.
    @Test
    fun `a long tool call keeps the full command beside the shortened one`() {
        val command = "go test ./... -run " + "Test".repeat(60)
        val lines = transcript(
            listOf(
                row(
                    "E1", "tool_call",
                    """{"call_id":"c1","tool":"bash","source":"model","arguments":{"command":"$command"}}""",
                )
            ),
            synced = true,
        )

        val line = lines.single() as Line.ToolRan
        assertTrue("the shown summary should be cut", line.summary.endsWith("…"))
        assertEquals(command, line.full)
    }

    @Test
    fun `a long tool result is marked for collapsing`() {
        val long = "x".repeat(COLLAPSED_OUTPUT_CHARS + 50)
        val lines = transcript(
            listOf(
                row(
                    "E1", "tool_result",
                    """{"call_id":"c1","tool":"bash","content":"$long","status":"ok"}""",
                )
            ),
            synced = true,
        )

        val line = lines.single() as Line.ToolOutput
        assertTrue("a long result should be collapsible", line.truncated)
        assertEquals(long.length, line.text.length)
    }

    @Test
    fun `a short tool result is not collapsed`() {
        val lines = transcript(
            listOf(
                row(
                    "E1", "tool_result",
                    """{"call_id":"c1","tool":"read","content":"two lines","status":"ok"}""",
                )
            ),
            synced = true,
        )
        assertFalse((lines.single() as Line.ToolOutput).truncated)
    }

    /** Spec 15: the gap shows at the gap, not as a footnote. */
    @Test
    fun `an unsynced transcript opens with a gap`() {
        val lines = transcript(
            listOf(row("E9", "message", """{"role":"user","content":"latest"}""")),
            synced = false,
        )

        val gap = lines.first() as Line.Gap
        assertFalse("there are events, so this is truncated not empty", gap.neverFetched)
        assertEquals(2, lines.size)
    }

    /** Never fetched and fetched-but-behind must not look alike. */
    @Test
    fun `a never fetched session says so rather than looking empty`() {
        val gap = transcript(emptyList(), synced = false).single() as Line.Gap
        assertTrue(gap.neverFetched)
    }

    /** A fully synced session has no gap line at all. */
    @Test
    fun `a synced transcript has no gap`() {
        val lines = transcript(
            listOf(row("E1", "message", """{"role":"user","content":"hi"}""")),
            synced = true,
        )
        assertTrue(lines.none { it is Line.Gap })
    }

    @Test
    fun `notices and vetoes are shown`() {
        val lines = transcript(
            listOf(
                row("E1", "notice", """{"source":"daemon","level":"warn","message":"context is filling"}"""),
                row("E2", "stop_veto", """{"module":"verify","reason":"the gate failed"}""", 2),
            ),
            synced = true,
        )

        assertEquals("context is filling", (lines[0] as Line.Note).text)
        assertEquals("verify", (lines[1] as Line.Veto).module)
    }

    /** Injected context is the module talking to the model, not to the reader. */
    @Test
    fun `context events are not rendered`() {
        val lines = transcript(
            listOf(row("E1", "context", """{"source":"module:memory","slot":"prefix","content":"## Memory"}""")),
            synced = true,
        )
        assertTrue(lines.isEmpty())
    }

    /** A row that will not decode costs its line and nothing else. */
    @Test
    fun `an unreadable row does not break the transcript`() {
        val good = row("E1", "message", """{"role":"user","content":"kept"}""")
        val bad = EventRow(id = "E2", sessionId = "S1", ordinal = 2, type = "message", raw = "not json")

        val lines = transcript(listOf(good, bad), synced = true)

        assertEquals(1, lines.size)
        assertEquals("kept", (lines.single() as Line.UserSaid).text)
    }

    @Test
    fun `a summarize compaction is shown so the gap in context is visible`() {
        val lines = transcript(
            listOf(
                row(
                    "E1", "compaction",
                    """{"mode":"summarize","range_start":"E0","range_end":"E0","summary":"what happened"}""",
                )
            ),
            synced = true,
        )
        assertEquals("what happened", (lines.single() as Line.Compacted).summary)
    }
}

// Issue 39: thinking reaches the phone but was rendering as nothing.
class ThinkingLineTest {

    private fun ev(id: String, type: String, data: String) = EventRow(
        id = id, sessionId = "S1", ordinal = 1, type = type,
        raw = """{"id":"$id","type":"$type","timestamp":"2026-09-17T00:00:00Z","data":$data}""",
    )

    @Test
    fun `a thinking event becomes its own kind of line`() {
        val lines = transcript(
            listOf(ev("E1", "thinking", """{"content":"I should read the test first.","source":"model"}""")),
            synced = true,
        )

        val thought = lines.single() as? Line.Thought
            ?: error("thinking rendered as ${lines.single()::class.simpleName}, not a Thought")
        assertEquals("I should read the test first.", thought.text)
    }

    // A provider that reports no reasoning must leave no trace.
    @Test
    fun `an empty thought is not a line`() {
        val lines = transcript(
            listOf(ev("E1", "thinking", """{"content":"   ","source":"model"}""")),
            synced = true,
        )
        assertTrue("an empty thought should render nothing", lines.isEmpty())
    }

    // It must not be mistaken for the answer or for a tool call.
    @Test
    fun `a thought sits between the prompt and the answer`() {
        val lines = transcript(
            listOf(
                ev("E1", "message", """{"role":"user","content":"go"}"""),
                ev("E2", "thinking", """{"content":"weighing it up","source":"model"}"""),
                ev("E3", "message", """{"role":"assistant","content":"done"}"""),
            ),
            synced = true,
        )

        assertEquals(3, lines.size)
        assertTrue(lines[0] is Line.UserSaid)
        assertTrue(lines[1] is Line.Thought)
        assertTrue(lines[2] is Line.AgentSaid)
    }
}
