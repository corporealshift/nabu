package com.nabu.client.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color

/** A palette the owner can choose. */
enum class Scheme(val label: String, val blurb: String) {
    Verdigris("Verdigris", "Bronze that has been in the ground"),
    Dusk("Dusk", "A ziggurat at last light"),
}

/** Light, dark, or whatever the phone is doing. */
enum class Mode(val label: String) { System("System"), Light("Light"), Dark("Dark") }

/**
 * The colours the app actually uses. Material's scheme has no notion of a tool
 * output block or a veto, so these are named for the thing rather than a slot.
 */
data class NabuColors(
    val background: Color,
    val surface: Color,
    val mine: Color,
    val ink: Color,
    val muted: Color,
    val line: Color,
    val accent: Color,
    val onAccent: Color,
    val danger: Color,
    val code: Color,
    val codeInk: Color,
    val markStroke: Color,
    val markShadow: Color,
    val dark: Boolean,
)

private val VerdigrisDark = NabuColors(
    background = Color(0xFF131A16),
    surface = Color(0xFF1F2A23),
    mine = Color(0xFF28352C),
    ink = Color(0xFFDCE5DC),
    muted = Color(0xFF849689),
    line = Color(0xFF2A382F),
    accent = Color(0xFF5EA88C),
    onAccent = Color(0xFF0D120F),
    danger = Color(0xFFE0917A),
    code = Color(0xFF0D120F),
    codeInk = Color(0xFF93A497),
    markStroke = Color(0xFFDCE5DC),
    markShadow = Color(0xFF5EA88C),
    dark = true,
)

private val VerdigrisLight = NabuColors(
    background = Color(0xFFF3F5F1),
    surface = Color(0xFFE6EBE4),
    mine = Color(0xFFDCE4D9),
    ink = Color(0xFF18201B),
    muted = Color(0xFF5F6B62),
    line = Color(0xFFD5DDD2),
    accent = Color(0xFF2F7A62),
    onAccent = Color(0xFFF3F5F1),
    danger = Color(0xFFA8442A),
    code = Color(0xFFE0E7DE),
    codeInk = Color(0xFF38423A),
    markStroke = Color(0xFF18201B),
    markShadow = Color(0xFF2F7A62),
    dark = false,
)

private val DuskDark = NabuColors(
    background = Color(0xFF1A1826),
    surface = Color(0xFF272438),
    mine = Color(0xFF312D45),
    ink = Color(0xFFE8DFE2),
    muted = Color(0xFF8A82A0),
    line = Color(0xFF332F48),
    accent = Color(0xFFD5654A),
    onAccent = Color(0xFF1A1826),
    danger = Color(0xFFE2806B),
    code = Color(0xFF131120),
    codeInk = Color(0xFF9E97B2),
    markStroke = Color(0xFFE8DFE2),
    markShadow = Color(0xFFD5654A),
    dark = true,
)

private val DuskLight = NabuColors(
    background = Color(0xFFF6F4F5),
    surface = Color(0xFFEAE6EA),
    mine = Color(0xFFE1DCE3),
    ink = Color(0xFF221F2A),
    muted = Color(0xFF6B6678),
    line = Color(0xFFDCD7DE),
    accent = Color(0xFFB0432B),
    onAccent = Color(0xFFF6F4F5),
    danger = Color(0xFF9E3A22),
    code = Color(0xFFE4DFE5),
    codeInk = Color(0xFF3C3747),
    markStroke = Color(0xFF221F2A),
    markShadow = Color(0xFFB0432B),
    dark = false,
)

/** The four palettes, so a picker can show one without rendering a screen. */
fun paletteFor(scheme: Scheme, dark: Boolean): NabuColors = when {
    scheme == Scheme.Verdigris && dark -> VerdigrisDark
    scheme == Scheme.Verdigris -> VerdigrisLight
    dark -> DuskDark
    else -> DuskLight
}

private val LocalNabuColors = staticCompositionLocalOf { VerdigrisDark }

object NabuTheme {
    val colors: NabuColors
        @Composable @ReadOnlyComposable get() = LocalNabuColors.current
}

@Composable
fun NabuTheme(
    scheme: Scheme,
    dark: Boolean,
    content: @Composable () -> Unit,
) {
    val c = paletteFor(scheme, dark)

    // Material's own components still need a scheme; every slot a component
    // might reach for is mapped, or it falls back to the baseline purple.
    val material = if (c.dark) {
        darkColorScheme(
            primary = c.accent, onPrimary = c.onAccent,
            background = c.background, onBackground = c.ink,
            surface = c.surface, onSurface = c.ink,
            surfaceVariant = c.mine, onSurfaceVariant = c.muted,
            surfaceContainerLowest = c.background, surfaceContainerLow = c.background,
            surfaceContainer = c.surface, surfaceContainerHigh = c.surface,
            surfaceContainerHighest = c.surface,
            primaryContainer = c.mine, onPrimaryContainer = c.ink,
            error = c.danger, onError = c.onAccent,
            errorContainer = c.surface, onErrorContainer = c.danger,
            outline = c.line, outlineVariant = c.line,
        )
    } else {
        lightColorScheme(
            primary = c.accent, onPrimary = c.onAccent,
            background = c.background, onBackground = c.ink,
            surface = c.surface, onSurface = c.ink,
            surfaceVariant = c.mine, onSurfaceVariant = c.muted,
            surfaceContainerLowest = c.background, surfaceContainerLow = c.background,
            surfaceContainer = c.surface, surfaceContainerHigh = c.surface,
            surfaceContainerHighest = c.surface,
            primaryContainer = c.mine, onPrimaryContainer = c.ink,
            error = c.danger, onError = c.onAccent,
            errorContainer = c.surface, onErrorContainer = c.danger,
            outline = c.line, outlineVariant = c.line,
        )
    }

    CompositionLocalProvider(LocalNabuColors provides c) {
        MaterialTheme(colorScheme = material, content = content)
    }
}
