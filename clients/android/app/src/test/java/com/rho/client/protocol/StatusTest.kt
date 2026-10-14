package com.rho.client.protocol

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * The runner writes clients/runs/testdata/status from the same builders that
 * set a status, so a field it renames shows up here as a failure rather than
 * on the phone as a blank.
 */
class StatusTest {

    private val fixtures: File by lazy {
        var dir: File? = File("").absoluteFile
        while (dir != null && !File(dir, "clients/runs/testdata/status").isDirectory) {
            dir = dir.parentFile
        }
        requireNotNull(dir) { "could not find clients/runs/testdata/status from ${File("").absolutePath}" }
        File(dir, "clients/runs/testdata/status")
    }

    private fun fixture(kind: String, name: String) = StatusData(
        source = "client",
        kind = kind,
        data = RhoJson.parseToJsonElement(File(fixtures, name).readText()).jsonObject,
    )

    @Test
    fun `the runner's run status reads`() {
        val parsed = parseStatus(fixture("run", "run.json"))
        val run = (parsed as? ParsedStatus.Run)?.status ?: error("not a run: $parsed")
        assertEquals("In-Flight notes: data, state and actions (no UI)", run.title)
        assertEquals("work", run.step)
        assertEquals(2, run.task)
        assertEquals(listOf("Notes in Room", "Notes in state", "Actions"), run.tasks)
        assertEquals(1, run.attempt)
        assertEquals("01M4PFQCAXFW19C4PHD50DSQSM", run.session)
        assertEquals("01M498J1N9D785RFV6AZBRZN7G", run.goal)
        assertEquals(41, run.pr.number)
        assertEquals(1, run.fixes)
        assertEquals(1, run.revisions)
        assertTrue(run.since.isNotEmpty() && run.started.isNotEmpty())
        assertEquals(8, run.history.size)
        val failed = run.history[5]
        assertEquals("work", failed.step)
        assertEquals(1, failed.task)
        assertEquals(false, failed.ok)
        assertTrue(failed.why.endsWith("…"))
        assertEquals("01M4P8ENR4XCTQT7HMWDVSCWG8", run.history[1].session)
        assertEquals("asked for a revision of verify.sh", run.history[4].result)
        assertTrue(run.brief.startsWith("# In-Flight notes"))
    }

    @Test
    fun `a run waiting for Claude says why`() {
        val parsed = parseStatus(fixture("run", "parked.json"))
        val run = (parsed as? ParsedStatus.Run)?.status ?: error("not a run: $parsed")
        assertEquals("plan-review", run.step)
        assertEquals("You've hit your weekly limit · resets 2pm", run.parked)
        val fresh = parseStatus(fixture("run", "run.json")) as ParsedStatus.Run
        assertEquals("", fresh.status.parked)
    }

    @Test
    fun `the runner's goal status reads`() {
        val parsed = parseStatus(fixture("goal", "goal.json"))
        val goal = (parsed as? ParsedStatus.Goal)?.status ?: error("not a goal: $parsed")
        assertEquals("runs", goal.step)
        assertEquals(2, goal.round)
        assertEquals(10, goal.rounds)
        assertEquals(3, goal.briefs.size)
        assertEquals("failed", goal.briefs[1].state)
        assertEquals("work", goal.briefs[1].failedAt)
        assertEquals("01M4P8EH2VXBN4WJZ8R8PJ1HKX", goal.briefs[2].run)
        assertEquals(1, goal.verdicts.size)
        assertEquals(false, goal.verdicts[0].met)
        assertTrue(goal.verdicts[0].at.isNotEmpty())
    }

    @Test
    fun `a newer version is not guessed at`() {
        val newer = StatusData("client", "run", JsonObject(mapOf("version" to JsonPrimitive(2))))
        assertEquals(ParsedStatus.Unsupported("run", 2), parseStatus(newer))
    }

    @Test
    fun `an unknown kind is unsupported, and no status is null`() {
        val odd = StatusData("client", "build", JsonObject(mapOf("version" to JsonPrimitive(1))))
        assertEquals(ParsedStatus.Unsupported("build", 1), parseStatus(odd))
        assertNull(parseStatus(null))
    }
}
