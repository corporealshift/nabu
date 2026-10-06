package com.nabu.client.notify

import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The phone's half of notifications (docs/specs/2026-10-06-phone-notifications-design.md). */
class PushTest {

    @After
    fun reset() {
        OnScreen.sessionId = null
        OnScreen.foreground = false
    }

    @Test
    fun `a push is read from the daemon's data`() {
        val push = Push.parse(
            mapOf("kind" to "done", "session_id" to "01M4", "state" to "idle", "tasks_done" to "5", "tasks_total" to "6"),
        )
        assertEquals(Push(kind = "done", sessionId = "01M4", state = "idle", tasksDone = 5, tasksTotal = 6), push)
        assertNull("not one of ours", Push.parse(mapOf("title" to "hello")))
        assertNull(Push.parse(mapOf("kind" to "done")))
    }

    @Test
    fun `the words come from the mirror, the facts from the push`() {
        val goal = Push(kind = Push.LABEL, sessionId = "G", label = "goal:blocked")
        assertEquals(Wording("Goal blocked: Add offline sync", "breezeway", Channels.RUNS), wording(goal, "breezeway", "Add offline sync"))
        assertEquals("Run done: Median", wording(goal.copy(label = "run:done"), "x", "Median").title)
        assertEquals("deploy:ok: Median", wording(goal.copy(label = "deploy:ok"), "x", "Median").title)

        val question = Push(kind = Push.QUESTION, sessionId = "S", requestId = "R1")
        assertEquals(Wording("liftoff is asking you something", "fix the build", Channels.QUESTIONS), wording(question, "liftoff", "fix the build"))

        val stopped = Push(kind = Push.STOPPED, sessionId = "S", state = "error")
        assertEquals("liftoff stopped: error", wording(stopped, "liftoff", "x").title)

        val done = Push(kind = Push.DONE, sessionId = "S", tasksDone = 5, tasksTotal = 6)
        assertEquals("liftoff finished: 5/6 tasks", wording(done, "liftoff", "x").title)
        assertEquals("liftoff finished", wording(done.copy(tasksTotal = 0), "liftoff", "x").title)

        // A session the phone has not mirrored yet still says something.
        assertEquals(Wording("nabu is asking you something", "a session", Channels.QUESTIONS), wording(question, "", ""))
    }

    @Test
    fun `a resolution takes its question's place, and a repeat replaces the last`() {
        val asked = Push(kind = Push.QUESTION, sessionId = "S", requestId = "R1")
        assertEquals(notificationId(asked), notificationId(asked.copy(kind = Push.RESOLVED)))
        assertNotEquals(notificationId(asked), notificationId(asked.copy(requestId = "R2")))
        val done = Push(kind = Push.LABEL, sessionId = "S", label = "run:done")
        assertEquals(notificationId(done), notificationId(done.copy(tasksDone = 3)))
        assertNotEquals(notificationId(done), notificationId(done.copy(label = "run:failed")))
    }

    @Test
    fun `a push for the session on screen is dropped only while the app is in front`() {
        OnScreen.sessionId = "S"
        assertFalse("in the background, it shows", OnScreen.showing("S"))
        OnScreen.foreground = true
        assertTrue(OnScreen.showing("S"))
        assertFalse(OnScreen.showing("OTHER"))
    }
}
