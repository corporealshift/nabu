package com.nabu.client.ui.markdown

/** One block of a reply. Blocks stack; spans run inside them. */
sealed interface Block {
    data class Paragraph(val spans: List<Span>) : Block
    data class Heading(val level: Int, val spans: List<Span>) : Block
    data class Code(val language: String, val text: String) : Block
    data class Bullets(val items: List<List<Span>>) : Block
    data class Numbers(val start: Int, val items: List<List<Span>>) : Block
    data class Quote(val spans: List<Span>) : Block
    data object Rule : Block
    data class Table(val header: List<List<Span>>, val rows: List<List<List<Span>>>) : Block
}

private val HEADING = Regex("""^(#{1,6})\s+(.*)$""")
private val BULLET = Regex("""^\s{0,3}[-*+]\s+(.*)$""")
private val NUMBER = Regex("""^\s{0,3}(\d{1,9})[.)]\s+(.*)$""")
private val RULE = Regex("""^\s{0,3}([-*_])(\s*\1){2,}\s*$""")
private val DIVIDER = Regex("""^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$""")

/**
 * The block layer of markdown, over a reply that arrives as one string.
 *
 * Strict enough that prose is not mistaken for markup — a table needs its
 * divider row, a rule needs three markers — and forgiving enough that an
 * unterminated fence renders as code rather than swallowing the reply.
 */
fun blocks(text: String): List<Block> {
    val lines = text.replace("\r\n", "\n").split('\n')
    val out = mutableListOf<Block>()
    var i = 0

    while (i < lines.size) {
        val line = lines[i]

        if (line.isBlank()) {
            i++
            continue
        }

        if (line.trimStart().startsWith("```")) {
            val language = line.trimStart().removePrefix("```").trim()
            val body = mutableListOf<String>()
            i++
            while (i < lines.size && !lines[i].trimStart().startsWith("```")) {
                body += lines[i]
                i++
            }
            i++ // the closing fence, or the end
            out += Block.Code(language, body.joinToString("\n").trim('\n'))
            continue
        }

        if (RULE.matches(line)) {
            out += Block.Rule
            i++
            continue
        }

        val heading = HEADING.find(line)
        if (heading != null) {
            out += Block.Heading(
                heading.groupValues[1].length,
                inline(heading.groupValues[2].trim()),
            )
            i++
            continue
        }

        // A table is a header row plus a divider; without the divider it is prose.
        if (line.contains('|') && i + 1 < lines.size && DIVIDER.matches(lines[i + 1])) {
            val header = cells(line)
            val rows = mutableListOf<List<List<Span>>>()
            i += 2
            while (i < lines.size && lines[i].contains('|') && lines[i].isNotBlank()) {
                rows += cells(lines[i]).let { row ->
                    row + List((header.size - row.size).coerceAtLeast(0)) { emptyList() }
                }.take(header.size)
                i++
            }
            out += Block.Table(header, rows)
            continue
        }

        if (BULLET.matches(line)) {
            val items = mutableListOf<List<Span>>()
            while (i < lines.size) {
                val m = BULLET.find(lines[i]) ?: break
                items += inline(m.groupValues[1].trim())
                i++
            }
            out += Block.Bullets(items)
            continue
        }

        val numbered = NUMBER.find(line)
        if (numbered != null) {
            val items = mutableListOf<List<Span>>()
            while (i < lines.size) {
                val m = NUMBER.find(lines[i]) ?: break
                items += inline(m.groupValues[2].trim())
                i++
            }
            out += Block.Numbers(numbered.groupValues[1].toIntOrNull() ?: 1, items)
            continue
        }

        if (line.trimStart().startsWith(">")) {
            val body = mutableListOf<String>()
            while (i < lines.size && lines[i].trimStart().startsWith(">")) {
                body += lines[i].trimStart().removePrefix(">").trim()
                i++
            }
            out += Block.Quote(inline(body.joinToString(" ").trim()))
            continue
        }

        // A paragraph runs to the next blank line or the next block that starts.
        val body = mutableListOf<String>()
        while (i < lines.size && lines[i].isNotBlank() && !startsABlock(lines, i)) {
            body += lines[i].trim()
            i++
        }
        if (body.isEmpty()) { // defensive: never fail to consume a line
            body += lines[i].trim()
            i++
        }
        out += Block.Paragraph(inline(body.joinToString(" ")))
    }

    return out
}

/** Whether this line begins something other than the paragraph in progress. */
private fun startsABlock(lines: List<String>, i: Int): Boolean {
    val line = lines[i]
    return line.trimStart().startsWith("```") ||
        line.trimStart().startsWith(">") ||
        HEADING.matches(line) ||
        RULE.matches(line) ||
        BULLET.matches(line) ||
        NUMBER.matches(line) ||
        (line.contains('|') && i + 1 < lines.size && DIVIDER.matches(lines[i + 1]))
}

/** One table row's cells, without the outer pipes. */
private fun cells(line: String): List<List<Span>> =
    line.trim().removePrefix("|").removeSuffix("|")
        .split('|')
        .map { inline(it.trim()) }
