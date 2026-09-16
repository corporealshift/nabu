package com.nabu.client

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.lifecycle.viewmodel.compose.viewModel
import com.nabu.client.data.EventRow
import com.nabu.client.data.OutboxRow
import com.nabu.client.ui.Connection
import com.nabu.client.ui.NabuViewModel
import com.nabu.client.ui.SessionListScreen
import com.nabu.client.ui.SettingsScreen
import com.nabu.client.ui.TranscriptScreen
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { MaterialTheme { Surface { App() } } }
    }
}

private sealed interface Screen {
    data object Sessions : Screen
    data object Settings : Screen
    data class Transcript(val id: String) : Screen
}

@Composable
private fun App(vm: NabuViewModel = viewModel()) {
    val settings by vm.settings.collectAsState()
    val sessions by vm.sessions.collectAsState()
    val connection by vm.connection.collectAsState()
    val permission by vm.pendingPermission.collectAsState()

    // With nowhere to connect to, the first screen is the one that fixes that.
    var screen: Screen by remember { mutableStateOf(Screen.Sessions) }
    LaunchedEffect(settings) {
        val s = settings ?: return@LaunchedEffect
        if (s.host.isBlank()) screen = Screen.Settings else vm.reconnect()
    }

    permission?.let { req ->
        // Deny sits in the confirm position on purpose: spec 15 wants the safe
        // answer to be the easier one.
        AlertDialog(
            onDismissRequest = { },
            title = { Text("Permission needed") },
            text = { Text("${req.risk.uppercase()}  ${req.tool}\n\n${req.summary}") },
            confirmButton = { TextButton(onClick = { vm.answerPermission(false) }) { Text("Deny") } },
            dismissButton = {
                TextButton(onClick = { vm.answerPermission(true) }) { Text("Approve") }
            },
        )
    }

    when (val s = screen) {
        is Screen.Settings -> SettingsScreen(
            current = settings ?: com.nabu.client.settings.Settings(),
            onSave = { vm.save(it); screen = Screen.Sessions },
        )

        is Screen.Sessions -> SessionListScreen(
            sessions = sessions,
            connection = connection,
            onOpen = { screen = Screen.Transcript(it) },
            onSettings = { screen = Screen.Settings },
        )

        is Screen.Transcript -> {
            val row by vm.watchSession(s.id).collectAsState(initial = null)
            val events by vm.watchEvents(s.id).collectAsState(initial = emptyList<EventRow>())
            val pending by vm.watchPending(s.id).collectAsState(initial = emptyList<OutboxRow>())
            TranscriptScreen(
                title = row?.workspace?.substringAfterLast('/')?.ifBlank { s.id } ?: s.id,
                events = events,
                synced = row?.synced ?: false,
                pending = pending,
                onSend = { vm.sendPrompt(s.id, it) },
                onBack = { screen = Screen.Sessions },
            )
        }
    }
}
