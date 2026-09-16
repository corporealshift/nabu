package com.nabu.client.ui.markdown

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class BlocksTest {

    private fun text(b: Block) = (b as Block.Paragraph).spans.joinToString("") { it.text }

    @Test
    fun `a blank line separates paragraphs`() {
        val out = blocks("first\n\nsecond")

        assertEquals(2, out.size)
        assertEquals("first", text(out[0]))
        assertEquals("second", text(out[1]))
    }

    /** A wrapped sentence is one paragraph, not two lines. */
    @Test
    fun `a single newline joins a paragraph`() {
        assertEquals("one two", text(blocks("one\ntwo").single()))
    }

    @Test
    fun `headings carry their level`() {
        val out = blocks("# Big\n\n### Small")

        assertEquals(1, (out[0] as Block.Heading).level)
        assertEquals(3, (out[1] as Block.Heading).level)
        assertEquals("Small", (out[1] as Block.Heading).spans.single().text)
    }

    @Test
    fun `a fenced block keeps its lines and language`() {
        val code = blocks("```kotlin\nval x = 1\nval y = 2\n```").single() as Block.Code

        assertEquals("kotlin", code.language)
        assertEquals("val x = 1\nval y = 2", code.text)
    }

    /** Markup inside a fence is code, and blank lines inside it are kept. */
    @Test
    fun `a fence is literal`() {
        val code = blocks("```\n# not a heading\n\nstill code\n```").single() as Block.Code

        assertEquals("# not a heading\n\nstill code", code.text)
    }

    /** An unterminated fence still renders, rather than eating the reply. */
    @Test
    fun `an unclosed fence runs to the end`() {
        val code = blocks("```\nhalf a block").single() as Block.Code

        assertEquals("half a block", code.text)
    }

    @Test
    fun `bullets become one list`() {
        val list = blocks("- one\n- two\n* three").single() as Block.Bullets

        assertEquals(3, list.items.size)
        assertEquals("two", list.items[1].joinToString("") { it.text })
    }

    @Test
    fun `numbered items keep their numbers`() {
        val list = blocks("1. first\n2. second").single() as Block.Numbers

        assertEquals(1, list.start)
        assertEquals(2, list.items.size)
    }

    @Test
    fun `a numbered list may start anywhere`() {
        assertEquals(3, (blocks("3. third\n4. fourth").single() as Block.Numbers).start)
    }

    @Test
    fun `a quote drops its markers`() {
        val quote = blocks("> quoted\n> still quoted").single() as Block.Quote

        assertEquals("quoted still quoted", quote.spans.joinToString("") { it.text })
    }

    @Test
    fun `a rule is a rule`() {
        assertTrue(blocks("---").single() is Block.Rule)
        assertTrue(blocks("***").single() is Block.Rule)
    }

    @Test
    fun `a table keeps its header and rows`() {
        val table = blocks(
            """
            | Tool | What it does |
            |---|---|
            | `read` | Read a file |
            | `bash` | Run a command |
            """.trimIndent()
        ).single() as Block.Table

        assertEquals(2, table.header.size)
        assertEquals("What it does", table.header[1].joinToString("") { it.text })
        assertEquals(2, table.rows.size)
        assertEquals("read", table.rows[0][0].single().text)
        assertTrue("a cell keeps its code marks", table.rows[0][0].single().code)
    }

    /** A ragged row is padded rather than dropped: the content still matters. */
    @Test
    fun `a short row is padded to the header`() {
        val table = blocks("| a | b | c |\n|---|---|---|\n| one |").single() as Block.Table

        assertEquals(3, table.rows.single().size)
    }

    /** A pipe in prose is not a table. */
    @Test
    fun `pipes without a divider stay a paragraph`() {
        assertTrue(blocks("a | b\nc | d").single() is Block.Paragraph)
    }

    @Test
    fun `empty input is no blocks`() {
        assertEquals(emptyList<Block>(), blocks("   \n\n  "))
    }

    /** The case that started this: a real reply, end to end. */
    @Test
    fun `a mixed reply parses into its parts`() {
        val out = blocks(
            """
            Here's what I've got:

            **Tools:**

            | Tool | What it does |
            |---|---|
            | `read` | Read a text file |

            - one
            - two

            ```sh
            go test ./...
            ```

            That's the inventory.
            """.trimIndent()
        )

        assertEquals(
            listOf(
                Block.Paragraph::class, Block.Paragraph::class, Block.Table::class,
                Block.Bullets::class, Block.Code::class, Block.Paragraph::class,
            ),
            out.map { it::class },
        )
    }
}
