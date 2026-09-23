package com.nabu.client.ui

import com.nabu.client.protocol.NabuJson
import com.nabu.client.protocol.SessionStats
import org.junit.Assert.assertEquals
import org.junit.Test

/** Issue 38: the phone shows the daemon's numbers; these are the bits it works out itself. */
class StatsTest {

    @Test
    fun `counts read the way a person reads them`() {
        assertEquals("999", compactCount(999))
        assertEquals("1.3K", compactCount(1284))
        assertEquals("13K", compactCount(12_900))
        assertEquals("77.1M", compactCount(77_085_054))
    }

    @Test
    fun `working time is hours and minutes`() {
        assertEquals("45s", spanOf(45))
        assertEquals("12m", spanOf(12 * 60 + 5))
        assertEquals("3h 29m", spanOf(12_580))
    }

    @Test
    fun `an axis tops out at a round number`() {
        assertEquals(1L, niceMax(0))
        assertEquals(200_000L, niceMax(190_741))
        assertEquals(50_000_000L, niceMax(30_000_000))
        assertEquals(100L, niceMax(100))
    }

    @Test
    fun `a tap reads the nearest turn`() {
        assertEquals(0, nearestIndex(0f, 100f, 11))
        assertEquals(5, nearestIndex(52f, 100f, 11))
        assertEquals(10, nearestIndex(140f, 100f, 11))
        assertEquals(0, nearestIndex(10f, 100f, 1))
    }

    // The example in spec 7.21, as the daemon sends it.
    @Test
    fun `the daemon's payload decodes`() {
        val s = NabuJson.decodeFromString(
            SessionStats.serializer(),
            """{"session_id":"S1","turns":41,"prompts":6,
               "tokens":{"input":1840000,"output":21000,"cached":0,"peak_context":118000,"context_window":256000},
               "per_turn":[{"at":"2026-09-20T10:00:00Z","input":12000,"output":300}],
               "tools":[{"tool":"read","calls":60,"errors":2}],
               "compactions":{"summarize":1,"clear_results":2},
               "vetoes":3,"interruptions":1,"started_at":"2026-09-20T10:00:00Z",
               "last_event_at":"2026-09-20T12:00:00Z","working_seconds":5400,
               "rereads":[{"path":"engine.rs","reads":7}]}""",
        )
        assertEquals(41, s.turns)
        assertEquals(118_000L, s.tokens.peakContext)
        assertEquals(2, s.compactions.clearResults)
        assertEquals("read", s.tools.single().tool)
        assertEquals(7, s.rereads.single().reads)
        assertEquals(5400L, s.workingSeconds)
    }
}
