package com.nabu.client.ui.markdown

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.nabu.client.ui.CopyButton
import com.nabu.client.ui.theme.NabuColors
import com.nabu.client.ui.theme.NabuTheme

/**
 * A reply, rendered. The agent writes markdown whether or not anything reads
 * it, so a phone that shows the asterisks is showing its working.
 */
@Composable
fun MarkdownText(text: String, modifier: Modifier = Modifier) {
    val c = NabuTheme.colors
    val parsed = remember(text) { blocks(text) }

    Column(modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        parsed.forEach { block ->
            when (block) {
                is Block.Paragraph -> Body(block.spans, c)

                is Block.Heading -> Text(
                    annotated(block.spans, c),
                    style = MaterialTheme.typography.bodyMedium,
                    color = c.ink,
                    fontWeight = FontWeight.Bold,
                    fontSize = headingSize(block.level),
                    modifier = Modifier.padding(top = 4.dp),
                )

                is Block.Code -> CodeBlock(block, c)

                is Block.Bullets -> Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    block.items.forEach { Item("•", it, c) }
                }

                is Block.Numbers -> Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    block.items.forEachIndexed { n, item ->
                        Item("${block.start + n}.", item, c)
                    }
                }

                is Block.Quote -> Row(
                    Modifier.fillMaxWidth().height(IntrinsicSize.Min),
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    Box(Modifier.width(3.dp).fillMaxHeight().background(c.accent))
                    Body(block.spans, c)
                }

                Block.Rule -> Box(Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
                    Box(Modifier.fillMaxWidth().height(1.dp).background(c.line))
                }

                is Block.Table -> TableBlock(block, c)
            }
        }
    }
}

@Composable
private fun Body(spans: List<Span>, c: NabuColors) {
    Text(
        annotated(spans, c),
        style = MaterialTheme.typography.bodyMedium,
        color = c.ink,
    )
}

@Composable
private fun Item(marker: String, spans: List<Span>, c: NabuColors) {
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(
            marker,
            style = MaterialTheme.typography.bodyMedium,
            color = c.muted,
        )
        Body(spans, c)
    }
}

@Composable
private fun CodeBlock(block: Block.Code, c: NabuColors) {
    // Code is not reflowed: it scrolls sideways rather than wrapping mid-token.
    Box(Modifier.fillMaxWidth()) {
        Column(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(8.dp))
                .background(c.code)
                .padding(10.dp),
        ) {
            if (block.language.isNotEmpty()) {
                Text(
                    block.language,
                    style = MaterialTheme.typography.labelSmall,
                    color = c.muted,
                    modifier = Modifier.padding(bottom = 4.dp),
                )
            }
            Text(
                block.text,
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
                color = c.codeInk,
                softWrap = false,
                modifier = Modifier
                    .padding(end = 24.dp)
                    .horizontalScroll(rememberScrollState()),
            )
        }
        // The block's own text, never the reply around it: a command copied out
        // of a fence should paste into a shell and run.
        CopyButton(
            block.text,
            description = "Copy code",
            modifier = Modifier.align(Alignment.TopEnd).padding(2.dp),
        )
    }
}

@Composable
private fun TableBlock(block: Block.Table, c: NabuColors) {
    // A table is the one thing allowed to be wider than the screen.
    val widths = columnWidths(block)
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(c.code)
            .horizontalScroll(rememberScrollState())
            .padding(10.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(COLUMN_GAP)) {
            block.header.forEachIndexed { col, cell ->
                Text(
                    annotated(cell, c),
                    style = MaterialTheme.typography.labelMedium,
                    color = c.muted,
                    fontWeight = FontWeight.SemiBold,
                    modifier = Modifier.width(widths[col]),
                )
            }
        }
        Box(Modifier.width(tableWidth(widths)).height(1.dp).background(c.line))
        block.rows.forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(COLUMN_GAP)) {
                row.forEachIndexed { col, cell ->
                    Text(
                        annotated(cell, c),
                        style = MaterialTheme.typography.bodySmall,
                        color = c.ink,
                        modifier = Modifier.width(widths[col]),
                    )
                }
            }
        }
    }
}

/**
 * A width per column, so cells line up down the table. Taken from the widest
 * cell's character count, which is close enough without measuring text.
 */
private fun columnWidths(block: Block.Table): List<Dp> =
    block.header.indices.map { col ->
        val chars = (listOf(block.header) + block.rows)
            .mapNotNull { row -> row.getOrNull(col) }
            .maxOfOrNull { cell -> cell.sumOf { it.text.length } } ?: 6
        (chars.coerceIn(4, 34) * 7.6).dp
    }

/** The table's own width, since a scrolling row has none to fill. */
private fun tableWidth(widths: List<Dp>): Dp =
    widths.fold(0.dp) { acc, w -> acc + w } + (COLUMN_GAP * (widths.size - 1).coerceAtLeast(0))

private val COLUMN_GAP = 16.dp

private fun headingSize(level: Int) = when (level) {
    1 -> 21.sp
    2 -> 18.sp
    3 -> 16.sp
    else -> 15.sp
}

/** Spans as one styled string, so a sentence with emphasis stays one flow. */
private fun annotated(spans: List<Span>, c: NabuColors): AnnotatedString = buildAnnotatedString {
    spans.forEach { s ->
        withStyle(
            SpanStyle(
                fontWeight = if (s.bold) FontWeight.Bold else null,
                fontStyle = if (s.italic) FontStyle.Italic else null,
                fontFamily = if (s.code) FontFamily.Monospace else null,
                color = when {
                    s.link != null -> c.accent
                    s.code -> c.accent
                    else -> c.ink
                },
                textDecoration = when {
                    s.strike -> TextDecoration.LineThrough
                    s.link != null -> TextDecoration.Underline
                    else -> null
                },
            )
        ) { append(s.text) }
    }
}
