package com.mdesign.mcontrol.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

// MControl Brand Palette (#1B2E4B, #5A7B9B, #6BB6B5)
val MDesignBgDark = Color(0xFF101D30)
val MDesignCardDark = Color(0xFF1B2E4B)
val MDesignBorderDark = Color(0xFF334F72)
val MDesignAccent = Color(0xFF6BB6B5)
val MDesignSteel = Color(0xFF5A7B9B)
val MDesignGreen = Color(0xFF22C55E)
val MDesignPurple = Color(0xFF5A7B9B)
val MDesignAmber = Color(0xFFF59E0B)
val MDesignRed = Color(0xFFEF4444)

private val DarkPalette = darkColorScheme(
    primary = MDesignAccent,
    onPrimary = Color(0xFF0E1B2C),
    secondary = MDesignSteel,
    onSecondary = Color(0xFFFFFFFF),
    tertiary = MDesignGreen,
    background = MDesignBgDark,
    onBackground = Color(0xFFEAF3F7),
    surface = MDesignCardDark,
    onSurface = Color(0xFFEAF3F7),
    surfaceVariant = Color(0xFF233A5C),
    onSurfaceVariant = Color(0xFFA5BCCF),
    outline = MDesignBorderDark,
    error = MDesignRed
)

private val LightPalette = lightColorScheme(
    primary = Color(0xFF1B2E4B),
    onPrimary = Color.White,
    secondary = Color(0xFF5A7B9B),
    onSecondary = Color.White,
    tertiary = Color(0xFF6BB6B5),
    background = Color(0xFFF2F7F9),
    onBackground = Color(0xFF1B2E4B),
    surface = Color(0xFFFFFFFF),
    onSurface = Color(0xFF1B2E4B),
    surfaceVariant = Color(0xFFE3EEF2),
    onSurfaceVariant = Color(0xFF5A7B9B),
    outline = Color(0xFFBCD0DC),
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
