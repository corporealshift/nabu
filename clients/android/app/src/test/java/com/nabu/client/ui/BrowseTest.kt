package com.nabu.client.ui

import com.nabu.client.net.BrowseEntry
import com.nabu.client.net.BrowseResult
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class BrowseTest {

    private fun entry(name: String, path: String, repo: Boolean = false) =
        BrowseEntry(name = name, path = path, isRepo = repo)

    @Test
    fun `a listing becomes the picker's state`() {
        val state = BrowseState().applied(
            BrowseResult(
                path = "C:/Users/kyle/projects",
                parent = "C:/Users/kyle",
                entries = listOf(entry("nabu", "C:/Users/kyle/projects/nabu", repo = true)),
            )
        )

        assertEquals("C:/Users/kyle/projects", state.at)
        assertEquals("C:/Users/kyle", state.parent)
        assertEquals(1, state.entries.size)
        assertTrue(state.entries[0].isRepo)
        assertFalse("a loaded listing is not still loading", state.loading)
    }

    // A failed listing is cleared by the next one that works, or the reader is
    // left staring at an error about a directory they have already left.
    @Test
    fun `a new listing clears an earlier error`() {
        val failed = BrowseState(error = "no such directory", loading = true)
        val state = failed.applied(BrowseResult(path = "/a", entries = emptyList()))

        assertNull(state.error)
        assertFalse(state.loading)
    }

    // At the top the entries are roots, not the contents of a directory, so
    // there is no "here" to start in.
    @Test
    fun `the top level is not somewhere a session can start`() {
        val top = BrowseState().applied(
            BrowseResult(path = "", parent = null, entries = listOf(entry("/a", "/a")))
        )

        assertTrue(top.atTop)
        assertFalse(top.canStartHere)
        assertNull("there is nothing above the roots", top.parent)
    }

    @Test
    fun `a directory below a root can be started in`() {
        val state = BrowseState().applied(
            BrowseResult(path = "/a/b", parent = "/a", entries = emptyList())
        )

        assertFalse(state.atTop)
        assertTrue(state.canStartHere)
    }

    // A directory that is not a repository is still a valid workspace: the
    // daemon allows it, and occasionally it is what the reader wants.
    @Test
    fun `a plain directory can still be started in`() {
        val state = BrowseState().applied(
            BrowseResult(path = "/a/notes", parent = "/a", entries = emptyList())
        )
        assertTrue(state.canStartHere)
    }

    @Test
    fun `starting is refused while loading or after an error`() {
        val base = BrowseState().applied(BrowseResult(path = "/a/b", parent = "/a"))

        assertFalse(base.copy(loading = true).canStartHere)
        assertFalse(base.copy(error = "boom").canStartHere)
    }

    // The full path does not fit on a phone and the last segment alone is
    // ambiguous when every project has a src.
    @Test
    fun `crumbs shorten a long path from the tail`() {
        assertEquals(
            "…/kyle/projects/nabu",
            crumbs("C:/Users/kyle/projects/nabu"),
        )
        assertEquals("a/b", crumbs("a/b"))
        assertEquals("a/b/c", crumbs("a/b/c"))
        assertEquals("Places", crumbs(""))
    }

    @Test
    fun `crumbs ignores a trailing slash`() {
        assertEquals("a/b", crumbs("a/b/"))
    }

    // A root has no parent on screen to give a bare name meaning, so it is
    // shown by enough of its path to be recognised.
    @Test
    fun `roots are labelled by path and children by name`() {
        val e = entry("projects", "C:/Users/kyle/projects")

        assertEquals("…/kyle/projects", entryLabel(e, atTop = true))
        assertEquals("projects", entryLabel(e, atTop = false))
    }
}
