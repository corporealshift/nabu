package com.nabu.client.net

import com.nabu.client.protocol.Event
import com.nabu.client.protocol.NabuJson
import com.nabu.client.protocol.PROTOCOL_VERSION
import com.nabu.client.protocol.RpcCodes
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.TimeUnit

/** A JSON-RPC envelope. Only one of result or error is ever set. */
@Serializable
data class Rpc(
    val jsonrpc: String = "2.0",
    val id: JsonElement? = null,
    val method: String? = null,
    val params: JsonElement? = null,
    val result: JsonElement? = null,
    val error: RpcError? = null,
) {
    val isNotification: Boolean get() = method != null && id == null
    val isRequest: Boolean get() = method != null && id != null
}

@Serializable
data class RpcError(val code: Int = 0, val message: String = "", val data: JsonElement? = null)

/** An event pushed by the daemon for a subscribed session. */
data class SessionEvent(val sessionId: String, val event: Event)

/** A permission request the daemon wants answered. */
data class PermissionRequest(
    val id: JsonElement,
    val requestId: String,
    val tool: String,
    val summary: String,
    val risk: String,
)

/** A question a module put to whoever is watching. */
data class AskRequest(
    val id: JsonElement,
    val requestId: String,
    val sessionId: String,
    val question: String,
    val choices: List<String>,
)

/** What the client emits while it is connected. */
sealed interface Incoming {
    data class Event(val value: SessionEvent) : Incoming
    data class Permission(val value: PermissionRequest) : Incoming
    data class Ask(val value: AskRequest) : Incoming
    data class Delta(val sessionId: String, val text: String) : Incoming
    data class Other(val value: Rpc) : Incoming
}

/**
 * A failed call. [code] is the daemon's JSON-RPC code when it answered and
 * refused, and null when the call never got an answer at all — a dropped
 * socket, a timeout, no connection. Callers that queue work need the
 * difference: only a refusal is worth giving up on.
 */
class DaemonException(
    message: String,
    val code: Int? = null,
) : Exception(message) {
    /** True when retrying this exact call can only fail the same way. */
    val isPermanent: Boolean get() = code != null && code in RpcCodes.PERMANENT
}

/**
 * One connection to the daemon. OkHttp reads on a single thread and writes are
 * serialised, because concurrent use corrupts the frame stream rather than
 * failing cleanly. Reconnecting is the caller's job.
 */
class DaemonClient(
    private val baseUrl: String,
    private val token: String,
    private val clientName: String = "nabu-android",
    private val version: String = "0.1",
    private val http: OkHttpClient = defaultHttp(),
) {
    companion object {
        fun defaultHttp(): OkHttpClient = OkHttpClient.Builder()
            // A turn can be silent for minutes, so pings keep an idle but
            // healthy connection alive.
            .pingInterval(20, TimeUnit.SECONDS)
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .connectTimeout(15, TimeUnit.SECONDS)
            .build()
    }

    private val writeLock = Mutex()

    // Touched from coroutines and OkHttp's reader thread; a coroutine Mutex
    // would not exclude the latter.
    private val pending = java.util.concurrent.ConcurrentHashMap<String, CompletableDeferred<Rpc>>()
    private val nextId = java.util.concurrent.atomic.AtomicLong(0)
    @Volatile private var socket: WebSocket? = null

    private val _incoming = MutableSharedFlow<Incoming>(
        replay = 0,
        extraBufferCapacity = 256,
        onBufferOverflow = BufferOverflow.SUSPEND,
    )

    /** Everything the daemon pushes: events, permission prompts, deltas. */
    val incoming: SharedFlow<Incoming> = _incoming

    private val connected = CompletableDeferred<Unit>()
    private val closed = CompletableDeferred<String>()

    /** Opens the socket and completes the handshake, throwing if it is refused. */
    suspend fun connect() {
        val url = baseUrl.replace(Regex("^http"), "ws")
        val request = Request.Builder().url(url).apply {
            if (token.isNotEmpty()) header("Authorization", "Bearer $token")
        }.build()

        socket = http.newWebSocket(request, Listener())
        connected.await()

        val hello = call("nabu.hello", buildJsonObject {
            put("client_name", clientName)
            put("client_version", version)
            put("protocol_version", PROTOCOL_VERSION)
        })
        if (hello.error != null) {
            close()
            throw DaemonException("handshake refused: ${hello.error.message}")
        }
    }

    /** Makes one call and suspends until its own response arrives. */
    suspend fun call(method: String, params: JsonElement? = null): Rpc {
        val id = nextId.incrementAndGet().toString()
        val waiter = CompletableDeferred<Rpc>()
        pending[id] = waiter
        try {
            send(Rpc(id = JsonPrimitive(id), method = method, params = params))
            return waiter.await()
        } finally {
            pending.remove(id)
        }
    }

    /** Calls and throws on an error response, for callers that only want the result. */
    suspend fun callOrThrow(method: String, params: JsonElement? = null): JsonElement {
        val r = call(method, params)
        r.error?.let { throw DaemonException("$method: ${it.message}", it.code) }
        return r.result ?: JsonObject(emptyMap())
    }

    /** Answers a request the daemon sent, such as a permission prompt. */
    suspend fun respond(id: JsonElement, result: JsonElement) {
        send(Rpc(id = id, result = result))
    }

    private suspend fun send(msg: Rpc) {
        val text = NabuJson.encodeToString(Rpc.serializer(), msg)
        // One writer at a time.
        writeLock.withLock {
            val s = socket ?: throw DaemonException("not connected")
            if (!s.send(text)) throw DaemonException("the send queue is full or the socket is closed")
        }
    }

    /**
     * Suspends until the connection is gone, and says why. [incoming] is a
     * SharedFlow and so never completes; this is what a reconnect loop waits on.
     */
    suspend fun awaitClosed(): String = closed.await()

    fun close() {
        socket?.close(1000, null)
        socket = null
        closed.complete("closed by this client")
    }

    private inner class Listener : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            connected.complete(Unit)
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            val msg = try {
                NabuJson.decodeFromString(Rpc.serializer(), text)
            } catch (_: Exception) {
                return // one unreadable frame is not the end of the connection
            }
            when {
                msg.isNotification -> emit(msg)
                msg.isRequest -> emit(msg)
                else -> deliver(msg)
            }
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            fail(t.message ?: "connection failed")
        }

        // OkHttp does not answer a peer-initiated close for us, and without the
        // reply onClosed never arrives.
        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            webSocket.close(1000, null)
            fail(if (reason.isEmpty()) "the daemon closed the connection" else reason)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            fail(if (reason.isEmpty()) "connection closed" else reason)
        }
    }

    /** Routes a response to whichever call is waiting for it. */
    private fun deliver(msg: Rpc) {
        val id = msg.id?.jsonPrimitive?.content ?: return
        pending.remove(id)?.complete(msg)
    }

    private fun emit(msg: Rpc) {
        _incoming.tryEmit(classify(msg))
    }

    private fun fail(reason: String) {
        if (!connected.isCompleted) connected.completeExceptionally(DaemonException(reason))
        closed.complete(reason)
        pending.values.forEach { it.completeExceptionally(DaemonException(reason)) }
        pending.clear()
    }

    /** Turns a notification or request into something the app can act on. */
    private fun classify(msg: Rpc): Incoming = when (msg.method) {
        "nabu.session.event" -> {
            val p = msg.params?.jsonObject
            val ev = p?.get("event")
            if (ev != null) {
                Incoming.Event(SessionEvent(
                    sessionId = p["session_id"]?.jsonPrimitive?.content ?: "",
                    event = NabuJson.decodeFromJsonElement(Event.serializer(), ev),
                ))
            } else Incoming.Other(msg)
        }

        "nabu.session.delta" -> {
            val p = msg.params?.jsonObject
            Incoming.Delta(
                sessionId = p?.get("session_id")?.jsonPrimitive?.content ?: "",
                text = p?.get("text")?.jsonPrimitive?.content ?: "",
            )
        }

        "nabu.rpc.permission.request" -> {
            val p = msg.params?.jsonObject
            Incoming.Permission(PermissionRequest(
                id = msg.id ?: JsonPrimitive(""),
                requestId = p?.get("request_id")?.jsonPrimitive?.content ?: "",
                tool = p?.get("tool")?.jsonPrimitive?.content ?: "",
                summary = p?.get("summary")?.jsonPrimitive?.content ?: "",
                risk = p?.get("risk")?.jsonPrimitive?.content ?: "low",
            ))
        }

        "nabu.rpc.ui.ask" -> {
            val p = msg.params?.jsonObject
            Incoming.Ask(AskRequest(
                id = msg.id ?: JsonPrimitive(""),
                requestId = p?.get("request_id")?.jsonPrimitive?.content ?: "",
                sessionId = p?.get("session_id")?.jsonPrimitive?.content ?: "",
                question = p?.get("question")?.jsonPrimitive?.content ?: "",
                choices = p?.get("choices")?.jsonArray
                    ?.mapNotNull { it.jsonPrimitive.contentOrNull }
                    ?: emptyList(),
            ))
        }

        else -> Incoming.Other(msg)
    }
}

/** Params for events_after, kept typed so the field names cannot drift. */
@Serializable
data class EventsAfterParams(
    @SerialName("session_id") val sessionId: String,
    @SerialName("last_event_id") val lastEventId: String? = null,
)

@Serializable
data class EventsAfterResult(
    val events: List<Event> = emptyList(),
    val synced: Boolean = false,
)

/** One entry of the session list. */
@Serializable
data class SessionSummary(
    @SerialName("session_id") val sessionId: String = "",
    val workspace: String = "",
    val state: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

/** One directory a session could be started in (spec 7.15). */
@Serializable
data class BrowseEntry(
    val name: String = "",
    val path: String = "",
    @SerialName("is_repo") val isRepo: Boolean = false,
)

/**
 * One level of the daemon's directory tree.
 *
 * [parent] is null at a configured root, which is how the picker knows where
 * climbing stops without having to be refused to find out.
 */
@Serializable
data class BrowseResult(
    val path: String = "",
    val parent: String? = null,
    val entries: List<BrowseEntry> = emptyList(),
)

/** What nabu.session.create returns; only the id is used here. */
@Serializable
data class CreateSessionResult(
    @SerialName("session_id") val sessionId: String = "",
)
