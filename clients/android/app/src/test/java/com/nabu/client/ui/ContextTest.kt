package com.nabu.client.ui

import com.nabu.client.data.EventRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ContextTest {

    private fun row(id: String, ordinal: Long, type: String, data: String) = EventRow(
        id = id, sessionId = "S1", ordinal = ordinal, type = type,
        raw = """{"id":"$id","type":"$type","timestamp":"2026-09-17T00:00:00Z","data":$data}""",
    )

    private fun session(window: Int) = row(
        "E1", 1, "session",
        """{"workspace":"C:/w","workspace_key":"w-1","context_window":$window,""" +
            """"options":{"model":"m","compaction_enabled":true,"permission_mode":"ask"}}""",
    )

    private fun assistant(id: String, ordinal: Long, input: Int) = row(
        id, ordinal, "message",
        """{"role":"assistant","content":"ok","usage":{"input_tokens":$input,"output_tokens":5}}""",
    )

    @Test
    fun `fullness is the last request against the window`() {
        val used = contextUsed(listOf(session(1000), assistant("E2", 2, 400)))
        assertEquals(0.4f, used!!, 0.001f)
    }

    // Every request carries the whole conversation again, so summing them would
    // count the same tokens once per turn and sail past a hundred percent.
    @Test
    fun `it is the last request, not the sum of them`() {
        val used = contextUsed(
            listOf(session(1000), assistant("E2", 2, 300), assistant("E3", 3, 500)),
        )
        assertEquals(0.5f, used!!, 0.001f)
    }

    @Test
    fun `an unknown window is not a percentage`() {
        assertNull(contextUsed(listOf(session(0), assistant("E2", 2, 400))))
    }

    @Test
    fun `nothing is reported before the first turn`() {
        assertNull(contextUsed(listOf(session(1000))))
    }

    @Test
    fun `a session with no events reports nothing`() {
        assertNull(contextUsed(emptyList()))
    }

    // A user message carries no usage and must not be mistaken for a request.
    @Test
    fun `user messages do not count`() {
        val rows = listOf(
            session(1000),
            row("E2", 2, "message", """{"role":"user","content":"go"}"""),
            assistant("E3", 3, 250),
        )
        assertEquals(0.25f, contextUsed(rows)!!, 0.001f)
    }
}
