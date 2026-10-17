package com.rho.client.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.rho.client.ui.theme.RhoTheme
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * The sessions being fetched right now, so a screen can say it is loading
 * rather than look empty (issue 179). One session can be fetched twice at
 * once, by the catch-up and by opening it, so each is counted.
 */
class InFlight {
    private val counts = HashMap<String, Int>()
    private val _ids = MutableStateFlow<Set<String>>(emptySet())
    val ids: StateFlow<Set<String>> = _ids.asStateFlow()

    suspend fun <T> track(id: String, block: suspend () -> T): T {
        bump(id, 1)
        try {
            return block()
        } finally {
            bump(id, -1)
        }
    }

    private fun bump(id: String, by: Int) = synchronized(counts) {
        val n = (counts[id] ?: 0) + by
        if (n <= 0) counts.remove(id) else counts[id] = n
        _ids.value = counts.keys.toSet()
    }
}

/** Whether the session list is still being fetched: connecting, or catching up once connected. */
fun listLoading(connection: Connection, catchingUp: Boolean): Boolean =
    connection == Connection.Connecting || (connection == Connection.Connected && catchingUp)

/** What an empty session list says, which is not "no sessions" until it has asked. */
fun emptyListText(connection: Connection, catchingUp: Boolean): String = when {
    connection == Connection.Connecting -> "Connecting to the daemon…"
    connection == Connection.Connected && catchingUp -> "Loading sessions…"
    connection == Connection.Connected -> "No sessions yet."
    else -> "Nothing mirrored yet. Connect to fetch sessions."
}

/** A card's note on its mirror, or null when it is whole. Spec 4 obliges disclosing a partial one. */
fun syncLabel(synced: Boolean, downloading: Boolean): String? = when {
    synced -> null
    downloading -> "downloading…"
    else -> "partially synced"
}

/** The gap card's heading and line: whether anything is mirrored, and whether it is on its way. */
fun gapText(neverFetched: Boolean, downloading: Boolean): Pair<String, String> = when {
    neverFetched && downloading -> "Downloading" to "Fetching this session from the daemon."
    neverFetched -> "Not downloaded" to "This session exists on the daemon but nothing has been fetched yet."
    downloading -> "Downloading earlier events" to "This device is catching up. What follows is only the most recent part."
    else -> "Earlier events not downloaded" to "This device is behind. What follows is only the most recent part."
}

/** A thin bar under a top bar. Its room is kept when idle, so the page does not jump. */
@Composable
fun LoadingBar(loading: Boolean) {
    Box(Modifier.fillMaxWidth().height(2.dp)) {
        if (loading) {
            LinearProgressIndicator(
                modifier = Modifier.fillMaxWidth().height(2.dp),
                color = RhoTheme.colors.accent,
                trackColor = RhoTheme.colors.surface,
            )
        }
    }
}
