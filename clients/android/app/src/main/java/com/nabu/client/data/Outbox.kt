package com.nabu.client.data

import com.nabu.client.net.DaemonClient
import com.nabu.client.settings.Settings

/** What a drain attempt found, and therefore whether to try again. */
enum class Drain { Nothing, Sent, Failed }

/**
 * Sends whatever is waiting, over a connection opened for the purpose.
 *
 * This is the path taken when connectivity returns with the app closed, so it
 * owns the socket it opens and closes it again. [connect] is a parameter so the
 * caller decides what a connection is; the worker passes the real one.
 */
suspend fun drainOutbox(
    repo: SessionRepository,
    settings: Settings,
    connect: suspend (Settings) -> DaemonClient,
): Drain {
    // Nowhere to send it is not a failure to retry: it is a daemon not set up.
    if (settings.host.isBlank() || settings.token.isBlank()) return Drain.Nothing
    if (repo.pendingCount() == 0) return Drain.Nothing

    var client: DaemonClient? = null
    return try {
        val c = connect(settings)
        client = c
        repo.flushOutbox(c)
        if (repo.pendingCount() == 0) Drain.Sent else Drain.Failed
    } catch (e: Exception) {
        // Usually connect() failing. Swallowing it left the queue showing a
        // count and no reason, which is the state hardest to diagnose from
        // the outside, so it goes on the rows.
        repo.noteOutboxError(e.message ?: "could not reach the daemon")
        Drain.Failed
    } finally {
        client?.close()
    }
}
