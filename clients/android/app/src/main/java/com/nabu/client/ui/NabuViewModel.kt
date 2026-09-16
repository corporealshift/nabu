package com.nabu.client.ui

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.room.Room
import com.nabu.client.data.MirrorDb
import com.nabu.client.data.SessionRepository
import com.nabu.client.data.SessionRow
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.Incoming
import com.nabu.client.net.SessionSummary
import com.nabu.client.protocol.NabuJson
import com.nabu.client.settings.Settings
import com.nabu.client.settings.SettingsStore
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put

/** How the app is currently placed with respect to the daemon. */
enum class Connection { Offline, Connecting, Connected }

class NabuViewModel(app: Application) : AndroidViewModel(app) {

    private val db = Room.databaseBuilder(app, MirrorDb::class.java, "nabu-mirror")
        .fallbackToDestructiveMigration()
        .build()

    private val repo = SessionRepository(db)
    private val settingsStore = SettingsStore(app)

    private var client: DaemonClient? = null
    private var loop: Job? = null

    private val _connection = MutableStateFlow(Connection.Offline)
    val connection: StateFlow<Connection> = _connection.asStateFlow()

    private val _error = MutableStateFlow<String?>(null)
    val error: StateFlow<String?> = _error.asStateFlow()

    val settings: StateFlow<Settings?> =
        settingsStore.settings.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** The screens read the mirror, never the socket. */
    val sessions: StateFlow<List<SessionRow>> =
        repo.watchSessions().stateIn(viewModelScope, SharingStarted.Eagerly, emptyList())

    fun watchSession(id: String) = repo.watchSession(id)
    fun watchEvents(id: String) = repo.watchEvents(id)
    fun watchPending(id: String) = repo.watchPending(id)

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
            var backoff = 1_000L
            while (true) {
                val s = settings.first { it != null }!!
                if (s.host.isBlank()) {
                    _connection.value = Connection.Offline
                    return@launch
                }
                _connection.value = Connection.Connecting
                try {
                    runConnection(s)
                    backoff = 1_000L
                } catch (e: Exception) {
                    _error.value = e.message
                } finally {
                    _connection.value = Connection.Offline
                    client = null
                }
                delay(backoff)
                backoff = (backoff * 2).coerceAtMost(30_000L)
            }
        }
    }

    private suspend fun runConnection(s: Settings) {
        val c = DaemonClient(baseUrl = "http://${s.host}:${s.port}/", token = s.token)
        c.connect()
        client = c
        _connection.value = Connection.Connected
        _error.value = null

        val listed = c.callOrThrow("nabu.session.list").jsonObject["sessions"]
            ?.jsonArray
            ?.map { NabuJson.decodeFromJsonElement(SessionSummary.serializer(), it) }
            ?: emptyList()
        repo.recordSessions(listed)

        // Anything composed offline goes as soon as there is a connection.
        repo.flushOutbox(c)

        for (summary in listed) {
            runCatching { repo.sync(c, summary.sessionId) }
        }

        // Suspends until the connection drops, which is what makes it retry.
        c.incoming.collect { msg ->
            when (msg) {
                is Incoming.Event -> repo.record(msg)
                is Incoming.Permission -> _pendingPermission.value = msg.value
                else -> Unit
            }
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
                c.respond(req.id, buildJsonObject { put("approved", approve) })
            }
            _pendingPermission.value = null
        }
    }

    /** Queues a prompt, written locally first so losing signal cannot lose it. */
    fun sendPrompt(sessionId: String, text: String) {
        viewModelScope.launch {
            repo.queuePrompt(sessionId, text, newClientId())
            client?.let { runCatching { repo.flushOutbox(it) } }
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
