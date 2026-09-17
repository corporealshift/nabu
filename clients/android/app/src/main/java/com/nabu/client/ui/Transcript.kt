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

/** One rendered line of a transcript. */
sealed interface Line {
    val key: String

    data class UserSaid(override val key: String, val text: String) : Line
    data class AgentSaid(override val key: String, val text: String) : Line
    data class ToolRan(override val key: String, val tool: String, val summary: String) : Line
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

private fun render(key: String, e: Event): Line? = when (e.type) {
    "message" -> e.payload<MessageData>()?.let { d ->
        val text = d.content.trim()
        when {
            text.isEmpty() -> null
            d.role == "user" -> Line.UserSaid(key, text)
            else -> Line.AgentSaid(key, text)
        }
    }

    "tool_call" -> e.payload<ToolCallData>()?.let { d ->
        Line.ToolRan(key, d.tool, summarise(d))
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

/** A one-line description of a tool call: what it did, not its whole payload. */
private fun summarise(d: ToolCallData): String {
    val args = d.arguments.toString()
    val interesting = Regex(""""(command|path|pattern|name|query)"\s*:\s*"((\\.|[^"\\])*)"""")
        .find(args)?.groupValues?.get(2)
    val text = interesting ?: args
    return if (text.length > 120) text.take(120) + "…" else text
}
