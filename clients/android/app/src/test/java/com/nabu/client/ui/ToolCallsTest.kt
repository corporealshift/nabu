package com.nabu.client.ui

import com.nabu.client.protocol.NabuJson
import kotlinx.serialization.json.JsonElement
import org.junit.Assert.assertEquals
import org.junit.Test

/** The drill-down from stats (spec 7.26): what the phone works out to show a call. */
class ToolCallsTest {

    private fun args(json: String): JsonElement = NabuJson.parseToJsonElement(json)

    @Test
    fun `a call is shown by the argument that says what it was for`() {
        assertEquals("createFromFile copies", argumentsInShort(args("""{"query":"createFromFile copies","count":5}""")))
        assertEquals("https://developer.android.com/x", argumentsInShort(args("""{"url":"https://developer.android.com/x"}""")))
        assertEquals("does Room copy the file?", argumentsInShort(args("""{"question":"does Room copy the file?"}""")))
        assertEquals("a.go", argumentsInShort(args("""{"path":"a.go","offset":10}""")))
    }

    @Test
    fun `the query wins over the path when a call has both`() {
        assertEquals("needle", argumentsInShort(args("""{"path":"src","query":"needle"}""")))
    }

    @Test
    fun `a call with nothing telling is shown as its JSON`() {
        assertEquals("""{"seconds":30}""", argumentsInShort(args("""{"seconds":30}""")))
        assertEquals("", argumentsInShort(null))
    }

    @Test
    fun `a telling argument that is not text is not used`() {
        assertEquals("""{"query":3}""", argumentsInShort(args("""{"query":3}""")))
    }

    @Test
    fun `a long or many-line argument is one short line`() {
        assertEquals("go test ./... and more", argumentsInShort(args("""{"command":"go test ./...\n  and more"}""")))
        val long = argumentsInShort(args("""{"query":"${"a".repeat(300)}"}"""), max = 20)
        assertEquals(20, long.length)
        assertEquals('…', long.last())
    }

    @Test
    fun `periods and kinds read as words`() {
        assertEquals("today", periodWords(1))
        assertEquals("the last 7 days", periodWords(7))
        assertEquals("every session", kindWords("all"))
        assertEquals("unattended sessions", kindWords("unattended"))
        assertEquals("interactive sessions", kindWords("interactive"))
    }
}
