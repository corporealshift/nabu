package com.nabu.client.protocol

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.serializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/** Must match protocol.Version in Go; the daemon refuses a mismatch. */
const val PROTOCOL_VERSION = "1.0"

/**
 * Lenient on unknown keys and permissive about missing ones. A phone that
 * cannot decode a log the daemon considers valid is a phone that shows
 * nothing, so decoding leans towards keeping what it understands.
 */
val NabuJson: Json = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    explicitNulls = false
    coerceInputValues = true
}

/** One entry in a session log. */
@Serializable
data class Event(
    val id: String = "",
    @SerialName("parent_id") val parentId: String? = null,
    val timestamp: String = "",
    val type: String = "",
    /** Kept raw so an event type this client does not know still survives. */
    val data: JsonElement = JsonObject(emptyMap()),
)

@Serializable
data class Options(
    val model: String = "",
    @SerialName("compaction_enabled") val compactionEnabled: Boolean = true,
    @SerialName("permission_mode") val permissionMode: String = "",
)

@Serializable
data class SessionData(
    val workspace: String = "",
    @SerialName("workspace_key") val workspaceKey: String = "",
    val options: Options = Options(),
)

@Serializable
data class Usage(
    @SerialName("input_tokens") val inputTokens: Int = 0,
    @SerialName("output_tokens") val outputTokens: Int = 0,
    @SerialName("cached_tokens") val cachedTokens: Int = 0,
)

@Serializable
data class MessageData(
    val role: String = "",
    val content: String = "",
    /** The outbox item this prompt came from, when a client sent one. */
    @SerialName("client_id") val clientId: String? = null,
    val usage: Usage? = null,
    val interrupted: Boolean = false,
)

@Serializable
data class ToolCallData(
    @SerialName("call_id") val callId: String = "",
    val tool: String = "",
    val arguments: JsonElement = JsonObject(emptyMap()),
    val source: String = "",
)

@Serializable
data class ToolResultData(
    @SerialName("call_id") val callId: String = "",
    val tool: String = "",
    val content: String = "",
    val status: String = "",
)

@Serializable
data class StateChangeData(
    val from: String? = null,
    val to: String = "",
    val reason: String = "",
)

@Serializable
data class OptionsChangeData(
    val key: String = "",
    val from: JsonElement? = null,
    val to: JsonElement? = null,
    val source: String = "",
)

@Serializable
data class Task(
    val id: String = "",
    val title: String = "",
    val status: String = "",
    val note: String = "",
    @SerialName("done_when") val doneWhen: String = "",
    val check: String = "",
    @SerialName("blocked_by") val blockedBy: List<String> = emptyList(),
)

@Serializable
data class TasksData(val tasks: List<Task> = emptyList(), val source: String = "")

@Serializable
data class GoalData(
    val condition: String = "",
    val state: String = "",
    val reason: String = "",
    val source: String = "",
)

@Serializable
data class BudgetData(
    @SerialName("max_turns") val maxTurns: Int = 0,
    @SerialName("max_tokens") val maxTokens: Int = 0,
    @SerialName("max_usd") val maxUsd: Double = 0.0,
    val source: String = "",
)

@Serializable
data class NoticeData(
    val source: String = "",
    val level: String = "",
    val message: String = "",
)

@Serializable
data class StopVetoData(val module: String = "", val reason: String = "")

@Serializable
data class CompactionData(
    val mode: String = "",
    @SerialName("range_start") val rangeStart: String = "",
    @SerialName("range_end") val rangeEnd: String = "",
    val summary: String = "",
)

@Serializable
data class ContextData(
    val source: String = "",
    val slot: String = "",
    val content: String = "",
)

/** Event type names, matching protocol/types.go. */
object EventType {
    const val SESSION = "session"
    const val MESSAGE = "message"
    const val TOOL_CALL = "tool_call"
    const val TOOL_RESULT = "tool_result"
    const val OPTIONS_CHANGE = "options_change"
    const val STATE_CHANGE = "state_change"
    const val TASKS = "tasks"
    const val GOAL = "goal"
    const val CHECK = "check"
    const val STOP_VETO = "stop_veto"
    const val BUDGET = "budget"
    const val NOTICE = "notice"
    const val REPORT = "report"
    const val COMPACTION = "compaction"
    const val CONTEXT = "context"
}

/**
 * Decodes an event's payload, returning null rather than throwing. One event a
 * phone cannot read should cost that line of the transcript, not the screen.
 */
inline fun <reified T> Event.payload(): T? = try {
    NabuJson.decodeFromJsonElement(serializer<T>(), data)
} catch (_: Exception) {
    null
}

/** A string field of a raw payload, for types without a class of their own. */
fun Event.field(name: String): String? =
    ((data as? JsonObject)?.get(name) as? JsonPrimitive)?.content
