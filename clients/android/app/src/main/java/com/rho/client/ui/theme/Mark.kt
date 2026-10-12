package com.rho.client.ui.theme

import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.drawscope.clipPath

/**
 * The app's mark: Cauldron RHO's mountain, two bands meeting at a peak, with a
 * ring sitting in the hollow beneath it. The launcher and status-bar icons are
 * the same geometry.
 *
 * Drawn as geometry rather than a vector asset so it can take any two colours
 * the current palette hands it. It is twice as wide as it is tall.
 */
@Composable
fun Mark(
    mountain: Color,
    ring: Color,
    modifier: Modifier = Modifier,
) {
    Canvas(modifier) {
        // Proportions from the 960 by 480 grid the mark was traced on.
        val sx = size.width / 960f
        val sy = size.height / 480f
        fun triangle(path: Path, ax: Float, ay: Float, bx: Float, by: Float, cx: Float, cy: Float) {
            path.moveTo(ax * sx, ay * sy)
            path.lineTo(bx * sx, by * sy)
            path.lineTo(cx * sx, cy * sy)
            path.close()
        }
        fun oval(cx: Float, cy: Float, rx: Float, ry: Float) =
            Rect((cx - rx) * sx, (cy - ry) * sy, (cx + rx) * sx, (cy + ry) * sy)

        val hollow = Path().apply { triangle(this, 160f, 480f, 480f, 160f, 800f, 480f) }
        val bands = Path().apply {
            fillType = PathFillType.EvenOdd
            triangle(this, 0f, 480f, 480f, 0f, 960f, 480f)
            addPath(hollow)
        }
        val ringPath = Path().apply {
            fillType = PathFillType.EvenOdd
            addOval(oval(480f, 320f, 200f, 160f))
            addOval(oval(480f, 312f, 118f, 82f))
        }

        drawPath(bands, mountain)
        clipPath(hollow) { drawPath(ringPath, ring) }
    }
}
