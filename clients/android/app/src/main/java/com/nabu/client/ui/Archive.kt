package com.nabu.client.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.nabu.client.net.SessionSummary
import com.nabu.client.ui.theme.NabuTheme

/**
 * Confirms putting a session away (issue 56). Asked rather than done on the
 * long press alone, because a long press is also how a scroll that stalls
 * reads.
 */
@Composable
fun ArchiveDialog(title: String, onArchive: () -> Unit, onDismiss: () -> Unit) {
    val c = NabuTheme.colors
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = c.surface,
        titleContentColor = c.ink,
        textContentColor = c.muted,
        title = { Text("Archive this session?") },
        text = {
            Text(
                "$title leaves the list, here and on the daemon. Nothing in it is " +
                    "deleted: Archived, in the top bar, brings it back.",
            )
        },
        confirmButton = { TextButton(onClick = onArchive) { Text("Archive") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/**
 * What is in the daemon's archive, each with a way back. Asked for when shown,
 * never mirrored: an archived session is one the reader put away.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ArchivedScreen(
    archived: List<SessionSummary>?,
    connection: Connection,
    error: String?,
    onRestore: (String) -> Unit,
    onBack: () -> Unit,
) {
    val c = NabuTheme.colors
    Scaffold(containerColor = c.background, topBar = {
        TopAppBar(
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = c.background,
                titleContentColor = c.ink,
            ),
            title = { Text("Archived", style = MaterialTheme.typography.titleSmall) },
            navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
        )
    }) { padding ->
        val message = when {
            connection != Connection.Connected -> "The archive is on the daemon. Connect to see it."
            archived == null -> error ?: "Loading…"
            archived.isEmpty() -> "Nothing archived."
            else -> null
        }
        if (message != null) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text(message, style = MaterialTheme.typography.bodyMedium, color = c.muted)
            }
            return@Scaffold
        }
        LazyColumn(
            modifier = Modifier.fillMaxSize().padding(padding),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(12.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            items(archived.orEmpty(), key = { it.sessionId }) { s ->
                Card(
                    colors = CardDefaults.cardColors(containerColor = c.surface),
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Row(
                        Modifier.padding(14.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Column(Modifier.weight(1f)) {
                            Text(
                                projectName(s.workspace.ifBlank { s.sessionId }),
                                style = MaterialTheme.typography.titleSmall,
                                color = c.ink,
                            )
                            Text(
                                "${s.state}  ·  …${s.sessionId.takeLast(8)}",
                                style = MaterialTheme.typography.labelMedium,
                                color = c.muted,
                            )
                        }
                        TextButton(onClick = { onRestore(s.sessionId) }) { Text("Restore") }
                    }
                }
            }
        }
    }
}
