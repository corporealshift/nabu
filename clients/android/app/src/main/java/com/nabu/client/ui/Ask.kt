package com.nabu.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.nabu.client.net.AskRequest
import com.nabu.client.ui.markdown.MarkdownText
import com.nabu.client.ui.theme.NabuTheme
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * The answer the daemon reads (spec 7.15). Surrounding space is a keyboard
 * artefact rather than part of the answer.
 */
fun askReply(answer: String) = buildJsonObject {
    put("answer", answer.trim())
}

/** Whether what has been typed is an answer. An empty one unblocks the agent with nothing. */
fun canSend(typed: String) = typed.isNotBlank()

/**
 * Whether the text field is offered. Always: the options a model thought of are
 * rarely the whole truth, and a question with none can only be answered this way.
 */
@Suppress("UNUSED_PARAMETER")
fun offersFreeText(request: AskRequest) = true

/**
 * Adds a question to those waiting, once. The daemon sends a client every
 * question still open when it subscribes, so coming back from the background
 * brings back one already held (issue 109). Questions from different sessions
 * wait their turn rather than replacing each other.
 */
fun withQuestion(waiting: List<AskRequest>, next: AskRequest): List<AskRequest> =
    if (waiting.any { it.requestId == next.requestId }) waiting else waiting + next

/**
 * A question from the agent.
 *
 * It is not dismissible: the agent is blocked until this is answered, and a
 * dialog swiped away by accident would hang the turn until it timed out.
 * Choices answer with one tap; the text field is always there under them.
 */
@Composable
fun AskSheet(
    request: AskRequest,
    onAnswer: (String) -> Unit,
) {
    val c = NabuTheme.colors
    var typed by remember(request.requestId) { mutableStateOf("") }
    val scroll = rememberScrollState()

    AlertDialog(
        onDismissRequest = { /* a question is answered, not dismissed */ },
        containerColor = c.surface,
        titleContentColor = c.ink,
        textContentColor = c.ink,
        title = { Text("The agent is asking") },
        text = {
            Column(
                Modifier.heightIn(max = 420.dp).verticalScroll(scroll),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                // The model writes markdown: a numbered list of options, bold
                // labels. As plain text it read as one block with asterisks in
                // it, which is how a question became hard to answer (issue 70).
                MarkdownText(request.question)

                for (choice in request.choices) {
                    ChoiceRow(choice, onPick = { onAnswer(choice) })
                }

                if (offersFreeText(request)) {
                    OutlinedTextField(
                        value = typed,
                        onValueChange = { typed = it },
                        placeholder = {
                            Text(if (request.choices.isEmpty()) "Answer" else "Or say something else")
                        },
                        maxLines = 4,
                        shape = RoundedCornerShape(12.dp),
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        },
        confirmButton = {
            Button(
                onClick = { onAnswer(typed.trim()) },
                enabled = canSend(typed),
                colors = ButtonDefaults.buttonColors(
                    containerColor = c.accent,
                    contentColor = c.onAccent,
                ),
            ) { Text("Send") }
        },
    )
}

/** One offered answer. The whole row is the target, because a phone is thumbs. */
@Composable
private fun ChoiceRow(choice: String, onPick: () -> Unit) {
    val c = NabuTheme.colors
    Text(
        choice,
        style = MaterialTheme.typography.bodyMedium,
        fontWeight = FontWeight.Medium,
        color = c.ink,
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(10.dp))
            .background(c.code)
            .clickable(onClick = onPick)
            .padding(horizontal = 14.dp, vertical = 12.dp),
    )
}
