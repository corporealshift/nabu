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

    private fun listing(path: String, repo: Boolean) =
        BrowseResult(path = path, parent = "/a", entries = emptyList(), isRepo = repo)

    // The runner branches a worktree from the directory, so anywhere else a
    // run would only fail at setup, out of sight.
    @Test
    fun `a run starts only in a repository`() {
        val run = BrowseState(purpose = Purpose.Run)

        val repo = run.applied(listing("/a/nabu", repo = true))
        assertTrue(repo.canStartHere)
        assertFalse(repo.needsRepo)

        val plain = run.applied(listing("/a/notes", repo = false))
        assertFalse(plain.canStartHere)
        assertTrue("the picker says why, rather than just having no button", plain.needsRepo)
    }

    @Test
    fun `a session still starts anywhere, and never asks for a repository`() {
        val plain = BrowseState().applied(listing("/a/notes", repo = false))
        assertTrue(plain.canStartHere)
        assertFalse(plain.needsRepo)
    }

    @Test
    fun `the purpose survives moving around the tree`() {
        val state = BrowseState(purpose = Purpose.Run)
            .applied(listing("/a/nabu", repo = true))
            .applied(listing("/a/nabu/src", repo = false))
        assertEquals(Purpose.Run, state.purpose)
        assertFalse("is_repo is the listed directory's, not carried over", state.isRepo)
    }

    @Test
    fun `the top level is never somewhere to run, and says nothing about repositories`() {
        val top = BrowseState(purpose = Purpose.Run).applied(BrowseResult(path = "", parent = null))
        assertFalse(top.canStartHere)
        assertFalse(top.needsRepo)
    }

    @Test
    fun `is_repo is read from the daemon's listing`() {
        val json = """{"path":"/a/nabu","parent":"/a","is_repo":true,"entries":[]}"""
        assertTrue(com.nabu.client.protocol.NabuJson.decodeFromString(BrowseResult.serializer(), json).isRepo)
        val old = """{"path":"/a/nabu","parent":"/a","entries":[]}"""
        assertFalse("a daemon without the field reads as not a repository", com.nabu.client.protocol.NabuJson.decodeFromString(BrowseResult.serializer(), old).isRepo)
    }
}
