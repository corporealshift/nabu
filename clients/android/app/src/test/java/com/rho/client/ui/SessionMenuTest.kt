package com.rho.client.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class SessionMenuTest {

    @Test
    fun `a session of its own offers everything, effort showing its level`() {
        val items = sessionMenuItems(hasStatus = true, effort = "low", canRun = true, compacting = false)
        assertEquals(listOf("Status", "Effort low", "Run", "Compact", "Stats"), items.map { it.label })
        assertEquals(
            listOf(MenuItem.Status, MenuItem.Effort, MenuItem.Run, MenuItem.Compact, MenuItem.Stats),
            items.map { it.item },
        )
    }

    // A step of a run is the runner's: it is not handed over as a run of its own.
    @Test
    fun `a step of a run has no Run, and no Status where the screen has none`() {
        val items = sessionMenuItems(hasStatus = false, effort = "", canRun = false, compacting = false)
        assertEquals(listOf("Effort auto", "Compact", "Stats"), items.map { it.label })
    }

    @Test
    fun `compact is greyed while a summary is written`() {
        val compact = sessionMenuItems(hasStatus = false, effort = "", canRun = true, compacting = true)
            .single { it.item == MenuItem.Compact }
        assertEquals("Compacting…", compact.label)
        assertFalse(compact.enabled)
    }
}
