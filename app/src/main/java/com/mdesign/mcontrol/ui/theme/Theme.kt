package com.mdesign.mcontrol.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

val MDesignBgDark = Color(0xFF090F1D)
val MDesignCardDark = Color(0xFF111B2E)
val MDesignBorderDark = Color(0xFF223454)
val MDesignAccent = Color(0xFF38BDF8)
val MDesignGreen = Color(0xFF22C55E)
val MDesignPurple = Color(0xFFA855F7)
val MDesignAmber = Color(0xFFF59E0B)
val MDesignRed = Color(0xFFEF4444)

private val DarkPalette = darkColorScheme(
    primary = MDesignAccent,
    onPrimary = Color(0xFF061322),
    secondary = MDesignPurple,
    tertiary = MDesignGreen,
    background = MDesignBgDark,
    onBackground = Color(0xFFE6EEF9),
    surface = MDesignCardDark,
    onSurface = Color(0xFFE6EEF9),
    surfaceVariant = Color(0xFF172540),
    onSurfaceVariant = Color(0xFF94A3B8),
    outline = MDesignBorderDark,
    error = MDesignRed
)

private val LightPalette = lightColorScheme(
    primary = Color(0xFF0284C7),
    onPrimary = Color.White,
    secondary = Color(0xFF7C3AED),
    tertiary = Color(0xFF16A34A),
    background = Color(0xFFF1F5FB),
    onBackground = Color(0xFF0F172A),
    surface = Color(0xFFFFFFFF),
    onSurface = Color(0xFF0F172A),
    surfaceVariant = Color(0xFFE5EDF9),
    onSurfaceVariant = Color(0xFF475569),
    outline = Color(0xFFCBD5E1),
    error = Color(0xFFDC2626)
)

@Composable
fun MDesignTheme(themeMode: String = "dark", content: @Composable () -> Unit) {
    val dark = when (themeMode) {
        "light" -> false
        "auto" -> isSystemInDarkTheme()
        else -> true
    }
    MaterialTheme(
        colorScheme = if (dark) DarkPalette else LightPalette,
        content = content
    )
}
