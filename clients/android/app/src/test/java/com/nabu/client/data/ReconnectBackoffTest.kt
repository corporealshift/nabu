package com.nabu.client.data

import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The connection loop's arithmetic and its wake-up, tested without a socket.
 *
 * The loop itself lives in NabuViewModel and needs an Application, but the two
 * things that were wrong are pure: when the backoff resets, and whether a
 * foreground nudge cuts the wait short. Both are reproduced here exactly as the
 * loop does them.
 */
class ReconnectBackoffTest {

    private val min = 1_000L
    private val max = 30_000L

    private fun grow(b: Long) = (b * 2).coerceAtMost(max)

    // The bug: runConnection always throws, so a reset placed after it never
    // ran. Each drop doubled the wait and nothing ever brought it back down, so
    // an app that had been backgrounded a few times waited 30s to retry even
    // when the daemon was right there. Restarting the app was the only cure,
    // which is exactly what was reported.
    @Test
    fun `a wait that never resets reaches the ceiling and stays`() {
        var backoff = min
        repeat(8) { backoff = grow(backoff) }
        assertEquals("without a reset it pins at the ceiling", max, backoff)
    }

    @Test
    fun `a connection that succeeded brings the wait back down`() {
        var backoff = min
        repeat(8) { backoff = grow(backoff) }
        assertEquals(max, backoff)

        // What the loop now does the moment the socket opens.
        val onConnected = { backoff = min }
        onConnected()

        assertEquals("the next retry after a good connection is quick", min, backoff)
    }

    @Test
    fun `the wait doubles up to the ceiling and no further`() {
        val seen = mutableListOf<Long>()
        var backoff = min
        repeat(7) {
            seen += backoff
            backoff = grow(backoff)
        }
        assertEquals(listOf(1_000L, 2_000L, 4_000L, 8_000L, 16_000L, 30_000L, 30_000L), seen)
    }

    // Coming back to the app has to cut the wait short. Frozen, the process
    // could not count down; on return it would otherwise sit out a delay that
    // had already elapsed in wall-clock time.
    @Test
    fun `a foreground nudge ends the wait early`() = runTest {
        val wake = Channel<Unit>(Channel.CONFLATED)

        wake.trySend(Unit) // the app came back
        val woke = withTimeoutOrNull(max) { wake.receive() }

        assertTrue("the loop should wake rather than wait out the backoff", woke != null)
        assertTrue("and not by burning the whole delay", currentTime < max)
    }

    @Test
    fun `with no nudge the wait runs its course`() = runTest {
        val wake = Channel<Unit>(Channel.CONFLATED)

        val woke = withTimeoutOrNull(5_000L) { wake.receive() }

        assertEquals("nothing woke it", null, woke)
        assertEquals("so it waited the whole backoff", 5_000L, currentTime)
    }

    // Conflated on purpose: ten foreground events and one mean the same thing,
    // and a buffered channel would queue them into ten immediate retries.
    @Test
    fun `repeated nudges collapse into one`() = runTest {
        val wake = Channel<Unit>(Channel.CONFLATED)
        repeat(10) { wake.trySend(Unit) }

        assertTrue("the first is delivered", withTimeoutOrNull(100) { wake.receive() } != null)
        assertEquals("and there is not a queue behind it", null, withTimeoutOrNull(100) { wake.receive() })
    }
}
