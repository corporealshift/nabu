package com.rho.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
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
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.rho.client.data.parseInstant
import com.rho.client.protocol.GoalStatus
import com.rho.client.protocol.GoalStatusBrief
import com.rho.client.protocol.Verdict
import com.rho.client.ui.theme.RhoTheme

/*
 * A goal's page (docs/specs/2026-10-11-run-page-design.md): its rounds, newest
 * first, each with what the check said and the briefs it ran.
 */

/** One round of a goal. [millis] is how long it took, when its verdicts say. */
data class RoundView(
    val round: Int,
    val briefs: List<GoalStatusBrief>,
    val verdict: Verdict?,
    val millis: Long?,
)

/** The goal's rounds, newest first. A round takes from the verdict before it, or the goal's start, to its own. */
fun rounds(s: GoalStatus): List<RoundView> {
    val numbers = (s.briefs.map { it.round } + s.verdicts.map { it.round } + listOfNotNull(s.round.takeIf { it > 0 }))
        .distinct().sorted()
    val verdictOf = s.verdicts.associateBy { it.round }
    return numbers.map { n ->
        val verdict = verdictOf[n]
        val end = verdict?.let { parseInstant(it.at) }
        val start = if (n == numbers.first()) parseInstant(s.started) else verdictOf[n - 1]?.let { parseInstant(it.at) }
        RoundView(
            round = n,
            briefs = s.briefs.filter { it.round == n },
            verdict = verdict,
            millis = if (end != null && start != null) (end - start).coerceAtLeast(0) else null,
        )
    }.reversed()
}

/** The header's line: "round 6 of 10 · runs". */
fun goalLine(s: GoalStatus): String = buildList {
    if (s.round > 0) add(if (s.rounds > 0) "round ${s.round} of ${s.rounds}" else "round ${s.round}")
    add(s.step)
}.joinToString(" · ")

/** A brief's state as the page says it: a goal's finished run was merged. */
fun briefState(b: GoalStatusBrief): String = when (b.state) {
    "done" -> "merged"
    "failed" -> if (b.failedAt.isNotBlank()) "failed at ${b.failedAt}" else "failed"
    else -> b.state
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun GoalPage(
    title: String,
    status: GoalStatus,
    tree: TreeLoad,
    onBack: () -> Unit,
    onChat: () -> Unit,
    onOpenRun: (String) -> Unit,
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
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(status.text.ifBlank { "Goal" }, style = MaterialTheme.typography.titleMedium, color = c.ink)
                Text(goalLine(status), style = MaterialTheme.typography.bodyMedium, color = c.accent)
                parkedLine(status.parked)?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = c.muted)
                }
                if (status.why.isNotBlank()) {
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
            }

            rounds(status).forEach { RoundCard(it, onOpenRun) }

            GoalStats(status, tree)
        }
    }
}

@Composable
private fun RoundCard(r: RoundView, onOpenRun: (String) -> Unit) {
    val c = RhoTheme.colors
    var showReason by remember(r.round) { mutableStateOf(false) }
    Column(
        Modifier.fillMaxWidth().background(c.surface, RoundedCornerShape(12.dp)).padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Round ${r.round}", style = MaterialTheme.typography.titleSmall, color = c.ink, modifier = Modifier.weight(1f))
            r.millis?.let { Text(spanOf(it / 1000), style = MaterialTheme.typography.labelMedium, color = c.muted) }
        }
        r.briefs.forEach { b ->
            Row(
                Modifier.fillMaxWidth()
                    .then(if (b.run.isNotBlank()) Modifier.clickable { onOpenRun(b.run) } else Modifier)
                    .padding(vertical = 4.dp),
                verticalAlignment = Alignment.Top,
            ) {
                val mark = when (b.state) {
                    "done" -> "✓"
                    "failed" -> "✗"
                    "running" -> "…"
                    else -> "·"
                }
                Text(mark, style = MaterialTheme.typography.bodyMedium,
                    color = if (b.state == "failed") c.danger else c.accent, modifier = Modifier.width(20.dp))
                Column(Modifier.weight(1f)) {
                    Text(b.title, style = MaterialTheme.typography.bodyMedium, color = c.ink)
                    Text(briefState(b) + if (b.why.isNotBlank()) ": ${b.why}" else "",
                        style = MaterialTheme.typography.bodySmall, color = c.muted, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
        }
        r.verdict?.let { v ->
            Text(
                (if (v.met) "Check: met" else "Check: not met") + if (showReason) " ▾" else " ▸",
                style = MaterialTheme.typography.labelMedium,
                color = if (v.met) c.accent else c.muted,
                modifier = Modifier.clickable { showReason = !showReason }.padding(vertical = 2.dp),
            )
            if (showReason) Text(v.reason, style = MaterialTheme.typography.bodySmall, color = c.muted)
        }
    }
}

@Composable
private fun GoalStats(s: GoalStatus, load: TreeLoad) {
    val c = RhoTheme.colors
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Heading("Stats", "Every run of the goal, and their steps, summed.")
        val merged = s.briefs.count { it.state == "done" }
        val failed = s.briefs.count { it.state == "failed" }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Tile("Runs merged", merged.toString(), Modifier.weight(1f))
            Tile("Runs failed", failed.toString(), Modifier.weight(1f))
            Tile("Rounds", s.round.toString(), Modifier.weight(1f))
        }
        val tree = load.tree
        if (tree == null) {
            Text(load.error ?: "Loading…", style = MaterialTheme.typography.bodySmall, color = c.muted)
        } else {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Tile("Working", spanOf(tree.workingSeconds), Modifier.weight(1f))
                Tile("Turns", compactCount(tree.turns.toLong()), Modifier.weight(1f))
                Tile("Tokens in", compactCount(tree.tokens.input), Modifier.weight(1f))
            }
        }
    }
}
