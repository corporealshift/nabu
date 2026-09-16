package com.nabu.client.protocol

import kotlinx.serialization.json.jsonPrimitive

/**
 * A session's current state, derived by one left-to-right pass over its log.
 *
 * Spec 5 makes the projection normative: this and the Go implementation must
 * agree on every log, which is what the shared conformance vectors check.
 */
data class State(
    val state: String = "idle",
    val options: Options = Options(),
    val goal: GoalData? = null,
    val tasks: List<Task> = emptyList(),
    val budget: BudgetData = BudgetData(source = "daemon"),
    val turns: Int = 0,
    val usage: Usage = Usage(),
    val lastEventId: String = "",
    val compactedThrough: String = "",
) {
    /** Whether the goal still governs the loop. */
    val goalActive: Boolean
        get() = goal != null && (goal.state == "set" || goal.state == "unmet")

    val openTasks: List<Task>
        get() = tasks.filter { it.status == "pending" || it.status == "in_progress" }
}

/** Folds a log into its state. */
fun project(log: List<Event>): State {
    var st = State()
    for (e in log) {
        st = st.copy(lastEventId = e.id)
        when (e.type) {
            EventType.SESSION ->
                e.payload<SessionData>()?.let { st = st.copy(options = it.options) }

            EventType.OPTIONS_CHANGE ->
                e.payload<OptionsChangeData>()?.let { d ->
                    val to = d.to?.jsonPrimitive
                    st = when (d.key) {
                        "model" -> st.copy(options = st.options.copy(model = to?.content ?: ""))
                        "compaction_enabled" ->
                            st.copy(options = st.options.copy(
                                compactionEnabled = to?.content?.toBoolean() ?: true))
                        "permission_mode" ->
                            st.copy(options = st.options.copy(permissionMode = to?.content ?: ""))
                        else -> st
                    }
                }

            EventType.STATE_CHANGE ->
                e.payload<StateChangeData>()?.let { st = st.copy(state = it.to) }

            EventType.GOAL ->
                e.payload<GoalData>()?.let { st = st.copy(goal = it) }

            EventType.TASKS ->
                e.payload<TasksData>()?.let { st = st.copy(tasks = it.tasks) }

            EventType.BUDGET ->
                e.payload<BudgetData>()?.let { st = st.copy(budget = it) }

            EventType.MESSAGE ->
                e.payload<MessageData>()?.let { d ->
                    if (d.role == "assistant") {
                        // A turn is an assistant message, so this counts model
                        // round trips rather than anything the human did.
                        st = st.copy(turns = st.turns + 1)
                        d.usage?.let { u ->
                            st = st.copy(usage = Usage(
                                inputTokens = st.usage.inputTokens + u.inputTokens,
                                outputTokens = st.usage.outputTokens + u.outputTokens,
                                cachedTokens = st.usage.cachedTokens + u.cachedTokens,
                            ))
                        }
                    }
                }

            EventType.COMPACTION ->
                e.payload<CompactionData>()?.let {
                    if (it.mode == "summarize") st = st.copy(compactedThrough = it.rangeEnd)
                }
        }
    }
    return st
}
