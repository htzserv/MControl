package com.mdesign.mcontrol.data

data class PanelProfile(
    val id: String,
    val name: String,
    val baseUrl: String, // e.g. https://panel.example.com:2053/secretpath
    val username: String,
    val password: String,
    val token: String = "",
    val trustAllSsl: Boolean = true
)

data class LocalStats(
    val hostname: String = "Master",
    val os: String = "",
    val ipv4: String = "N/A",
    val ipv6: String = "N/A",
    val cpuPercent: Double = 0.0,
    val loadAvg: String = "0.00",
    val ramUsedMb: Long = 0,
    val ramTotalMb: Long = 0,
    val ramPercent: Double = 0.0,
    val diskPercent: Double = 0.0,
    val netRxSpeed: Long = 0,
    val netTxSpeed: Long = 0,
    val netRxTotal: Long = 0,
    val netTxTotal: Long = 0,
    val uptime: String = "",
    val bbr: String = ""
)

data class ServerItem(
    val id: Long,
    val name: String,
    val role: String, // "iran" | "foreign"
    val host: String,
    val port: Int,
    val status: String,
    val country: String,
    val countryCode: String,
    val cpu: String,
    val ram: String,
    val uptime: String,
    val ipv4: String,
    val ipv6: String,
    val authMode: String,
    val pingMs: Double,
    val outboundPingMs: Double,
    val reversePingMs: Double,
    val useOutbound: Boolean,
    val lastError: String
)

data class TunnelItem(
    val id: Long,
    val name: String,
    val engine: String,
    val transport: String,
    val iranName: String,
    val iranHost: String,
    val foreignName: String,
    val foreignHost: String,
    val tunnelPort: Int,
    val ports: String,
    val status: String,
    val lastError: String
)

data class TunnelMetrics(
    val iranStatus: String = "?",
    val foreignStatus: String = "?",
    val ping: String = "N/A",
    val conns: String = "0",
    val bhConnected: Boolean? = null,
    val bhStatus: String = "",
    val bhTrafficBytes: Long = 0,
    val bhUptimeSec: Long = 0,
    val bhGoroutines: Int = 0
)

data class MonitorItem(
    val id: Long,
    val name: String,
    val type: String,
    val target: String,
    val status: String,
    val lastLatencyMs: Int,
    val uptime24h: Double,
    val enabled: Boolean
)

data class LoginResult(
    val ok: Boolean,
    val token: String = "",
    val require2fa: Boolean = false,
    val error: String = ""
)

fun flagEmojiFor(cc: String): String {
    val clean = cc.trim().uppercase()
    if (clean.length != 2) return "🌐"
    val first = Character.codePointAt(clean, 0) - 0x41 + 0x1F1E6
    val second = Character.codePointAt(clean, 1) - 0x41 + 0x1F1E6
    return String(Character.toChars(first)) + String(Character.toChars(second))
}

fun formatBytesSpeed(bytesPerSec: Long): String {
    val b = bytesPerSec.toDouble()
    return when {
        b >= 1e9 -> String.format("%.2f GB/s", b / 1e9)
        b >= 1e6 -> String.format("%.2f MB/s", b / 1e6)
        b >= 1e3 -> String.format("%.1f KB/s", b / 1e3)
        else -> "$bytesPerSec B/s"
    }
}

fun formatBytes(bytes: Long): String {
    val b = bytes.toDouble()
    return when {
        b >= 1073741824.0 -> String.format("%.2f GB", b / 1073741824.0)
        b >= 1048576.0 -> String.format("%.1f MB", b / 1048576.0)
        b >= 1024.0 -> String.format("%.1f KB", b / 1024.0)
        else -> "$bytes B"
    }
}

data class BenchResultItem(
    val rank: Int,
    val engine: String,
    val transport: String,
    val ipMode: String,
    val underlay: String,
    val label: String,
    val ok: Boolean,
    val mbps: Double,
    val pingMs: Double,
    val lossPct: Double,
    val score: Int,
    val error: String = ""
)

data class BenchJobState(
    val running: Boolean = false,
    val done: Boolean = false,
    val percent: Int = 0,
    val current: String = "Ready",
    val results: List<BenchResultItem> = emptyList()
)

data class PanelUpdateCheck(
    val currentVersion: String = "",
    val stableAvailable: Boolean = false,
    val stableVersion: String = "",
    val betaAvailable: Boolean = false,
    val betaTag: String = "",
    val betaBuild: Int = 0,
    val mirrorBase: String = ""
)
