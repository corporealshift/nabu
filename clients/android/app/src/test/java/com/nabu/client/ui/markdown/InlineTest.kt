package com.nabu.client.ui.markdown

import org.junit.Assert.assertEquals
import org.junit.Test

class InlineTest {

    private fun plain(spans: List<Span>) = spans.joinToString("") { it.text }

    @Test
    fun `text with no markup is one span`() {
        assertEquals(listOf(Span("just words")), inline("just words"))
    }

    @Test
    fun `bold is marked and its markers removed`() {
        assertEquals(
            listOf(Span("a "), Span("bold", bold = true), Span(" word")),
            inline("a **bold** word"),
        )
    }

    @Test
    fun `underscores mark bold too`() {
        assertEquals(listOf(Span("bold", bold = true)), inline("__bold__"))
    }

    @Test
    fun `italic is marked`() {
        assertEquals(
            listOf(Span("an "), Span("aside", italic = true)),
            inline("an *aside*"),
        )
    }

    /** The agent writes ***both*** often enough to matter. */
    @Test
    fun `bold and italic nest`() {
        assertEquals(
            listOf(Span("both", bold = true, italic = true)),
            inline("***both***"),
        )
    }

    @Test
    fun `code spans are marked`() {
        assertEquals(
            listOf(Span("run "), Span("go test ./...", code = true)),
            inline("run `go test ./...`"),
        )
    }

    /** Markup inside a code span is text, which is the whole point of one. */
    @Test
    fun `a code span is literal`() {
        assertEquals(
            listOf(Span("**not bold**", code = true)),
            inline("`**not bold**`"),
        )
    }

    @Test
    fun `strikethrough is marked`() {
        assertEquals(listOf(Span("gone", strike = true)), inline("~~gone~~"))
    }

    @Test
    fun `a link keeps its text and carries its target`() {
        assertEquals(
            listOf(Span("the spec", link = "https://example.com/spec")),
            inline("[the spec](https://example.com/spec)"),
        )
    }

    /** A lone marker is a character someone typed, not the start of markup. */
    @Test
    fun `an unclosed marker stays literal`() {
        assertEquals("2 * 3 and a_b", plain(inline("2 * 3 and a_b")))
        assertEquals(listOf(Span("2 * 3 and a_b")), inline("2 * 3 and a_b"))
    }

    @Test
    fun `an unclosed code span stays literal`() {
        assertEquals(listOf(Span("a ` backtick")), inline("a ` backtick"))
    }

    /** A path with underscores is not three italics. */
    @Test
    fun `underscores inside a word do not mark italic`() {
        assertEquals(listOf(Span("do_the_thing")), inline("do_the_thing"))
    }

    @Test
    fun `a backslash escapes a marker`() {
        assertEquals(listOf(Span("*not italic*")), inline("""\*not italic\*"""))
    }

    @Test
    fun `empty text is no spans`() {
        assertEquals(emptyList<Span>(), inline(""))
    }
}
