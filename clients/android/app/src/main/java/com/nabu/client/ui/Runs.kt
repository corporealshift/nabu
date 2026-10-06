package com.nabu.client.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedButton
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
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

/** Starts every label the runner reads or sets on a goal's home (docs/specs/2026-10-06-goals-design.md). */
const val GOAL_PREFIX = "goal:"

/**
 * A session's labels once it is handed to the runner as a goal: any goal
 * label it had is replaced by goal:requested, and the rest are kept. On a
 * blocked goal that is also how it is resumed.
 */
fun goalLabels(current: List<String>): List<String> =
    current.filterNot { it.startsWith(GOAL_PREFIX) } + "${GOAL_PREFIX}requested"

/** How far a goal is, from its home's labels, as "goal: runs, round 2"; null for a session that is not a goal's home. */
fun goalStatus(labels: List<String>): String? {
    var step: String? = null
    var round: String? = null
    for (l in labels) {
        if (!l.startsWith(GOAL_PREFIX)) continue
        val rest = l.removePrefix(GOAL_PREFIX)
        if (rest.startsWith("round:")) round = rest.removePrefix("round:") else step = rest
    }
    return step?.let { if (round != null) "goal: $it, round $round" else "goal: $it" }
}

/**
 * A card in the list, and how many of its ancestors are listed above it: a
 * run's steps are one deep, a goal's runs one deep and their steps two.
 */
data class Placed(val card: SessionCard, val depth: Int) {
    val child: Boolean get() = depth > 0
}

/** How far the list is indented, whatever the parents say. */
private const val MAX_DEPTH = 4

/**
 * The list with each session straight after its parent, and its own children
 * after it in turn, in the daemon's order otherwise. A session whose parent
 * is not listed (archived, say) stands on its own.
 */
fun groupByParent(cards: List<SessionCard>): List<Placed> {
    val listed = cards.map { it.row.id }.toSet()
    val children = cards.filter { it.options.parent in listed }.groupBy { it.options.parent }
    val out = ArrayList<Placed>(cards.size)
    fun add(card: SessionCard, depth: Int) {
        out += Placed(card, minOf(depth, MAX_DEPTH))
        children[card.row.id]?.forEach { add(it, depth + 1) }
    }
    for (card in cards) {
        if (card.options.parent !in listed) add(card, 0)
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

/** What is under a session: how many sessions, and how many of them are working. */
data class Under(val total: Int, val working: Int = 0) {
    /** As the card says it: "9 sessions · 1 working". */
    val label: String
        get() = "$total ${if (total == 1) "session" else "sessions"}" + if (working > 0) " · $working working" else ""
}

/**
 * What is under each session, at any depth. A folded card says when
 * something under it is running: a goal's home never runs itself, and with
 * its steps folded away nothing else on the screen would show the work.
 */
fun underCounts(cards: List<SessionCard>): Map<String, Under> {
    val parent = cards.associate { it.row.id to it.options.parent }
    val out = HashMap<String, Under>()
    for (card in cards) {
        val running = card.row.state == "running"
        var p = card.options.parent
        var depth = 0
        while (p.isNotEmpty() && p in parent && depth <= MAX_DEPTH) {
            val u = out[p] ?: Under(0)
            out[p] = Under(u.total + 1, u.working + if (running) 1 else 0)
            p = parent[p].orEmpty()
            depth++
        }
    }
    return out
}

/**
 * The grouped list with the children of every session not in [expanded] left
 * out: each family starts folded, and opens one level at a time (issue 135).
 */
fun visible(placed: List<Placed>, expanded: Set<String>): List<Placed> {
    val shown = HashSet<String>()
    return placed.filter { p ->
        val parent = p.card.options.parent
        // Grouped order puts a parent before its children, so it is decided first.
        val show = p.depth == 0 || (parent in shown && parent in expanded)
        if (show) shown += p.card.row.id
        show
    }
}

/**
 * The text if [text] is a /goal command: "" for a plain /goal, which resumes a
 * blocked goal. Null for anything else.
 */
fun parseGoal(text: String): String? {
    val t = text.trim()
    if (t == "/goal") return ""
    if (t.startsWith("/goal ") || t.startsWith("/goal\n")) return t.removePrefix("/goal").trim()
    return null
}

/**
 * The brief for a run or a goal started from the list, in [workspace]. A
 * screen rather than a dialog: a brief runs to paragraphs, and the keyboard
 * takes half of whatever holds it. There is no conversation to write one
 * from, so it is required. [onStart] says whether it is a goal, and reports a
 * failure through its callback, which puts the reason here and lets the reader
 * try again.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BriefScreen(
    workspace: String,
    onStart: (brief: String, goal: Boolean, onFailed: (String) -> Unit) -> Unit,
    onBack: () -> Unit,
) {
    val c = NabuTheme.colors
    var brief by rememberSaveable { mutableStateOf("") }
    var starting by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    Scaffold(
        containerColor = c.background,
        topBar = {
            TopAppBar(
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = c.background,
                    titleContentColor = c.ink,
                ),
                title = {
                    Text(
                        "Run or goal in ${projectName(workspace)}",
                        style = MaterialTheme.typography.titleSmall,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
                navigationIcon = { TextButton(onClick = onBack) { Text("Cancel") } },
            )
        },
        bottomBar = {
            Row(
                Modifier.fillMaxWidth().navigationBarsPadding().imePadding().padding(12.dp),
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                val start = { goal: Boolean ->
                    starting = true
                    error = null
                    onStart(brief.trim(), goal) { starting = false; error = it }
                }
                OutlinedButton(
                    onClick = { start(true) },
                    enabled = brief.isNotBlank() && !starting,
                    modifier = Modifier.weight(1f),
                ) { Text("Start goal") }
                Button(
                    onClick = { start(false) },
                    enabled = brief.isNotBlank() && !starting,
                    modifier = Modifier.weight(1f),
                ) { Text(if (starting) "Starting…" else "Start run") }
            }
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).padding(horizontal = 16.dp)) {
            Text(
                "Say what to build or change, and what done looks like. A run plans, works and " +
                    "checks it in a worktree of its own, then opens a pull request. A goal is " +
                    "broader: the runner breaks it into runs, merges each into one branch, and " +
                    "checks the whole until it is met, then opens one pull request.",
                style = MaterialTheme.typography.bodySmall,
                color = c.muted,
            )
            error?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodySmall,
                    color = c.danger,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
            OutlinedTextField(
                value = brief,
                onValueChange = { brief = it },
                label = { Text("Brief or goal") },
                modifier = Modifier.fillMaxWidth().weight(1f).padding(vertical = 12.dp),
            )
        }
    }
}

/**
 * Asks what to hand the runner: a run's brief, or a goal. [goalText] is the
 * goal of a session that already is a goal's home, offered for editing: asking
 * again resumes a blocked goal, and the edit is the owner's guidance.
 */
@Composable
fun RunDialog(goalText: String?, onRun: (String) -> Unit, onGoal: (String) -> Unit, onDismiss: () -> Unit) {
    val c = NabuTheme.colors
    var text by remember { mutableStateOf(goalText.orEmpty()) }
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = c.surface,
        titleContentColor = c.ink,
        textContentColor = c.muted,
        title = { Text(if (goalText != null) "Resume this goal?" else "Hand this session to the runner?") },
        text = {
            Column {
                Text(
                    if (goalText != null) {
                        "The runner takes the goal up again where it stopped. Edit it to tell the " +
                            "runner what to do differently."
                    } else {
                        "A run plans, works and checks one brief in a worktree of its own, then opens a " +
                            "pull request; leave its brief empty to have it written from this " +
                            "conversation. A goal is broader: the runner breaks it into runs, merges " +
                            "each into one branch, and checks the whole until it is met. Either way, " +
                            "the work appears under this session."
                    },
                )
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it },
                    label = { Text(if (goalText != null) "Goal" else "Brief or goal") },
                    minLines = 3,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            Row {
                TextButton(onClick = { onGoal(text.trim()) }, enabled = text.isNotBlank()) {
                    Text(if (goalText != null) "Resume goal" else "Goal")
                }
                if (goalText == null) {
                    TextButton(onClick = { onRun(text.trim()) }) { Text("Run") }
                }
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}
