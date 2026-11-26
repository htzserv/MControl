package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.mdesign.mcontrol.data.MonitorItem
import com.mdesign.mcontrol.data.PanelProfile
import com.mdesign.mcontrol.ui.theme.*

@Composable
fun UptimeScreen(
    monitors: List<MonitorItem>,
    onRefresh: () -> Unit,
    onCheckNow: (Long) -> Unit
) {
    Column(Modifier.fillMaxSize().padding(horizontal = 14.dp, vertical = 10.dp)) {
        Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text("Uptime Monitors (${monitors.size})", fontWeight = FontWeight.ExtraBold, fontSize = 18.sp)
            IconButton(onClick = onRefresh) {
                Icon(Icons.Default.Refresh, contentDescription = "Refresh", tint = MDesignAccent)
            }
        }

        if (monitors.isEmpty()) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("No uptime monitors configured.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            return
        }

        LazyColumn(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            items(monitors, key = { it.id }) { m ->
                val isUp = m.status.equals("up", ignoreCase = true)
                val dotCol = when {
                    isUp -> MDesignGreen
                    m.status.equals("down", ignoreCase = true) -> MDesignRed
                    else -> MDesignAmber
                }
                Card(
                    shape = RoundedCornerShape(16.dp),
                    colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
                    border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Row(
                        Modifier.fillMaxWidth().padding(14.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween
                    ) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp), modifier = Modifier.weight(1f)) {
                            Box(Modifier.size(11.dp).clip(CircleShape).background(dotCol))
                            Column {
                                Text(m.name, fontWeight = FontWeight.Bold, fontSize = 14.sp)
                                Text(
                                    "${m.type.uppercase()} • ${m.target}",
                                    fontSize = 11.sp,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                                Text(
                                    "24h: ${String.format("%.2f%%", m.uptime24h)} • ${m.lastLatencyMs} ms",
                                    fontSize = 11.sp,
                                    color = dotCol,
                                    fontWeight = FontWeight.Bold
                                )
                            }
                        }
                        OutlinedButton(
                            onClick = { onCheckNow(m.id) },
                            contentPadding = PaddingValues(horizontal = 10.dp, vertical = 4.dp)
                        ) {
                            Text("Check", fontSize = 11.sp)
                        }
                    }
                }
            }
        }
    }
}

@Composable
fun SettingsScreen(
    activePanel: PanelProfile?,
    themeMode: String,
    bgAlerts: Boolean,
    bioLock: Boolean,
    onChangeTheme: (String) -> Unit,
    onToggleBgAlerts: (Boolean) -> Unit,
    onToggleBioLock: (Boolean) -> Unit,
    onSwitchPanel: () -> Unit,
    onLogout: () -> Unit
) {
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(14.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Text("Settings", fontWeight = FontWeight.ExtraBold, fontSize = 20.sp)

        Card(
            shape = RoundedCornerShape(18.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
            modifier = Modifier.fillMaxWidth()
        ) {
            Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Connected Panel", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MDesignAccent)
                Text(activePanel?.name ?: "None", fontWeight = FontWeight.Bold, fontSize = 16.sp)
                Text(activePanel?.baseUrl ?: "", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    FilledTonalButton(onClick = onSwitchPanel, modifier = Modifier.weight(1f)) {
                        Text("Switch / Add Panel", fontSize = 12.sp)
                    }
                    OutlinedButton(onClick = onLogout, modifier = Modifier.weight(1f)) {
                        Text("Sign Out", fontSize = 12.sp, color = MDesignRed)
                    }
                }
            }
        }

        Card(
            shape = RoundedCornerShape(18.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
            modifier = Modifier.fillMaxWidth()
        ) {
            Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("MDesign Appearance", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MDesignAccent)
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf("dark" to "🌙 Dark", "light" to "☀️ Light", "auto" to "◐ Auto").forEach { (k, lbl) ->
                        FilterChip(
                            selected = themeMode == k,
                            onClick = { onChangeTheme(k) },
                            label = { Text(lbl, fontSize = 12.sp) },
                            modifier = Modifier.weight(1f)
                        )
                    }
                }
            }
        }

        Card(
            shape = RoundedCornerShape(18.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
            modifier = Modifier.fillMaxWidth()
        ) {
            Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("Security & Background Alerts", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MDesignAccent)
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                    Column(Modifier.weight(1f)) {
                        Text("Background Outage Push Alerts", fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Text("Checks servers & monitors every 15 min even when closed", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Switch(checked = bgAlerts, onCheckedChange = onToggleBgAlerts)
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outline.copy(alpha = 0.4f))
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                    Column(Modifier.weight(1f)) {
                        Text("Biometric App Lock", fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Text("Require fingerprint / face unlock on app open", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Switch(checked = bioLock, onCheckedChange = onToggleBioLock)
                }
            }
        }
    }
}
