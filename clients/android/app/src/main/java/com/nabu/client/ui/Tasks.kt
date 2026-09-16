package com.nabu.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nabu.client.data.EventRow
import com.nabu.client.protocol.Task
import com.nabu.client.protocol.project
import com.nabu.client.ui.theme.NabuTheme

/** Statuses that are finished, however they finished. */
private val CLOSED = setOf("done", "cancelled")

/** The session's tasks, from the same fold the daemon uses (spec 5). */
fun tasksOf(rows: List<EventRow>): List<Task> = project(decode(rows)).tasks

/** The whole snapshot back, with one task closed: `update_tasks` takes a list. */
fun withDone(tasks: List<Task>, id: String): List<Task> =
    tasks.map { if (it.id == id) it.copy(status = "done") else it }

/** Taps not yet confirmed by a snapshot, so a tap shows immediately. */
fun overlay(tasks: List<Task>, done: Set<String>): List<Task> =
    if (done.isEmpty()) tasks else withClosed(tasks, done)

private fun withClosed(tasks: List<Task>, done: Set<String>) =
    tasks.map { if (it.id in done && it.status !in CLOSED) it.copy(status = "done") else it }

/**
 * The task card. Tapping a task completes it, because typing a status on a
 * phone is not something anyone does twice.
 */
@Composable
fun TaskCard(tasks: List<Task>, onDone: (String) -> Unit) {
    if (tasks.isEmpty()) return
    val c = NabuTheme.colors
    val closed = tasks.count { it.status in CLOSED }

    Column(
        Modifier
            .fillMaxWidth()
            .background(c.surface, RoundedCornerShape(12.dp))
            .padding(12.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        Text(
            "Tasks  $closed/${tasks.size}",
            style = MaterialTheme.typography.labelMedium,
            color = c.muted,
            fontWeight = FontWeight.SemiBold,
            modifier = Modifier.padding(bottom = 4.dp),
        )
        Column(
            Modifier.heightIn(max = 180.dp).verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            tasks.forEach { TaskRow(it, onDone) }
        }
    }
}

@Composable
private fun TaskRow(task: Task, onDone: (String) -> Unit) {
    val c = NabuTheme.colors
    val finished = task.status in CLOSED

    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .clickable(enabled = !finished) { onDone(task.id) }
            .padding(vertical = 7.dp, horizontal = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier
                .size(16.dp)
                .clip(CircleShape)
                .background(if (finished) c.accent else Color.Transparent),
            contentAlignment = Alignment.Center,
        ) {
            if (!finished) {
                Box(
                    Modifier
                        .size(16.dp)
                        .clip(CircleShape)
                        .background(c.line)
                )
                Box(
                    Modifier
                        .size(12.dp)
                        .clip(CircleShape)
                        .background(c.surface)
                )
            }
        }
        Text(
            task.title,
            style = MaterialTheme.typography.bodySmall,
            color = if (finished) c.muted else c.ink,
            textDecoration = if (finished) TextDecoration.LineThrough else null,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (task.status == "in_progress" || task.status == "blocked" || task.status == "failed") {
            Text(
                task.status.replace('_', ' '),
                style = MaterialTheme.typography.labelSmall,
                color = if (task.status == "in_progress") c.accent else c.danger,
            )
        }
    }
}
