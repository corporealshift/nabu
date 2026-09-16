package com.nabu.client.data

import com.nabu.client.net.DaemonClient
import com.nabu.client.net.Incoming
import com.nabu.client.net.SessionSummary
import com.nabu.client.protocol.Event
import com.nabu.client.protocol.NabuJson
import kotlinx.coroutines.flow.Flow
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

/**
 * Joins the daemon to the local mirror. Everything the daemon sends is written
 * to Room and screens read Room, so offline rendering needs no separate path.
 */
class SessionRepository(
    private val db: MirrorDb,
    private val now: () -> Long = System::currentTimeMillis,
) {
    fun watchSessions(): Flow<List<SessionRow>> = db.sessions().watchAll()
    fun watchEvents(sessionId: String): Flow<List<EventRow>> = db.events().watch(sessionId)
    fun watchSession(sessionId: String): Flow<SessionRow?> = db.sessions().watch(sessionId)
    fun watchPending(sessionId: String): Flow<List<OutboxRow>> =
        db.outbox().watchPendingFor(sessionId)

    /** Records the sessions the daemon knows about, without their events. */
    suspend fun recordSessions(summaries: List<SessionSummary>) {
        for (s in summaries) {
            val existing = db.sessions().get(s.sessionId)
            db.sessions().upsert(
                SessionRow(
                    id = s.sessionId,
                    workspace = s.workspace,
                    state = s.state,
                    // Listed but never fetched is unsynced, not empty (spec 4).
                    cursor = existing?.cursor ?: "",
                    synced = existing?.synced ?: false,
                    workspaceKey = existing?.workspaceKey ?: "",
                    updatedAt = now(),
                )
            )
        }
    }

    /**
     * Brings one session's mirror up to date. Subscribing precedes fetching so
     * an event appended during catch-up is not lost in the gap.
     */
    suspend fun sync(client: DaemonClient, sessionId: String) {
        client.callOrThrow("nabu.session.subscribe", buildJsonObject {
            put("session_id", sessionId)
        })

        val row = db.sessions().get(sessionId)
        val cursor = row?.cursor.orEmpty()

        val result = try {
            client.callOrThrow("nabu.session.events_after", buildJsonObject {
                put("session_id", sessionId)
                if (cursor.isNotEmpty()) put("last_event_id", cursor)
            })
        } catch (e: Exception) {
            // An unrecognised cursor means this device's idea of the log is
            // wrong, and refetching beats staying silently behind.
            if (cursor.isEmpty()) throw e
            db.sessions().markSynced(sessionId, "", false, now())
            client.callOrThrow("nabu.session.events_after", buildJsonObject {
                put("session_id", sessionId)
            })
        }

        val obj = result.jsonObject
        val events = obj["events"]?.jsonArray?.map {
            NabuJson.decodeFromJsonElement(Event.serializer(), it)
        } ?: emptyList()
        val synced = obj["synced"]?.jsonPrimitive?.boolean ?: false

        apply(sessionId, events, synced)
    }

    /** Writes one pushed event, which is the live path after catch-up. */
    suspend fun record(event: Incoming.Event) {
        apply(event.value.sessionId, listOf(event.value.event), synced = true)
    }

    /** Ordinals continue, so a later batch cannot sort above an earlier one. */
    internal suspend fun apply(sessionId: String, events: List<Event>, synced: Boolean) {
        if (db.sessions().get(sessionId) == null) {
            db.sessions().upsert(SessionRow(id = sessionId, updatedAt = now()))
        }
        var ordinal = db.events().lastOrdinal(sessionId) ?: 0L
        val rows = events.map { e ->
            ordinal += 1
            EventRow(
                id = e.id,
                sessionId = sessionId,
                ordinal = ordinal,
                type = e.type,
                raw = NabuJson.encodeToString(Event.serializer(), e),
            )
        }
        val cursor = events.lastOrNull()?.id
            ?: db.sessions().get(sessionId)?.cursor.orEmpty()
        db.append(sessionId, rows, cursor, synced)

        events.forEach { e ->
            if (e.type == "state_change") {
                val to = (e.data as? JsonObject)?.get("to")?.jsonPrimitive?.content
                if (!to.isNullOrEmpty()) db.sessions().setState(sessionId, to)
            }
        }
    }

    /** Queues a prompt. It exists locally before any send is attempted. */
    suspend fun queuePrompt(sessionId: String, content: String, clientId: String) {
        db.outbox().put(
            OutboxRow(
                clientId = clientId,
                sessionId = sessionId,
                content = content,
                createdAt = now(),
            )
        )
    }

    /**
     * Sends everything pending, oldest first. The client id makes a retry safe;
     * an item clears only once the daemon returns an event id for it.
     */
    suspend fun flushOutbox(client: DaemonClient) {
        for (item in db.outbox().pending()) {
            try {
                val result = client.callOrThrow("nabu.session.send_prompt", buildJsonObject {
                    put("session_id", item.sessionId)
                    put("content", item.content)
                    put("client_id", item.clientId)
                })
                val eventId = result.jsonObject["event_id"]?.jsonPrimitive?.content
                if (eventId.isNullOrEmpty()) {
                    db.outbox().markFailed(item.clientId, "the daemon returned no event id")
                } else {
                    db.outbox().markSent(item.clientId, eventId)
                }
            } catch (e: Exception) {
                // Stop at the first failure rather than reorder the rest.
                db.outbox().markFailed(item.clientId, e.message ?: "send failed")
                return
            }
        }
    }
}
