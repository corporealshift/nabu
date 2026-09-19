package com.nabu.client.ui

import com.nabu.client.net.BrowseEntry
import com.nabu.client.net.BrowseResult

/**
 * What the directory picker is showing.
 *
 * The daemon owns the tree, so this holds only what the screen needs to draw
 * itself. `at` is empty at the top, where the entries are the configured roots
 * rather than the contents of anything.
 */
data class BrowseState(
    val at: String = "",
    val parent: String? = null,
    val entries: List<BrowseEntry> = emptyList(),
    val loading: Boolean = false,
    val error: String? = null,
) {
    /** True at the top level, where the entries are roots and there is no up. */
    val atTop: Boolean get() = at.isEmpty()

    /**
     * Whether a session can start here.
     *
     * Not at the top: the entries there are roots, and "here" is not a
     * directory at all. Everywhere else it is allowed, including a directory
     * that is not a repository, because the daemon allows it and occasionally
     * that is what the reader wants.
     */
    val canStartHere: Boolean get() = !atTop && error == null && !loading
}

/** Folds a daemon listing into the picker's state. */
fun BrowseState.applied(result: BrowseResult): BrowseState = copy(
    at = result.path,
    parent = result.parent,
    entries = result.entries,
    loading = false,
    error = null,
)

/**
 * What to show as the current location.
 *
 * The full path is unreadable on a phone and the last segment alone is
 * ambiguous when every project has a src. The tail is the compromise: enough to
 * know where you are, short enough to fit.
 */
fun crumbs(path: String, keep: Int = 3): String {
    if (path.isEmpty()) return "Places"
    val parts = path.trimEnd('/').split('/').filter { it.isNotEmpty() }
    if (parts.size <= keep) return parts.joinToString("/")
    return "…/" + parts.takeLast(keep).joinToString("/")
}

/**
 * The name to show for an entry.
 *
 * A root is listed by its whole path, because at the top level there is no
 * parent to give a bare name any meaning.
 */
fun entryLabel(entry: BrowseEntry, atTop: Boolean): String =
    if (atTop) crumbs(entry.path, keep = 2) else entry.name
