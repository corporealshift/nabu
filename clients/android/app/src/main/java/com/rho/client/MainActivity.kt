package com.rho.client

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import com.rho.client.notify.EXTRA_SESSION_ID
import com.rho.client.notify.OnScreen
import com.rho.client.notify.ensureChannels
import com.rho.client.notify.pushAvailable
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
import com.rho.client.ui.parseGoal
import com.rho.client.ui.parseRun
import com.rho.client.ui.theme.Mode
import com.rho.client.ui.theme.RhoTheme
import com.rho.client.ui.theme.Scheme
import com.rho.client.ui.LocalTaskCardFold
import com.rho.client.ui.TaskCardFold
import com.rho.client.ui.overlay
import com.rho.client.ui.projectName
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import com.rho.client.data.OutboxRow
import com.rho.client.ui.Connection
import com.rho.client.ui.RhoViewModel
import com.rho.client.ui.ArchivedScreen
import com.rho.client.ui.ArtifactScreen
import com.rho.client.ui.AskSheet
import com.rho.client.ui.Line
import com.rho.client.ui.LocalOpenArtifact
import com.rho.client.ui.OverallStatsScreen
import com.rho.client.ui.StatsScreen
import com.rho.client.ui.ToolCallsScreen
import com.rho.client.ui.PermissionSheet
import com.rho.client.ui.BriefScreen
import com.rho.client.ui.BrowseScreen
import com.rho.client.ui.Purpose
import com.rho.client.ui.recentWorkspaces
import com.rho.client.ui.SessionListScreen
import com.rho.client.ui.SettingsScreen
import com.rho.client.ui.TranscriptScreen
import com.rho.client.ui.GoalPage
import com.rho.client.ui.LiveStep
import com.rho.client.ui.RunPage
import com.rho.client.ui.TreeLoad
import com.rho.client.ui.UnsupportedStatus
import com.rho.client.protocol.ParsedStatus
import com.rho.client.protocol.parseStatus
import androidx.compose.runtime.produceState
import kotlinx.coroutines.delay
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
    data object OverallStats : Screen
    /** One tool's calls; [sessionId] when they are one session's, opened from its stats. */
    data class ToolCalls(val tool: String, val sessionId: String?) : Screen
}

@Composable
private fun App(opening: String?, onOpened: () -> Unit, vm: RhoViewModel = viewModel()) {
    val settings by vm.settings.collectAsState()
    val systemDark = isSystemInDarkTheme()
    val chosen = settings
    val dark = when (chosen?.mode ?: Mode.System) {
        Mode.System -> systemDark
        Mode.Light -> false
        Mode.Dark -> true
    }

    RhoTheme(scheme = chosen?.scheme ?: Scheme.Verdigris, dark = dark) {
        Surface(color = RhoTheme.colors.background) {
            val collapsed = chosen?.tasksCollapsed ?: false
            val fold = TaskCardFold(collapsed = collapsed, onToggle = { vm.setTasksCollapsed(!collapsed) })
            CompositionLocalProvider(LocalTaskCardFold provides fold) {
                Screens(vm = vm, systemDark = systemDark, opening = opening, onOpened = onOpened)
            }
        }
    }
}

@Composable
private fun Screens(vm: RhoViewModel, systemDark: Boolean, opening: String?, onOpened: () -> Unit) {
    val settings by vm.settings.collectAsState()
    val sessions by vm.sessions.collectAsState()
    val connection by vm.connection.collectAsState()
    val permission by vm.pendingPermission.collectAsState()
    val ask by vm.pendingAsk.collectAsState()
    val error by vm.error.collectAsState()
    val expanded by vm.expanded.collectAsState()
    val compacting by vm.compacting.collectAsState()
    val catchingUp by vm.catchingUp.collectAsState()
    val syncing by vm.syncing.collectAsState()

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
            current = settings ?: com.rho.client.settings.Settings(),
            systemDark = systemDark,
            onAppearance = { sc, md -> vm.setAppearance(sc, md) },
            onSave = { vm.save(it); screen = Screen.Sessions },
        )

        is Screen.Stats -> {
            val stats by vm.stats.collectAsState()
            StatsScreen(
                state = stats,
                onBack = { screen = Screen.Transcript(s.id) },
                onTool = { tool -> vm.loadToolCalls(tool, s.id); screen = Screen.ToolCalls(tool, s.id) },
            )
        }

        is Screen.OverallStats -> {
            val overall by vm.overall.collectAsState()
            OverallStatsScreen(
                state = overall,
                onChange = { days, kind -> vm.loadOverallStats(days, kind) },
                onTool = { tool -> vm.loadToolCalls(tool, null); screen = Screen.ToolCalls(tool, null) },
                onBack = { screen = Screen.Sessions },
            )
        }

        is Screen.ToolCalls -> {
            val calls by vm.toolCalls.collectAsState()
            ToolCallsScreen(
                state = calls,
                onOpenSession = { screen = Screen.Transcript(it) },
                // Back to whichever stats it was opened from; both keep what they showed.
                onBack = { screen = s.sessionId?.let { Screen.Stats(it) } ?: Screen.OverallStats },
            )
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
            catchingUp = catchingUp,
            syncing = syncing,
            onOpen = { screen = Screen.Transcript(it) },
            onSettings = { screen = Screen.Settings },
            onNewSession = { vm.startBrowsing(); screen = Screen.Browse },
            onNewRun = { vm.startBrowsing(Purpose.Run); screen = Screen.Browse },
            onArchive = { vm.archiveSession(it) },
            onArchived = { vm.loadArchived(); screen = Screen.Archived },
            onStats = { vm.loadOverallStats(); screen = Screen.OverallStats },
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

        is Screen.Transcript -> {
            val homeView by vm.transcript(s.id).collectAsState()
            val parsed = remember(homeView.status) { parseStatus(homeView.status) }
            // A run's or goal's home opens on its status; Chat is a tap away.
            var chat by remember(s.id) { mutableStateOf(false) }
            val title = sessions.firstOrNull { it.row.id == s.id }?.row?.workspace?.let { projectName(it) }?.ifBlank { null } ?: s.id
            if (parsed != null && !chat) {
                StatusPage(
                    vm = vm,
                    home = s.id,
                    title = title,
                    parsed = parsed,
                    connected = connection == Connection.Connected,
                    sessions = sessions,
                    onBack = { screen = Screen.Sessions },
                    onChat = { chat = true },
                    onOpen = { screen = Screen.Transcript(it) },
                )
            } else TranscriptBody(s, vm, sessions, connection, error, compacting,
                onScreen = { screen = it }, onStatus = parsed?.let { { chat = false } })
        }
    }
}

/** A run's or goal's status page, with the numbers it fetches for itself. */
@Composable
private fun StatusPage(
    vm: RhoViewModel,
    home: String,
    title: String,
    parsed: ParsedStatus,
    connected: Boolean,
    sessions: List<com.rho.client.ui.SessionCard>,
    onBack: () -> Unit,
    onChat: () -> Unit,
    onOpen: (String) -> Unit,
) {
    // The tree is asked for once a minute while the page is open: often
    // enough for a run whose steps take minutes, and no more.
    LaunchedEffect(home, connected) {
        if (!connected) return@LaunchedEffect
        while (true) {
            vm.loadTree(home)
            delay(60_000)
        }
    }
    val trees by vm.trees.collectAsState()
    val tree = trees[home] ?: TreeLoad()
    val now by produceState(System.currentTimeMillis()) {
        while (true) {
            delay(30_000)
            value = System.currentTimeMillis()
        }
    }
    // Offered only while the home's label names a check that can be skipped,
    // which also hides it once the skip is asked for.
    val labels = sessions.firstOrNull { it.row.id == home }?.options?.labels.orEmpty()
    val onSkip = if (connected && com.rho.client.ui.skipLabels(labels) != null) {
        { vm.skipCheck(home, labels) }
    } else null
    when (parsed) {
        is ParsedStatus.Run -> {
            val step = parsed.status.session
            val stepFlow = remember(step) { vm.transcript(step.ifBlank { home }) }
            val stepView by stepFlow.collectAsState()
            val row = sessions.firstOrNull { it.row.id == step }?.row
            val live = if (step.isNotBlank() && row != null) {
                LiveStep(row.state, stepView.turns, stepView.maxTurns, stepView.contextUsed, row.updatedAt)
            } else null
            RunPage(title, parsed.status, tree, live, now, onBack, onChat, onOpen, onOpenGoal = onOpen, onSkip = onSkip)
        }
        is ParsedStatus.Goal -> GoalPage(title, parsed.status, tree, onBack, onChat, onOpenRun = onOpen, onSkip = onSkip)
        is ParsedStatus.Unsupported -> UnsupportedStatus(title, parsed, onBack, onChat)
    }
}

@Composable
private fun TranscriptBody(
    s: Screen.Transcript,
    vm: RhoViewModel,
    sessions: List<com.rho.client.ui.SessionCard>,
    connection: Connection,
    error: String?,
    compacting: Boolean,
    onScreen: (Screen) -> Unit,
    onStatus: (() -> Unit)?,
) {
    CompositionLocalProvider(
        LocalOpenArtifact provides { line -> onScreen(Screen.Artifact(s.id, line)) },
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
        val syncing by vm.syncing.collectAsState()
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
            downloading = s.id in syncing,
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
            onContext = { vm.setContext(s.id, it) },
            onEffort = { vm.setEffort(s.id, it) },
            onStats = { vm.loadStats(s.id); onScreen(Screen.Stats(s.id)) },
            onRetryBlocked = { vm.retryBlocked(it) },
            onDiscardBlocked = { vm.discardBlocked(it) },
            onBack = { onScreen(Screen.Sessions) },
            onRun = { vm.startRun(s.id, it, view.options.labels) },
            onGoal = { vm.startGoal(s.id, it, view.options.labels) },
            parentTitle = view.options.parent.takeIf { it.isNotBlank() }?.let { parent ->
                val home = sessions.firstOrNull { it.row.id == parent }
                home?.title?.ifBlank { null } ?: home?.row?.workspace?.let { projectName(it) } ?: parent.takeLast(6)
            },
            onOpenParent = { if (view.options.parent.isNotBlank()) onScreen(Screen.Transcript(view.options.parent)) },
            onStatus = onStatus,
        )
    }
}
