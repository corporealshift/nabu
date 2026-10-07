package com.nabu.client.protocol

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** How much work a session was (spec 7.21). The daemon computes it; the phone shows it. */
@Serializable
data class SessionStats(
    @SerialName("session_id") val sessionId: String = "",
    val turns: Int = 0,
    val prompts: Int = 0,
    val tokens: TokenStats = TokenStats(),
    @SerialName("per_turn") val perTurn: List<TurnStats> = emptyList(),
    val tools: List<ToolStats> = emptyList(),
    val compactions: CompactionStats = CompactionStats(),
    val vetoes: Int = 0,
    val interruptions: Int = 0,
    @SerialName("working_seconds") val workingSeconds: Long = 0,
    val rereads: List<Reread> = emptyList(),
)

@Serializable
data class TokenStats(
    val input: Long = 0,
    val output: Long = 0,
    val cached: Long = 0,
    @SerialName("peak_context") val peakContext: Long = 0,
    @SerialName("context_window") val contextWindow: Long = 0,
)

@Serializable
data class TurnStats(val at: String = "", val input: Long = 0, val output: Long = 0)

@Serializable
data class ToolStats(val tool: String = "", val calls: Int = 0, val errors: Int = 0)

@Serializable
data class CompactionStats(
    val summarize: Int = 0,
    @SerialName("clear_results") val clearResults: Int = 0,
)

@Serializable
data class Reread(val path: String = "", val reads: Int = 0)

/** One day's usage across sessions (spec 7.22). */
@Serializable
data class UsageDay(
    val date: String = "",
    val turns: Int = 0,
    val input: Long = 0,
    val output: Long = 0,
)

/** The work done across sessions over the last days (spec 7.25). */
@Serializable
data class WindowStats(
    val days: Int = 0,
    val kind: String = "all",
    val sessions: Int = 0,
    val turns: Int = 0,
    val prompts: Int = 0,
    val tokens: TokenStats = TokenStats(),
    val tools: List<WindowTool> = emptyList(),
    val compactions: CompactionStats = CompactionStats(),
    val vetoes: Int = 0,
    val interruptions: Int = 0,
    @SerialName("working_seconds") val workingSeconds: Long = 0,
    @SerialName("per_day") val perDay: List<UsageDay> = emptyList(),
    val skipped: Int = 0,
)

/** One tool across sessions: how often, how often it failed, and how many sessions used it. */
@Serializable
data class WindowTool(
    val tool: String = "",
    val calls: Int = 0,
    val errors: Int = 0,
    val sessions: Int = 0,
)

/** One tool call and the start of what came back (spec 7.26). */
@Serializable
data class ToolCall(
    @SerialName("session_id") val sessionId: String = "",
    val label: String = "",
    val at: String = "",
    val arguments: kotlinx.serialization.json.JsonElement? = null,
    val status: String = "",
    val kind: String = "",
    val result: String = "",
    val archived: Boolean = false,
)

@Serializable
data class ToolCalls(
    val calls: List<ToolCall> = emptyList(),
    val truncated: Boolean = false,
    val skipped: Int = 0,
)
