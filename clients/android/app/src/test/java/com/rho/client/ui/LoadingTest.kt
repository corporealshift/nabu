package com.rho.client.ui

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class LoadingTest {

    @Test
    fun `a session is in flight while it is fetched, and not after`() = runTest {
        val inFlight = InFlight()
        val release = CompletableDeferred<Unit>()
        val fetch = async { inFlight.track("s1") { release.await() } }
        yield()
        assertEquals(setOf("s1"), inFlight.ids.value)
        release.complete(Unit)
        fetch.await()
        assertEquals(emptySet<String>(), inFlight.ids.value)
    }

    // Opening a session fetches it while the catch-up may be fetching it too.
    @Test
    fun `a session fetched twice at once stays in flight until both end`() = runTest {
        val inFlight = InFlight()
        val first = CompletableDeferred<Unit>()
        val second = CompletableDeferred<Unit>()
        val a = async { inFlight.track("s1") { first.await() } }
        val b = async { inFlight.track("s1") { second.await() } }
        yield()
        first.complete(Unit)
        a.await()
        assertEquals(setOf("s1"), inFlight.ids.value)
        second.complete(Unit)
        b.await()
        assertEquals(emptySet<String>(), inFlight.ids.value)
    }

    @Test
    fun `a failed fetch is no longer in flight`() = runTest {
        val inFlight = InFlight()
        runCatching { inFlight.track("s1") { error("gone") } }
        assertEquals(emptySet<String>(), inFlight.ids.value)
    }

    @Test
    fun `the list loads while connecting and catching up, not offline or settled`() {
        assertTrue(listLoading(Connection.Connecting, catchingUp = false))
        assertTrue(listLoading(Connection.Connected, catchingUp = true))
        assertFalse(listLoading(Connection.Connected, catchingUp = false))
        assertFalse(listLoading(Connection.Offline, catchingUp = true))
    }

    @Test
    fun `an empty list says no sessions only once it has asked`() {
        assertEquals("Connecting to the daemon…", emptyListText(Connection.Connecting, catchingUp = false))
        assertEquals("Loading sessions…", emptyListText(Connection.Connected, catchingUp = true))
        assertEquals("No sessions yet.", emptyListText(Connection.Connected, catchingUp = false))
        assertEquals("Nothing mirrored yet. Connect to fetch sessions.", emptyListText(Connection.Offline, catchingUp = false))
    }

    @Test
    fun `a card says downloading while fetched, partially synced otherwise`() {
        assertNull(syncLabel(synced = true, downloading = true))
        assertEquals("downloading…", syncLabel(synced = false, downloading = true))
        assertEquals("partially synced", syncLabel(synced = false, downloading = false))
    }

    @Test
    fun `the gap says downloading while the daemon is asked`() {
        assertEquals("Downloading", gapText(neverFetched = true, downloading = true).first)
        assertEquals("Not downloaded", gapText(neverFetched = true, downloading = false).first)
        assertEquals("Downloading earlier events", gapText(neverFetched = false, downloading = true).first)
        assertEquals("Earlier events not downloaded", gapText(neverFetched = false, downloading = false).first)
    }

    @Test
    fun `the gap carries whether it is downloading`() {
        val gap = withGap(emptyList(), synced = false, fetched = false, downloading = true).single() as Line.Gap
        assertTrue(gap.downloading)
        assertTrue(gap.neverFetched)
    }

    // Until the mirror is read, an empty view is not an empty session.
    @Test
    fun `a view is loaded only once the feed has read the mirror`() {
        assertFalse(TranscriptView().loaded)
        assertTrue(TranscriptFeed().view().loaded)
    }
}
