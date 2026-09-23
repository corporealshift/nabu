package com.nabu.client.data

import com.nabu.client.net.BrowseResult
import com.nabu.client.net.CreateSessionResult
import com.nabu.client.net.DaemonClient
import com.nabu.client.net.DaemonException
import com.nabu.client.net.Incoming
import com.nabu.client.net.SessionSummary
import com.nabu.client.protocol.Event
import com.nabu.client.protocol.NabuJson
import com.nabu.client.protocol.payload
import com.nabu.client.protocol.MessageData
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
                    updatedAt = interactionTime(s.updatedAt, existing?.updatedAt ?: 0, now()),
                )
            )
        }
    }

    /**
     * Where catch-up resumes from. A cursor with no events behind it is a
     * mirror that was emptied under it, and asking for events after the end of
     * the log would return nothing forever, so it starts again.
     */
    internal suspend fun cursorFor(sessionId: String): String {
        val cursor = db.sessions().get(sessionId)?.cursor.orEmpty()
        if (cursor.isEmpty()) return ""
        return if (db.events().count(sessionId) == 0) "" else cursor
    }

    /**
     * Brings one session's mirror up to date. Subscribing precedes fetching so
     * an event appended during catch-up is not lost in the gap.
     */
    suspend fun sync(client: DaemonClient, sessionId: String) {
        client.callOrThrow("nabu.session.subscribe", buildJsonObject {
            put("session_id", sessionId)
        })

        val cursor = cursorFor(sessionId)

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
        val existing = db.sessions().get(sessionId)
        if (existing == null) {
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
        db.append(sessionId, rows, cursor, synced,
            interactionAfter(events, existing?.updatedAt ?: 0, now()))

        events.forEach { e ->
            if (e.type == "state_change") {
                val to = (e.data as? JsonObject)?.get("to")?.jsonPrimitive?.content
                if (!to.isNullOrEmpty()) db.sessions().setState(sessionId, to)
            }
        }
    }

    /**
     * The last thing the user asked, which is what tells two sessions in one
     * workspace apart. Empty when nothing has been asked yet.
     */
    suspend fun latestPrompt(sessionId: String): String {
        for (raw in db.events().recentPrompts(sessionId)) {
            val event = runCatching {
                NabuJson.decodeFromString(Event.serializer(), raw)
            }.getOrNull() ?: continue
            val message = event.payload<MessageData>() ?: continue
            if (message.role == "user") return message.content.trim()
        }
        return ""
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

    /** How many prompts are still waiting to be sent. */
    suspend fun pendingCount(): Int = db.outbox().pending().size

    /** Prompts the daemon refused for good, app-wide. */
    fun watchBlocked(): Flow<List<OutboxRow>> = db.outbox().watchBlocked()

    /** Blocked prompts, asked once rather than watched. */
    suspend fun blockedPrompts(): List<OutboxRow> = db.outbox().blocked()

    /**
     * Records why the queue is not moving, when the cause is the connection
     * rather than any one prompt. Without this a stuck queue shows a count
     * and no reason, which is the hardest possible thing to diagnose.
     */
    suspend fun noteOutboxError(reason: String) = db.outbox().noteErrorOnPending(reason)

    /** Puts a blocked prompt back in the queue. */
    suspend fun retryBlocked(clientId: String) = db.outbox().unblock(clientId)

    /** Throws a blocked prompt away. */
    suspend fun discardBlocked(clientId: String) = db.outbox().discard(clientId)

    /**
     * Resumes a paused session (spec 7.9). Only `paused` is accepted; a
     * session that has ended is terminal and needs a new one.
     */
    /**
     * Cancels the turn in flight, leaving the session alive.
     *
     * Not nabu.session.stop, which ends the session outright. Stopping what it
     * is doing and being finished with it are different intentions and only one
     * of them is recoverable.
     */
    suspend fun interruptSession(client: DaemonClient, sessionId: String) {
        client.callOrThrow("nabu.session.interrupt", buildJsonObject {
            put("session_id", sessionId)
        })
    }

    /** How much work a session was (spec 7.21). Asked, never mirrored: it is cheap to ask again. */
    suspend fun stats(client: DaemonClient, sessionId: String): com.nabu.client.protocol.SessionStats {
        val result = client.callOrThrow("nabu.session.stats", buildJsonObject {
            put("session_id", sessionId)
        })
        return NabuJson.decodeFromJsonElement(com.nabu.client.protocol.SessionStats.serializer(), result)
    }

    /** Tokens and turns per day across sessions (spec 7.22). */
    suspend fun usage(client: DaemonClient, days: Int): List<com.nabu.client.protocol.UsageDay> {
        val result = client.callOrThrow("nabu.usage", buildJsonObject { put("days", days) })
        return result.jsonObject["days"]?.jsonArray
            ?.map { NabuJson.decodeFromJsonElement(com.nabu.client.protocol.UsageDay.serializer(), it) }
            ?: emptyList()
    }

    suspend fun resumeSession(client: DaemonClient, sessionId: String) {
        client.callOrThrow("nabu.session.resume", buildJsonObject {
            put("session_id", sessionId)
        })
    }

    /**
     * Summarises a session's history now, rather than waiting for it to cross
     * the automatic threshold (spec 7.15). Returns the mode that actually ran.
     *
     * The daemon refuses a running session and tells the caller to interrupt
     * first. That rule is not repeated here: two copies of it would drift, and
     * only the daemon's copy is the one that decides.
     */
    suspend fun compactSession(client: DaemonClient, sessionId: String): String {
        val result = client.callOrThrow("nabu.session.compact", buildJsonObject {
            put("session_id", sessionId)
        })
        return result.jsonObject["mode"]?.jsonPrimitive?.content.orEmpty()
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
            } catch (e: DaemonException) {
                // A refusal the daemon will repeat is not worth retrying, and
                // leaving it at the head of the queue stops every later prompt
                // with it. Park it for the user to resolve and carry on.
                if (e.isPermanent) {
                    db.outbox().markBlocked(item.clientId, e.message ?: "the daemon refused it")
                    continue
                }
                db.outbox().markFailed(item.clientId, e.message ?: "send failed")
                return
            } catch (e: Exception) {
                // Transport, not judgement: stop rather than reorder the rest.
                db.outbox().markFailed(item.clientId, e.message ?: "send failed")
                return
            }
        }
    }
    /**
     * Lists directories the daemon will start a session in.
     *
     * A null [path] asks for the configured roots, which is where a picker has
     * to start: the phone cannot see the daemon's filesystem and has no way to
     * guess a path worth typing.
     */
    suspend fun browse(client: DaemonClient, path: String?): BrowseResult {
        val result = client.callOrThrow("nabu.workspace.browse", buildJsonObject {
            if (!path.isNullOrEmpty()) put("path", path)
        })
        return NabuJson.decodeFromJsonElement(BrowseResult.serializer(), result)
    }

    /**
     * Creates one directory inside [parent] and returns the new directory's
     * listing, so the caller can move into what it just made.
     *
     * [name] is one directory name, never a path. The daemon enforces that; the
     * client does not pre-validate, because two copies of the same rule drift
     * and only one of them is the one that matters.
     */
    suspend fun createDirectory(client: DaemonClient, parent: String, name: String): BrowseResult {
        val result = client.callOrThrow("nabu.workspace.create_directory", buildJsonObject {
            put("parent", parent)
            put("name", name)
        })
        return NabuJson.decodeFromJsonElement(BrowseResult.serializer(), result)
    }

    /**
     * Starts a session in [workspace] and returns its id.
     *
     * The path handed in is one the daemon itself produced, so nothing here
     * rewrites it: a client massaging a path would be guessing about a
     * filesystem it cannot see.
     */
    suspend fun createSession(client: DaemonClient, workspace: String): String {
        val result = client.callOrThrow("nabu.session.create", buildJsonObject {
            put("workspace", workspace)
        })
        val created = NabuJson.decodeFromJsonElement(CreateSessionResult.serializer(), result)
        // Record it straight away, so the list shows what the reader just made
        // rather than waiting for the next refresh. The real summary replaces
        // this row the next time the daemon is asked.
        if (created.sessionId.isNotEmpty()) {
            recordSessions(
                listOf(
                    SessionSummary(
                        sessionId = created.sessionId,
                        workspace = workspace,
                        state = "idle",
                    )
                )
            )
        }
        return created.sessionId
    }
}

/**
 * When a session was last worked on, which is what the list is ordered by.
 *
 * The daemon's `updated_at` is its last event's timestamp, so it is the only
 * honest answer. Stamping the local clock as each row was recorded instead
 * ordered the list by the order the daemon happened to list them in, reversed
 * — and since it lists newest-created first, that put the oldest session on
 * top (issue 50).
 *
 * The daemon's word replaces this device's, even when it is older: a mirror
 * that recorded a wrong time once would otherwise keep it forever.
 */
internal fun interactionTime(updatedAt: String, existing: Long, fallback: Long): Long =
    parseInstant(updatedAt) ?: if (existing > 0) existing else fallback

/**
 * The same question answered by a batch of events rather than by a listing.
 *
 * This one never moves a session backwards: a device catching up on an old
 * session already knows something newer, and mirroring the old batch is not
 * news.
 */
internal fun interactionAfter(events: List<Event>, existing: Long, fallback: Long): Long {
    val newest = events.mapNotNull { parseInstant(it.timestamp) }.maxOrNull()
        ?: return if (existing > 0) existing else fallback
    return maxOf(newest, existing)
}

/** Go writes RFC 3339 with nanoseconds, and with an offset rather than always Z. */
internal fun parseInstant(text: String): Long? {
    if (text.isBlank()) return null
    return runCatching { java.time.OffsetDateTime.parse(text).toInstant().toEpochMilli() }
        .recoverCatching { java.time.Instant.parse(text).toEpochMilli() }
        .getOrNull()
}
