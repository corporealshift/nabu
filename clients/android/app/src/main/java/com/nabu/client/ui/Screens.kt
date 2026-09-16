package com.nabu.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.nabu.client.data.EventRow
import com.nabu.client.data.OutboxRow
import com.nabu.client.data.SessionRow
import com.nabu.client.settings.Settings

/** Host, port and token. The token is required, so the screen says why. */
@Composable
fun SettingsScreen(current: Settings, onSave: (Settings) -> Unit) {
    var host by remember(current) { mutableStateOf(current.host) }
    var port by remember(current) { mutableStateOf(current.port.toString()) }
    var token by remember(current) { mutableStateOf(current.token) }

    Column(
        modifier = Modifier.fillMaxSize().systemBarsPadding().padding(24.dp),
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
                onSave(Settings(host.trim(), port.toIntOrNull() ?: 8737, token.trim()))
            },
            enabled = host.isNotBlank() && token.isNotBlank(),
        ) { Text("Save and connect") }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionListScreen(
    sessions: List<SessionRow>,
    connection: Connection,
    error: String?,
    onOpen: (String) -> Unit,
    onSettings: () -> Unit,
) {
    Scaffold(topBar = {
        TopAppBar(
            title = { Text("nabu") },
            actions = {
                Text(
                    connectionLabel(connection),
                    style = MaterialTheme.typography.labelMedium,
                    modifier = Modifier.padding(end = 12.dp),
                )
                TextButton(onClick = onSettings) { Text("Settings") }
            },
        )
    }) { padding ->
        if (error != null && connection != Connection.Connected) {
            Card(
                colors = CardDefaults.cardColors(
                    containerColor = MaterialTheme.colorScheme.errorContainer,
                ),
                modifier = Modifier.fillMaxWidth().padding(padding).padding(12.dp),
            ) {
                Column(Modifier.padding(12.dp)) {
                    Text("Cannot reach the daemon", style = MaterialTheme.typography.titleSmall)
                    Text(error, style = MaterialTheme.typography.bodySmall)
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
            items(sessions, key = { it.id }) { s ->
                Card(
                    modifier = Modifier.fillMaxWidth().clickable { onOpen(s.id) },
                ) {
                    Column(Modifier.padding(14.dp)) {
                        Text(
                            s.workspace.ifBlank { s.id },
                            style = MaterialTheme.typography.titleSmall,
                        )
                        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            Text(s.state, style = MaterialTheme.typography.labelMedium)
                            if (!s.synced) {
                                // Spec 4 obliges a client to disclose this.
                                Text(
                                    "partially synced",
                                    style = MaterialTheme.typography.labelMedium,
                                    color = MaterialTheme.colorScheme.error,
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
    onSend: (String) -> Unit,
    onBack: () -> Unit,
) {
    val lines = remember(events, synced) { transcript(events, synced) }
    val listState = rememberLazyListState()

    LaunchedEffect(lines.size) {
        if (lines.isNotEmpty()) listState.animateScrollToItem(lines.size - 1)
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title, style = MaterialTheme.typography.titleSmall) },
                navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
            )
        },
        bottomBar = { Composer(pending, onSend) },
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
private fun Composer(pending: List<OutboxRow>, onSend: (String) -> Unit) {
    var text by remember { mutableStateOf("") }
    Column(Modifier.fillMaxWidth().padding(12.dp)) {
        if (pending.isNotEmpty()) {
            // A prompt the user believes was sent and was not is the failure
            // the outbox exists to prevent, so pending items are visible.
            Text(
                "${pending.size} waiting to send",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.error,
            )
        }
        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            OutlinedTextField(
                value = text, onValueChange = { text = it },
                placeholder = { Text("Send a prompt") },
                modifier = Modifier.weight(1f),
            )
            Button(
                onClick = { onSend(text.trim()); text = "" },
                enabled = text.isNotBlank(),
            ) { Text("Send") }
        }
    }
}

@Composable
private fun LineView(line: Line) {
    when (line) {
        is Line.Gap -> Card(
            colors = CardDefaults.cardColors(
                containerColor = MaterialTheme.colorScheme.errorContainer,
            ),
            modifier = Modifier.fillMaxWidth(),
        ) {
            Column(Modifier.padding(12.dp)) {
                Text(
                    if (line.neverFetched) "Not downloaded" else "Earlier events not downloaded",
                    style = MaterialTheme.typography.titleSmall,
                )
                Text(
                    if (line.neverFetched)
                        "This session exists on the daemon but nothing has been fetched yet."
                    else "This device is behind. What follows is only the most recent part.",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }

        is Line.UserSaid -> Bubble("You", line.text, MaterialTheme.colorScheme.primaryContainer)
        is Line.AgentSaid -> Bubble("nabu", line.text, MaterialTheme.colorScheme.surfaceVariant)

        is Line.ToolRan -> Text(
            "▸ ${line.tool}  ${line.summary}",
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
        )

        is Line.ToolOutput -> {
            var expanded by remember(line.key) { mutableStateOf(false) }
            val shown =
                if (expanded || !line.truncated) line.text
                else line.text.take(COLLAPSED_OUTPUT_CHARS)
            Column(
                Modifier.fillMaxWidth()
                    .background(
                        MaterialTheme.colorScheme.surfaceVariant,
                        RoundedCornerShape(6.dp),
                    )
                    .clickable(enabled = line.truncated) { expanded = !expanded }
                    .padding(10.dp)
            ) {
                Text(
                    shown,
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                )
                if (line.truncated) {
                    Text(
                        if (expanded) "tap to collapse"
                        else "… ${line.text.length - COLLAPSED_OUTPUT_CHARS} more, tap to expand",
                        style = MaterialTheme.typography.labelSmall,
                    )
                }
            }
        }

        is Line.Note -> Text(
            "note: ${line.text}",
            style = MaterialTheme.typography.bodySmall,
            color = if (line.level == "error") MaterialTheme.colorScheme.error
            else MaterialTheme.colorScheme.onSurfaceVariant,
        )

        is Line.Veto -> Text(
            "${line.module} refused the stop: ${line.reason}",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
        )

        is Line.Compacted -> Text(
            "— earlier conversation summarised —",
            style = MaterialTheme.typography.labelMedium,
        )
    }
}

@Composable
private fun Bubble(who: String, text: String, colour: androidx.compose.ui.graphics.Color) {
    Column(
        Modifier.fillMaxWidth()
            .background(colour, RoundedCornerShape(10.dp))
            .padding(12.dp)
    ) {
        Text(who, style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.Bold)
        Text(text, style = MaterialTheme.typography.bodyMedium)
    }
}

private fun connectionLabel(c: Connection) = when (c) {
    Connection.Connected -> "connected"
    Connection.Connecting -> "connecting"
    Connection.Offline -> "offline"
}
