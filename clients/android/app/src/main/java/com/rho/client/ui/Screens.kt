package com.rho.client.ui

import androidx.compose.foundation.background
import androidx.compose.ui.draw.alpha
import androidx.compose.animation.core.tween
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.LinearEasing
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.IconButton
import androidx.compose.material3.Icon
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material.icons.Icons
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.rho.client.data.OutboxRow
import com.rho.client.data.SessionRow
import com.rho.client.protocol.Task
import com.rho.client.settings.Settings
import com.rho.client.ui.markdown.MarkdownText
import com.rho.client.ui.theme.Mode
import com.rho.client.ui.theme.RhoTheme
import com.rho.client.ui.theme.Scheme
import com.rho.client.ui.theme.Wedge

/** Host, port and token. The token is required, so the screen says why. */
@Composable
fun SettingsScreen(
    current: Settings,
    systemDark: Boolean,
    onAppearance: (Scheme, Mode) -> Unit,
    onSave: (Settings) -> Unit,
) {
    var host by remember(current) { mutableStateOf(current.host) }
    var port by remember(current) { mutableStateOf(current.port.toString()) }
    var token by remember(current) { mutableStateOf(current.token) }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .systemBarsPadding()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text("Connect to a daemon", style = MaterialTheme.typography.headlineSmall)
        Text(
            "The daemon refuses any connection that is not from its own machine " +
                "without a token, so this is not optional.",
            style = MaterialTheme.typography.bodySmall,
        )
        OutlinedTextField(
            value = host, onValueChange = { host = it },
            label = { Text("Host") },
            placeholder = { Text("100.x.y.z, or a tailnet name") },
            singleLine = true, modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = port, onValueChange = { port = it.filter(Char::isDigit) },
            label = { Text("Port") }, singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = token, onValueChange = { token = it },
            label = { Text("Token") }, singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
        Button(
            onClick = {
                // copy, so saving the connection does not discard the palette
                onSave(
                    current.copy(
                        host = host.trim(),
                        port = port.toIntOrNull() ?: 8737,
                        token = token.trim(),
                    )
                )
            },
            enabled = host.isNotBlank() && token.isNotBlank(),
        ) { Text("Save and connect") }

        AppearanceSection(
            scheme = current.scheme,
            mode = current.mode,
            systemDark = systemDark,
            onChange = onAppearance,
        )
    }
}

@OptIn(
    ExperimentalMaterial3Api::class,
    androidx.compose.foundation.ExperimentalFoundationApi::class,
    androidx.compose.foundation.layout.ExperimentalLayoutApi::class,
)
@Composable
fun SessionListScreen(
    sessions: List<SessionCard>,
    connection: Connection,
    error: String?,
    /** Still listing and fetching every session, once connected. */
    catchingUp: Boolean = false,
    /** The sessions being fetched right now. */
    syncing: Set<String> = emptySet(),
    onOpen: (String) -> Unit,
    onSettings: () -> Unit,
    onNewSession: () -> Unit,
    onArchive: (String) -> Unit = {},
    onArchived: () -> Unit = {},
    onStats: () -> Unit = {},
    onNewRun: () -> Unit = {},
    /** Sessions opened to show what is under them; the rest are folded. */
    expanded: Set<String> = emptySet(),
    onToggle: (String) -> Unit = {},
) {
    val counts = remember(sessions) { underCounts(sessions) }
    var archiving by remember { mutableStateOf<SessionCard?>(null) }
    archiving?.let { card ->
        val under = counts[card.row.id]?.total ?: 0
        ArchiveDialog(
            title = card.title.ifBlank { projectName(card.row.workspace.ifBlank { card.row.id }) } +
                if (under > 0) ", with the $under ${if (under == 1) "session" else "sessions"} under it," else "",
            onArchive = { onArchive(card.row.id); archiving = null },
            onDismiss = { archiving = null },
        )
    }
    Scaffold(containerColor = RhoTheme.colors.background, floatingActionButton = {
        // Only when connected: starting a session needs the daemon, and a
        // button that cannot work is worse than no button.
        if (connection == Connection.Connected) {
            // A run starts from here too, rather than from inside a session
            // made only to hand over: picking a repository and writing a brief
            // is the whole of it.
            Column(horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                ExtendedFloatingActionButton(
                    onClick = onNewRun,
                    containerColor = RhoTheme.colors.surface,
                    contentColor = RhoTheme.colors.accent,
                ) { Text("Run or goal") }
                ExtendedFloatingActionButton(
                    onClick = onNewSession,
                    containerColor = RhoTheme.colors.accent,
                    contentColor = RhoTheme.colors.onAccent,
                ) { Text("New session") }
            }
        }
    }, topBar = {
        Column {
            TopAppBar(
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = RhoTheme.colors.background,
                    titleContentColor = RhoTheme.colors.ink,
                ),
                title = { Text("Rho", fontWeight = FontWeight.SemiBold) },
                actions = {
                    Text(
                        connectionLabel(connection),
                        style = MaterialTheme.typography.labelMedium,
                        color = RhoTheme.colors.muted,
                        modifier = Modifier.padding(end = 12.dp),
                    )
                    TextButton(onClick = onStats) { Text("Stats") }
                    TextButton(onClick = onArchived) { Text("Archived") }
                    TextButton(onClick = onSettings) { Text("Settings") }
                },
            )
            LoadingBar(listLoading(connection, catchingUp))
        }
    }) { padding ->
        // With sessions mirrored, a lost connection is a line above them, not a
        // screen in place of them. Swapping the whole list for an error card
        // made every reconnect flash between the two (issue 69), and hid the
        // offline copy the mirror exists to show.
        val offline = error != null && connection != Connection.Connected
        if (offline && sessions.isEmpty()) {
            Card(
                colors = CardDefaults.cardColors(containerColor = RhoTheme.colors.surface),
                modifier = Modifier.fillMaxWidth().padding(padding).padding(12.dp),
            ) {
                Column(Modifier.padding(12.dp)) {
                    Text(
                        "Cannot reach the daemon",
                        style = MaterialTheme.typography.titleSmall,
                        color = RhoTheme.colors.danger,
                    )
                    Text(
                        error.orEmpty(),
                        style = MaterialTheme.typography.bodySmall,
                        color = RhoTheme.colors.muted,
                    )
                }
            }
            return@Scaffold
        }
        if (sessions.isEmpty()) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text(
                    emptyListText(connection, catchingUp),
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            return@Scaffold
        }
        LazyColumn(
            modifier = Modifier.fillMaxSize().padding(padding),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            if (offline) {
                item(key = "offline") {
                    Text(
                        if (connection == Connection.Connecting) "Reconnecting — ${error.orEmpty()}"
                        else "Offline — ${error.orEmpty()}",
                        style = MaterialTheme.typography.labelMedium,
                        color = RhoTheme.colors.danger,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            items(visible(groupByParent(sessions), expanded), key = { it.card.row.id }) { placed ->
                val card = placed.card
                val s = card.row
                Card(
                    colors = CardDefaults.cardColors(containerColor = RhoTheme.colors.surface),
                    // Held to archive (issue 56): the list is where sessions pile up.
                    // A run's steps sit under its home, indented.
                    modifier = Modifier.fillMaxWidth().padding(start = (20 * placed.depth).dp).combinedClickable(
                        onClick = { onOpen(s.id) },
                        onLongClick = { archiving = card },
                        onLongClickLabel = "Archive",
                    ),
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(
                        Modifier.weight(1f).padding(14.dp),
                        verticalArrangement = Arrangement.spacedBy(4.dp),
                    ) {
                        // Several sessions can share one workspace, so the last
                        // prompt leads: it is what tells them apart.
                        Text(
                            card.title.ifBlank { "Nothing asked yet" },
                            style = MaterialTheme.typography.titleSmall,
                            color = if (card.title.isBlank()) RhoTheme.colors.muted
                            else RhoTheme.colors.ink,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                        // Wraps rather than squeezes: in a Row, whatever did not fit was
                        // crushed to a sliver and stacked a letter at a time, which made a
                        // card many lines tall.
                        FlowRow(
                            horizontalArrangement = Arrangement.spacedBy(10.dp),
                            verticalArrangement = Arrangement.spacedBy(2.dp),
                        ) {
                            Text(
                                "${projectName(s.workspace.ifBlank { s.id })}  ·  ${s.state}",
                                style = MaterialTheme.typography.labelMedium,
                                color = RhoTheme.colors.muted,
                            )
                            (runStatus(card.options.labels) ?: goalStatus(card.options.labels))?.let {
                                Text(
                                    it,
                                    style = MaterialTheme.typography.labelMedium,
                                    color = RhoTheme.colors.accent,
                                )
                            }
                            counts[s.id]?.let { under ->
                                Text(
                                    under.label,
                                    style = MaterialTheme.typography.labelMedium,
                                    color = if (under.working > 0) RhoTheme.colors.accent else RhoTheme.colors.muted,
                                )
                            }
                            syncLabel(s.synced, s.id in syncing)?.let {
                                Text(
                                    it,
                                    style = MaterialTheme.typography.labelMedium,
                                    color = if (s.id in syncing) RhoTheme.colors.muted else RhoTheme.colors.danger,
                                )
                            }
                            stoppedAge(s.state, s.updatedAt, System.currentTimeMillis())?.let {
                                Text(
                                    it,
                                    style = MaterialTheme.typography.labelMedium,
                                    color = RhoTheme.colors.muted,
                                )
                            }
                        }
                    }
                    // Unfolding has a button of its own, a thumb wide; the
                    // rest of the card opens the session (issue 135).
                    counts[s.id]?.let {
                        val open = s.id in expanded
                        IconButton(onClick = { onToggle(s.id) }, modifier = Modifier.size(48.dp)) {
                            Icon(
                                if (open) Icons.Filled.ExpandLess else Icons.Filled.ExpandMore,
                                contentDescription = if (open) "Fold" else "Show the sessions under it",
                                tint = RhoTheme.colors.accent,
                            )
                        }
                    }
                    }
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TranscriptScreen(
    title: String,
    view: TranscriptView,
    synced: Boolean,
    state: String,
    pending: List<OutboxRow>,
    blocked: List<OutboxRow>,
    tasks: List<Task>,
    compacting: Boolean,
    error: String?,
    onSend: (String) -> Unit,
    /** This session is being fetched from the daemon. */
    downloading: Boolean = false,
    onTaskDone: (String) -> Unit,
    onResume: () -> Unit,
    onInterrupt: () -> Unit,
    onCompact: () -> Unit,
    onStats: () -> Unit = {},
    onContext: (String) -> Unit = {},
    onEffort: (String) -> Unit = {},
    onRetryBlocked: (String) -> Unit,
    onDiscardBlocked: (String) -> Unit,
    onBack: () -> Unit,
    onRun: (String) -> Unit = {},
    onGoal: (String) -> Unit = {},
    /** What to call this session's run home, when it is a step of one. */
    parentTitle: String? = null,
    onOpenParent: () -> Unit = {},
    /** Back to the run's or goal's status page, when the session has one. */
    onStatus: (() -> Unit)? = null,
) {
    var choosingContext by remember { mutableStateOf(false) }
    if (choosingContext) {
        ContextDialog(
            current = view.options.context,
            onPick = { onContext(it); choosingContext = false },
            onDismiss = { choosingContext = false },
        )
    }
    var choosingEffort by remember { mutableStateOf(false) }
    if (choosingEffort) {
        EffortDialog(
            current = view.options.effort,
            onPick = { onEffort(it); choosingEffort = false },
            onDismiss = { choosingEffort = false },
        )
    }
    var running by remember { mutableStateOf(false) }
    if (running) {
        RunDialog(
            goalText = view.options.description.takeIf { goalStatus(view.options.labels) != null },
            onRun = { onRun(it); running = false },
            onGoal = { onGoal(it); running = false },
            onDismiss = { running = false },
        )
    }
    // Nothing, not a gap, until the mirror has been read: an unread mirror is
    // not one that is behind.
    val lines = remember(view, synced, downloading) {
        if (!view.loaded) emptyList() else withGap(view.lines, synced, view.fetched, downloading)
    }
    val listState = rememberLazyListState()

    // Jump on opening, glide after. Animating from the top of two thousand
    // lines to the bottom is a long way to scroll just to arrive.
    var shown by remember { mutableStateOf(0) }
    LaunchedEffect(lines.size) {
        if (lines.isEmpty()) return@LaunchedEffect
        if (shown == 0) listState.scrollToItem(lines.size - 1)
        else listState.animateScrollToItem(lines.size - 1)
        shown = lines.size
    }

    Scaffold(
        containerColor = RhoTheme.colors.background,
        topBar = {
            Column {
                TopAppBar(
                    colors = TopAppBarDefaults.topAppBarColors(
                        containerColor = RhoTheme.colors.background,
                        titleContentColor = RhoTheme.colors.ink,
                    ),
                    title = { Text(title, style = MaterialTheme.typography.titleSmall) },
                    actions = {
                        // The badge stays out: it warns before compaction, and a
                        // warning in a menu is never seen.
                        ContextBadge(view.contextUsed, view.options.context) { choosingContext = true }
                        SessionMenu(
                            // A step of a run is the runner's, not a thing to hand over.
                            sessionMenuItems(onStatus != null, view.options.effort, view.options.parent.isBlank(), compacting),
                        ) { item ->
                            when (item) {
                                MenuItem.Status -> onStatus?.invoke()
                                MenuItem.Effort -> choosingEffort = true
                                MenuItem.Run -> running = true
                                MenuItem.Compact -> onCompact()
                                MenuItem.Stats -> onStats()
                            }
                        }
                    },
                    navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
                )
                // Until the mirror is read, and while the daemon is asked: an
                // empty page otherwise looks like an empty session.
                LoadingBar(downloading || !view.loaded)
            }
        },
        bottomBar = {
            Composer(
                state, compacting, pending, blocked, tasks,
                onSend, onTaskDone, onResume, onInterrupt, onRetryBlocked, onDiscardBlocked,
            )
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            // The daemon's refusals land here: compacting a running session is
            // turned down, and a reason nobody sees is not a reason.
            error?.let { RefusalBanner(it) }
            val runOf = runStatus(view.options.labels)
            RunBar(runOf ?: goalStatus(view.options.labels), parentTitle, runOf != null, onOpenParent)
            // A run's or a goal's home is never prompted, so its brief is what
            // it says. A goal's runs have a parent and are homes all the same.
            if (view.options.description.isNotBlank() && (view.options.parent.isBlank() || runOf != null)) {
                Text(
                    view.options.description.trim(),
                    style = MaterialTheme.typography.bodySmall,
                    color = RhoTheme.colors.muted,
                    maxLines = 6,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp),
                )
            }
            LazyColumn(
                state = listState,
                modifier = Modifier.fillMaxSize(),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                // One container per line rather than one around the list: a
                // LazyColumn recycles its items, and a selection spanning an
                // item that scrolls out of composition loses its anchor. Per
                // line, a selection survives because everything it covers
                // stays composed.
                items(lines, key = { it.key }) {
                    SelectionContainer { LineView(it) }
                }
            }
        }
    }
}

/** The session's actions behind one icon: with effort among them, the title had no room. */
@Composable
private fun SessionMenu(items: List<MenuEntry>, onPick: (MenuItem) -> Unit) {
    val c = RhoTheme.colors
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }) {
            Icon(Icons.Filled.MoreVert, contentDescription = "More", tint = c.ink)
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }, containerColor = c.surface) {
            items.forEach { entry ->
                DropdownMenuItem(
                    text = { Text(entry.label, color = if (entry.enabled) c.ink else c.muted) },
                    enabled = entry.enabled,
                    onClick = { open = false; onPick(entry.item) },
                )
            }
        }
    }
}

/** What the session menu can do. */
enum class MenuItem { Status, Effort, Run, Compact, Stats }

/** One line of the session menu: its label says the current setting where there is one. */
data class MenuEntry(val item: MenuItem, val label: String, val enabled: Boolean = true)

/**
 * The session menu, in order. Status only where the screen has one, Run only
 * for a session of its own, and Compact greyed while a summary is written.
 *
 * Compact is not greyed while the session is running. The daemon owns that
 * rule and states it plainly when it refuses; a second copy on the phone would
 * be one more thing to keep in step, and a disabled control explains nothing.
 */
fun sessionMenuItems(hasStatus: Boolean, effort: String, canRun: Boolean, compacting: Boolean): List<MenuEntry> =
    buildList {
        if (hasStatus) add(MenuEntry(MenuItem.Status, "Status"))
        add(MenuEntry(MenuItem.Effort, effortChipText(effort).replaceFirstChar { it.uppercase() }))
        if (canRun) add(MenuEntry(MenuItem.Run, "Run"))
        add(MenuEntry(MenuItem.Compact, if (compacting) "Compacting…" else "Compact", enabled = !compacting))
        add(MenuEntry(MenuItem.Stats, "Stats"))
    }

/** What the daemon said when it turned something down. */
@Composable
private fun RefusalBanner(text: String) {
    val c = RhoTheme.colors
    Text(
        text,
        style = MaterialTheme.typography.labelMedium,
        color = c.danger,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 6.dp),
    )
}

@Composable
private fun Composer(
    state: String,
    compacting: Boolean,
    pending: List<OutboxRow>,
    blocked: List<OutboxRow>,
    tasks: List<Task>,
    onSend: (String) -> Unit,
    onTaskDone: (String) -> Unit,
    onResume: () -> Unit,
    onInterrupt: () -> Unit,
    onRetryBlocked: (String) -> Unit,
    onDiscardBlocked: (String) -> Unit,
) {
    var text by remember { mutableStateOf("") }
    val c = RhoTheme.colors
    Column(
        Modifier
            .fillMaxWidth()
            .background(c.background)
            // Rises with the keyboard, and clears the gesture bar and the
            // screen's rounded corners when it is down.
            .imePadding()
            .navigationBarsPadding()
            .padding(horizontal = 12.dp, vertical = 10.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        WorkingIndicator(state, compacting)
        // A requested compaction runs while the session is idle, and on a
        // local model it takes minutes: it needs a way out as much as a turn.
        if (state == "running" || compacting) StopBar(onInterrupt)
        TaskCard(tasks, onTaskDone)
        if (state == "paused") ResumeBar(onResume)
        BlockedPrompts(blocked, onRetryBlocked, onDiscardBlocked)
        if (pending.isNotEmpty()) {
            // A prompt the user believes was sent and was not is the failure
            // the outbox exists to prevent, so pending items are visible.
            // Why it is still waiting, not just that it is.
            val reason = pending.firstNotNullOfOrNull { it.lastError }
            Text(
                if (reason == null) "${pending.size} waiting to send"
                else "${pending.size} waiting to send — $reason",
                style = MaterialTheme.typography.labelMedium,
                color = c.danger,
            )
        }
        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            OutlinedTextField(
                value = text, onValueChange = { text = it },
                placeholder = { Text("Send a prompt") },
                // Grows with what is typed, then scrolls inside itself rather
                // than pushing the transcript off the screen.
                maxLines = 6,
                shape = RoundedCornerShape(20.dp),
                modifier = Modifier.weight(1f),
            )
            val ready = text.isNotBlank()
            Box(
                Modifier
                    .size(46.dp)
                    .clip(CircleShape)
                    .background(if (ready) c.accent else Color.Transparent)
                    .clickable(enabled = ready) { onSend(text.trim()); text = "" },
                contentAlignment = Alignment.Center,
            ) {
                Wedge(
                    stroke = if (ready) c.onAccent else c.muted,
                    // On the filled button the accent tail would vanish, so the
                    // wedge keeps its two tones by dropping the tail's alpha.
                    shadow = if (ready) c.onAccent.copy(alpha = 0.55f) else c.line,
                    modifier = Modifier.size(24.dp),
                )
            }
        }
    }
}

/**
 * Offered when a session is paused, which is the one ended state the daemon
 * will take back (spec 7.9). Completed and errored sessions are terminal and
 * get no button, because resume would refuse them.
 */
/**
 * Stops the turn in flight.
 *
 * Beside the working indicator rather than in the toolbar: the question "is it
 * still going, and can I stop it" has one answer and should have one place.
 */
@Composable
private fun StopBar(onInterrupt: () -> Unit) {
    val c = RhoTheme.colors
    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.End,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        TextButton(onClick = onInterrupt) {
            Text("Stop", color = c.danger)
        }
    }
}

@Composable
private fun ResumeBar(onResume: () -> Unit) {
    val c = RhoTheme.colors
    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            "Paused",
            style = MaterialTheme.typography.labelMedium,
            color = c.muted,
            modifier = Modifier.weight(1f),
        )
        TextButton(onClick = onResume) { Text("Resume") }
    }
}

/**
 * Prompts the daemon refused for good. Shown on every session rather than
 * only the one they belong to: a blocked prompt used to sit in another
 * session's queue jamming this one, with nothing on screen to say so.
 */
@Composable
private fun BlockedPrompts(
    blocked: List<OutboxRow>,
    onRetry: (String) -> Unit,
    onDiscard: (String) -> Unit,
) {
    if (blocked.isEmpty()) return
    val c = RhoTheme.colors
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        for (row in blocked) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .background(c.surface)
                    .padding(horizontal = 10.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(2.dp),
            ) {
                Text(
                    row.content.trim().take(80),
                    style = MaterialTheme.typography.bodySmall,
                    color = c.ink,
                )
                Text(
                    row.lastError ?: "the daemon refused it",
                    style = MaterialTheme.typography.labelSmall,
                    color = c.danger,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                    TextButton(onClick = { onRetry(row.clientId) }) { Text("Retry") }
                    TextButton(onClick = { onDiscard(row.clientId) }) { Text("Discard") }
                }
            }
        }
    }
}

@Composable
private fun LineView(line: Line) {
    when (line) {
        is Line.Artifact -> ArtifactLine(line)

        is Line.Gap -> Card(
            colors = CardDefaults.cardColors(containerColor = RhoTheme.colors.surface),
            modifier = Modifier.fillMaxWidth(),
        ) {
            Column(Modifier.padding(12.dp)) {
                val (heading, detail) = gapText(line.neverFetched, line.downloading)
                Text(
                    heading,
                    style = MaterialTheme.typography.titleSmall,
                    color = if (line.downloading) RhoTheme.colors.ink else RhoTheme.colors.danger,
                )
                Text(detail, style = MaterialTheme.typography.bodySmall, color = RhoTheme.colors.muted)
            }
        }

        // Only what the reader wrote gets a bubble. The agent's own words are
        // the page, and a bubble around them just adds a wall to read past.
        is Line.UserSaid -> Row(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.End,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Spacer(Modifier.width(12.dp))
            LineCopyButton(line)
            Column(
                Modifier
                    .weight(1f, fill = false)
                    .background(RhoTheme.colors.mine, RoundedCornerShape(16.dp))
                    .padding(horizontal = 14.dp, vertical = 10.dp),
            ) {
                Text(line.text, style = MaterialTheme.typography.bodyMedium, color = RhoTheme.colors.ink)
                if (line.timestamp.isNotEmpty()) {
                    Text(line.timestamp, style = MaterialTheme.typography.labelSmall, color = RhoTheme.colors.muted)
                }
            }
        }

        is Line.AgentSaid -> Row(Modifier.fillMaxWidth()) {
            Column(Modifier.weight(1f).padding(vertical = 2.dp)) {
                MarkdownText(line.text)
                if (line.timestamp.isNotEmpty()) {
                    Text(line.timestamp, style = MaterialTheme.typography.labelSmall, color = RhoTheme.colors.muted)
                }
            }
            LineCopyButton(line)
        }

        is Line.ToolRan -> Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "▸ ${line.tool}  ${line.summary}",
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
                color = RhoTheme.colors.accent,
                modifier = Modifier.weight(1f),
            )
            LineCopyButton(line)
        }

        is Line.ToolOutput -> {
            var expanded by remember(line.key) { mutableStateOf(false) }
            val shown =
                if (expanded || !line.truncated) line.text
                else line.text.take(COLLAPSED_OUTPUT_CHARS)
            Box(Modifier.fillMaxWidth()) {
                Column(
                    Modifier.fillMaxWidth()
                        .background(RhoTheme.colors.code, RoundedCornerShape(6.dp))
                        .clickable(enabled = line.truncated) { expanded = !expanded }
                        .padding(10.dp)
                ) {
                    Text(
                        shown,
                        // Room for the copy control, which floats over the
                        // corner rather than taking a line of its own.
                        modifier = Modifier.padding(end = 24.dp),
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        color = RhoTheme.colors.codeInk,
                    )
                    if (line.truncated) {
                        Text(
                            if (expanded) "tap to collapse"
                            else "… ${line.text.length - COLLAPSED_OUTPUT_CHARS} more, tap to expand",
                            style = MaterialTheme.typography.labelSmall,
                            color = RhoTheme.colors.muted,
                        )
                    }
                }
                // Copies the whole result, not the part on screen: a reader who
                // reaches for copy wants the output, not what happened to fit.
                LineCopyButton(line, Modifier.align(Alignment.TopEnd).padding(2.dp))
            }
        }

        is Line.Thought -> ThoughtLine(line)

        is Line.Note -> Text(
            "note: ${line.text}",
            style = MaterialTheme.typography.bodySmall,
            color = if (line.level == "error") RhoTheme.colors.danger
            else RhoTheme.colors.muted,
        )

        is Line.Veto -> Text(
            "${line.module} refused the stop: ${line.reason}",
            style = MaterialTheme.typography.bodySmall,
            color = RhoTheme.colors.danger,
        )

        // The marker says a summary happened; the copy control is how the
        // summary itself gets out, since it is not otherwise on screen.
        is Line.Compacted -> Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "— earlier conversation summarised —",
                style = MaterialTheme.typography.labelMedium,
                color = RhoTheme.colors.muted,
                modifier = Modifier.weight(1f),
            )
            LineCopyButton(line)
        }
    }
}

/**
 * Says the agent is alive while a slow model works.
 *
 * A turn can be silent for minutes, and a screen that has stopped changing is
 * otherwise indistinguishable from a dropped connection. The dot moves, so
 * nothing has to be inferred from stillness.
 */
@Composable
private fun WorkingIndicator(state: String, compacting: Boolean = false) {
    if (state != "running" && !compacting) return
    val c = RhoTheme.colors

    val move = rememberInfiniteTransition(label = "working")
    val alpha by move.animateFloat(
        initialValue = 0.25f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(700, easing = LinearEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "pulse",
    )

    Row(
        Modifier.fillMaxWidth().padding(bottom = 2.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier
                .size(8.dp)
                .clip(CircleShape)
                .alpha(alpha)
                .background(c.accent)
        )
        Text(
            if (compacting) "summarising the history — this can take minutes" else "working",
            style = MaterialTheme.typography.labelMedium,
            color = c.muted,
        )
    }
}

/**
 * A thought, collapsed to one line until asked for.
 *
 * Neither a bubble nor a tool block: reasoning is not the answer and not a
 * command, and dressing it as either misleads. A rule down the left marks it as
 * an aside, and it stays out of the way until tapped.
 */
@Composable
private fun ThoughtLine(line: Line.Thought) {
    val c = RhoTheme.colors
    var expanded by remember(line.key) { mutableStateOf(false) }

    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(6.dp))
            .clickable { expanded = !expanded }
            .padding(vertical = 4.dp)
            .height(IntrinsicSize.Min),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Box(Modifier.width(2.dp).fillMaxHeight().background(c.line))
        Column(Modifier.weight(1f)) {
            Text(
                if (expanded) "thought · tap to hide"
                else "thought · ${line.text.split(Regex("\\s+")).size} words, tap to read",
                style = MaterialTheme.typography.labelSmall,
                color = c.muted,
            )
            if (expanded) {
                Text(
                    line.text,
                    style = MaterialTheme.typography.bodySmall,
                    color = c.muted,
                    fontStyle = FontStyle.Italic,
                    modifier = Modifier.padding(top = 3.dp),
                )
            }
        }
        // Only once it is open: a copy control beside a collapsed one-liner
        // offers to copy something the reader cannot see.
        if (expanded) LineCopyButton(line)
    }
}

/**
 * What the context badge says: how full the model's context is, and whether
 * the session is large. With no percentage it still says "context", because
 * it is where the size is chosen, and a new session is the one most worth
 * choosing for.
 *
 * The percentage is left out when the window was never configured: a
 * percentage of an unknown number would be an invention.
 */
fun contextBadgeText(used: Float?, size: String): String {
    val full = used?.let { "context ${(it * 100).toInt()}%" } ?: "context"
    return if (size == "large") "$full · large" else full
}

/** How full the model's context is, warning before compaction rather than after. */
@Composable
private fun ContextBadge(used: Float?, size: String, onClick: () -> Unit) {
    val c = RhoTheme.colors
    TextButton(onClick = onClick) {
        Text(
            contextBadgeText(used, size),
            style = MaterialTheme.typography.labelMedium,
            color = when {
                used == null -> c.muted
                used >= 0.85f -> c.danger
                used >= 0.6f -> c.accent
                else -> c.muted
            },
        )
    }
}

/**
 * Normal or large: how much of the model's window the session fills before
 * it is summarized (docs/specs/2026-10-08-context-size-design.md).
 */
@Composable
private fun ContextDialog(current: String, onPick: (String) -> Unit, onDismiss: () -> Unit) {
    val c = RhoTheme.colors
    val large = current == "large"
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = c.surface,
        titleContentColor = c.ink,
        textContentColor = c.muted,
        title = { Text(if (large) "This session is large" else "This session is normal") },
        text = {
            Text(
                "Normal is summarized sooner, which keeps a local model quick. Large fills the " +
                    "model's whole window first, for work that has to hold a lot at once, such as " +
                    "long documents. A run or goal started from this session uses the same size.",
            )
        },
        confirmButton = {
            TextButton(onClick = { onPick(if (large) "normal" else "large") }) {
                Text(if (large) "Make it normal" else "Make it large")
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/** The effort chip's text: the level, or "auto" when the server decides. */
fun effortChipText(effort: String) = "effort " + effort.ifBlank { "auto" }

/** The choices, in order: "" is Default, the server's own level. */
val effortLevels = listOf("", "none", "low", "medium", "high", "xhigh", "max")

/**
 * How hard the model thinks (docs/specs/2026-10-16-session-effort-design.md).
 * On a run's or goal's home it is what every later session under it uses.
 */
@Composable
private fun EffortDialog(current: String, onPick: (String) -> Unit, onDismiss: () -> Unit) {
    val c = RhoTheme.colors
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = c.surface,
        titleContentColor = c.ink,
        textContentColor = c.muted,
        title = { Text("How hard the model thinks") },
        text = {
            Column {
                Text(
                    "Lower is faster and writes less. It applies from the next model call. On a " +
                        "run's or goal's home, every later session under it uses it. On Strata, high, " +
                        "xhigh and max are the same.",
                    style = MaterialTheme.typography.bodySmall,
                )
                effortLevels.forEach { level ->
                    TextButton(onClick = { onPick(level) }) {
                        Text(
                            (if (level == current) "✓ " else "") + level.ifBlank { "Default (the server decides)" },
                            color = if (level == current) c.accent else c.ink,
                        )
                    }
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/** The workspace's last segment: the whole path does not fit on a phone. */
fun projectName(workspace: String) =
    workspace.trimEnd('\\', '/').substringAfterLast('\\').substringAfterLast('/')

private fun connectionLabel(c: Connection) = when (c) {
    Connection.Connected -> "connected"
    Connection.Connecting -> "connecting"
    Connection.Offline -> "offline"
}

/**
 * Picks a directory on the daemon's machine to start a session in.
 *
 * The phone cannot see that filesystem, so the daemon is asked one level at a
 * time. Repositories are marked, because at a glance one directory looks like
 * another and a repository is nearly always what is wanted.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BrowseScreen(
    state: BrowseState,
    onOpen: (String?) -> Unit,
    onStartHere: (String) -> Unit,
    onCreateDirectory: (String) -> Unit,
    onBack: () -> Unit,
    recent: List<String> = emptyList(),
) {
    val c = RhoTheme.colors
    var naming by remember { mutableStateOf(false) }
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
                        crumbs(state.at),
                        style = MaterialTheme.typography.titleSmall,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
                navigationIcon = { TextButton(onClick = onBack) { Text("Cancel") } },
                actions = {
                    // Only below a root: the top level lists roots, and there is
                    // no directory there to create anything inside.
                    if (!state.atTop) {
                        TextButton(onClick = { naming = true }) { Text("New folder") }
                    }
                },
            )
        },
        bottomBar = {
            if (state.canStartHere) {
                Box(Modifier.fillMaxWidth().navigationBarsPadding().padding(12.dp)) {
                    Button(
                        onClick = { onStartHere(state.at) },
                        modifier = Modifier.fillMaxWidth(),
                    ) { Text(if (state.purpose == Purpose.Run) "Work here" else "Start here") }
                }
            } else if (state.needsRepo) {
                // Said rather than a button that is simply missing, which
                // would leave the reader wondering what they did wrong.
                Text(
                    "A run needs a git repository. Open one to run there.",
                    style = MaterialTheme.typography.bodySmall,
                    color = c.muted,
                    modifier = Modifier.fillMaxWidth().navigationBarsPadding().padding(16.dp),
                )
            }
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            if (state.error != null) {
                Text(
                    state.error,
                    style = MaterialTheme.typography.bodySmall,
                    color = c.danger,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                )
            }
            if (state.loading) {
                Text(
                    "Reading…",
                    style = MaterialTheme.typography.bodySmall,
                    color = c.muted,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                )
            }
            if (naming) {
                NewFolderDialog(
                    into = crumbs(state.at),
                    onDismiss = { naming = false },
                    onConfirm = { name ->
                        naming = false
                        onCreateDirectory(name)
                    },
                )
            }
            LazyColumn(Modifier.fillMaxSize()) {
                // Up is a row rather than a toolbar button so the thumb reaches
                // it, and it is absent at a root because there is nowhere above.
                if (state.parent != null) {
                    item(key = "..") {
                        BrowseRow(label = "..", isRepo = false, onClick = { onOpen(state.parent) })
                    }
                }
                // Recent directories open rather than start: "Start here" is
                // the next tap, and the reader sees where they are first.
                if (state.atTop && recent.isNotEmpty()) {
                    item(key = "recent-header") { BrowseHeader("Recent") }
                    items(recent, key = { "recent:$it" }) { path ->
                        BrowseRow(label = crumbs(path, keep = 2), isRepo = false, onClick = { onOpen(path) })
                    }
                    item(key = "places-header") { BrowseHeader("Places") }
                }
                items(state.entries, key = { it.path }) { entry ->
                    BrowseRow(
                        label = entryLabel(entry, state.atTop),
                        isRepo = entry.isRepo,
                        onClick = { onOpen(entry.path) },
                    )
                }
                if (state.entries.isEmpty() && !state.loading && state.error == null) {
                    item(key = "empty") {
                        Text(
                            "Nothing to open here.",
                            style = MaterialTheme.typography.bodySmall,
                            color = c.muted,
                            modifier = Modifier.padding(16.dp),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun BrowseHeader(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelMedium,
        color = RhoTheme.colors.muted,
        modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 16.dp, bottom = 4.dp),
    )
}

@Composable
private fun BrowseRow(label: String, isRepo: Boolean, onClick: () -> Unit) {
    val c = RhoTheme.colors
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(
            label,
            style = MaterialTheme.typography.bodyMedium,
            color = c.ink,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (isRepo) {
            Text(
                "repo",
                style = MaterialTheme.typography.labelSmall,
                color = c.onAccent,
                modifier = Modifier
                    .background(c.accent, RoundedCornerShape(4.dp))
                    .padding(horizontal = 6.dp, vertical = 2.dp),
            )
        }
    }
}

/**
 * Asks for a directory name.
 *
 * The name is not validated here. The daemon owns that rule, and a second copy
 * of it in the client would drift from the one that actually decides. Only the
 * obviously-empty case is stopped, to save a round trip that cannot succeed.
 */
@Composable
private fun NewFolderDialog(
    into: String,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit,
) {
    var name by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("New folder") },
        text = {
            Column {
                Text(
                    "in $into",
                    style = MaterialTheme.typography.bodySmall,
                    color = RhoTheme.colors.muted,
                )
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    singleLine = true,
                    placeholder = { Text("name") },
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                )
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onConfirm(name.trim()) },
                enabled = name.isNotBlank(),
            ) { Text("Create") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/**
 * Where this session stands in a run: the status a home carries, and for a
 * step, the home it belongs to, which a tap opens.
 */
@Composable
private fun RunBar(status: String?, parentTitle: String?, isRun: Boolean, onOpenParent: () -> Unit) {
    if (status == null && parentTitle == null) return
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        status?.let {
            Text(it, style = MaterialTheme.typography.labelMedium, color = RhoTheme.colors.accent)
        }
        parentTitle?.let {
            TextButton(onClick = onOpenParent) {
                Text(
                    if (isRun) "Run of: $it" else "Step of: $it",
                    style = MaterialTheme.typography.labelMedium,
                    color = RhoTheme.colors.muted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}
