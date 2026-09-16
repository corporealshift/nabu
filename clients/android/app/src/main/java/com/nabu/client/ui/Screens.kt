package com.nabu.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
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
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
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
) {
    Scaffold(containerColor = NabuTheme.colors.background, topBar = {
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
    pending: List<OutboxRow>,
    tasks: List<Task>,
    onSend: (String) -> Unit,
    onTaskDone: (String) -> Unit,
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
                navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
            )
        },
        bottomBar = { Composer(pending, tasks, onSend, onTaskDone) },
    ) { padding ->
        LazyColumn(
            state = listState,
            modifier = Modifier.fillMaxSize().padding(padding),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            items(lines, key = { it.key }) { LineView(it) }
        }
    }
}

@Composable
private fun Composer(
    pending: List<OutboxRow>,
    tasks: List<Task>,
    onSend: (String) -> Unit,
    onTaskDone: (String) -> Unit,
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
        TaskCard(tasks, onTaskDone)
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
        ) {
            Spacer(Modifier.width(40.dp))
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

        is Line.AgentSaid -> Text(
            line.text,
            style = MaterialTheme.typography.bodyMedium,
            color = NabuTheme.colors.ink,
            modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
        )

        is Line.ToolRan -> Text(
            "▸ ${line.tool}  ${line.summary}",
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
            color = NabuTheme.colors.accent,
        )

        is Line.ToolOutput -> {
            var expanded by remember(line.key) { mutableStateOf(false) }
            val shown =
                if (expanded || !line.truncated) line.text
                else line.text.take(COLLAPSED_OUTPUT_CHARS)
            Column(
                Modifier.fillMaxWidth()
                    .background(NabuTheme.colors.code, RoundedCornerShape(6.dp))
                    .clickable(enabled = line.truncated) { expanded = !expanded }
                    .padding(10.dp)
            ) {
                Text(
                    shown,
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
        }

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

        is Line.Compacted -> Text(
            "— earlier conversation summarised —",
            style = MaterialTheme.typography.labelMedium,
            color = NabuTheme.colors.muted,
        )
    }
}

/** The workspace's last segment: the whole path does not fit on a phone. */
fun projectName(workspace: String) =
    workspace.trimEnd('\\', '/').substringAfterLast('\\').substringAfterLast('/')

private fun connectionLabel(c: Connection) = when (c) {
    Connection.Connected -> "connected"
    Connection.Connecting -> "connecting"
    Connection.Offline -> "offline"
}
