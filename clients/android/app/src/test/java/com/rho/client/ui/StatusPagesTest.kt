package com.rho.client.ui

import com.rho.client.data.parseInstant
import com.rho.client.protocol.GoalStatus
import com.rho.client.protocol.RhoJson
import com.rho.client.protocol.RunStatus
import com.rho.client.protocol.TreeMember
import com.rho.client.protocol.TreeStats
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/** The Run and Goal pages' working-out, on the runner's own example statuses. */
class StatusPagesTest {

    private fun fixture(name: String): String {
        var dir: File? = File("").absoluteFile
        while (dir != null && !File(dir, "clients/runs/testdata/status").isDirectory) dir = dir.parentFile
        requireNotNull(dir) { "could not find clients/runs/testdata/status" }
        return File(dir, "clients/runs/testdata/status/$name").readText()
    }

    private val run: RunStatus by lazy { RhoJson.decodeFromString(RunStatus.serializer(), fixture("run.json")) }
    private val goal: GoalStatus by lazy { RhoJson.decodeFromString(GoalStatus.serializer(), fixture("goal.json")) }

    private val minute = 60_000L

    @Test
    fun `a parked gate says it waits for Claude, and why`() {
        assertEquals("Waiting for Claude: You've hit your weekly limit", parkedLine("You've hit your weekly limit"))
        assertNull(parkedLine(""))
    }

    @Test
    fun `the timeline times each step from the one before`() {
        val now = parseInstant(run.since)!! + 42 * minute
        val rows = timeline(run, now)
        assertEquals(run.history.size + 1, rows.size)
        assertEquals(1_000L, rows[0].millis) // setup, a second after the start
        assertEquals(93 * minute - 1_000L, rows[1].millis)
        assertEquals("plan", rows[1].step)
        assertEquals("01M4P8ENR4XCTQT7HMWDVSCWG8", rows[1].session)
        assertEquals("asked for a revision of verify.sh", rows[4].result)
        val failed = rows[5]
        assertEquals(false, failed.ok)
        assertEquals(1, failed.task)
        assertTrue(failed.result.startsWith("the session ended paused"))
        val current = rows.last()
        assertNull(current.ok)
        assertEquals("work", current.step)
        assertEquals(2, current.task)
        assertEquals(42 * minute, current.millis)
    }

    @Test
    fun `a finished run has no current row`() {
        val done = run.copy(step = "done")
        assertEquals(run.history.size, timeline(done, 0).size)
        assertEquals("done", stepLine(done, 0))
        assertEquals("failed at work", stepLine(run.copy(step = "failed", failedAt = "work"), 0))
    }

    @Test
    fun `the step line says the task, the try and the time in the step`() {
        val now = parseInstant(run.since)!! + 42 * minute
        assertEquals("work · task 2 of 3 · try 2 · 42m in this step", stepLine(run, now))
        assertEquals("plan · 42m in this step", stepLine(run.copy(step = "plan", task = 0, attempt = 0), now))
    }

    @Test
    fun `turns are summed per task, with every try counted`() {
        val tree = TreeStats(members = listOf(
            TreeMember(sessionId = "01M4PFKEYY6GEREN9YBJ11GC6D", turns = 10),
            TreeMember(sessionId = "01M4PFQCAXFW19C4PHD50DSQSM", turns = 20),
            TreeMember(sessionId = "01M4P8ENR4XCTQT7HMWDVSCWG8", turns = 130),
        ))
        val bars = turnsPerTask(run, tree)
        assertEquals(listOf(1, 2), bars.map { it.task })
        assertEquals(TaskBar(1, "Notes in Room", 30, 2), bars[0])
        assertEquals(TaskBar(2, "Notes in state", 20, 1), bars[1])
        // Without the tree yet, the tries are still known.
        assertEquals(listOf(0, 0), turnsPerTask(run, null).map { it.turns })
        assertEquals(2, claudeGates(run))
    }

    @Test
    fun `rounds come newest first, with their briefs, verdicts and times`() {
        val rs = rounds(goal)
        assertEquals(listOf(2, 1), rs.map { it.round })
        assertEquals(listOf("In-Flight notes"), rs[0].briefs.map { it.title })
        assertNull(rs[0].verdict)
        assertNull(rs[0].millis)
        assertEquals(2, rs[1].briefs.size)
        assertEquals(false, rs[1].verdict?.met)
        assertEquals(4 * 60 * minute, rs[1].millis)
        assertEquals("round 2 of 10 · runs", goalLine(goal))
        assertEquals("merged", briefState(goal.briefs[0]))
        assertEquals("failed at work", briefState(goal.briefs[1]))
    }
}
