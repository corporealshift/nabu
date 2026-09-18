package com.nabu.client.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import com.nabu.client.ui.theme.NabuTheme
import kotlinx.coroutines.delay

/** How long the tick stays up after a copy, before the icon returns. */
private const val COPIED_MILLIS = 1200L

/**
 * Puts [text] on the clipboard when tapped.
 *
 * Deliberately a tap target and not a long-press: long-press belongs to the
 * selection handles, and a control that competed with them would make
 * drag-selecting a phrase a coin toss.
 *
 * Android 13 and later show their own "copied" toast, so the tick here is for
 * the older devices that show nothing and would otherwise leave the reader
 * wondering whether the tap registered.
 */
@Composable
fun CopyButton(text: String, description: String, modifier: Modifier = Modifier) {
    val clipboard = LocalClipboardManager.current
    var copied by remember(text) { mutableStateOf(false) }

    if (copied) {
        LaunchedEffect(text) {
            delay(COPIED_MILLIS)
            copied = false
        }
    }

    IconButton(
        onClick = {
            clipboard.setText(AnnotatedString(text))
            copied = true
        },
        modifier = modifier.size(28.dp),
    ) {
        Icon(
            imageVector = if (copied) Icons.Outlined.Check else Icons.Outlined.ContentCopy,
            contentDescription = if (copied) "Copied" else description,
            tint = if (copied) NabuTheme.colors.accent else NabuTheme.colors.muted,
            modifier = Modifier.size(15.dp),
        )
    }
}

/**
 * A copy control for a line, or nothing when the line has nothing to copy.
 *
 * Kept as its own composable so every call site gets the same answer to "is
 * this copyable", rather than each one deciding for itself.
 */
@Composable
fun LineCopyButton(line: Line, modifier: Modifier = Modifier) {
    val text = line.copyText() ?: return
    CopyButton(text, description = "Copy message", modifier = modifier)
}

/** Reserves the copy control's width so text does not reflow when it appears. */
@Composable
fun CopyButtonSpacer() {
    Box(Modifier.size(28.dp))
}
