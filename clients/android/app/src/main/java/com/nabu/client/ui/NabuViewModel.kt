package com.nabu.client.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.nabu.client.data.MirrorDb
import com.nabu.client.data.OutboxWorker
import com.nabu.client.data.SessionRepository
import com.nabu.client.data.SessionRow
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.DaemonException
import com.nabu.client.net.Incoming
import com.nabu.client.net.SessionSummary
import com.nabu.client.protocol.EventType
import com.nabu.client.protocol.NabuJson
import com.nabu.client.settings.Settings
import com.nabu.client.settings.SettingsStore
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put

/** How the app is currently placed with respect to the daemon. */
enum class Connection { Offline, Connecting, Connected }

/** A session as the list shows it: the mirror's row plus what was last asked. */
data class SessionCard(val row: SessionRow, val prompt: String)

/** Reconnect backoff. A dropped connection is routine; a long wait after one is not. */
private const val MIN_BACKOFF = 1_000L
private const val MAX_BACKOFF = 30_000L

class NabuViewModel(app: Application) : AndroidViewModel(app) {

    private val db = MirrorDb.get(app)

    private val repo = SessionRepository(db)
    private val settingsStore = SettingsStore(app)

    private var client: DaemonClient? = null
    private var loop: Job? = null

    /**
     * Wakes the connection loop out of its backoff.
     *
     * Conflated because ten nudges and one mean the same thing: try now. The
     * loop waits on this instead of sleeping blind, so returning to the app does
     * not mean sitting out a delay that was counting down while the process was
     * frozen.
     */
    private val wake = Channel<Unit>(Channel.CONFLATED)

    private val _connection = MutableStateFlow(Connection.Offline)
    val connection: StateFlow<Connection> = _connection.asStateFlow()

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error.asStateFlow()

    /**
     * Whether a compaction is in flight. It is a model call on the daemon and
     * takes seconds, during which nothing else changes on screen — so without
     * this the control looks like it did nothing.
     */
    private val _compacting = MutableStateFlow(false)
    val compacting: StateFlow<Boolean> = _compacting.asStateFlow()

    val settings: StateFlow<Settings?> =
        settingsStore.settings.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** The screens read the mirror, never the socket. */
    val sessions: StateFlow<List<SessionCard>> =
        repo.watchSessions()
            .map { rows -> rows.map { SessionCard(it, repo.latestPrompt(it.id)) } }
            .stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    fun watchSession(id: String) = repo.watchSession(id)
    fun watchEvents(id: String) = repo.watchEvents(id)
    fun watchPending(id: String) = repo.watchPending(id)

    /** Appearance saves without disturbing the connection. */
    fun setAppearance(scheme: com.nabu.client.ui.theme.Scheme, mode: com.nabu.client.ui.theme.Mode) {
        viewModelScope.launch { settingsStore.saveAppearance(scheme, mode) }
    }

    fun save(settings: Settings) {
        viewModelScope.launch {
            settingsStore.save(settings)
            reconnect()
        }
    }

    /** Keeps a connection up, retrying with backoff. No screen waits on it. */
    fun reconnect() {
        loop?.cancel()
        loop = viewModelScope.launch {
            var backoff = MIN_BACKOFF
            while (true) {
                val s = settings.first { it != null }!!
                if (s.host.isBlank()) {
                    _connection.value = Connection.Offline
                    return@launch
                }
                _connection.value = Connection.Connecting
                try {
                    // Reset when the socket actually opened, not when
                    // runConnection returns: it never returns, it throws when
                    // the connection drops. Resetting on return meant the
                    // backoff only ever grew, so an app that had dropped a few
                    // times waited 30s to retry even after a good connection.
                    runConnection(s) { backoff = MIN_BACKOFF }
                } catch (e: Exception) {
                    _error.value = e.message
                } finally {
                    _connection.value = Connection.Offline
                }
                // Wake early when the app comes back, rather than sitting out a
                // delay that elapsed while the process was frozen.
                withTimeoutOrNull(backoff) { wake.receive() }
                backoff = (backoff * 2).coerceAtMost(MAX_BACKOFF)
            }
        }
    }

    /**
     * Called when the app comes to the foreground.
     *
     * Android freezes a backgrounded process, which kills the socket without the
     * loop ever noticing. Nothing else asks it to try again: the only other
     * callers of reconnect are a settings change and a save.
     */
    fun onForeground() {
        wake.trySend(Unit)
    }

    private suspend fun runConnection(s: Settings, onConnected: () -> Unit) {
        val c = DaemonClient(baseUrl = "http://${s.host}:${s.port}/", token = s.token)
        try {
            runConnected(s, c, onConnected)
        } finally {
            // The socket outlives the coroutine unless it is closed here.
            // Cancelling stops the collection, not the connection, and a
            // reconnect that only dropped the reference left the old websocket
            // open: the daemon has logged five live ones from this phone at
            // once, all reaped together when the OS froze the process.
            runCatching { c.close() }
            // Only if it is still ours. A newer loop may already have connected
            // and installed its own, and clearing that would say offline over a
            // live connection and fail the next prompt.
            if (client === c) client = null
        }
    }

    private suspend fun runConnected(s: Settings, c: DaemonClient, onConnected: () -> Unit) {
        c.connect()
        client = c
        _connection.value = Connection.Connected
        _error.value = null
        onConnected()

        val listed = c.callOrThrow("nabu.session.list").jsonObject["sessions"]
            ?.jsonArray
            ?.map { NabuJson.decodeFromJsonElement(SessionSummary.serializer(), it) }
            ?: emptyList()
        repo.forgetUnlisted(listed)
        repo.recordSessions(listed)

        // Anything composed offline goes as soon as there is a connection.
        repo.flushOutbox(c)

        for (summary in listed) {
            runCatching { repo.sync(c, summary.sessionId) }
        }

        // incoming never completes, so the drop is what this waits on.
        coroutineScope {
            val pump = launch {
                c.incoming.collect { msg ->
                    when (msg) {
                        is Incoming.Event -> {
                            repo.record(msg)
                            // The snapshot is authoritative; taps stop speaking.
                            if (msg.value.event.type == EventType.TASKS) {
                                _tapped.value = _tapped.value - msg.value.sessionId
                            }
                        }
                        is Incoming.Permission -> _pendingPermission.value = msg.value
                        is Incoming.Ask -> _pendingAsk.value = msg.value
                        else -> Unit
                    }
                }
            }
            val reason = c.awaitClosed()
            pump.cancel()
            throw DaemonException(reason)
        }
    }

    private val _pendingPermission = MutableStateFlow<com.nabu.client.net.PermissionRequest?>(null)
    val pendingPermission: StateFlow<com.nabu.client.net.PermissionRequest?> =
        _pendingPermission.asStateFlow()

    fun answerPermission(approve: Boolean) {
        val req = _pendingPermission.value ?: return
        val c = client ?: return
        viewModelScope.launch {
            runCatching {
                c.respond(req.id, permissionReply(approve))
            }
            _pendingPermission.value = null
        }
    }

    /**
     * The question the agent is waiting on, if any. One at a time: the agent is
     * blocked until it is answered, so it cannot ask a second thing meanwhile.
     */
    private val _pendingAsk = MutableStateFlow<com.nabu.client.net.AskRequest?>(null)
    val pendingAsk: StateFlow<com.nabu.client.net.AskRequest?> = _pendingAsk.asStateFlow()

    /** Answers the agent's question. A blank answer is not one, and is ignored. */
    fun answerQuestion(answer: String) {
        val req = _pendingAsk.value ?: return
        if (!canSend(answer)) return
        val c = client ?: return
        viewModelScope.launch {
            runCatching {
                c.respond(req.id, askReply(answer))
            }
            _pendingAsk.value = null
        }
    }

    /**
     * Tasks tapped but not yet confirmed by a snapshot, so a tap shows at once
     * rather than two seconds later.
     */
    private val _tapped = MutableStateFlow<Map<String, Set<String>>>(emptyMap())
    val tapped: StateFlow<Map<String, Set<String>>> = _tapped.asStateFlow()

    /** Completes a task. `update_tasks` takes the whole snapshot, not a diff. */
    fun completeTask(sessionId: String, tasks: List<com.nabu.client.protocol.Task>, taskId: String) {
        _tapped.value = _tapped.value + (sessionId to (_tapped.value[sessionId].orEmpty() + taskId))
        viewModelScope.launch {
            val c = client
            if (c == null) {
                forget(sessionId, taskId)
                _error.value = "not connected"
                return@launch
            }
            try {
                c.callOrThrow("nabu.session.update_tasks", buildJsonObject {
                    put("session_id", sessionId)
                    put(
                        "tasks",
                        NabuJson.encodeToJsonElement(
                            ListSerializer(com.nabu.client.protocol.Task.serializer()),
                            withDone(tasks, taskId),
                        ),
                    )
                })
            } catch (e: Exception) {
                forget(sessionId, taskId)
                _error.value = e.message
            }
        }
    }

    private fun forget(sessionId: String, taskId: String) {
        _tapped.value = _tapped.value + (sessionId to (_tapped.value[sessionId].orEmpty() - taskId))
    }

    /** Queues a prompt, written locally first so losing signal cannot lose it. */
    // ------------------------------------------------------------- browsing

    private val _browse = MutableStateFlow(BrowseState())
    val browse: StateFlow<BrowseState> = _browse.asStateFlow()

    /**
     * Opens one level of the daemon's directory tree.
     *
     * A null path asks for the configured roots, which is how the picker
     * starts. A failure is shown in place rather than closing the screen: the
     * reader is one tap from somewhere that works.
     */
    fun openDirectory(path: String?) {
        val c = client
        if (c == null) {
            _browse.value = _browse.value.copy(error = "not connected", loading = false)
            return
        }
        _browse.value = _browse.value.copy(loading = true, error = null)
        viewModelScope.launch {
            try {
                _browse.value = _browse.value.applied(repo.browse(c, path))
            } catch (e: Exception) {
                _browse.value = _browse.value.copy(
                    loading = false,
                    error = e.message ?: "could not read that directory",
                )
            }
        }
    }

    /** Resets the picker to the roots, so reopening it does not resume mid-tree. */
    fun startBrowsing() {
        _browse.value = BrowseState()
        openDirectory(null)
    }

    /**
     * Creates a directory where the picker currently is, and moves into it.
     *
     * Moving in is the point: a directory made and then left behind is a step
     * for nothing, and "Start here" is the next tap.
     */
    fun createDirectory(name: String) {
        val c = client
        val at = _browse.value.at
        if (c == null || at.isEmpty()) {
            _browse.value = _browse.value.copy(error = "not connected")
            return
        }
        _browse.value = _browse.value.copy(loading = true, error = null)
        viewModelScope.launch {
            try {
                _browse.value = _browse.value.applied(repo.createDirectory(c, at, name))
            } catch (e: Exception) {
                _browse.value = _browse.value.copy(
                    loading = false,
                    error = e.message ?: "could not create that directory",
                )
            }
        }
    }

    /**
     * Starts a session in [workspace] and hands its id to [onCreated].
     *
     * The caller navigates rather than this doing it, so the view model does
     * not have to know what a screen is.
     */
    fun createSession(workspace: String, onCreated: (String) -> Unit) {
        val c = client
        if (c == null) {
            _browse.value = _browse.value.copy(error = "not connected")
            return
        }
        _browse.value = _browse.value.copy(loading = true, error = null)
        viewModelScope.launch {
            try {
                val id = repo.createSession(c, workspace)
                _browse.value = _browse.value.copy(loading = false)
                if (id.isNotEmpty()) onCreated(id)
            } catch (e: Exception) {
                _browse.value = _browse.value.copy(
                    loading = false,
                    error = e.message ?: "could not start a session there",
                )
            }
        }
    }

    fun sendPrompt(sessionId: String, text: String) {
        viewModelScope.launch {
            repo.queuePrompt(sessionId, text, newClientId())
            val c = client
            if (c == null) {
                repo.noteOutboxError("not connected")
            } else {
                runCatching { repo.flushOutbox(c) }
                    .onFailure { repo.noteOutboxError(it.message ?: "send failed") }
            }
            // Whatever happened just now, the queue is drained again when
            // there is a network, with or without this app in the foreground.
            if (repo.pendingCount() > 0) OutboxWorker.schedule(getApplication())
        }
    }

    /** Prompts the daemon refused for good, app-wide. */
    fun watchBlocked() = repo.watchBlocked()

    /**
     * Puts a blocked prompt back in the queue and tries it straight away.
     * Worth doing after resuming the session it was meant for.
     */
    fun retryBlocked(clientId: String) {
        viewModelScope.launch {
            repo.retryBlocked(clientId)
            client?.let { runCatching { repo.flushOutbox(it) } }
            if (repo.pendingCount() > 0) OutboxWorker.schedule(getApplication())
        }
    }

    fun discardBlocked(clientId: String) {
        viewModelScope.launch { repo.discardBlocked(clientId) }
    }

    /**
     * Resumes a paused session. Spec 7.9 takes only `paused`; a completed or
     * errored session is terminal and the caller is offered a new session
     * instead, so a refusal here is worth surfacing rather than swallowing.
     */
    /**
     * Stops the turn in flight. The daemon records the partial reply as
     * interrupted and returns the session to idle, so nothing said so far is
     * lost.
     */
    fun interruptSession(sessionId: String) {
        viewModelScope.launch {
            val c = client
            if (c == null) {
                _error.value = "not connected"
                return@launch
            }
            runCatching { repo.interruptSession(c, sessionId) }
                .onSuccess { _error.value = null }
                .onFailure { _error.value = it.message }
        }
    }

    fun resumeSession(sessionId: String) {
        viewModelScope.launch {
            val c = client
            if (c == null) {
                _error.value = "not connected"
                return@launch
            }
            runCatching { repo.resumeSession(c, sessionId) }
                .onSuccess {
                    _error.value = null
                    // Anything queued for it was blocked on it being paused.
                    runCatching { repo.flushOutbox(c) }
                }
                .onFailure { _error.value = it.message ?: "could not resume the session" }
        }
    }

    /**
     * Summarises the session's history on request (spec 7.15).
     *
     * A success needs no message of its own: the daemon appends a `compaction`
     * event and the transcript renders it. A refusal does — the daemon turns
     * one down while it is running, and the reason is the whole point.
     */
    fun compactSession(sessionId: String) {
        if (_compacting.value) return
        viewModelScope.launch {
            val c = client
            if (c == null) {
                _error.value = "not connected"
                return@launch
            }
            _compacting.value = true
            runCatching { repo.compactSession(c, sessionId) }
                .onSuccess { _error.value = null }
                .onFailure { _error.value = it.message ?: "could not compact the session" }
            _compacting.value = false
        }
    }

    // ------------------------------------------------------------- archive

    private val _archived = MutableStateFlow<List<SessionSummary>?>(null)

    /** The daemon's archive, or null until it has been asked for. */
    val archived: StateFlow<List<SessionSummary>?> = _archived.asStateFlow()

    /** Puts a session away (issue 56). It leaves the list here and on the daemon. */
    fun archiveSession(sessionId: String) {
        viewModelScope.launch {
            val c = client ?: run { _error.value = "not connected"; return@launch }
            runCatching { repo.archiveSession(c, sessionId) }
                .onSuccess { _error.value = null }
                .onFailure { _error.value = it.message ?: "could not archive the session" }
        }
    }

    /** Asks the daemon what is archived. */
    fun loadArchived() {
        viewModelScope.launch {
            val c = client ?: run { _error.value = "not connected"; return@launch }
            runCatching { repo.listArchived(c) }
                .onSuccess { _archived.value = it; _error.value = null }
                .onFailure { _error.value = it.message ?: "could not list the archive" }
        }
    }

    /** Brings a session back and mirrors it, then hands over its id to open. */
    fun restoreSession(sessionId: String, then: (String) -> Unit) {
        viewModelScope.launch {
            val c = client ?: run { _error.value = "not connected"; return@launch }
            runCatching {
                repo.restoreSession(c, sessionId)
                repo.recordSessions(repo.listSessions(c).filter { it.sessionId == sessionId })
                repo.sync(c, sessionId)
            }
                .onSuccess {
                    _error.value = null
                    _archived.value = _archived.value?.filterNot { it.sessionId == sessionId }
                    then(sessionId)
                }
                .onFailure { _error.value = it.message ?: "could not restore the session" }
        }
    }

    /** The id that makes a retry safe to repeat. */
    private fun newClientId(): String =
        "outbox-" + java.util.UUID.randomUUID().toString().replace("-", "").take(20)

    override fun onCleared() {
        loop?.cancel()
        client?.close()
        db.close()
        super.onCleared()
    }
}
