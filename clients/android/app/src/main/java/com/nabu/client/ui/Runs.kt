package com.nabu.client.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import com.nabu.client.ui.theme.NabuTheme

// Orchestrated runs, as the phone sees them
// (docs/specs/2026-09-30-orchestrated-runs-design.md). The runner is a process
// on the daemon's machine; the phone starts a run by labeling a session, and
// watches one through the labels the runner sets on its home and the parent
// every step session carries. The daemon gives labels no meaning; this does.

/** Starts every label the runner reads or sets on a run's home. */
const val RUN_PREFIX = "run:"

/**
 * A session's labels once it is handed to the runner: any run label it had is
 * replaced by run:requested, and the rest are kept. On a failed run that is
 * also how it is resumed.
 */
fun runLabels(current: List<String>): List<String> =
    current.filterNot { it.startsWith(RUN_PREFIX) } + "${RUN_PREFIX}requested"

/** How far a run is, from its home's labels, as "run: fix (3/10)"; null for a session that is not a run's home. */
fun runStatus(labels: List<String>): String? {
    var step: String? = null
    var attempt: String? = null
    for (l in labels) {
        if (!l.startsWith(RUN_PREFIX)) continue
        val rest = l.removePrefix(RUN_PREFIX)
        if (rest.startsWith("attempt:")) attempt = rest.removePrefix("attempt:") else step = rest
    }
    return step?.let { if (attempt != null) "run: $it ($attempt)" else "run: $it" }
}

/** A card in the list, and whether it is drawn under its run's home. */
data class Placed(val card: SessionCard, val child: Boolean)

/**
 * The list with each step session straight after its home, in the daemon's
 * order otherwise. A step whose home is not listed (archived, say) stands on
 * its own.
 */
fun groupByParent(cards: List<SessionCard>): List<Placed> {
    val listed = cards.map { it.row.id }.toSet()
    val children = cards.filter { it.options.parent in listed }.groupBy { it.options.parent }
    val out = ArrayList<Placed>(cards.size)
    for (card in cards) {
        if (card.options.parent in listed) continue
        out += Placed(card, child = false)
        children[card.row.id]?.forEach { out += Placed(it, child = true) }
    }
    return out
}

/**
 * The brief if [text] is a /run command, as in the terminal client: "" for a
 * plain /run, whose brief the runner writes from the conversation. Null for
 * anything else, which is a prompt.
 */
fun parseRun(text: String): String? {
    val t = text.trim()
    if (t == "/run") return ""
    if (t.startsWith("/run ") || t.startsWith("/run\n")) return t.removePrefix("/run").trim()
    return null
}

/** Asks for a run's brief before handing the session to the runner. */
@Composable
fun RunDialog(onRun: (String) -> Unit, onDismiss: () -> Unit) {
    val c = NabuTheme.colors
    var brief by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = c.surface,
        titleContentColor = c.ink,
        textContentColor = c.muted,
        title = { Text("Hand this session to the runner?") },
        text = {
            Column {
                Text(
                    "The runner plans, works and checks in a worktree of its own, then opens a " +
                        "pull request. Its steps appear under this session. Leave the brief " +
                        "empty to have it written from this conversation.",
                )
                OutlinedTextField(
                    value = brief,
                    onValueChange = { brief = it },
                    label = { Text("Brief") },
                    minLines = 3,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = { TextButton(onClick = { onRun(brief.trim()) }) { Text("Run") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}
