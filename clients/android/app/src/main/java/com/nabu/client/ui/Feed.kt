package com.nabu.client.ui

import com.nabu.client.data.EventRow
import com.nabu.client.protocol.MessageData
import com.nabu.client.protocol.SessionData
import com.nabu.client.protocol.State
import com.nabu.client.protocol.Task
import com.nabu.client.protocol.payload
import com.nabu.client.protocol.project

/** What the transcript screen shows of one session. */
data class TranscriptView(
    /** The rendered lines, without the gap marker: that depends on the sync state. */
    val lines: List<Line> = emptyList(),
    val tasks: List<Task> = emptyList(),
    /** How full the context is, or null when that cannot be known. */
    val contextUsed: Float? = null,
    /** Whether any event has been mirrored at all. */
    val fetched: Boolean = false,
)

/**
 * One session's transcript, kept current by folding in only what is new.
 *
 * The screen used to rebuild everything from every row on each event: decode
 * the whole log's JSON three times over (lines, tasks, context), on the main
 * thread. On a session two thousand events long that is megabytes per event,
 * and a running session produces one every few seconds (issue 82).
 *
 * Not thread-safe: one collector owns it.
 */
class TranscriptFeed {
    private val lines = ArrayList<Line>()
    private var state = State()
    private var window = 0
    private var lastInput = 0

    /** The last row folded in. Rows at or below it have been seen. */
    var lastOrdinal = 0L
        private set

    /** Folds in rows that follow [lastOrdinal], in order. Earlier ones are ignored. */
    fun add(rows: List<EventRow>) {
        val fresh = rows.filter { it.ordinal > lastOrdinal }
        if (fresh.isEmpty()) return
        val events = decode(fresh)
        for (e in events) {
            render(e.id, e)?.let { lines += it }
            when (e.type) {
                "session" -> e.payload<SessionData>()?.let { window = it.contextWindow }
                "message" -> e.payload<MessageData>()?.let { m ->
                    if (m.role == "assistant") m.usage?.let { lastInput = it.inputTokens }
                }
            }
        }
        state = project(events, state)
        lastOrdinal = fresh.last().ordinal
    }

    /** Starts again, for a mirror emptied underneath the feed. */
    fun reset() {
        lines.clear()
        state = State()
        window = 0
        lastInput = 0
        lastOrdinal = 0
    }

    /** A snapshot: the screen holds it while the feed moves on. */
    fun view(): TranscriptView = TranscriptView(
        lines = lines.toList(),
        tasks = state.tasks,
        contextUsed = contextFraction(window, lastInput),
        fetched = lastOrdinal > 0,
    )
}

/**
 * The lines with the gap marker in front when the mirror is behind. Spec 15
 * wants the gap visible where it is, not as a footnote elsewhere.
 */
fun withGap(lines: List<Line>, synced: Boolean, fetched: Boolean): List<Line> =
    if (synced) lines else listOf(Line.Gap(key = "gap-top", neverFetched = !fetched)) + lines
