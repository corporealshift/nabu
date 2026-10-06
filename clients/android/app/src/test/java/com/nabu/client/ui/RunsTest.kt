package com.nabu.client.ui

import com.nabu.client.data.SessionRow
import com.nabu.client.protocol.Options
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** The phone's half of orchestrated runs, matching the terminal client's. */
class RunsTest {

    @Test
    fun `run labels replace any run label and keep the rest`() {
        assertEquals(listOf("run:requested"), runLabels(emptyList()))
        assertEquals(listOf("mine", "run:requested"), runLabels(listOf("mine")))
        // A failed run is resumed by asking again; its step labels go.
        assertEquals(
            listOf("mine", "run:requested"),
            runLabels(listOf("run:failed", "run:attempt:10/10", "mine")),
        )
    }

    @Test
    fun `run status reads a home's labels`() {
        assertNull(runStatus(emptyList()))
        assertNull(runStatus(listOf("mine", "guard:no-push")))
        assertEquals("run: requested", runStatus(listOf("run:requested")))
        assertEquals("run: fix (3/10)", runStatus(listOf("run:fix", "run:attempt:3/10")))
        assertNull("an attempt alone is no step", runStatus(listOf("run:attempt:3/10")))
    }

    private fun card(id: String, parent: String = "") =
        SessionCard(SessionRow(id), prompt = id, options = Options(parent = parent))

    @Test
    fun `steps are listed under their home`() {
        // The daemon lists newest first, so a run's steps come before its home.
        val placed = groupByParent(
            listOf(card("step2", "home"), card("other"), card("step1", "home"), card("home"), card("orphan", "gone")),
        )
        assertEquals(listOf("other", "home", "step2", "step1", "orphan"), placed.map { it.card.row.id })
        assertEquals(listOf(false, false, true, true, false), placed.map { it.child })
    }

    @Test
    fun `slash run is a command, anything else a prompt`() {
        assertEquals("", parseRun("/run"))
        assertEquals("", parseRun("  /run  "))
        assertEquals("add a cache", parseRun("/run add a cache"))
        assertEquals("add a cache\nwith tests", parseRun("/run\nadd a cache\nwith tests"))
        assertNull(parseRun("/running late"))
        assertNull(parseRun("please /run this"))
        assertNull(parseRun("add a cache"))
    }
}

class SessionCardTitleTest {
    @Test
    fun `a run's home is named by its brief`() {
        val home = SessionCard(SessionRow("H"), prompt = "", options = Options(description = "\n  Add a Count function\n\nIt returns how many..."))
        assertEquals("Add a Count function", home.title)
        val goalRun = SessionCard(SessionRow("R"), prompt = "", options = Options(description = "# Median\n\nAdd a Median function."))
        assertEquals("Median", goalRun.title)
        assertEquals("asked", SessionCard(SessionRow("S"), prompt = "asked", options = Options(description = "brief")).title)
        assertEquals("", SessionCard(SessionRow("E"), prompt = "").title)
        assertEquals(
            "Do task 2 of 4 of this run: **Add Count**",
            SessionCard(SessionRow("T"), prompt = "Do task 2 of 4 of this run:\n\n**Add Count**").title,
        )
    }
}
