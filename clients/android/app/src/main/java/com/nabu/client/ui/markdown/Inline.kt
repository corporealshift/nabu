package com.nabu.client.ui.markdown

/** A run of text with the marks that apply to it. */
data class Span(
    val text: String,
    val bold: Boolean = false,
    val italic: Boolean = false,
    val code: Boolean = false,
    val strike: Boolean = false,
    val link: String? = null,
)

/**
 * The inline layer of markdown: emphasis, code spans, strikethrough, links.
 *
 * A marker with no partner stays literal rather than swallowing the rest of the
 * line, because the agent writes `2 * 3` and `do_the_thing` as often as it
 * writes emphasis.
 */
fun inline(text: String): List<Span> = merge(scan(text, Marks()))

private data class Marks(
    val bold: Boolean = false,
    val italic: Boolean = false,
    val strike: Boolean = false,
)

private fun scan(text: String, marks: Marks): List<Span> {
    val out = mutableListOf<Span>()
    val literal = StringBuilder()
    var i = 0

    fun flush() {
        if (literal.isNotEmpty()) {
            out += Span(literal.toString(), marks.bold, marks.italic, strike = marks.strike)
            literal.clear()
        }
    }

    while (i < text.length) {
        val c = text[i]

        // A backslash makes the next character itself, whatever it is.
        if (c == '\\' && i + 1 < text.length) {
            literal.append(text[i + 1])
            i += 2
            continue
        }

        if (c == '`') {
            val end = text.indexOf('`', i + 1)
            if (end > i + 1) {
                flush()
                out += Span(text.substring(i + 1, end), code = true)
                i = end + 1
                continue
            }
        }

        if (c == '[') {
            val link = readLink(text, i)
            if (link != null) {
                flush()
                out += scan(link.label, marks).map { it.copy(link = link.href) }
                i = link.end
                continue
            }
        }

        val marker = markerAt(text, i)
        if (marker != null) {
            val end = closingIndex(text, i + marker.length, marker)
            if (end > 0 && emphasisIsMarkup(text, i, end, marker)) {
                flush()
                out += scan(text.substring(i + marker.length, end), marks.plus(marker))
                i = end + marker.length
                continue
            }
        }

        literal.append(c)
        i++
    }

    flush()
    return out
}

/** The emphasis run starting here, longest first so *** beats ** beats *. */
private fun markerAt(text: String, i: Int): String? {
    for (m in listOf("***", "___", "~~", "**", "__", "*", "_")) {
        if (text.startsWith(m, i)) return m
    }
    return null
}

private fun Marks.plus(marker: String) = when (marker) {
    "***", "___" -> copy(bold = true, italic = true)
    "**", "__" -> copy(bold = true)
    "~~" -> copy(strike = true)
    else -> copy(italic = true)
}

/** Where this marker closes, or -1. Escaped markers do not close it. */
private fun closingIndex(text: String, from: Int, marker: String): Int {
    var i = from
    while (i < text.length) {
        if (text[i] == '\\') {
            i += 2
            continue
        }
        if (text.startsWith(marker, i)) return i
        i++
    }
    return -1
}

/**
 * Whether a matched pair is emphasis rather than punctuation. An underscore
 * inside a word (`do_the_thing`) is part of the word, and a marker with a space
 * just inside it (`2 * 3 *`) is arithmetic.
 */
private fun emphasisIsMarkup(text: String, open: Int, close: Int, marker: String): Boolean {
    val inner = text.substring(open + marker.length, close)
    if (inner.isEmpty() || inner.first().isWhitespace() || inner.last().isWhitespace()) return false

    if (marker.startsWith("_")) {
        val before = text.getOrNull(open - 1)
        val after = text.getOrNull(close + marker.length)
        if (before != null && before.isLetterOrDigit()) return false
        if (after != null && after.isLetterOrDigit()) return false
    }
    return true
}

private class Link(val label: String, val href: String, val end: Int)

/** `[label](href)`, or null if this bracket does not start one. */
private fun readLink(text: String, open: Int): Link? {
    val labelEnd = text.indexOf(']', open + 1)
    if (labelEnd < 0 || text.getOrNull(labelEnd + 1) != '(') return null
    val hrefEnd = text.indexOf(')', labelEnd + 2)
    if (hrefEnd < 0) return null
    return Link(
        label = text.substring(open + 1, labelEnd),
        href = text.substring(labelEnd + 2, hrefEnd),
        end = hrefEnd + 1,
    )
}

/** Neighbours that carry identical marks are one span, not several. */
private fun merge(spans: List<Span>): List<Span> {
    val out = mutableListOf<Span>()
    for (s in spans) {
        if (s.text.isEmpty()) continue
        val last = out.lastOrNull()
        if (last != null && last.copy(text = "") == s.copy(text = "")) {
            out[out.size - 1] = last.copy(text = last.text + s.text)
        } else {
            out += s
        }
    }
    return out
}
