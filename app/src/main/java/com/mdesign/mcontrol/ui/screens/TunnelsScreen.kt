package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
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
import com.mdesign.mcontrol.data.TunnelItem
import com.mdesign.mcontrol.data.TunnelMetrics
import com.mdesign.mcontrol.data.formatBytes
import com.mdesign.mcontrol.ui.theme.*

@Composable
fun TunnelsScreen(
    tunnels: List<TunnelItem>,
    metricsMap: Map<Long, TunnelMetrics>,
    onRefresh: () -> Unit,
    onCheckHealth: (Long) -> Unit,
    onRotatePort: (Long) -> Unit,
    onAction: (Long, String) -> Unit
) {
    Column(Modifier.fillMaxSize().padding(horizontal = 14.dp, vertical = 10.dp)) {
        Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text("Tunnels (${tunnels.size})", fontWeight = FontWeight.ExtraBold, fontSize = 18.sp)
            IconButton(onClick = onRefresh) {
                Icon(Icons.Default.Refresh, contentDescription = "Refresh", tint = MDesignAccent)
            }
        }

        if (tunnels.isEmpty()) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("No tunnels configured yet.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            return
        }

        LazyColumn(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            items(tunnels, key = { it.id }) { tu ->
                val ok = tu.status == "active"
                val m = metricsMap[tu.id]

                Card(
                    shape = RoundedCornerShape(18.dp),
                    colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
                    border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Row(
                            Modifier.fillMaxWidth(),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.SpaceBetween
                        ) {
                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Box(Modifier.size(10.dp).clip(CircleShape).background(if (ok) MDesignGreen else MDesignRed))
                                Text(tu.name, fontWeight = FontWeight.Bold, fontSize = 15.sp)
                            }
                            Surface(
                                shape = RoundedCornerShape(99.dp),
                                color = MDesignAccent.copy(alpha = 0.16f)
                            ) {
                                val protoLbl = buildString {
                                    append(tu.engine.uppercase())
                                    if (tu.transport.isNotBlank() && tu.engine == "backhaul") {
                                        append(":").append(tu.transport)
                                    }
                                }
                                Text(protoLbl, color = MDesignAccent, fontSize = 10.sp, fontWeight = FontWeight.ExtraBold, modifier = Modifier.padding(horizontal = 9.dp, vertical = 3.dp))
                            }
                        }

                        Text(
                            "${tu.iranName} ⇄ ${tu.foreignName} • :${tu.tunnelPort}",
                            fontSize = 12.sp,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                        Text("Ports: ${tu.ports}", fontSize = 12.sp, color = MDesignAccent)

                        if (m != null) {
                            Surface(
                                shape = RoundedCornerShape(12.dp),
                                color = MaterialTheme.colorScheme.surfaceVariant,
                                modifier = Modifier.fillMaxWidth()
                            ) {
                                Column(Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                                    Text(
                                        "Iran: ${m.iranStatus} • Foreign: ${m.foreignStatus} • Ping: ${m.ping}",
                                        fontSize = 11.sp,
                                        fontWeight = FontWeight.Bold,
                                        color = MDesignGreen
                                    )
                                    if (m.bhStatus.isNotBlank()) {
                                        Text(
                                            "Backhaul /health: ${m.bhStatus} • ${formatBytes(m.bhTrafficBytes)} • Up ${m.bhUptimeSec / 60}m",
                                            fontSize = 11.sp,
                                            color = MDesignAccent
                                        )
                                    }
                                }
                            }
                        }

                        if (tu.lastError.isNotBlank()) {
                            Text(tu.lastError, fontSize = 11.sp, color = MDesignRed)
                        }

                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            FilledTonalButton(
                                onClick = { onCheckHealth(tu.id) },
                                contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                                modifier = Modifier.weight(1f)
                            ) { Text("Health", fontSize = 11.sp) }
                            OutlinedButton(
                                onClick = { onRotatePort(tu.id) },
                                contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                                modifier = Modifier.weight(1f)
                            ) { Text("Rotate", fontSize = 11.sp) }
                            OutlinedButton(
                                onClick = { onAction(tu.id, "restart") },
                                contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                                modifier = Modifier.weight(1f)
                            ) { Text("Restart", fontSize = 11.sp) }
                        }
                    }
                }
            }
        }
    }
}
