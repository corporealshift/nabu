package com.nabu.client.ui

import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The connection loop itself, under virtual time, with the socket replaced by
 * a function that behaves like one.
 */
class ConnectionLoopTest {

    private val connection = MutableStateFlow(Connection.Offline)
    private val error = MutableStateFlow<String?>(null)

    private fun TestScope.loop(connectOnce: suspend (() -> Unit) -> Unit) =
        ConnectionLoop(backgroundScope, connection, error, connectOnce)

    // Issue 69. The screen is recreated — a rotation, the system switching to
    // dark mode, coming back from the background — and asks for a connection.
    // That used to restart the loop, and the loop it replaced, on its way out,
    // wrote "Offline" and its own cancellation message over the new one after
    // it had connected. The screen said the daemon could not be reached over a
    // working socket until the next real drop.
    @Test
    fun `a replaced loop does not speak over the one that replaced it`() = runTest {
        var calls = 0
        val l = loop { onConnected ->
            val mine = ++calls
            onConnected()
            try {
                awaitCancellation()
            } finally {
                // Closing a socket takes a moment, so the old loop finishes
                // after the new one is already up.
                if (mine == 1) withContext(NonCancellable) { delay(100) }
            }
        }

        l.start()
        runCurrent()
        assertEquals(Connection.Connected, connection.value)

        l.start(restart = true)
        advanceTimeBy(1_000)
        runCurrent()

        assertEquals(2, calls)
        assertEquals(Connection.Connected, connection.value)
        assertNull("being replaced is not a connection error", error.value)
    }

    @Test
    fun `asking again while connected leaves the connection alone`() = runTest {
        var calls = 0
        val l = loop { onConnected ->
            calls++
            onConnected()
            awaitCancellation()
        }

        l.start()
        runCurrent()
        l.start()
        runCurrent()

        assertEquals("a second start must not reconnect", 1, calls)
        assertEquals(Connection.Connected, connection.value)
    }

    @Test
    fun `a drop is reported and retried, and a good connection resets the wait`() = runTest {
        var calls = 0
        val l = loop { onConnected ->
            calls++
            onConnected()
            if (calls == 1) throw java.io.IOException("connection reset by peer")
            awaitCancellation()
        }

        l.start()
        runCurrent()
        assertEquals(Connection.Offline, connection.value)
        assertEquals("connection reset by peer", error.value)

        advanceTimeBy(999)
        runCurrent()
        assertEquals("the retry waits out the backoff", 1, calls)

        advanceTimeBy(2)
        runCurrent()
        assertEquals(2, calls)
        assertEquals(Connection.Connected, connection.value)
        assertNull(error.value)
    }

    @Test
    fun `a nudge ends the wait early`() = runTest {
        var calls = 0
        val l = loop {
            calls++
            throw java.io.IOException("no route to host")
        }

        l.start()
        runCurrent()
        advanceTimeBy(1_001) // second attempt; the next wait is 2s
        runCurrent()
        assertEquals(2, calls)

        l.nudge()
        runCurrent()
        assertEquals("coming back to the app tries at once", 3, calls)
    }

    @Test
    fun `nothing to connect to ends the loop`() = runTest {
        var calls = 0
        val l = loop { calls++ }

        l.start()
        advanceTimeBy(60_000)
        runCurrent()

        assertEquals(1, calls)
        assertEquals(Connection.Offline, connection.value)
    }
}
