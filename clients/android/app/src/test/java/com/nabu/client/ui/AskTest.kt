package com.nabu.client.ui

import com.nabu.client.net.AskRequest
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Issue 36: the agent can put a question to whoever is watching. The phone is
 * often the only client attached, so an unanswerable question here leaves the
 * agent blocked until it times out.
 */
class AskTest {

    private fun question(vararg choices: String) = AskRequest(
        id = JsonPrimitive(1),
        requestId = "r1",
        sessionId = "S1",
        question = "which design do you want?",
        choices = choices.toList(),
    )

    /** Spec 7.15: the daemon reads an answer field. */
    @Test
    fun `the answer is an answer field`() {
        assertEquals("""{"answer":"the fast one"}""", askReply("the fast one").toString())
    }

    /** The field name lives in the daemon; drifting from it answers nothing. */
    @Test
    fun `the answer field matches the one the daemon reads`() {
        var dir: java.io.File? = java.io.File("").absoluteFile
        while (dir != null && !java.io.File(dir, "daemon/api/requests.go").isFile) dir = dir.parentFile
        val go = java.io.File(requireNotNull(dir), "daemon/api/requests.go").readText()
        val reply = go.substringAfter("type askReply struct").substringBefore("}")

        assertTrue("the daemon no longer reads an answer field: $reply", reply.contains("json:\"answer\""))
    }

    /** An empty answer is not an answer, and sending one unblocks the agent with nothing. */
    @Test
    fun `a blank answer cannot be sent`() {
        assertFalse(canSend(""))
        assertFalse(canSend("   "))
        assertTrue(canSend("neither"))
    }

    /** Surrounding space is a keyboard artefact, not part of the answer. */
    @Test
    fun `the answer is trimmed`() {
        assertEquals("""{"answer":"blue"}""", askReply("  blue  ").toString())
    }

    /**
     * A question with no choices is still a question: the text field is the
     * only way to answer it, so it can never be hidden.
     */
    @Test
    fun `free text is offered whether or not there are choices`() {
        assertTrue(offersFreeText(question()))
        assertTrue(offersFreeText(question("alpha", "beta")))
    }
}
