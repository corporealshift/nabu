package com.nabu.client.ui

import com.nabu.client.data.EventRow
import com.nabu.client.protocol.CompactionData
import com.nabu.client.protocol.Event
import com.nabu.client.protocol.MessageData
import com.nabu.client.protocol.NabuJson
import com.nabu.client.protocol.NoticeData
import com.nabu.client.protocol.StopVetoData
import com.nabu.client.protocol.ThinkingData
import com.nabu.client.protocol.ToolCallData
import com.nabu.client.protocol.ToolResultData
import com.nabu.client.protocol.payload
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** One rendered line of a transcript. */
sealed interface Line {
    val key: String

    data class UserSaid(override val key: String, val text: String, val timestamp: String = "") : Line
    data class AgentSaid(override val key: String, val text: String, val timestamp: String = "") : Line
    /**
     * A tool call. [summary] is shortened to fit one line; [full] is what it
     * was shortened from, so copying yields the whole command rather than an
     * ellipsis.
     */
    data class ToolRan(
        override val key: String,
        val tool: String,
        val summary: String,
        val full: String,
    ) : Line

    data class ToolOutput(
        override val key: String,
        val tool: String,
        val text: String,
        val truncated: Boolean,
    ) : Line

    /**
     * What the model thought before answering. Its own kind of line because it
     * is neither the answer nor a tool call, and reading it as either is
     * misleading.
     */
    data class Thought(override val key: String, val text: String) : Line

    data class Note(override val key: String, val text: String, val level: String) : Line
    data class Veto(override val key: String, val module: String, val reason: String) : Line
    data class Compacted(override val key: String, val summary: String) : Line

    /** A page the agent made (issue 41). The call carries the whole page. */
    data class Artifact(override val key: String, val name: String, val title: String, val html: String) : Line

    /**
     * Events this device has not fetched. Spec 15 wants the gap visible at the
     * gap, not as a footnote elsewhere.
     */
    data class Gap(override val key: String, val neverFetched: Boolean) : Line
}

/** How much of a tool result is shown before it has to be expanded. */
const val COLLAPSED_OUTPUT_CHARS = 600

/**
 * Builds the transcript. Behind-the-daemon and never-fetched are different
 * states and the reader is told which.
 */
fun transcript(rows: List<EventRow>, synced: Boolean): List<Line> {
    val out = mutableListOf<Line>()

    if (!synced) {
        out += Line.Gap(key = "gap-top", neverFetched = rows.isEmpty())
    }

    for (event in decode(rows)) {
        val line = render(event.id, event) ?: continue
        out += line
    }
    return out
}

/** Mirrored rows as events. One unreadable row costs its line, not the screen. */
fun decode(rows: List<EventRow>): List<Event> = rows.mapNotNull { row ->
    runCatching { NabuJson.decodeFromString(Event.serializer(), row.raw) }.getOrNull()
}

/** The line one event contributes, or null for an event that is not worth one. */
internal fun render(key: String, e: Event): Line? = when (e.type) {
    "message" -> e.payload<MessageData>()?.let { d ->
        val text = d.content.trim()
        when {
            text.isEmpty() -> null
            d.role == "user" -> Line.UserSaid(key, text, formatMessageTime(e.timestamp))
            else -> Line.AgentSaid(key, text, formatMessageTime(e.timestamp))
        }
    }

    "tool_call" -> e.payload<ToolCallData>()?.let { d ->
        artifactOf(key, d) ?: run {
            val full = interesting(d)
            Line.ToolRan(key, d.tool, shorten(full), full)
        }
    }

    "tool_result" -> e.payload<ToolResultData>()?.let { d ->
        val text = d.content.trimEnd()
        Line.ToolOutput(
            key = key,
            tool = d.tool,
            text = text,
            truncated = text.length > COLLAPSED_OUTPUT_CHARS,
        )
    }

    "thinking" -> e.payload<ThinkingData>()?.let { d ->
        val text = d.content.trim()
        if (text.isEmpty()) null else Line.Thought(key, text)
    }

    "notice" -> e.payload<NoticeData>()?.let { Line.Note(key, it.message, it.level) }

    "stop_veto" -> e.payload<StopVetoData>()?.let { Line.Veto(key, it.module, it.reason) }

    "compaction" -> e.payload<CompactionData>()?.let {
        if (it.mode == "summarize") Line.Compacted(key, it.summary) else null
    }

    // context is what a module injected, and report is rendered as its own
    // card rather than inline. Everything else is not worth a line.
    else -> null
}

private val messageTimeFormatter = DateTimeFormatter.ofPattern("MMM d, HH:mm")

/** When a message was sent, in the reader's own timezone rather than the daemon's. */
internal fun formatMessageTime(timestamp: String, zone: ZoneId = ZoneId.systemDefault()): String =
    runCatching {
        OffsetDateTime.parse(timestamp).toInstant().atZone(zone).format(messageTimeFormatter)
    }.getOrDefault("")

/**
 * How long an idle session has sat untouched, or null under five minutes: a
 * session that went quiet a moment ago is not news.
 */
internal fun idleAge(updatedAt: Long, now: Long): String? {
    val age = now - updatedAt
    if (updatedAt <= 0L || age < 5 * 60 * 1000L) return null
    return if (age >= 60 * 60 * 1000L) {
        val hours = age / (60 * 60 * 1000L)
        "$hours hour${if (hours == 1L) "" else "s"} ago"
    } else {
        val minutes = age / (60 * 1000L)
        "$minutes minute${if (minutes == 1L) "" else "s"} ago"
    }
}

/** The argument worth showing: the command, path, pattern, name or query. */
private fun interesting(d: ToolCallData): String {
    val args = d.arguments.toString()
    return Regex(""""(command|path|pattern|name|query)"\s*:\s*"((\\.|[^"\\])*)"""")
        .find(args)?.groupValues?.get(2) ?: args
}

/** A one-line description of a tool call: what it did, not its whole payload. */
private fun shorten(text: String): String =
    if (text.length > 120) text.take(120) + "…" else text

/**
 * What this line puts on the clipboard, or null when it has nothing worth
 * copying.
 *
 * Source, not render. An agent reply copies as the markdown it was written in,
 * so a table pasted into a terminal still has its pipes and a code block has no
 * invented indentation. A tool result copies whole even when the screen has it
 * collapsed: someone reaching for copy wants the output, not the part that fit.
 */
fun Line.copyText(): String? = when (this) {
    is Line.UserSaid -> text.ifBlank { null }
    is Line.AgentSaid -> text.ifBlank { null }
    is Line.Thought -> text.ifBlank { null }
    is Line.ToolRan -> if (full.isBlank()) null else "$tool $full"
    is Line.ToolOutput -> text.ifBlank { null }
    is Line.Note -> text.ifBlank { null }
    is Line.Veto -> "$module refused the stop: $reason"
    is Line.Compacted -> summary.ifBlank { null }
    // A gap is the absence of events. There is nothing behind it to copy.
    is Line.Gap -> null
    // The page's source: what someone copying it wants to keep or share.
    is Line.Artifact -> html.ifBlank { null }
}
