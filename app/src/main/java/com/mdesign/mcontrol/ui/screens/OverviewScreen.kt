package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CleaningServices
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.RestartAlt
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.mdesign.mcontrol.data.LocalStats
import com.mdesign.mcontrol.data.ServerItem
import com.mdesign.mcontrol.data.formatBytes
import com.mdesign.mcontrol.data.formatBytesSpeed
import com.mdesign.mcontrol.ui.theme.*

@Composable
fun OverviewScreen(
    stats: LocalStats,
    servers: List<ServerItem>,
    loading: Boolean,
    onRefresh: () -> Unit,
    onCleanRam: () -> Unit,
    onRestartPanel: () -> Unit,
    onOpenMasterTerminal: () -> Unit
) {
    val iranCnt = servers.count { it.role == "iran" }
    val forCnt = servers.count { it.role != "iran" }
    val onlineCnt = servers.count { it.status == "online" }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(14.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        // 4 Stat Pills (2x2 on compact phones)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            StatMiniCard("TOTAL", servers.size.toString(), MDesignAccent, Modifier.weight(1f))
            StatMiniCard("IRAN", iranCnt.toString(), MDesignGreen, Modifier.weight(1f))
            StatMiniCard("FOREIGN", forCnt.toString(), MDesignPurple, Modifier.weight(1f))
            StatMiniCard("ONLINE", onlineCnt.toString(), MDesignAmber, Modifier.weight(1f))
        }

        // Master Header Card
        Card(
            shape = RoundedCornerShape(18.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
            modifier = Modifier.fillMaxWidth()
        ) {
            Row(
                Modifier.fillMaxWidth().padding(14.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Column(Modifier.weight(1f)) {
                    Text(stats.hostname, fontWeight = FontWeight.ExtraBold, fontSize = 17.sp)
                    Text(
                        "IPv4: ${stats.ipv4} • Disk: ${String.format("%.0f%%", stats.diskPercent)}",
                        fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    if (stats.ipv6.isNotBlank() && stats.ipv6 != "N/A") {
                        Text("IPv6: ${stats.ipv6}", fontSize = 11.sp, color = MDesignPurple)
                    }
                }
                IconButton(onClick = onRefresh, enabled = !loading) {
                    Icon(Icons.Default.Refresh, contentDescription = "Refresh", tint = MDesignAccent)
                }
            }
        }

        // 2x2 Master Telemetry Grid (CPU, RAM, Network, Toolbox)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            MetricCard(
                title = "CPU",
                value = String.format("%.1f%%", stats.cpuPercent),
                sub = "Load: ${stats.loadAvg}",
                progress = (stats.cpuPercent / 100.0).toFloat().coerceIn(0f, 1f),
                accent = MDesignAccent,
                modifier = Modifier.weight(1f)
            )
            MetricCard(
                title = "RAM",
                value = String.format("%.1f%%", stats.ramPercent),
                sub = "${stats.ramUsedMb} / ${stats.ramTotalMb} MB",
                progress = (stats.ramPercent / 100.0).toFloat().coerceIn(0f, 1f),
                accent = MDesignGreen,
                modifier = Modifier.weight(1f)
            )
        }

        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Card(
                shape = RoundedCornerShape(18.dp),
                colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
                modifier = Modifier.weight(1f)
            ) {
                Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text("Network", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, fontWeight = FontWeight.Bold)
                    Text("↓ ${formatBytesSpeed(stats.netRxSpeed)}", color = MDesignGreen, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                    Text("↑ ${formatBytesSpeed(stats.netTxSpeed)}", color = MDesignAmber, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                    Spacer(Modifier.height(2.dp))
                    Text(
                        "↓${formatBytes(stats.netRxTotal)} / ↑${formatBytes(stats.netTxTotal)}",
                        fontSize = 10.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }

            Card(
                shape = RoundedCornerShape(18.dp),
                colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
                border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
                modifier = Modifier.weight(1f)
            ) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text("Toolbox", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, fontWeight = FontWeight.Bold)
                    FilledTonalButton(
                        onClick = onOpenMasterTerminal,
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                        modifier = Modifier.fillMaxWidth().height(34.dp)
                    ) {
                        Icon(Icons.Default.Terminal, null, Modifier.size(15.dp))
                        Spacer(Modifier.width(4.dp))
                        Text("Terminal", fontSize = 11.sp)
                    }
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        OutlinedButton(
                            onClick = onCleanRam,
                            contentPadding = PaddingValues(0.dp),
                            modifier = Modifier.weight(1f).height(32.dp)
                        ) {
                            Icon(Icons.Default.CleaningServices, null, Modifier.size(14.dp))
                        }
                        OutlinedButton(
                            onClick = onRestartPanel,
                            contentPadding = PaddingValues(0.dp),
                            modifier = Modifier.weight(1f).height(32.dp)
                        ) {
                            Icon(Icons.Default.RestartAlt, null, Modifier.size(14.dp), tint = MDesignRed)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun StatMiniCard(label: String, value: String, accent: Color, modifier: Modifier) {
    Card(
        shape = RoundedCornerShape(14.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
        modifier = modifier
    ) {
        Column(
            Modifier.padding(vertical = 10.dp, horizontal = 6.dp).fillMaxWidth(),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            Text(value, fontWeight = FontWeight.ExtraBold, fontSize = 18.sp, color = accent)
            Text(label, fontSize = 9.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, fontWeight = FontWeight.Bold)
        }
    }
}

@Composable
private fun MetricCard(
    title: String,
    value: String,
    sub: String,
    progress: Float,
    accent: Color,
    modifier: Modifier
) {
    Card(
        shape = RoundedCornerShape(18.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
        modifier = modifier
    ) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(title, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, fontWeight = FontWeight.Bold)
            Text(value, fontSize = 22.sp, fontWeight = FontWeight.ExtraBold, color = accent)
            LinearProgressIndicator(
                progress = { progress },
                color = accent,
                trackColor = MaterialTheme.colorScheme.surfaceVariant,
                modifier = Modifier.fillMaxWidth().height(6.dp)
            )
            Text(sub, fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
