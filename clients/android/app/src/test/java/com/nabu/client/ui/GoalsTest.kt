package com.nabu.client.ui

import com.nabu.client.data.SessionRow
import com.nabu.client.protocol.Options
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** The phone's half of goals (docs/specs/2026-10-06-goals-design.md), matching the terminal client's. */
class GoalsTest {

    @Test
    fun `asking for a goal replaces its goal labels and keeps the rest`() {
        assertEquals(listOf("goal:requested"), goalLabels(emptyList()))
        assertEquals(listOf("mine", "goal:requested"), goalLabels(listOf("mine")))
        // A blocked goal is resumed by asking again.
        assertEquals(listOf("mine", "goal:requested"), goalLabels(listOf("goal:blocked", "goal:round:3", "mine")))
    }

    @Test
    fun `a goal's status comes from its labels`() {
        assertNull(goalStatus(emptyList()))
        assertNull(goalStatus(listOf("run:plan")))
        assertEquals("goal: requested", goalStatus(listOf("goal:requested")))
        assertEquals("goal: runs, round 2", goalStatus(listOf("goal:runs", "goal:round:2")))
        assertNull("a round alone is no step", goalStatus(listOf("goal:round:2")))
    }

    @Test
    fun `slash goal is a command, anything else is not`() {
        assertEquals("", parseGoal("/goal"))
        assertEquals("add offline sync", parseGoal("/goal add offline sync"))
        assertEquals("add offline sync\nto the client", parseGoal("/goal\nadd offline sync\nto the client"))
        assertNull(parseGoal("/goalkeeper"))
        assertNull(parseGoal("set a /goal"))
        assertNull(parseRun("/goal add offline sync"))
    }

    private fun card(id: String, parent: String = "") =
        SessionCard(SessionRow(id), prompt = id, options = Options(parent = parent))

    @Test
    fun `a goal's runs nest under it, and their steps under them`() {
        val placed = groupByParent(
            listOf(card("step", "run1"), card("run2", "goal"), card("run1", "goal"), card("other"), card("goal")),
        )
        assertEquals(listOf("other", "goal", "run2", "run1", "step"), placed.map { it.card.row.id })
        assertEquals(listOf(0, 0, 1, 1, 2), placed.map { it.depth })
    }

    // A goal's home never runs: folded, it is the count that says work is going on.
    @Test
    fun `a folded card says when something under it is working`() {
        val step = SessionCard(SessionRow("step", state = "running"), prompt = "step", options = Options(parent = "run1"))
        val counts = underCounts(listOf(step, card("run1", "goal"), card("goal")))
        assertEquals(Under(2, working = 1), counts["goal"])
        assertEquals("2 sessions · 1 working", counts["goal"]!!.label)
        assertEquals("1 session · 1 working", counts["run1"]!!.label)
        assertEquals("3 sessions", Under(3).label)
    }

    // Issue 135: families start folded, and open one level at a time.
    @Test
    fun `a family is folded until it is opened, one level at a time`() {
        val cards = listOf(card("step", "run1"), card("run2", "goal"), card("run1", "goal"), card("other"), card("goal"))
        val placed = groupByParent(cards)
        assertEquals(listOf("other", "goal"), visible(placed, emptySet()).map { it.card.row.id })
        assertEquals(listOf("other", "goal", "run2", "run1"), visible(placed, setOf("goal")).map { it.card.row.id })
        assertEquals(listOf("other", "goal", "run2", "run1", "step"), visible(placed, setOf("goal", "run1")).map { it.card.row.id })
        // A run opened under a folded goal stays hidden with it.
        assertEquals(listOf("other", "goal"), visible(placed, setOf("run1")).map { it.card.row.id })

        assertEquals(mapOf("goal" to Under(3), "run1" to Under(1)), underCounts(cards))
    }
}
