package com.nabu.client.notify

// Phone notifications (docs/specs/2026-10-06-phone-notifications-design.md).
// The daemon sends identifiers, states, labels and counts, never words: the
// words are written here, from the phone's own mirror.

/** One notification as the daemon sent it. */
data class Push(
    val kind: String,
    val sessionId: String,
    val state: String = "",
    val label: String = "",
    val tasksDone: Int = 0,
    val tasksTotal: Int = 0,
    val requestId: String = "",
) {
    companion object {
        const val QUESTION = "question"
        const val RESOLVED = "resolved"
        const val LABEL = "label"
        const val STOPPED = "stopped"
        const val DONE = "done"

        /** Reads a message's data, or null when it is not one of ours. */
        fun parse(data: Map<String, String>): Push? {
            val kind = data["kind"] ?: return null
            val id = data["session_id"] ?: return null
            return Push(
                kind = kind,
                sessionId = id,
                state = data["state"].orEmpty(),
                label = data["label"].orEmpty(),
                tasksDone = data["tasks_done"]?.toIntOrNull() ?: 0,
                tasksTotal = data["tasks_total"]?.toIntOrNull() ?: 0,
                requestId = data["request_id"].orEmpty(),
            )
        }
    }
}

/** The two channels, each silenced on its own in Android's settings. */
object Channels {
    const val QUESTIONS = "questions"
    const val RUNS = "runs"
}

/** What a notification says, and where it goes. */
data class Wording(val title: String, val text: String, val channel: String)

/**
 * The words for [push]. [project] and [title] come from the mirror: the
 * session's workspace name, and what names it in the list. A session the
 * mirror has not seen yet is "a session".
 */
fun wording(push: Push, project: String, title: String): Wording {
    val where = project.ifBlank { "nabu" }
    val what = title.ifBlank { "a session" }
    return when (push.kind) {
        Push.QUESTION -> Wording("$where is asking you something", what, Channels.QUESTIONS)
        Push.LABEL -> {
            val outcome = when (push.label) {
                "run:done" -> "Run done"
                "run:failed" -> "Run failed"
                "goal:done" -> "Goal done"
                "goal:blocked" -> "Goal blocked"
                else -> push.label
            }
            Wording("$outcome: $what", where, Channels.RUNS)
        }
        Push.STOPPED -> Wording("$where stopped: ${push.state.ifBlank { "stopped" }}", what, Channels.RUNS)
        else -> {
            val tasks = if (push.tasksTotal > 0) ": ${push.tasksDone}/${push.tasksTotal} tasks" else ""
            Wording("$where finished$tasks", what, Channels.RUNS)
        }
    }
}

/**
 * The notification's id. A question and its resolution share one, so the
 * resolution can take the question away; anything else is one per session
 * and kind, so a second "run done" replaces the first.
 */
fun notificationId(push: Push): Int =
    if (push.kind == Push.QUESTION || push.kind == Push.RESOLVED) push.requestId.hashCode()
    else (push.sessionId + "|" + push.kind + "|" + push.label).hashCode()

/** What the app is showing, so a notification for it can be dropped. */
object OnScreen {
    @Volatile var sessionId: String? = null
    @Volatile var foreground: Boolean = false

    /** Whether a push for [sessionId] would only repeat what is on screen. */
    fun showing(sessionId: String): Boolean = foreground && this.sessionId == sessionId
}
