package com.rho.client.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class EffortChipTest {

    @Test
    fun `an unset effort reads auto`() {
        assertEquals("effort auto", effortChipText(""))
    }

    @Test
    fun `a set effort reads its level`() {
        assertEquals("effort medium", effortChipText("medium"))
    }

    // The dialog offers what the daemon accepts: default first, then every level.
    @Test
    fun `the choices are default and the six levels`() {
        assertEquals(listOf("", "none", "low", "medium", "high", "xhigh", "max"), effortLevels)
    }
}
