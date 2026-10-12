package com.rho.client.protocol

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonPrimitive

/*
 * A run's and a goal's status: the runner's snapshot, set on its home
 * (docs/specs/2026-10-11-run-page-design.md). The shapes are the runner's
 * (clients/runs/status.go); its testdata/status files are decoded by the tests
 * here, so the two cannot drift.
 */

/** The status version this app reads. */
const val STATUS_VERSION = 1

/** A run's snapshot. Task numbers count from 1; 0 is no task. */
@Serializable
data class RunStatus(
    val version: Int = 0,
    val title: String = "",
    val step: String = "",
    val task: Int = 0,
    val tasks: List<String> = emptyList(),
    val attempt: Int = 0,
    val since: String = "",
    val session: String = "",
    val waiting: Boolean = false,
    val goal: String = "",
    @SerialName("goal_title") val goalTitle: String = "",
    val pr: StatusPr = StatusPr(),
    val fixes: Int = 0,
    @SerialName("ci_fixes") val ciFixes: Int = 0,
    val revisions: Int = 0,
    @SerialName("failed_at") val failedAt: String = "",
    val why: String = "",
    val history: List<StatusRow> = emptyList(),
    val brief: String = "",
    val started: String = "",
    val updated: String = "",
)

/** A pull request, or none when [number] is 0. */
@Serializable
data class StatusPr(val number: Int = 0, val url: String = "")

/** One finished step of a run. */
@Serializable
data class StatusRow(
    val step: String = "",
    val task: Int = 0,
    val session: String = "",
    val ok: Boolean = false,
    val result: String = "",
    val why: String = "",
    val at: String = "",
)

/** A goal's snapshot. */
@Serializable
data class GoalStatus(
    val version: Int = 0,
    val text: String = "",
    val step: String = "",
    val round: Int = 0,
    val rounds: Int = 0,
    val since: String = "",
    val briefs: List<GoalStatusBrief> = emptyList(),
    val verdicts: List<Verdict> = emptyList(),
    val pr: StatusPr = StatusPr(),
    val why: String = "",
    val started: String = "",
    val updated: String = "",
)

/** One of a goal's briefs: a run, once it has one. */
@Serializable
data class GoalStatusBrief(
    val title: String = "",
    val round: Int = 0,
    val state: String = "",
    val run: String = "",
    @SerialName("pr_url") val prUrl: String = "",
    @SerialName("failed_at") val failedAt: String = "",
    val why: String = "",
)

/** What the check said at the end of a round. */
@Serializable
data class Verdict(
    val round: Int = 0,
    val met: Boolean = false,
    val reason: String = "",
    val at: String = "",
)

/** A session's status, read. */
sealed interface ParsedStatus {
    data class Run(val status: RunStatus) : ParsedStatus
    data class Goal(val status: GoalStatus) : ParsedStatus

    /** A status from a runner newer than this app, or one it cannot read. */
    data class Unsupported(val kind: String, val version: Int) : ParsedStatus
}

/** Reads a status, or null when there is none. */
fun parseStatus(status: StatusData?): ParsedStatus? {
    if (status == null) return null
    val version = runCatching { status.data["version"]?.jsonPrimitive?.intOrNull }.getOrNull() ?: 0
    if (version != STATUS_VERSION) return ParsedStatus.Unsupported(status.kind, version)
    return runCatching {
        when (status.kind) {
            "run" -> ParsedStatus.Run(RhoJson.decodeFromJsonElement(RunStatus.serializer(), status.data))
            "goal" -> ParsedStatus.Goal(RhoJson.decodeFromJsonElement(GoalStatus.serializer(), status.data))
            else -> ParsedStatus.Unsupported(status.kind, version)
        }
    }.getOrElse { ParsedStatus.Unsupported(status.kind, version) }
}

/** A session and every session under it (spec 7.21 with tree). */
@Serializable
data class TreeStats(
    @SerialName("session_id") val sessionId: String = "",
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
    @SerialName("started_at") val startedAt: String = "",
    @SerialName("last_event_at") val lastEventAt: String = "",
    val members: List<TreeMember> = emptyList(),
    val skipped: Int = 0,
)

/** One session of a tree, measured alone. */
@Serializable
data class TreeMember(
    @SerialName("session_id") val sessionId: String = "",
    val parent: String = "",
    val labels: List<String> = emptyList(),
    val turns: Int = 0,
    @SerialName("working_seconds") val workingSeconds: Long = 0,
    val tokens: TokenStats = TokenStats(),
    @SerialName("started_at") val startedAt: String = "",
    @SerialName("last_event_at") val lastEventAt: String = "",
    val archived: Boolean = false,
)
