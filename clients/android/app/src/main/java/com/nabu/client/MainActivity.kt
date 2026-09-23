package com.nabu.client

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.isSystemInDarkTheme
import com.nabu.client.ui.theme.Mode
import com.nabu.client.ui.theme.NabuTheme
import com.nabu.client.ui.theme.Scheme
import com.nabu.client.ui.LocalTaskCardFold
import com.nabu.client.ui.TaskCardFold
import com.nabu.client.ui.overlay
import com.nabu.client.ui.projectName
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import com.nabu.client.data.OutboxRow
import com.nabu.client.ui.Connection
import com.nabu.client.ui.NabuViewModel
import com.nabu.client.ui.ArchivedScreen
import com.nabu.client.ui.ArtifactScreen
import com.nabu.client.ui.AskSheet
import com.nabu.client.ui.Line
import com.nabu.client.ui.LocalOpenArtifact
import com.nabu.client.ui.StatsScreen
import com.nabu.client.ui.PermissionSheet
import com.nabu.client.ui.BrowseScreen
import com.nabu.client.ui.SessionListScreen
import com.nabu.client.ui.SettingsScreen
import com.nabu.client.ui.TranscriptScreen
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent { App() }
    }
}

private sealed interface Screen {
    data object Sessions : Screen
    data object Settings : Screen
    data object Browse : Screen
    data object Archived : Screen
    data class Transcript(val id: String) : Screen
    data class Artifact(val sessionId: String, val line: Line.Artifact) : Screen
    data class Stats(val id: String) : Screen
}

@Composable
private fun App(vm: NabuViewModel = viewModel()) {
    val settings by vm.settings.collectAsState()
    val systemDark = isSystemInDarkTheme()
    val chosen = settings
    val dark = when (chosen?.mode ?: Mode.System) {
        Mode.System -> systemDark
        Mode.Light -> false
        Mode.Dark -> true
    }

    NabuTheme(scheme = chosen?.scheme ?: Scheme.Verdigris, dark = dark) {
        Surface(color = NabuTheme.colors.background) {
            val collapsed = chosen?.tasksCollapsed ?: false
            val fold = TaskCardFold(collapsed = collapsed, onToggle = { vm.setTasksCollapsed(!collapsed) })
            CompositionLocalProvider(LocalTaskCardFold provides fold) {
                Screens(vm = vm, systemDark = systemDark)
            }
        }
    }
}

@Composable
private fun Screens(vm: NabuViewModel, systemDark: Boolean) {
    val settings by vm.settings.collectAsState()
    val sessions by vm.sessions.collectAsState()
    val connection by vm.connection.collectAsState()
    val permission by vm.pendingPermission.collectAsState()
    val ask by vm.pendingAsk.collectAsState()
    val error by vm.error.collectAsState()
    val compacting by vm.compacting.collectAsState()

    // With nowhere to connect to, the first screen is the one that fixes that.
    var screen: Screen by remember { mutableStateOf(Screen.Sessions) }

    // Keyed on where it connects, not on all of Settings: changing the palette
    // would otherwise drop and rebuild the connection.
    val endpoint = settings?.let { "${it.host}:${it.port}:${it.token}" }
    LaunchedEffect(endpoint) {
        val s = settings ?: return@LaunchedEffect
        // connect, not reconnect: this runs again whenever the activity is
        // recreated, and restarting a healthy connection each time is a drop.
        if (s.host.isBlank()) screen = Screen.Settings else vm.connect()
    }

    // Android freezes a backgrounded process, which kills the socket while the
    // connection loop is in no position to notice. Coming back is the moment to
    // try again, rather than waiting out a backoff that elapsed while frozen.
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_START) vm.onForeground()
        }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }

    permission?.let { req ->
        PermissionSheet(request = req, onAnswer = { vm.answerPermission(it) })
    }

    ask?.let { req ->
        AskSheet(request = req, onAnswer = { vm.answerQuestion(it) })
    }

    when (val s = screen) {
        is Screen.Settings -> SettingsScreen(
            current = settings ?: com.nabu.client.settings.Settings(),
            systemDark = systemDark,
            onAppearance = { sc, md -> vm.setAppearance(sc, md) },
            onSave = { vm.save(it); screen = Screen.Sessions },
        )

        is Screen.Stats -> {
            val stats by vm.stats.collectAsState()
            StatsScreen(state = stats, onBack = { screen = Screen.Transcript(s.id) })
        }

        is Screen.Browse -> {
            val browse by vm.browse.collectAsState()
            BrowseScreen(
                state = browse,
                onOpen = { vm.openDirectory(it) },
                // Straight into the new session: starting one and then being
                // returned to a list to find it is a step for nothing.
                onStartHere = { path ->
                    vm.createSession(path) { id -> screen = Screen.Transcript(id) }
                },
                onCreateDirectory = { vm.createDirectory(it) },
                onBack = { screen = Screen.Sessions },
            )
        }

        is Screen.Sessions -> SessionListScreen(
            sessions = sessions,
            connection = connection,
            error = error,
            onOpen = { screen = Screen.Transcript(it) },
            onSettings = { screen = Screen.Settings },
            onNewSession = { vm.startBrowsing(); screen = Screen.Browse },
            onArchive = { vm.archiveSession(it) },
            onArchived = { vm.loadArchived(); screen = Screen.Archived },
        )

        is Screen.Archived -> {
            val archived by vm.archived.collectAsState()
            ArchivedScreen(
                archived = archived,
                connection = connection,
                error = error,
                onRestore = { id -> vm.restoreSession(id) { screen = Screen.Transcript(it) } },
                onBack = { screen = Screen.Sessions },
            )
        }

        is Screen.Artifact -> ArtifactScreen(s.line, onBack = { screen = Screen.Transcript(s.sessionId) })

        is Screen.Transcript -> CompositionLocalProvider(
            LocalOpenArtifact provides { line -> screen = Screen.Artifact(s.id, line) },
        ) {
            // Remembered per session: a flow asked for afresh on every
            // recomposition restarts its query, and any event anywhere
            // recomposes this screen.
            val rowFlow = remember(s.id) { vm.watchSession(s.id) }
            val pendingFlow = remember(s.id) { vm.watchPending(s.id) }
            val blockedFlow = remember { vm.watchBlocked() }
            val row by rowFlow.collectAsState(initial = null)
            val view by vm.transcript(s.id).collectAsState()
            val pending by pendingFlow.collectAsState(initial = emptyList<OutboxRow>())
            val blocked by blockedFlow.collectAsState(initial = emptyList<OutboxRow>())
            val tapped by vm.tapped.collectAsState()
            val tasks = remember(view.tasks, tapped) {
                overlay(view.tasks, tapped[s.id].orEmpty())
            }
            // Catch this one up first, rather than wherever it falls in the
            // connection's pass over every session.
            LaunchedEffect(s.id, connection) {
                if (connection == Connection.Connected) vm.syncNow(s.id)
            }
            TranscriptScreen(
                title = row?.workspace?.let { projectName(it) }?.ifBlank { s.id } ?: s.id,
                view = view,
                synced = row?.synced ?: false,
                state = row?.state ?: "idle",
                pending = pending,
                blocked = blocked,
                tasks = tasks,
                compacting = compacting,
                error = error,
                onSend = { vm.sendPrompt(s.id, it) },
                onTaskDone = { vm.completeTask(s.id, tasks, it) },
                onResume = { vm.resumeSession(s.id) },
                onInterrupt = { vm.interruptSession(s.id) },
                onCompact = { vm.compactSession(s.id) },
                onStats = { vm.loadStats(s.id); screen = Screen.Stats(s.id) },
                onRetryBlocked = { vm.retryBlocked(it) },
                onDiscardBlocked = { vm.discardBlocked(it) },
                onBack = { screen = Screen.Sessions },
            )
        }
    }
}
