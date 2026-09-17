package com.nabu.client.ui

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
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
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.nabu.client.net.PermissionRequest
import kotlinx.serialization.json.put
import kotlinx.serialization.json.buildJsonObject
import com.nabu.client.ui.theme.NabuTheme

/** Risks that may be approved with a tap. Anything unrecognised may not. */
private val TAPPABLE = setOf("low", "medium")

/**
 * The answer the daemon reads (spec 7.15). It takes a verdict, not a boolean:
 * anything else decodes as an empty verdict and silently denies.
 */
fun permissionReply(approve: Boolean) = buildJsonObject {
    put("verdict", if (approve) "approve" else "deny")
}

/** Whether approving takes a deliberate long press rather than a tap. */
fun needsHold(request: PermissionRequest) =
    request.risk.lowercase() !in TAPPABLE

/** Whether the approve control is live yet. */
fun canApprove(request: PermissionRequest, readToEnd: Boolean) =
    !needsHold(request) || readToEnd

/** What the approve control says, which is also what gesture it wants. */
fun approveLabel(request: PermissionRequest, readToEnd: Boolean) = when {
    !needsHold(request) -> "Approve"
    readToEnd -> "Hold to approve"
    else -> "Read it first"
}

/**
 * The permission prompt.
 *
 * Deny is one tap and sits in the confirm slot, under the thumb. Approving a
 * dangerous call needs the summary scrolled to its end and then held, and its
 * control sits at the opposite corner from the composer's send button, so no
 * gesture that just sent a prompt can approve anything.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun PermissionSheet(
    request: PermissionRequest,
    onAnswer: (Boolean) -> Unit,
) {
    val c = NabuTheme.colors
    val scroll = rememberScrollState()
    val readToEnd by remember(request.requestId) {
        derivedStateOf { scroll.value >= scroll.maxValue - 4 }
    }
    val hold = needsHold(request)
    val enabled = canApprove(request, readToEnd)
    var nudged by remember(request.requestId) { mutableStateOf(false) }

    AlertDialog(
        onDismissRequest = { /* a prompt is answered, not dismissed */ },
        containerColor = c.surface,
        titleContentColor = c.ink,
        textContentColor = c.ink,
        title = { Text(if (hold) "Dangerous command" else "Permission needed") },
        text = {
            Column {
                Text(
                    "${request.risk.uppercase()}  ${request.tool}",
                    style = MaterialTheme.typography.labelLarge,
                    color = if (hold) c.danger else c.muted,
                )
                Spacer(Modifier.padding(4.dp))
                Column(
                    Modifier
                        .heightIn(max = 260.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(c.code)
                        .verticalScroll(scroll)
                        .padding(10.dp),
                ) {
                    Text(
                        request.summary,
                        style = MaterialTheme.typography.bodyMedium,
                        fontFamily = FontFamily.Monospace,
                        color = c.codeInk,
                    )
                }
                if (hold) {
                    Spacer(Modifier.padding(4.dp))
                    Text(
                        if (!readToEnd) "Scroll to the end before you can approve."
                        else if (nudged) "Press and hold — a tap will not do it."
                        else "Press and hold to approve.",
                        style = MaterialTheme.typography.labelMedium,
                        color = c.danger,
                    )
                }
            }
        },
        // Deny occupies the confirm slot: the safe answer should be the one
        // under the thumb, and the dangerous one nowhere near it.
        confirmButton = {
            Button(
                onClick = { onAnswer(false) },
                colors = ButtonDefaults.buttonColors(
                    containerColor = c.accent,
                    contentColor = c.onAccent,
                ),
                modifier = Modifier.fillMaxWidth(),
            ) { Text("Deny") }
        },
        dismissButton = {
            Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.CenterStart) {
                ApproveControl(
                    label = approveLabel(request, readToEnd),
                    enabled = enabled,
                    hold = hold,
                    onApprove = { onAnswer(true) },
                    onTapped = { nudged = true },
                )
            }
        },
    )
}

/**
 * Approve. A tap when the gesture is a hold does not approve; it says so,
 * because a control that silently ignores you reads as broken.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ApproveControl(
    label: String,
    enabled: Boolean,
    hold: Boolean,
    onApprove: () -> Unit,
    onTapped: () -> Unit,
) {
    val c = NabuTheme.colors
    Box(
        Modifier
            .clip(RoundedCornerShape(10.dp))
            .border(1.dp, if (enabled) c.danger else c.line, RoundedCornerShape(10.dp))
            .combinedClickable(
                enabled = enabled,
                onClick = { if (hold) onTapped() else onApprove() },
                onLongClick = { if (hold) onApprove() },
            )
            .padding(horizontal = 16.dp, vertical = 10.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            label,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.Medium,
            color = if (enabled) c.danger else c.muted,
        )
    }
}
