package com.nabu.client.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * What each kind of line puts on the clipboard.
 *
 * The rule under test throughout: copying yields the source, not the render.
 */
class CopyTest {

    @Test
    fun `a reply copies as the markdown it was written in`() {
        val markdown = "## Heading\n\n| a | b |\n|---|---|\n| 1 | 2 |"
        assertEquals(markdown, Line.AgentSaid("E1", markdown).copyText())
    }

    @Test
    fun `what the reader wrote copies verbatim`() {
        assertEquals("do the thing", Line.UserSaid("E1", "do the thing").copyText())
    }

    // The screen shows at most COLLAPSED_OUTPUT_CHARS of a long result. Copying
    // what fit would hand back a truncated file, which is worse than useless
    // because it looks complete.
    @Test
    fun `a long tool result copies whole, not the part on screen`() {
        val long = "x".repeat(COLLAPSED_OUTPUT_CHARS * 3)
        val line = Line.ToolOutput("E1", "bash", long, truncated = true)

        val copied = line.copyText()

        assertEquals(long, copied)
        assertEquals(COLLAPSED_OUTPUT_CHARS * 3, copied!!.length)
    }

    // The summary on screen is cut at 120 characters with an ellipsis. Copying
    // that would paste a command that cannot run.
    @Test
    fun `a tool call copies the whole command, not the shortened one`() {
        val command = "go test ./... -run " + "Test".repeat(60)
        val line = Line.ToolRan("E1", "bash", summary = command.take(120) + "…", full = command)

        assertEquals("bash $command", line.copyText())
    }

    @Test
    fun `a thought copies its text`() {
        assertEquals("let me check", Line.Thought("E1", "let me check").copyText())
    }

    @Test
    fun `a veto copies the sentence the screen shows`() {
        val line = Line.Veto("E1", "verify", "tests were never run")
        assertEquals("verify refused the stop: tests were never run", line.copyText())
    }

    // The screen shows only a marker for a compaction. The summary is the whole
    // reason to offer copy on it at all.
    @Test
    fun `a compaction copies the summary the screen withholds`() {
        val line = Line.Compacted("E1", "Earlier: set up the daemon, fixed the race.")
        assertEquals("Earlier: set up the daemon, fixed the race.", line.copyText())
    }

    @Test
    fun `a gap has nothing behind it to copy`() {
        assertNull(Line.Gap("gap-top", neverFetched = true).copyText())
        assertNull(Line.Gap("gap-top", neverFetched = false).copyText())
    }

    // A control that copies an empty string is a control that does nothing. The
    // call sites use null to decide not to draw one.
    @Test
    fun `blank lines offer nothing to copy`() {
        assertNull(Line.AgentSaid("E1", "").copyText())
        assertNull(Line.ToolOutput("E1", "bash", "", truncated = false).copyText())
        assertNull(Line.Note("E1", "", "info").copyText())
        assertNull(Line.ToolRan("E1", "bash", "", full = "").copyText())
        assertNull(Line.Compacted("E1", "").copyText())
    }
}
