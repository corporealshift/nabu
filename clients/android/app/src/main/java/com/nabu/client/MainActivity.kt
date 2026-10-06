package com.nabu.client

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import com.nabu.client.notify.EXTRA_SESSION_ID
import com.nabu.client.notify.OnScreen
import com.nabu.client.notify.ensureChannels
import com.nabu.client.notify.pushAvailable
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
import com.nabu.client.ui.parseGoal
import com.nabu.client.ui.parseRun
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
import com.nabu.client.ui.BriefScreen
import com.nabu.client.ui.BrowseScreen
import com.nabu.client.ui.Purpose
import com.nabu.client.ui.recentWorkspaces
import com.nabu.client.ui.SessionListScreen
import com.nabu.client.ui.SettingsScreen
import com.nabu.client.ui.TranscriptScreen
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf

class MainActivity : ComponentActivity() {
    /** A session a tapped notification asked to open, until it is opened. */
    private val opening = mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        ensureChannels(this)
        opening.value = intent?.getStringExtra(EXTRA_SESSION_ID)
        setContent { App(opening = opening.value, onOpened = { opening.value = null }) }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        intent.getStringExtra(EXTRA_SESSION_ID)?.let { opening.value = it }
    }

    override fun onResume() {
        super.onResume()
        OnScreen.foreground = true
    }

    override fun onPause() {
        OnScreen.foreground = false
        super.onPause()
    }
}

private sealed interface Screen {
    data object Sessions : Screen
    data object Settings : Screen
    data object Browse : Screen
    data class Brief(val workspace: String) : Screen
    data object Archived : Screen
    data class Transcript(val id: String) : Screen
    data class Artifact(val sessionId: String, val line: Line.Artifact) : Screen
    data class Stats(val id: String) : Screen
}

@Composable
private fun App(opening: String?, onOpened: () -> Unit, vm: NabuViewModel = viewModel()) {
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
                Screens(vm = vm, systemDark = systemDark, opening = opening, onOpened = onOpened)
            }
        }
    }
}

@Composable
private fun Screens(vm: NabuViewModel, systemDark: Boolean, opening: String?, onOpened: () -> Unit) {
    val settings by vm.settings.collectAsState()
    val sessions by vm.sessions.collectAsState()
    val connection by vm.connection.collectAsState()
    val permission by vm.pendingPermission.collectAsState()
    val ask by vm.pendingAsk.collectAsState()
    val error by vm.error.collectAsState()
    val expanded by vm.expanded.collectAsState()
    val compacting by vm.compacting.collectAsState()

    // With nowhere to connect to, the first screen is the one that fixes that.
    var screen: Screen by remember { mutableStateOf(Screen.Sessions) }

    // A tapped notification opens its session.
    LaunchedEffect(opening) {
        val id = opening ?: return@LaunchedEffect
        screen = Screen.Transcript(id)
        onOpened()
    }
    // What is on screen, so a push for it is not shown twice.
    LaunchedEffect(screen) {
        OnScreen.sessionId = (screen as? Screen.Transcript)?.id
    }
    // Asked once a launch, and only by a build that can be pushed to; Android
    // stops asking by itself after two refusals.
    val context = LocalContext.current
    val askToNotify = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    LaunchedEffect(Unit) {
        if (Build.VERSION.SDK_INT >= 33 && pushAvailable(context) &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            askToNotify.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

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
                    if (browse.purpose == Purpose.Run) {
                        screen = Screen.Brief(path)
                    } else {
                        vm.createSession(path) { id -> screen = Screen.Transcript(id) }
                    }
                },
                onCreateDirectory = { vm.createDirectory(it) },
                onBack = { screen = Screen.Sessions },
                recent = remember(sessions) { recentWorkspaces(sessions) },
            )
        }

        // Into the run's home once it is made, where its status and then its
        // steps appear. Cancel goes back to the picker where it was.
        is Screen.Brief -> BriefScreen(
            workspace = s.workspace,
            onStart = { brief, goal, onFailed ->
                vm.createRun(s.workspace, brief, onCreated = { id -> screen = Screen.Transcript(id) }, onFailed = onFailed, goal = goal)
            },
            onBack = { screen = Screen.Browse },
        )

        is Screen.Sessions -> SessionListScreen(
            sessions = sessions,
            connection = connection,
            error = error,
            onOpen = { screen = Screen.Transcript(it) },
            onSettings = { screen = Screen.Settings },
            onNewSession = { vm.startBrowsing(); screen = Screen.Browse },
            onNewRun = { vm.startBrowsing(Purpose.Run); screen = Screen.Browse },
            onArchive = { vm.archiveSession(it) },
            onArchived = { vm.loadArchived(); screen = Screen.Archived },
            expanded = expanded,
            onToggle = { vm.toggleExpanded(it) },
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
                // /run is a command, as in the terminal client, not a prompt.
                onSend = { text ->
                    val brief = parseRun(text)
                    val goal = parseGoal(text)
                    when {
                        brief != null -> vm.startRun(s.id, brief, view.options.labels)
                        goal != null -> vm.startGoal(s.id, goal, view.options.labels)
                        else -> vm.sendPrompt(s.id, text)
                    }
                },
                onTaskDone = { vm.completeTask(s.id, tasks, it) },
                onResume = { vm.resumeSession(s.id) },
                onInterrupt = { vm.interruptSession(s.id) },
                onCompact = { vm.compactSession(s.id) },
                onStats = { vm.loadStats(s.id); screen = Screen.Stats(s.id) },
                onRetryBlocked = { vm.retryBlocked(it) },
                onDiscardBlocked = { vm.discardBlocked(it) },
                onBack = { screen = Screen.Sessions },
                onRun = { vm.startRun(s.id, it, view.options.labels) },
                onGoal = { vm.startGoal(s.id, it, view.options.labels) },
                parentTitle = view.options.parent.takeIf { it.isNotBlank() }?.let { parent ->
                    val home = sessions.firstOrNull { it.row.id == parent }
                    home?.title?.ifBlank { null } ?: home?.row?.workspace?.let { projectName(it) } ?: parent.takeLast(6)
                },
                onOpenParent = { if (view.options.parent.isNotBlank()) screen = Screen.Transcript(view.options.parent) },
            )
        }
    }
}
