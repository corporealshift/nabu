package com.nabu.client.ui.theme

import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path

/**
 * The cuneiform wedge: a stroke pressed with a reed and lifted, which gives it
 * a head and a tail. It points, so it serves as the send button as well as the
 * app's mark.
 *
 * Drawn as geometry rather than a vector asset so it can take any two colours
 * the current palette hands it.
 */
@Composable
fun Wedge(
    stroke: Color,
    shadow: Color,
    modifier: Modifier = Modifier,
) {
    Canvas(modifier) {
        val w = size.width
        val h = size.height

        // Proportions from the 64-unit grid the mark was designed on.
        fun p(x: Float, y: Float) = Offset(w * x / 64f, h * y / 64f)

        val head = Path().apply {
            moveTo(p(8f, 10f).x, p(8f, 10f).y)
            lineTo(p(54f, 26f).x, p(54f, 26f).y)
            lineTo(p(8f, 36f).x, p(8f, 36f).y)
            close()
        }
        val tail = Path().apply {
            moveTo(p(8f, 36f).x, p(8f, 36f).y)
            lineTo(p(54f, 26f).x, p(54f, 26f).y)
            lineTo(p(16f, 58f).x, p(16f, 58f).y)
            close()
        }

        drawPath(head, stroke)
        drawPath(tail, shadow)
    }
}
