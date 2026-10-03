package com.nabu.client.ui

import com.nabu.client.data.SessionRow
import com.nabu.client.protocol.Options
import org.junit.Assert.assertEquals
import org.junit.Test

/** The picker's recent directories (#115). */
class RecentTest {

    private fun card(id: String, workspace: String, at: Long, parent: String = "") =
        SessionCard(SessionRow(id = id, workspace = workspace, updatedAt = at), prompt = "", options = Options(parent = parent))

    @Test
    fun `the most recently used come first, each once`() {
        val cards = listOf(
            card("A", "C:/Users/kyle/projects/nabu", at = 10),
            card("B", "C:/Users/kyle/projects/breezeway", at = 30),
            card("C", "C:/Users/kyle/projects/nabu", at = 20),
        )
        assertEquals(
            listOf("C:/Users/kyle/projects/breezeway", "C:/Users/kyle/projects/nabu"),
            recentWorkspaces(cards),
        )
    }

    // The daemon reports Windows paths with either slash, and the picker
    // speaks forward slashes; one directory must not appear twice.
    @Test
    fun `slash direction, case and a trailing slash do not make a new directory`() {
        val cards = listOf(
            card("A", "C:\\Users\\kyle\\projects\\nabu", at = 30),
            card("B", "c:/users/kyle/projects/nabu/", at = 20),
            card("C", "C:/Users/kyle/projects/nabu", at = 10),
        )
        assertEquals(listOf("C:/Users/kyle/projects/nabu"), recentWorkspaces(cards))
    }

    // Those are worktrees the runner and the watcher make for themselves, not
    // places the reader chose.
    @Test
    fun `run steps and nabu's own worktrees are left out`() {
        val cards = listOf(
            card("STEP", "C:/Users/kyle/projects/nabu", at = 50, parent = "HOME"),
            card("RUN", "C:\\Users\\kyle\\.nabu\\runner\\worktrees\\add-median-x1", at = 40),
            card("REVIEW", "C:/Users/kyle/.nabu/github/worktrees/kyle-breezeway/review-7-abc", at = 30),
            card("EMPTY", "", at = 20),
            card("HOME", "C:/Users/kyle/projects/breezeway", at = 10),
        )
        assertEquals(listOf("C:/Users/kyle/projects/breezeway"), recentWorkspaces(cards))
    }

    @Test
    fun `there are at most six`() {
        val cards = (1..10).map { card("S$it", "/p/project$it", at = it.toLong()) }
        assertEquals((10 downTo 5).map { "/p/project$it" }, recentWorkspaces(cards))
    }
}
