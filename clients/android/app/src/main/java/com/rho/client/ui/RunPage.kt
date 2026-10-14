package com.rho.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.rho.client.data.parseInstant
import com.rho.client.protocol.RunStatus
import com.rho.client.protocol.TreeStats
import com.rho.client.ui.theme.RhoTheme

/*
 * A run's page (docs/specs/2026-10-11-run-page-design.md): where it is now,
 * how it got there, and what it took. Everything shown is worked out by the
 * plain functions first in this file, so it can be tested without a screen.
 */

/** A step of the run's timeline: finished, or the one under way. */
data class TimelineRow(
    val step: String,
    /** The task, from 1; 0 is none. */
    val task: Int,
    /** True, false, or null for the step still going. */
    val ok: Boolean?,
    /** One line on what came of it. */
    val result: String,
    val session: String,
    /** How long it took, or has taken so far; null when unknown. */
    val millis: Long?,
)

/** The finished steps, oldest first, then the current one unless the run is over. */
fun timeline(s: RunStatus, now: Long): List<TimelineRow> {
    val rows = ArrayList<TimelineRow>()
    var before = parseInstant(s.started)
    for (h in s.history) {
        val at = parseInstant(h.at)
        rows += TimelineRow(
            step = h.step,
            task = h.task,
            ok = h.ok,
            result = when {
                h.result.isNotBlank() -> h.result
                !h.ok -> h.why
                else -> ""
            },
            session = h.session,
            millis = if (at != null && before != null) (at - before).coerceAtLeast(0) else null,
        )
        if (at != null) before = at
    }
    if (s.step != "done" && s.step != "failed") {
        val since = parseInstant(s.since)
        rows += TimelineRow(
            step = s.step,
            task = s.task,
            ok = null,
            result = if (s.waiting) "waiting for a slot" else "",
            session = s.session,
            millis = since?.let { (now - it).coerceAtLeast(0) },
        )
    }
    return rows
}

/** A step as a person names it: "work · task 2", "plan-review". */
fun stepName(step: String, task: Int): String = if (task > 0) "$step · task $task" else step

/**
 * The header's line: the step, the task of how many, the try, and how long
 * the step has taken. A run that is over says how it ended.
 */
fun stepLine(s: RunStatus, now: Long): String {
    if (s.step == "done") return "done"
    if (s.step == "failed") return "failed at ${s.failedAt.ifBlank { "?" }}"
    val parts = mutableListOf(s.step)
    if (s.task > 0) parts += if (s.tasks.isNotEmpty()) "task ${s.task} of ${s.tasks.size}" else "task ${s.task}"
    if (s.attempt > 0) parts += "try ${s.attempt + 1}"
    parseInstant(s.since)?.let { parts += "${spanOf((now - it).coerceAtLeast(0) / 1000)} in this step" }
    return parts.joinToString(" · ")
}

/** One task's share of the work. */
data class TaskBar(val task: Int, val title: String, val turns: Int, val tries: Int)

/**
 * Turns per task, from the work sessions in the timeline and each session's
 * turns in the tree. A task's tries are its work sessions, the current one
 * included.
 */
fun turnsPerTask(s: RunStatus, tree: TreeStats?): List<TaskBar> {
    val turnsOf = tree?.members?.associate { it.sessionId to it.turns }.orEmpty()
    val sessions = linkedMapOf<Int, MutableList<String>>()
    for (h in s.history) {
        if (h.step == "work" && h.task > 0) sessions.getOrPut(h.task) { mutableListOf() } += h.session
    }
    if (s.step == "work" && s.task > 0 && s.session.isNotBlank()) {
        val list = sessions.getOrPut(s.task) { mutableListOf() }
        if (s.session !in list) list += s.session
    }
    return sessions.toSortedMap().map { (task, ids) ->
        TaskBar(
            task = task,
            title = s.tasks.getOrNull(task - 1).orEmpty(),
            turns = ids.sumOf { turnsOf[it] ?: 0 },
            tries = ids.size,
        )
    }
}

/**
 * What the page says of a gate parked because Claude is not answering (a
 * limit, an outage), or null when it is not
 * (docs/specs/2026-10-13-claude-park-and-skip-design.md).
 */
fun parkedLine(parked: String): String? = if (parked.isBlank()) null else "Waiting for Claude: $parked"

/** How many times Claude was consulted at the run's gates, from the timeline. */
fun claudeGates(s: RunStatus): Int =
    s.history.count { it.step in setOf("plan-review", "verify-review", "revise", "final-review") }

/** What a run's page shows of the step session under way. */
data class LiveStep(
    val state: String,
    val turns: Int,
    val maxTurns: Int,
    val contextUsed: Float?,
    val updatedAt: Long,
)

/** The tree's numbers as the page holds them. */
data class TreeLoad(val tree: TreeStats? = null, val error: String? = null)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RunPage(
    title: String,
    status: RunStatus,
    tree: TreeLoad,
    live: LiveStep?,
    now: Long,
    onBack: () -> Unit,
    onChat: () -> Unit,
    onOpenSession: (String) -> Unit,
    onOpenGoal: (String) -> Unit,
    onSkip: (() -> Unit)? = null,
) {
    val c = RhoTheme.colors
    val uri = LocalUriHandler.current
    Scaffold(containerColor = c.background, topBar = {
        TopAppBar(
            colors = TopAppBarDefaults.topAppBarColors(containerColor = c.background, titleContentColor = c.ink),
            title = { Text(title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis) },
            navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
            actions = {
                TextButton(onClick = onChat) {
                    Text("Chat", style = MaterialTheme.typography.labelMedium, color = c.accent)
                }
            },
        )
    }) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            // Header.
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(status.title.ifBlank { "Run" }, style = MaterialTheme.typography.titleMedium, color = c.ink)
                Text(stepLine(status, now), style = MaterialTheme.typography.bodyMedium, color = c.accent)
                if (status.step == "failed" && status.why.isNotBlank()) {
                    Text(status.why, style = MaterialTheme.typography.bodySmall, color = c.muted)
                }
                if (status.pr.url.isNotBlank()) {
                    Text(
                        "Pull request #${status.pr.number}",
                        style = MaterialTheme.typography.bodySmall,
                        color = c.accent,
                        modifier = Modifier.clickable { uri.openUri(status.pr.url) },
                    )
                }
                if (status.goal.isNotBlank()) {
                    Text(
                        "Part of: ${status.goalTitle.ifBlank { status.goal.takeLast(6) }}",
                        style = MaterialTheme.typography.bodySmall,
                        color = c.accent,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.clickable { onOpenGoal(status.goal) },
                    )
                }
            }

            // Now.
            if (status.step != "done" && status.step != "failed") {
                Column {
                    NowCard(status, live, now, onOpenSession)
                    if (onSkip != null) SkipCheck(status.step, onSkip)
                }
            }

            // Timeline.
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Heading("Timeline", "Every step so far. Tap one with a session to open it.")
                timeline(status, now).forEach { row -> TimelineLine(row, onOpenSession) }
            }

            // Stats.
            RunStats(status, tree)

            // Brief.
            if (status.brief.isNotBlank()) BriefFold(status.brief)
        }
    }
}

@Composable
private fun NowCard(s: RunStatus, live: LiveStep?, now: Long, onOpenSession: (String) -> Unit) {
    val c = RhoTheme.colors
    val open = s.session.isNotBlank()
    Column(
        Modifier.fillMaxWidth().background(c.surface, RoundedCornerShape(12.dp))
            .then(if (open) Modifier.clickable { onOpenSession(s.session) } else Modifier)
            .padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Text("Now", style = MaterialTheme.typography.labelMedium, color = c.muted)
        Text(stepName(s.step, s.task), style = MaterialTheme.typography.titleMedium, color = c.ink, fontWeight = FontWeight.SemiBold)
        val detail = when {
            s.parked.isNotBlank() -> parkedLine(s.parked)!!
            s.waiting -> "Waiting for a slot."
            !open -> "Done by the runner or Claude, without a session."
            live == null -> "Session ${s.session.takeLast(6)}."
            else -> buildList {
                add(live.state)
                add(if (live.maxTurns > 0) "turn ${live.turns} of ${live.maxTurns}" else "turn ${live.turns}")
                ago(live.updatedAt, now)?.let { add("last active $it") }
                live.contextUsed?.let { add("context ${(it * 100).toInt()}%") }
            }.joinToString(" · ")
        }
        Text(detail, style = MaterialTheme.typography.bodySmall, color = c.muted)
    }
}

@Composable
private fun TimelineLine(row: TimelineRow, onOpenSession: (String) -> Unit) {
    val c = RhoTheme.colors
    val mark = when (row.ok) {
        true -> "✓"
        false -> "✗"
        null -> "…"
    }
    val markColor = when (row.ok) {
        true -> c.accent
        false -> c.danger
        null -> c.muted
    }
    Row(
        Modifier.fillMaxWidth()
            .then(if (row.session.isNotBlank()) Modifier.clickable { onOpenSession(row.session) } else Modifier)
            .padding(vertical = 6.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Text(mark, style = MaterialTheme.typography.bodyMedium, color = markColor, modifier = Modifier.width(20.dp))
        Column(Modifier.weight(1f)) {
            Text(stepName(row.step, row.task), style = MaterialTheme.typography.bodyMedium, color = c.ink)
            if (row.result.isNotBlank()) {
                Text(row.result, style = MaterialTheme.typography.bodySmall, color = c.muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
        row.millis?.let {
            Text(spanOf(it / 1000), style = MaterialTheme.typography.labelMedium, color = c.muted)
        }
    }
}

@Composable
private fun RunStats(s: RunStatus, load: TreeLoad) {
    val c = RhoTheme.colors
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Heading("Stats", "Every session of the run, summed.")
        val tree = load.tree
        if (tree == null) {
            Text(load.error ?: "Loading…", style = MaterialTheme.typography.bodySmall, color = c.muted)
        } else {
            val wall = parseInstant(s.started)?.let { start ->
                val end = parseInstant(tree.lastEventAt) ?: parseInstant(s.updated) ?: start
                spanOf((end - start).coerceAtLeast(0) / 1000)
            } ?: "?"
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Tile("Wall time", wall, Modifier.weight(1f))
                Tile("Working", spanOf(tree.workingSeconds), Modifier.weight(1f))
                Tile("Turns", tree.turns.toString(), Modifier.weight(1f))
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Tile("Tokens in", compactCount(tree.tokens.input), Modifier.weight(1f))
                Tile("Tokens out", compactCount(tree.tokens.output), Modifier.weight(1f))
                Tile("Sessions", tree.sessions.toString(), Modifier.weight(1f))
            }
        }
        Text(
            "Claude reviewed ${claudeGates(s)}× · fixes ${s.fixes} · verify revisions ${s.revisions} · CI fixes ${s.ciFixes}",
            style = MaterialTheme.typography.bodySmall,
            color = c.muted,
        )
        val bars = turnsPerTask(s, tree)
        if (bars.isNotEmpty()) TaskBars(bars)
    }
}

/** Turns per task as bars, with the tries beside them. */
@Composable
private fun TaskBars(bars: List<TaskBar>) {
    val c = RhoTheme.colors
    val top = bars.maxOf { it.turns }.coerceAtLeast(1)
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Heading("Turns per task", "Tries beyond the first are marked.")
        bars.forEach { b ->
            Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(
                    "${b.task}. ${b.title}",
                    style = MaterialTheme.typography.labelMedium,
                    color = c.ink,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.width(120.dp),
                )
                Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
                    val frac = 0.6f * b.turns / top
                    if (frac > 0f) {
                        Box(
                            Modifier.weight(frac).height(12.dp)
                                .background(c.chart, RoundedCornerShape(topEnd = 4.dp, bottomEnd = 4.dp)),
                        )
                    }
                    Text(
                        " ${b.turns}" + if (b.tries > 1) "  ·  ${b.tries} tries" else "",
                        style = MaterialTheme.typography.labelMedium,
                        color = if (b.tries > 1) c.danger else c.muted,
                        maxLines = 1,
                        modifier = Modifier.weight(1f - frac),
                    )
                }
            }
        }
    }
}

/** The brief, folded until asked for. */
@Composable
internal fun BriefFold(brief: String) {
    val c = RhoTheme.colors
    var open by remember { mutableStateOf(false) }
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(
            if (open) "Brief ▾" else "Brief ▸",
            style = MaterialTheme.typography.titleSmall,
            color = c.ink,
            modifier = Modifier.clickable { open = !open }.padding(vertical = 4.dp),
        )
        if (open) Text(brief.trim(), style = MaterialTheme.typography.bodySmall, color = c.muted)
    }
}

/** A status this app cannot read: from a newer runner, most likely. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UnsupportedStatus(title: String, status: com.rho.client.protocol.ParsedStatus.Unsupported, onBack: () -> Unit, onChat: () -> Unit) {
    val c = RhoTheme.colors
    Scaffold(containerColor = c.background, topBar = {
        TopAppBar(
            colors = TopAppBarDefaults.topAppBarColors(containerColor = c.background, titleContentColor = c.ink),
            title = { Text(title, style = MaterialTheme.typography.titleSmall) },
            navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
        )
    }) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(
                "This ${status.kind}'s status is version ${status.version}, from a runner newer than this app. " +
                    "Update the app to read it.",
                style = MaterialTheme.typography.bodyMedium,
                color = c.muted,
            )
            TextButton(onClick = onChat) { Text("Open the chat") }
        }
    }
}
