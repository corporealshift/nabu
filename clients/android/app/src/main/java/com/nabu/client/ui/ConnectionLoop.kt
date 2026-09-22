package com.nabu.client.ui

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/** How the app is currently placed with respect to the daemon. */
enum class Connection { Offline, Connecting, Connected }

/**
 * Keeps one connection to the daemon up, retrying with backoff.
 *
 * [connectOnce] opens a connection and lives for as long as it does. It calls
 * `onConnected` once the socket is open, throws when the connection drops,
 * and returns normally only when there is nothing to connect to, which ends
 * the loop.
 *
 * Only the newest loop may speak. A loop that is replaced is cancelled, but a
 * cancelled coroutine still runs its catch and finally, and those used to
 * write "Offline" and the cancellation's own message over the state of the
 * loop that replaced it — after it had connected. The screen then said the
 * daemon could not be reached while the connection was fine, and nothing
 * corrected it until the next real drop (issue 69). Every write is checked
 * against the generation that made it.
 */
class ConnectionLoop(
    private val scope: CoroutineScope,
    private val connection: MutableStateFlow<Connection>,
    private val error: MutableStateFlow<String?>,
    private val connectOnce: suspend (onConnected: () -> Unit) -> Unit,
    private val minBackoff: Long = 1_000L,
    private val maxBackoff: Long = 30_000L,
    private val log: (String) -> Unit = {},
) {
    private var job: Job? = null
    @Volatile private var generation = 0

    /**
     * Wakes the loop out of its backoff. Conflated because ten nudges and one
     * mean the same thing: try now.
     */
    private val wake = Channel<Unit>(Channel.CONFLATED)

    /**
     * Starts the loop. Unless [restart] is set, a loop already running is left
     * alone: the screen asks for a connection every time it is recreated — a
     * rotation, a theme change, coming back from the background — and tearing
     * a healthy one down each time is how those became disconnects.
     */
    fun start(restart: Boolean = false) {
        if (!restart && job?.isActive == true) return
        job?.cancel()
        val gen = ++generation
        job = scope.launch { run(gen) }
    }

    /** Tries again now, rather than waiting out the backoff. */
    fun nudge() {
        wake.trySend(Unit)
    }

    fun stop() {
        generation++
        job?.cancel()
        job = null
    }

    private suspend fun run(gen: Int) {
        var backoff = minBackoff
        fun current() = gen == generation
        while (true) {
            if (current()) connection.value = Connection.Connecting
            try {
                connectOnce {
                    // Reset when the socket actually opened, not when the call
                    // returns: it never returns while connected.
                    backoff = minBackoff
                    if (current()) {
                        connection.value = Connection.Connected
                        error.value = null
                    }
                }
                if (current()) connection.value = Connection.Offline
                return // nothing to connect to
            } catch (e: CancellationException) {
                throw e // replaced or stopped: not a connection problem
            } catch (e: Exception) {
                val reason = e.message ?: e.javaClass.simpleName
                log("connection ended: $reason")
                if (current()) error.value = reason
            } finally {
                if (current() && connection.value != Connection.Offline) {
                    connection.value = Connection.Offline
                }
            }
            // Wake early when the app comes back, rather than sitting out a
            // delay that elapsed while the process was frozen.
            withTimeoutOrNull(backoff) { wake.receive() }
            backoff = (backoff * 2).coerceAtMost(maxBackoff)
        }
    }
}
