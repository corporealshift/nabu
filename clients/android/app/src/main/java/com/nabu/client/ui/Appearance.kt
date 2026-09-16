package com.nabu.client.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.nabu.client.ui.theme.Mode
import com.nabu.client.ui.theme.NabuTheme
import com.nabu.client.ui.theme.Scheme
import com.nabu.client.ui.theme.Wedge
import com.nabu.client.ui.theme.paletteFor

/**
 * Picking a palette.
 *
 * Each option shows its own colours rather than the ones currently in use, so
 * the choice is made by looking rather than by reading two names.
 */
@Composable
fun AppearanceSection(
    scheme: Scheme,
    mode: Mode,
    systemDark: Boolean,
    onChange: (Scheme, Mode) -> Unit,
) {
    val c = NabuTheme.colors

    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(
            "Appearance",
            color = c.ink,
            fontWeight = FontWeight.SemiBold,
        )

        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Scheme.entries.forEach { option ->
                SchemeCard(
                    scheme = option,
                    dark = when (mode) {
                        Mode.System -> systemDark
                        Mode.Light -> false
                        Mode.Dark -> true
                    },
                    selected = option == scheme,
                    modifier = Modifier.weight(1f),
                    onPick = { onChange(option, mode) },
                )
            }
        }

        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.fillMaxWidth(),
        ) {
            Mode.entries.forEach { option ->
                ModeChip(
                    label = option.label,
                    selected = option == mode,
                    modifier = Modifier.weight(1f),
                    onPick = { onChange(scheme, option) },
                )
            }
        }
    }
}

@Composable
private fun SchemeCard(
    scheme: Scheme,
    dark: Boolean,
    selected: Boolean,
    modifier: Modifier = Modifier,
    onPick: () -> Unit,
) {
    val c = NabuTheme.colors
    val preview = paletteFor(scheme, dark)

    Column(
        modifier = modifier
            .clip(RoundedCornerShape(12.dp))
            .background(preview.background)
            .border(
                width = if (selected) 2.dp else 1.dp,
                color = if (selected) c.accent else c.line,
                shape = RoundedCornerShape(12.dp),
            )
            .clickable(onClick = onPick)
            .padding(14.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(
            horizontalArrangement = Arrangement.spacedBy(10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Wedge(
                stroke = preview.markStroke,
                shadow = preview.markShadow,
                modifier = Modifier.size(30.dp),
            )
            Text(
                scheme.label,
                color = preview.ink,
                fontWeight = FontWeight.SemiBold,
            )
        }

        // The palette itself, in the order the app spends it.
        Row(
            horizontalArrangement = Arrangement.spacedBy(4.dp),
            modifier = Modifier.fillMaxWidth(),
        ) {
            listOf(preview.surface, preview.accent, preview.danger, preview.muted)
                .forEach { swatch ->
                    Box(
                        Modifier
                            .weight(1f)
                            .height(16.dp)
                            .clip(RoundedCornerShape(3.dp))
                            .background(swatch)
                    )
                }
        }

        Text(
            scheme.blurb,
            color = preview.muted,
            style = androidx.compose.material3.MaterialTheme.typography.labelSmall,
        )
    }
}

@Composable
private fun ModeChip(
    label: String,
    selected: Boolean,
    modifier: Modifier = Modifier,
    onPick: () -> Unit,
) {
    val c = NabuTheme.colors
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(8.dp))
            .background(if (selected) c.accent else c.surface)
            .border(1.dp, if (selected) c.accent else c.line, RoundedCornerShape(8.dp))
            .clickable(onClick = onPick)
            .padding(vertical = 9.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            label,
            color = if (selected) c.onAccent else c.muted,
            style = androidx.compose.material3.MaterialTheme.typography.labelLarge,
        )
    }
}
