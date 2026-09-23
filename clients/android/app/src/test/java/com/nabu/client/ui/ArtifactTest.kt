package com.nabu.client.ui

import com.nabu.client.data.EventRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Issue 41: a page the agent made shows in the transcript and opens sandboxed. */
class ArtifactTest {

    private fun call(tool: String, args: String) = EventRow(
        id = "E1", sessionId = "S1", ordinal = 1, type = "tool_call",
        raw = """{"id":"E1","type":"tool_call","timestamp":"2026-09-22T00:00:00Z",
            "data":{"call_id":"c1","tool":"$tool","arguments":$args,"source":"model"}}""",
    )

    @Test
    fun `a page is its own line, and other calls are not`() {
        val lines = transcript(
            listOf(call("artifact", """{"name":"timings","title":"Test timings","html":"<svg></svg>"}""")),
            synced = true,
        )
        val page = lines.single() as Line.Artifact
        assertEquals("timings", page.name)
        assertEquals("Test timings", page.title)
        assertEquals("<svg></svg>", page.copyText())

        val other = transcript(listOf(call("read", """{"path":"a.go"}""")), synced = true)
        assertTrue(other.single() is Line.ToolRan)
    }

    @Test
    fun `a call with no page falls back to an ordinary tool line`() {
        val lines = transcript(listOf(call("artifact", """{"name":"x","title":"t"}""")), synced = true)
        assertTrue(lines.single() is Line.ToolRan)
    }

    // The same cases as the daemon's Wrap: nothing in the page may come first.
    @Test
    fun `the sandbox comes before anything in the page`() {
        for (page in listOf(
            "<!DOCTYPE html><html><head><title>t</title></head><body>x</body></html>",
            "<svg><circle r=\"4\"/></svg>",
            "<script>new WebSocket('ws://127.0.0.1:8737')</script><html><body>x</body></html>",
            "\uFEFF  <!doctype html><p>x</p>",
        )) {
            val out = artifactPage(page)
            val policy = out.indexOf("Content-Security-Policy")
            assertTrue("no policy in $out", policy > 0)
            for (later in listOf("<script", "<html", "<head", "<svg", "<p>", "<body")) {
                val i = out.lowercase().indexOf(later)
                assertTrue("$later comes before the policy in $out", i < 0 || i > policy)
            }
            assertTrue(out.lowercase().startsWith("<!doctype html"))
        }
        assertTrue(ARTIFACT_SANDBOX.startsWith("default-src 'none'"))
    }
}
