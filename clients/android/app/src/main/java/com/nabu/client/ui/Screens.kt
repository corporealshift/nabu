package com.nabu.client.ui

import androidx.compose.foundation.background
import androidx.compose.ui.draw.alpha
import androidx.compose.animation.core.tween
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.LinearEasing
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
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
import com.nabu.client.data.EventRow
import com.nabu.client.data.OutboxRow
import com.nabu.client.data.SessionRow
import com.nabu.client.protocol.Task
import com.nabu.client.settings.Settings
import com.nabu.client.ui.markdown.MarkdownText
import com.nabu.client.ui.theme.Mode
import com.nabu.client.ui.theme.NabuTheme
import com.nabu.client.ui.theme.Scheme
import com.nabu.client.ui.theme.Wedge

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

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionListScreen(
    sessions: List<SessionCard>,
    connection: Connection,
    error: String?,
    onOpen: (String) -> Unit,
    onSettings: () -> Unit,
    onNewSession: () -> Unit,
) {
    Scaffold(containerColor = NabuTheme.colors.background, floatingActionButton = {
        // Only when connected: starting a session needs the daemon, and a
        // button that cannot work is worse than no button.
        if (connection == Connection.Connected) {
            ExtendedFloatingActionButton(
                onClick = onNewSession,
                containerColor = NabuTheme.colors.accent,
                contentColor = NabuTheme.colors.onAccent,
            ) { Text("New session") }
        }
    }, topBar = {
        TopAppBar(
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = NabuTheme.colors.background,
                titleContentColor = NabuTheme.colors.ink,
            ),
            title = { Text("Nabu", fontWeight = FontWeight.SemiBold) },
            actions = {
                Text(
                    connectionLabel(connection),
                    style = MaterialTheme.typography.labelMedium,
                    color = NabuTheme.colors.muted,
                    modifier = Modifier.padding(end = 12.dp),
                )
                TextButton(onClick = onSettings) { Text("Settings") }
            },
        )
    }) { padding ->
        if (error != null && connection != Connection.Connected) {
            Card(
                colors = CardDefaults.cardColors(containerColor = NabuTheme.colors.surface),
                modifier = Modifier.fillMaxWidth().padding(padding).padding(12.dp),
            ) {
                Column(Modifier.padding(12.dp)) {
                    Text(
                        "Cannot reach the daemon",
                        style = MaterialTheme.typography.titleSmall,
                        color = NabuTheme.colors.danger,
                    )
                    Text(
                        error,
                        style = MaterialTheme.typography.bodySmall,
                        color = NabuTheme.colors.muted,
                    )
                }
            }
            return@Scaffold
        }
        if (sessions.isEmpty()) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text(
                    if (connection == Connection.Connected) "No sessions yet."
                    else "Nothing mirrored yet. Connect to fetch sessions.",
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
            items(sessions, key = { it.row.id }) { card ->
                val s = card.row
                Card(
                    colors = CardDefaults.cardColors(containerColor = NabuTheme.colors.surface),
                    modifier = Modifier.fillMaxWidth().clickable { onOpen(s.id) },
                ) {
                    Column(
                        Modifier.padding(14.dp),
                        verticalArrangement = Arrangement.spacedBy(4.dp),
                    ) {
                        // Several sessions can share one workspace, so the last
                        // prompt leads: it is what tells them apart.
                        Text(
                            card.prompt.ifBlank { "Nothing asked yet" },
                            style = MaterialTheme.typography.titleSmall,
                            color = if (card.prompt.isBlank()) NabuTheme.colors.muted
                            else NabuTheme.colors.ink,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            Text(
                                "${projectName(s.workspace.ifBlank { s.id })}  ·  ${s.state}",
                                style = MaterialTheme.typography.labelMedium,
                                color = NabuTheme.colors.muted,
                            )
                            if (!s.synced) {
                                // Spec 4 obliges a client to disclose this.
                                Text(
                                    "partially synced",
                                    style = MaterialTheme.typography.labelMedium,
                                    color = NabuTheme.colors.danger,
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
    events: List<EventRow>,
    synced: Boolean,
    state: String,
    pending: List<OutboxRow>,
    blocked: List<OutboxRow>,
    tasks: List<Task>,
    onSend: (String) -> Unit,
    onTaskDone: (String) -> Unit,
    onResume: () -> Unit,
    onRetryBlocked: (String) -> Unit,
    onDiscardBlocked: (String) -> Unit,
    onBack: () -> Unit,
) {
    val lines = remember(events, synced) { transcript(events, synced) }
    val listState = rememberLazyListState()

    LaunchedEffect(lines.size) {
        if (lines.isNotEmpty()) listState.animateScrollToItem(lines.size - 1)
    }

    Scaffold(
        containerColor = NabuTheme.colors.background,
        topBar = {
            TopAppBar(
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = NabuTheme.colors.background,
                    titleContentColor = NabuTheme.colors.ink,
                ),
                title = { Text(title, style = MaterialTheme.typography.titleSmall) },
                actions = { ContextBadge(events) },
                navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
            )
        },
        bottomBar = {
            Composer(
                state, pending, blocked, tasks,
                onSend, onTaskDone, onResume, onRetryBlocked, onDiscardBlocked,
            )
        },
    ) { padding ->
        LazyColumn(
            state = listState,
            modifier = Modifier.fillMaxSize().padding(padding),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            // One container per line rather than one around the list: a
            // LazyColumn recycles its items, and a selection spanning an item
            // that scrolls out of composition loses its anchor. Per line, a
            // selection survives because everything it covers stays composed.
            items(lines, key = { it.key }) {
                SelectionContainer { LineView(it) }
            }
        }
    }
}

@Composable
private fun Composer(
    state: String,
    pending: List<OutboxRow>,
    blocked: List<OutboxRow>,
    tasks: List<Task>,
    onSend: (String) -> Unit,
    onTaskDone: (String) -> Unit,
    onResume: () -> Unit,
    onRetryBlocked: (String) -> Unit,
    onDiscardBlocked: (String) -> Unit,
) {
    var text by remember { mutableStateOf("") }
    val c = NabuTheme.colors
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
        WorkingIndicator(state)
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
@Composable
private fun ResumeBar(onResume: () -> Unit) {
    val c = NabuTheme.colors
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
    val c = NabuTheme.colors
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
        is Line.Gap -> Card(
            colors = CardDefaults.cardColors(containerColor = NabuTheme.colors.surface),
            modifier = Modifier.fillMaxWidth(),
        ) {
            Column(Modifier.padding(12.dp)) {
                Text(
                    if (line.neverFetched) "Not downloaded" else "Earlier events not downloaded",
                    style = MaterialTheme.typography.titleSmall,
                    color = NabuTheme.colors.danger,
                )
                Text(
                    if (line.neverFetched)
                        "This session exists on the daemon but nothing has been fetched yet."
                    else "This device is behind. What follows is only the most recent part.",
                    style = MaterialTheme.typography.bodySmall,
                    color = NabuTheme.colors.muted,
                )
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
            Text(
                line.text,
                style = MaterialTheme.typography.bodyMedium,
                color = NabuTheme.colors.ink,
                modifier = Modifier
                    .weight(1f, fill = false)
                    .background(NabuTheme.colors.mine, RoundedCornerShape(16.dp))
                    .padding(horizontal = 14.dp, vertical = 10.dp),
            )
        }

        is Line.AgentSaid -> Row(Modifier.fillMaxWidth()) {
            MarkdownText(
                line.text,
                modifier = Modifier.weight(1f).padding(vertical = 2.dp),
            )
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
                color = NabuTheme.colors.accent,
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
                        .background(NabuTheme.colors.code, RoundedCornerShape(6.dp))
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
                        color = NabuTheme.colors.codeInk,
                    )
                    if (line.truncated) {
                        Text(
                            if (expanded) "tap to collapse"
                            else "… ${line.text.length - COLLAPSED_OUTPUT_CHARS} more, tap to expand",
                            style = MaterialTheme.typography.labelSmall,
                            color = NabuTheme.colors.muted,
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
            color = if (line.level == "error") NabuTheme.colors.danger
            else NabuTheme.colors.muted,
        )

        is Line.Veto -> Text(
            "${line.module} refused the stop: ${line.reason}",
            style = MaterialTheme.typography.bodySmall,
            color = NabuTheme.colors.danger,
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
                color = NabuTheme.colors.muted,
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
private fun WorkingIndicator(state: String) {
    if (state != "running") return
    val c = NabuTheme.colors

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
            "working",
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
    val c = NabuTheme.colors
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
 * How full the model's context is, warning before compaction rather than after.
 *
 * Silent when the window was never configured: a percentage of an unknown
 * number would be an invention.
 */
@Composable
private fun ContextBadge(events: List<EventRow>) {
    val used = remember(events) { contextUsed(events) } ?: return
    val c = NabuTheme.colors

    Text(
        "context ${(used * 100).toInt()}%",
        style = MaterialTheme.typography.labelMedium,
        color = when {
            used >= 0.85f -> c.danger
            used >= 0.6f -> c.accent
            else -> c.muted
        },
        modifier = Modifier.padding(end = 12.dp),
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
) {
    val c = NabuTheme.colors
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
                    ) { Text("Start here") }
                }
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
private fun BrowseRow(label: String, isRepo: Boolean, onClick: () -> Unit) {
    val c = NabuTheme.colors
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
                    color = NabuTheme.colors.muted,
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
