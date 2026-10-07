package com.nabu.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nabu.client.protocol.ToolCall
import com.nabu.client.ui.theme.NabuTheme
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/** One tool's calls (spec 7.26): across sessions over a period, or one session's. */
data class ToolCallsState(
    val tool: String = "",
    /** Set when the calls are one session's, whatever the period and kind. */
    val sessionId: String? = null,
    val days: Int = 7,
    val kind: String = "all",
    val loading: Boolean = false,
    val calls: List<ToolCall> = emptyList(),
    val truncated: Boolean = false,
    val error: String? = null,
)

/**
 * The arguments that say what a call was for, in the order they are looked
 * for: a search's query, a fetch's url, a question to Claude, a file, a
 * command. A call with none of them is shown as its JSON.
 */
private val tellingArguments = listOf("query", "url", "question", "prompt", "path", "command", "pattern")

/** A call's arguments on one line: `createFromFile copies` rather than `{"query":"createFromFile copies"}`. */
fun argumentsInShort(args: JsonElement?, max: Int = 160): String {
    val telling = (args as? JsonObject)?.let { obj ->
        tellingArguments.firstNotNullOfOrNull { key -> (obj[key] as? JsonPrimitive)?.takeIf { it.isString }?.content }
    }
    val text = telling ?: args?.toString() ?: ""
    return text.replace(Regex("\\s+"), " ").trim().let { if (it.length > max) it.take(max - 1) + "…" else it }
}

private val pretty = Json { prettyPrint = true }

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ToolCallsScreen(state: ToolCallsState, onOpenSession: (String) -> Unit, onBack: () -> Unit) {
    val c = NabuTheme.colors
    var open by remember(state.calls) { mutableStateOf<ToolCall?>(null) }
    Scaffold(containerColor = c.background, topBar = {
        TopAppBar(
            colors = TopAppBarDefaults.topAppBarColors(containerColor = c.background, titleContentColor = c.ink),
            title = { Text(state.tool, style = MaterialTheme.typography.titleSmall) },
            // Back closes a call before it leaves the list.
            navigationIcon = { TextButton(onClick = { if (open != null) open = null else onBack() }) { Text("Back") } },
        )
    }) { padding ->
        val call = open
        when {
            call != null -> CallDetail(call, Modifier.padding(padding), onOpenSession)
            state.calls.isEmpty() -> Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text(
                    state.error ?: if (state.loading) "Loading…" else "No calls ${scopeOf(state)}.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = c.muted,
                )
            }
            else -> LazyColumn(Modifier.fillMaxSize().padding(padding)) {
                item {
                    Text(
                        "${state.calls.size}${if (state.truncated) "+" else ""} calls ${scopeOf(state)}, newest first.",
                        style = MaterialTheme.typography.bodySmall,
                        color = c.muted,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
                    )
                }
                items(state.calls) { CallRow(it, showSession = state.sessionId == null) { open = it } }
                if (state.truncated) {
                    item {
                        Text(
                            "Older calls are not shown.",
                            style = MaterialTheme.typography.bodySmall,
                            color = c.muted,
                            modifier = Modifier.padding(16.dp),
                        )
                    }
                }
            }
        }
    }
}

private fun scopeOf(s: ToolCallsState): String = when {
    s.sessionId != null -> "in this session"
    else -> "in ${kindWords(s.kind)} over ${periodWords(s.days)}"
}

@Composable
private fun CallRow(call: ToolCall, showSession: Boolean, onClick: () -> Unit) {
    val c = NabuTheme.colors
    Column(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                argumentsInShort(call.arguments),
                style = MaterialTheme.typography.bodyMedium,
                color = c.ink,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
            StatusMark(call)
        }
        Text(
            listOfNotNull(
                formatMessageTime(call.at).ifBlank { null },
                call.label.ifBlank { null }?.takeIf { showSession },
            ).joinToString("  ·  "),
            style = MaterialTheme.typography.labelSmall,
            color = c.muted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (call.result.isNotBlank()) {
            Text(
                call.result.replace(Regex("\\s+"), " ").trim(),
                style = MaterialTheme.typography.bodySmall,
                color = c.muted,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
    HorizontalDivider(color = c.line)
}

@Composable
private fun StatusMark(call: ToolCall) {
    val c = NabuTheme.colors
    val (text, color) = when (call.status) {
        "ok" -> return
        "pending" -> "pending" to c.muted
        else -> (call.kind.ifBlank { call.status }) to c.danger
    }
    Text(text, style = MaterialTheme.typography.labelSmall, color = color, modifier = Modifier.padding(start = 8.dp))
}

/** A call opened in full: what was asked, what came back, and whose it was. */
@Composable
private fun CallDetail(call: ToolCall, modifier: Modifier, onOpenSession: (String) -> Unit) {
    val c = NabuTheme.colors
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            listOfNotNull(formatMessageTime(call.at).ifBlank { null }, call.label.ifBlank { null }).joinToString("  ·  "),
            style = MaterialTheme.typography.bodySmall,
            color = c.muted,
        )
        Field("Asked", call.arguments?.let { pretty.encodeToString(JsonElement.serializer(), it) } ?: "")
        Field(
            when (call.status) {
                "ok" -> "Came back"
                "pending" -> "No answer yet"
                else -> "Failed" + if (call.kind.isNotBlank()) " (${call.kind})" else ""
            },
            call.result,
            note = if (call.result.length >= 400) "The first 400 characters. The session has the rest." else null,
        )
        if (call.archived) {
            Text(
                "This session is archived. Restore it from Archived to open it.",
                style = MaterialTheme.typography.bodySmall,
                color = c.muted,
            )
        } else {
            Button(
                onClick = { onOpenSession(call.sessionId) },
                colors = ButtonDefaults.buttonColors(containerColor = c.accent, contentColor = c.onAccent),
            ) { Text("Open session") }
        }
    }
}

@Composable
private fun Field(title: String, body: String, note: String? = null) {
    val c = NabuTheme.colors
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(title, style = MaterialTheme.typography.labelMedium, color = c.muted)
        if (body.isNotBlank()) {
            SelectionContainer {
                Text(
                    body,
                    style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                    color = c.codeInk,
                    modifier = Modifier.fillMaxWidth().background(c.code, RoundedCornerShape(8.dp)).padding(10.dp),
                )
            }
        }
        note?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = c.muted) }
    }
}
