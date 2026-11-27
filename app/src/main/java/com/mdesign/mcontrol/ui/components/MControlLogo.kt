package com.mdesign.mcontrol.ui.components

import androidx.compose.animation.core.*
import androidx.compose.foundation.Canvas
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import com.mdesign.mcontrol.ui.theme.MDesignAccent
import com.mdesign.mcontrol.ui.theme.MDesignSteel

/**
 * Exact 1:1 MControl Shield + M Pulse Waveform with animated Ping rings & Pulse sweep.
 */
@Composable
fun MControlAnimatedLogo(
    modifier: Modifier = Modifier,
    primaryColor: Color = MDesignAccent,
    secondaryColor: Color = MDesignSteel
) {
    val inf = rememberInfiniteTransition(label = "mc_logo")

    val ping1 by inf.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2600, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Restart
        ),
        label = "ping1"
    )
    val ping2 by inf.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2600, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Restart,
            initialStartOffset = StartOffset(1300)
        ),
        label = "ping2"
    )
    val pulsePhase by inf.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 2400, easing = LinearEasing),
            repeatMode = RepeatMode.Restart
        ),
        label = "pulse"
    )

    Canvas(modifier = modifier) {
        val side = minOf(size.width, size.height)
        val ox = (size.width - side) / 2f
        val oy = (size.height - side) / 2f
        val sc = side / 120f

        fun pt(x: Float, y: Float) = Offset(ox + x * sc, oy + y * sc)

        // 1. Expanding Ping Rings
        val center = pt(60f, 60f)
        for (p in listOf(ping1, ping2)) {
            val r = (36f + p * 22f) * sc
            val a = ((1f - p) * 0.58f).coerceIn(0f, 1f)
            drawCircle(
                color = primaryColor.copy(alpha = a),
                radius = r,
                center = center,
                style = Stroke(width = 1.6f * sc)
            )
        }

        val shieldStroke = Stroke(width = 4.2f * sc, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val waveStroke = Stroke(width = 4.3f * sc, cap = StrokeCap.Round, join = StrokeJoin.Round)

        // 2. Top Shield Arch (100% symmetrical around x=60)
        val shieldTop = Path().apply {
            moveTo(ox + 21f * sc, oy + 54f * sc)
            lineTo(ox + 21f * sc, oy + 27f * sc)
            quadraticBezierTo(ox + 21f * sc, oy + 21f * sc, ox + 27f * sc, oy + 19.5f * sc)
            quadraticBezierTo(ox + 60f * sc, oy + 11f * sc, ox + 93f * sc, oy + 19.5f * sc)
            quadraticBezierTo(ox + 99f * sc, oy + 21f * sc, ox + 99f * sc, oy + 27f * sc)
            lineTo(ox + 99f * sc, oy + 54f * sc)
        }
        drawPath(shieldTop, color = primaryColor, style = shieldStroke)

        // 3. Bottom Shield Bowl (100% symmetrical around x=60)
        val shieldBot = Path().apply {
            moveTo(ox + 24f * sc, oy + 68f * sc)
            cubicTo(
                ox + 29f * sc, oy + 84f * sc,
                ox + 43f * sc, oy + 96f * sc,
                ox + 60f * sc, oy + 104f * sc
            )
            cubicTo(
                ox + 77f * sc, oy + 96f * sc,
                ox + 91f * sc, oy + 84f * sc,
                ox + 96f * sc, oy + 68f * sc
            )
        }
        drawPath(shieldBot, color = primaryColor, style = shieldStroke)

        // 4. Outer Left & Right M Waves (mirrored direction for symmetrical pulse sweep)
        val waveLeft = Path().apply {
            moveTo(ox + 13f * sc, oy + 61f * sc)
            lineTo(ox + 20f * sc, oy + 61f * sc)
            lineTo(ox + 25f * sc, oy + 49f * sc)
            lineTo(ox + 32f * sc, oy + 71f * sc)
            lineTo(ox + 43f * sc, oy + 29f * sc)
            lineTo(ox + 50f * sc, oy + 51f * sc)
        }
        val waveRight = Path().apply {
            moveTo(ox + 107f * sc, oy + 61f * sc)
            lineTo(ox + 100f * sc, oy + 61f * sc)
            lineTo(ox + 95f * sc, oy + 49f * sc)
            lineTo(ox + 88f * sc, oy + 71f * sc)
            lineTo(ox + 77f * sc, oy + 29f * sc)
            lineTo(ox + 70f * sc, oy + 51f * sc)
        }
        drawPath(waveLeft, color = secondaryColor, style = waveStroke)
        drawPath(waveRight, color = secondaryColor, style = waveStroke)

        // 5. Center Heartbeat Pulse Wave (100% symmetrical around x=60)
        val waveMid = Path().apply {
            moveTo(ox + 43f * sc, oy + 61f * sc)
            lineTo(ox + 49f * sc, oy + 61f * sc)
            lineTo(ox + 54f * sc, oy + 45f * sc)
            lineTo(ox + 60f * sc, oy + 77f * sc)
            lineTo(ox + 66f * sc, oy + 45f * sc)
            lineTo(ox + 71f * sc, oy + 61f * sc)
            lineTo(ox + 77f * sc, oy + 61f * sc)
        }
        drawPath(waveMid, color = primaryColor, style = waveStroke)

        // 6. Animated Glowing Pulse Sweep
        val dashLen = 36f * sc
        val gapLen = 130f * sc
        val phase = (1f - pulsePhase) * (dashLen + gapLen)
        val pulseStroke = Stroke(
            width = 4.6f * sc,
            cap = StrokeCap.Round,
            join = StrokeJoin.Round,
            pathEffect = PathEffect.dashPathEffect(floatArrayOf(dashLen, gapLen), phase)
        )
        drawPath(waveLeft, color = primaryColor, style = pulseStroke)
        drawPath(waveRight, color = primaryColor, style = pulseStroke)
        drawPath(
            waveMid,
            color = Color.White.copy(alpha = 0.85f),
            style = Stroke(
                width = 3.4f * sc,
                cap = StrokeCap.Round,
                join = StrokeJoin.Round,
                pathEffect = PathEffect.dashPathEffect(floatArrayOf(28f * sc, 120f * sc), phase)
            )
        )
    }
}

/**
 * Custom rounded MControl wordmark matching the branding kit (bold M + stenciled split 'o').
 */
@Composable
fun MControlWordmark(
    modifier: Modifier = Modifier,
    mColor: Color = MaterialTheme.colorScheme.onBackground,
    controlColor: Color = MDesignAccent
) {
    Canvas(modifier = modifier) {
        val sx = size.width / 340f
        val sy = size.height / 76f
        val sc = minOf(sx, sy)
        val ox = (size.width - 340f * sc) / 2f
        val oy = (size.height - 76f * sc) / 2f

        val mStroke = Stroke(width = 9.0f * sc, cap = StrokeCap.Round, join = StrokeJoin.Round)
        val cStroke = Stroke(width = 8.0f * sc, cap = StrokeCap.Round, join = StrokeJoin.Round)

        // M
        val pM = Path().apply {
            moveTo(ox + 12f * sc, oy + 64f * sc)
            lineTo(ox + 12f * sc, oy + 18f * sc)
            cubicTo(ox + 12f * sc, oy + 16f * sc, ox + 14f * sc, oy + 15f * sc, ox + 16f * sc, oy + 17f * sc)
            lineTo(ox + 34f * sc, oy + 52f * sc)
            cubicTo(ox + 35f * sc, oy + 54f * sc, ox + 37f * sc, oy + 54f * sc, ox + 38f * sc, oy + 52f * sc)
            lineTo(ox + 56f * sc, oy + 17f * sc)
            cubicTo(ox + 58f * sc, oy + 15f * sc, ox + 60f * sc, oy + 16f * sc, ox + 60f * sc, oy + 18f * sc)
            lineTo(ox + 60f * sc, oy + 64f * sc)
        }
        drawPath(pM, color = mColor, style = mStroke)

        // C
        val pC = Path().apply {
            addArc(
                oval = Rect(ox + 76f * sc, oy + 17f * sc, ox + 122f * sc, oy + 63f * sc),
                startAngleDegrees = 38f,
                sweepAngleDegrees = 284f
            )
        }
        drawPath(pC, color = controlColor, style = cStroke)

        // o (stenciled split 'o')
        val pO1L = Path().apply {
            addArc(
                oval = Rect(ox + 125f * sc, oy + 30f * sc, ox + 157f * sc, oy + 62f * sc),
                startAngleDegrees = 104f,
                sweepAngleDegrees = 152f
            )
        }
        val pO1R = Path().apply {
            addArc(
                oval = Rect(ox + 125f * sc, oy + 30f * sc, ox + 157f * sc, oy + 62f * sc),
                startAngleDegrees = -76f,
                sweepAngleDegrees = 152f
            )
        }
        drawPath(pO1L, color = controlColor, style = cStroke)
        drawPath(pO1R, color = controlColor, style = cStroke)

        // n
        val pN = Path().apply {
            moveTo(ox + 172f * sc, oy + 62f * sc)
            lineTo(ox + 172f * sc, oy + 32f * sc)
            moveTo(ox + 172f * sc, oy + 44f * sc)
            cubicTo(ox + 172f * sc, oy + 34f * sc, ox + 179f * sc, oy + 30f * sc, ox + 187f * sc, oy + 30f * sc)
            cubicTo(ox + 195f * sc, oy + 30f * sc, ox + 201f * sc, oy + 35f * sc, ox + 201f * sc, oy + 44f * sc)
            lineTo(ox + 201f * sc, oy + 62f * sc)
        }
        drawPath(pN, color = controlColor, style = cStroke)

        // t
        val pT = Path().apply {
            moveTo(ox + 222f * sc, oy + 18f * sc)
            lineTo(ox + 222f * sc, oy + 54f * sc)
            cubicTo(ox + 222f * sc, oy + 60f * sc, ox + 226f * sc, oy + 63f * sc, ox + 232f * sc, oy + 63f * sc)
            lineTo(ox + 235f * sc, oy + 63f * sc)
            moveTo(ox + 213f * sc, oy + 33f * sc)
            lineTo(ox + 234f * sc, oy + 33f * sc)
        }
        drawPath(pT, color = controlColor, style = cStroke)

        // r
        val pR = Path().apply {
            moveTo(ox + 250f * sc, oy + 62f * sc)
            lineTo(ox + 250f * sc, oy + 32f * sc)
            moveTo(ox + 250f * sc, oy + 43f * sc)
            cubicTo(ox + 250f * sc, oy + 35f * sc, ox + 256f * sc, oy + 30f * sc, ox + 264f * sc, oy + 30f * sc)
            lineTo(ox + 267f * sc, oy + 30f * sc)
        }
        drawPath(pR, color = controlColor, style = cStroke)

        // o
        drawCircle(
            color = controlColor,
            radius = 16f * sc,
            center = Offset(ox + 292f * sc, oy + 46f * sc),
            style = cStroke
        )

        // l
        drawLine(
            color = controlColor,
            start = Offset(ox + 324f * sc, oy + 14f * sc),
            end = Offset(ox + 324f * sc, oy + 62f * sc),
            strokeWidth = 8.0f * sc,
            cap = StrokeCap.Round
        )
    }
}
