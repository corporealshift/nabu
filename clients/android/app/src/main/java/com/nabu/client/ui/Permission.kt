package com.nabu.client.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.nabu.client.net.PermissionRequest

/** Risk levels that get the harder treatment. */
private fun PermissionRequest.isHighRisk() = risk.equals("high", ignoreCase = true)

/**
 * The permission prompt.
 *
 * Spec 15 asks for more friction than the TUI's single keystroke. Deny is
 * always one tap; approving a high-risk call needs the summary scrolled to its
 * end first, so a pocket tap cannot run something destructive.
 */
@Composable
fun PermissionSheet(
    request: PermissionRequest,
    onAnswer: (Boolean) -> Unit,
) {
    val scroll = rememberScrollState()
    val readToEnd by remember(request.requestId) {
        derivedStateOf { scroll.value >= scroll.maxValue - 4 }
    }
    val canApprove = !request.isHighRisk() || readToEnd

    AlertDialog(
        onDismissRequest = { /* a prompt is answered, not dismissed */ },
        title = { Text(if (request.isHighRisk()) "Dangerous command" else "Permission needed") },
        text = {
            Column {
                Text(
                    "${request.risk.uppercase()}  ${request.tool}",
                    style = MaterialTheme.typography.labelLarge,
                    color = if (request.isHighRisk()) MaterialTheme.colorScheme.error
                    else MaterialTheme.colorScheme.onSurface,
                )
                Spacer(Modifier.padding(4.dp))
                Column(
                    Modifier.heightIn(max = 260.dp).verticalScroll(scroll),
                ) {
                    Text(
                        request.summary,
                        style = MaterialTheme.typography.bodyMedium,
                        fontFamily = FontFamily.Monospace,
                    )
                }
                if (request.isHighRisk() && !readToEnd) {
                    Spacer(Modifier.padding(4.dp))
                    Text(
                        "Scroll to the end to enable Approve.",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        // Deny occupies the confirm slot: the safe answer should be the one
        // under the thumb.
        confirmButton = {
            Button(
                onClick = { onAnswer(false) },
                modifier = Modifier.fillMaxWidth(),
            ) { Text("Deny") }
        },
        dismissButton = {
            Row(horizontalArrangement = Arrangement.Start, modifier = Modifier.fillMaxWidth()) {
                TextButton(
                    onClick = { onAnswer(true) },
                    enabled = canApprove,
                    colors = ButtonDefaults.textButtonColors(
                        contentColor = MaterialTheme.colorScheme.error,
                    ),
                ) { Text(if (canApprove) "Approve" else "Approve (read it first)") }
            }
        },
    )
}
