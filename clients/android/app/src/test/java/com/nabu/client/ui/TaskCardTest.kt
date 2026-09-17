package com.nabu.client.ui

import com.nabu.client.data.EventRow
import com.nabu.client.protocol.Task
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class TaskCardTest {

    private fun row(id: String, ordinal: Long, type: String, data: String) =
        EventRow(
            id = id,
            sessionId = "S1",
            ordinal = ordinal,
            type = type,
            raw = """{"id":"$id","type":"$type","timestamp":"2026-09-16T00:00:00Z","data":$data}""",
        )

    private fun snapshot(revision: Int, vararg tasks: String) = """
        {"revision":$revision,"source":"model","tasks":[${tasks.joinToString(",")}]}
    """.trimIndent()

    private fun task(id: String, title: String, status: String) =
        """{"id":"$id","title":"$title","status":"$status","blocked_by":[]}"""

    /** The card shows the last snapshot, not every snapshot ever written. */
    @Test
    fun `the newest snapshot wins`() {
        val rows = listOf(
            row("E1", 1, "tasks", snapshot(1, task("t1", "first", "pending"))),
            row(
                "E2", 2, "tasks",
                snapshot(2, task("t1", "first", "done"), task("t2", "second", "pending")),
            ),
        )

        val tasks = tasksOf(rows)

        assertEquals(listOf("t1", "t2"), tasks.map { it.id })
        assertEquals("done", tasks.first { it.id == "t1" }.status)
    }

    /** A log with no snapshot has no card, rather than an empty one. */
    @Test
    fun `a session with no tasks has none`() {
        val rows = listOf(row("E1", 1, "message", """{"role":"user","content":"hi"}"""))

        assertTrue(tasksOf(rows).isEmpty())
    }

    /** The snapshot sent back is the whole list, with one status changed. */
    @Test
    fun `completing a task changes only that task`() {
        val tasks = listOf(
            Task(id = "t1", title = "first", status = "in_progress"),
            Task(id = "t2", title = "second", status = "pending"),
        )

        val next = withDone(tasks, "t1")

        assertEquals("done", next.first { it.id == "t1" }.status)
        assertEquals("pending", next.first { it.id == "t2" }.status)
        assertEquals("the snapshot must stay whole", 2, next.size)
    }

    /**
     * A tap that shows nothing for two seconds reads as broken, so the tap is
     * believed until the daemon's own snapshot arrives.
     */
    @Test
    fun `a tapped task reads as done before the event arrives`() {
        val tasks = listOf(Task(id = "t1", title = "first", status = "pending"))

        assertEquals("done", overlay(tasks, setOf("t1")).single().status)
    }

    /** Once the snapshot says so, the overlay has nothing left to say. */
    @Test
    fun `an overlay for an already done task changes nothing`() {
        val tasks = listOf(Task(id = "t1", title = "first", status = "done"))

        assertEquals(tasks, overlay(tasks, setOf("t1")))
    }
}
