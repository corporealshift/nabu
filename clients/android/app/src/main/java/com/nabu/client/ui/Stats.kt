package com.nabu.client.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.RoundRect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nabu.client.protocol.SessionStats
import com.nabu.client.protocol.UsageDay
import com.nabu.client.ui.theme.NabuTheme

/** What the stats screen is showing, or why it is not. */
data class StatsState(
    val sessionId: String = "",
    val loading: Boolean = false,
    val session: SessionStats? = null,
    val days: List<UsageDay> = emptyList(),
    val error: String? = null,
)

/** A count the way a person reads one: 1,284 → 1.3K, 77085054 → 77.1M. */
fun compactCount(n: Long): String = when {
    n >= 1_000_000 -> "%.1fM".format(n / 1_000_000.0)
    n >= 10_000 -> "%.0fK".format(n / 1_000.0)
    n >= 1_000 -> "%.1fK".format(n / 1_000.0)
    else -> n.toString()
}

/** A span of working time: 45s, 12m, 3h 29m. */
fun spanOf(seconds: Long): String = when {
    seconds >= 3600 -> "${seconds / 3600}h ${seconds % 3600 / 60}m"
    seconds >= 60 -> "${seconds / 60}m"
    else -> "${seconds}s"
}

/**
 * A clean top for an axis: the next 1, 2 or 5 times a power of ten at or above
 * [v], so the one tick labelled reads as a round number.
 */
fun niceMax(v: Long): Long {
    if (v <= 0) return 1
    var step = 1L
    while (step * 10 <= v) step *= 10
    for (m in longArrayOf(1, 2, 5, 10)) if (step * m >= v) return step * m
    return step * 10
}

/** Which of [count] evenly spaced points an x position is nearest, for tap readouts. */
fun nearestIndex(x: Float, width: Float, count: Int): Int {
    if (count <= 1 || width <= 0f) return 0
    val i = Math.round(x / width * (count - 1))
    return i.coerceIn(0, count - 1)
}

/**
 * How much work a session was (issue 38): what it cost, how the context grew,
 * which tools it leaned on and which files it kept going back to.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun StatsScreen(state: StatsState, onBack: () -> Unit) {
    val c = NabuTheme.colors
    Scaffold(containerColor = c.background, topBar = {
        TopAppBar(
            colors = TopAppBarDefaults.topAppBarColors(containerColor = c.background, titleContentColor = c.ink),
            title = { Text("Stats", style = MaterialTheme.typography.titleSmall) },
            navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
        )
    }) { padding ->
        val s = state.session
        if (s == null) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text(
                    state.error ?: "Loading…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = c.muted,
                )
            }
            return@Scaffold
        }
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Tile("Turns", s.turns.toString(), Modifier.weight(1f))
                Tile("Prompts", s.prompts.toString(), Modifier.weight(1f))
                Tile("Working", spanOf(s.workingSeconds), Modifier.weight(1f))
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Tile("Tokens in", compactCount(s.tokens.input), Modifier.weight(1f))
                Tile("Tokens out", compactCount(s.tokens.output), Modifier.weight(1f))
                Tile("Vetoes", s.vetoes.toString(), Modifier.weight(1f))
            }
            Text(
                "Tokens in is every request summed: each one carries the conversation again." +
                    if (s.tokens.cached > 0) " ${compactCount(s.tokens.cached)} of it came from the cache." else "",
                style = MaterialTheme.typography.bodySmall,
                color = c.muted,
            )

            if (s.perTurn.isNotEmpty()) ContextChart(s)
            if (s.tools.isNotEmpty()) ToolBars(s)
            if (s.rereads.isNotEmpty()) Rereads(s)
            if (state.days.isNotEmpty()) DayColumns(state.days)

            Text(
                "Compactions: ${s.compactions.summarize} summarised, " +
                    "${s.compactions.clearResults} tool-output clears. Interrupted ${s.interruptions}×.",
                style = MaterialTheme.typography.bodySmall,
                color = c.muted,
            )
        }
    }
}

@Composable
private fun Tile(label: String, value: String, modifier: Modifier = Modifier) {
    val c = NabuTheme.colors
    Column(
        modifier.background(c.surface, RoundedCornerShape(12.dp)).padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = c.muted)
        Text(value, style = MaterialTheme.typography.titleLarge, color = c.ink, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun Heading(title: String, subtitle: String) {
    val c = NabuTheme.colors
    Column {
        Text(title, style = MaterialTheme.typography.titleSmall, color = c.ink)
        Text(subtitle, style = MaterialTheme.typography.bodySmall, color = c.muted)
    }
}

/**
 * Context per turn: one line, the window as the ceiling, the peak labelled.
 * Tapping reads out any turn, which is this chart's hover.
 */
@Composable
private fun ContextChart(s: SessionStats) {
    val c = NabuTheme.colors
    val values = s.perTurn.map { it.input }
    val peak = values.max()
    val top = if (s.tokens.contextWindow > 0) maxOf(s.tokens.contextWindow, peak) else niceMax(peak)
    var picked by remember(s.sessionId) { mutableStateOf<Int?>(null) }

    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Heading(
            "Context per turn",
            if (s.tokens.contextWindow > 0) "Peak ${compactCount(peak)} of a ${compactCount(s.tokens.contextWindow)} window. Drops are compactions."
            else "Peak ${compactCount(peak)}.",
        )
        Text(
            picked?.let { "Turn ${it + 1}: ${compactCount(values[it])}" } ?: "Tap the chart to read a turn.",
            style = MaterialTheme.typography.labelMedium,
            color = c.muted,
        )
        Canvas(
            Modifier.fillMaxWidth().height(140.dp).pointerInput(values) {
                detectTapGestures { o ->
                    val inset = 6.dp.toPx()
                    picked = nearestIndex(o.x - inset, size.width - 2 * inset, values.size)
                }
            },
        ) {
            // Inset by the marker's radius and ring, so a marker at either end
            // is drawn whole.
            val inset = 6.dp.toPx()
            val w = size.width - 2 * inset
            val h = size.height
            fun x(i: Int) = inset + if (values.size == 1) w / 2 else w * i / (values.size - 1)
            fun y(v: Long) = h - h * (v.toFloat() / top.toFloat())

            // The ceiling (the window) and the floor, as hairlines.
            drawLine(c.line, Offset(0f, y(top)), Offset(size.width, y(top)), strokeWidth = 1.dp.toPx())
            drawLine(c.line, Offset(0f, h), Offset(size.width, h), strokeWidth = 1.dp.toPx())

            val line = Path()
            values.forEachIndexed { i, v -> if (i == 0) line.moveTo(x(i), y(v)) else line.lineTo(x(i), y(v)) }
            val wash = Path().apply {
                addPath(line)
                lineTo(x(values.size - 1), h)
                lineTo(x(0), h)
                close()
            }
            drawPath(wash, c.chart.copy(alpha = 0.10f))
            drawPath(line, c.chart, style = Stroke(width = 2.dp.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round))

            val mark = picked ?: values.indexOf(peak)
            if (picked != null) {
                drawLine(c.muted, Offset(x(mark), 0f), Offset(x(mark), h), strokeWidth = 1.dp.toPx())
            }
            // An 8dp marker with a 2dp ring in the surface color.
            drawCircle(c.background, radius = 6.dp.toPx(), center = Offset(x(mark), y(values[mark])))
            drawCircle(c.chart, radius = 4.dp.toPx(), center = Offset(x(mark), y(values[mark])))
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text("turn 1", style = MaterialTheme.typography.labelSmall, color = c.muted)
            Text("turn ${values.size}", style = MaterialTheme.typography.labelSmall, color = c.muted)
        }
    }
}

/** Tool calls, most used first: bars grow from one baseline, value at the tip. */
@Composable
private fun ToolBars(s: SessionStats) {
    val c = NabuTheme.colors
    val shown = s.tools.take(8)
    val top = shown.maxOf { it.calls }.coerceAtLeast(1)
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Heading("Tools", "Calls per tool; failures beside them.")
        shown.forEach { t ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    t.tool,
                    style = MaterialTheme.typography.labelMedium,
                    color = c.ink,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.width(84.dp),
                )
                Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
                    // The longest bar takes 60% of the row, so the count and
                    // failures at its tip always have room.
                    val frac = 0.6f * t.calls / top
                    if (frac > 0f) {
                        Box(
                            Modifier.weight(frac).height(12.dp)
                                // Square at the baseline, rounded at the data end.
                                .background(c.chart, RoundedCornerShape(topEnd = 4.dp, bottomEnd = 4.dp)),
                        )
                    }
                    Text(
                        " ${t.calls}" + if (t.errors > 0) "  ·  ${t.errors} failed" else "",
                        style = MaterialTheme.typography.labelMedium,
                        color = c.muted,
                        maxLines = 1,
                        modifier = Modifier.weight(1f - frac),
                    )
                }
            }
        }
    }
}

/** Files the agent kept going back to: a place to look, not a verdict. */
@Composable
private fun Rereads(s: SessionStats) {
    val c = NabuTheme.colors
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Heading("Read again and again", "Files read three times or more. Sometimes a file changed; sometimes it went round in circles.")
        s.rereads.take(6).forEach { r ->
            Row {
                Text(
                    r.path.substringAfterLast('/'),
                    style = MaterialTheme.typography.bodySmall,
                    color = c.ink,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                Text("×${r.reads}", style = MaterialTheme.typography.bodySmall, color = c.muted)
            }
        }
    }
}

/** Tokens per day across every session, oldest first; tapping reads out a day. */
@Composable
private fun DayColumns(days: List<UsageDay>) {
    val c = NabuTheme.colors
    val totals = days.map { it.input + it.output }
    val top = niceMax(totals.max())
    var picked by remember(days) { mutableStateOf<Int?>(null) }

    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Heading("Tokens per day", "Every session, the last ${days.size} days.")
        Text(
            picked?.let { "${days[it].date}: ${compactCount(totals[it])} tokens, ${days[it].turns} turns" }
                ?: "Busiest: ${compactCount(totals.max())}. Tap a day to read it.",
            style = MaterialTheme.typography.labelMedium,
            color = c.muted,
        )
        Canvas(
            Modifier.fillMaxWidth().height(110.dp).pointerInput(days) {
                detectTapGestures { o ->
                    picked = (o.x / (size.width.toFloat() / days.size)).toInt().coerceIn(0, days.size - 1)
                }
            },
        ) {
            val slot = size.width / days.size
            val gap = 2.dp.toPx()
            val bar = minOf(slot - gap, 24.dp.toPx())
            val r = 4.dp.toPx()
            drawLine(c.line, Offset(0f, size.height), Offset(size.width, size.height), strokeWidth = 1.dp.toPx())
            totals.forEachIndexed { i, v ->
                if (v <= 0) return@forEachIndexed
                val h = size.height * (v.toFloat() / top.toFloat())
                val left = i * slot + (slot - bar) / 2
                val path = Path().apply {
                    addRoundRect(
                        RoundRect(
                            left = left, top = size.height - h, right = left + bar, bottom = size.height,
                            topLeftCornerRadius = CornerRadius(r), topRightCornerRadius = CornerRadius(r),
                        ),
                    )
                }
                drawPath(path, if (picked == null || picked == i) c.chart else c.chart.copy(alpha = 0.45f))
            }
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(days.first().date, style = MaterialTheme.typography.labelSmall, color = c.muted)
            Text(days.last().date, style = MaterialTheme.typography.labelSmall, color = c.muted)
        }
    }
}
