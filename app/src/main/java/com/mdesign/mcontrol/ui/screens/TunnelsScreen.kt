package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.mdesign.mcontrol.data.BenchJobState
import com.mdesign.mcontrol.data.ServerItem
import com.mdesign.mcontrol.data.TunnelItem
import com.mdesign.mcontrol.data.TunnelMetrics
import com.mdesign.mcontrol.data.formatBytes
import com.mdesign.mcontrol.ui.theme.*
import kotlinx.coroutines.delay

@Composable
fun TunnelsScreen(
    tunnels: List<TunnelItem>,
    servers: List<ServerItem>,
    metricsMap: Map<Long, TunnelMetrics>,
    benchJob: BenchJobState,
    onRefresh: () -> Unit,
    onCheckHealth: (Long) -> Unit,
    onRotatePort: (Long) -> Unit,
    onAction: (Long, String) -> Unit,
    onSpeedtest: (Long) -> Unit,
    onDeleteTunnel: (Long) -> Unit,
    onCreateTunnel: (name: String, engine: String, transport: String, iranId: Long, foreignId: Long, tunnelPort: Int, ports: String) -> Unit,
    onStartBenchmark: (iranId: Long, foreignId: Long, profile: String) -> Unit,
    onPollBenchmark: () -> Unit
) {
    var showCreate by remember { mutableStateOf(false) }
    var showBench by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf<TunnelItem?>(null) }

    LaunchedEffect(showBench, benchJob.running) {
        if (showBench) {
            onPollBenchmark()
            while (benchJob.running) {
                delay(2500)
                onPollBenchmark()
            }
        }
    }

    Column(Modifier.fillMaxSize().padding(horizontal = 14.dp, vertical = 10.dp)) {
        Row(
            Modifier.fillMaxWidth().padding(bottom = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text("Tunnels (${tunnels.size})", fontWeight = FontWeight.ExtraBold, fontSize = 18.sp)
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                OutlinedButton(
                    onClick = { showBench = true },
                    contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                    modifier = Modifier.height(34.dp)
                ) {
                    Icon(Icons.Default.Speed, null, Modifier.size(14.dp))
                    Spacer(Modifier.width(3.dp))
                    Text(if (benchJob.running) "${benchJob.percent}%" else "MSmart", fontSize = 11.sp)
                }
                FilledTonalButton(
                    onClick = { showCreate = true },
                    contentPadding = PaddingValues(horizontal = 10.dp, vertical = 4.dp),
                    modifier = Modifier.height(34.dp)
                ) {
                    Icon(Icons.Default.Add, null, Modifier.size(15.dp))
                    Spacer(Modifier.width(3.dp))
                    Text("New", fontSize = 12.sp)
                }
                IconButton(onClick = onRefresh, modifier = Modifier.size(34.dp)) {
                    Icon(Icons.Default.Refresh, contentDescription = "Refresh", tint = MDesignAccent)
                }
            }
        }

        if (tunnels.isEmpty()) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("No tunnels configured yet. Tap + New to deploy.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else {
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
                                    contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("Health", fontSize = 11.sp) }
                                OutlinedButton(
                                    onClick = { onSpeedtest(tu.id) },
                                    contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("Speed", fontSize = 11.sp) }
                                OutlinedButton(
                                    onClick = { onRotatePort(tu.id) },
                                    contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("Rotate", fontSize = 11.sp) }
                            }
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                                OutlinedButton(
                                    onClick = { onAction(tu.id, "restart") },
                                    contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("Restart", fontSize = 11.sp) }
                                OutlinedButton(
                                    onClick = { onAction(tu.id, if (ok) "stop" else "start") },
                                    contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text(if (ok) "Stop" else "Start", fontSize = 11.sp) }
                                IconButton(
                                    onClick = { confirmDelete = tu },
                                    modifier = Modifier.size(34.dp)
                                ) { Icon(Icons.Default.Delete, contentDescription = "Delete", tint = MDesignRed) }
                            }
                        }
                    }
                }
            }
        }
    }

    if (showCreate) {
        val iranList = servers.filter { it.role == "iran" }.ifEmpty { servers }
        val forList = servers.filter { it.role != "iran" }.ifEmpty { servers }
        var tName by remember { mutableStateOf("tun-${(100..999).random()}") }
        var iranId by remember { mutableStateOf(iranList.firstOrNull()?.id ?: 0L) }
        var foreignId by remember { mutableStateOf(forList.firstOrNull { it.id != iranId }?.id ?: forList.firstOrNull()?.id ?: 0L) }
        var engine by remember { mutableStateOf("backhaul") }
        var transport by remember { mutableStateOf("tcpmux") }
        var tPort by remember { mutableStateOf("3080") }
        var fwdPorts by remember { mutableStateOf("443, 8443") }

        val engines = listOf("backhaul", "rathole", "paqet", "gre", "ipip", "vxlan", "wireguard")
        val bhTransports = listOf("tcp", "tcpmux", "tls", "tlsmux", "ws", "wss", "xhttp", "xhttps", "xhttpsmux", "kcp", "kcpmux", "udp", "failover")

        AlertDialog(
            onDismissRequest = { showCreate = false },
            title = { Text("+ New 2-Sided Tunnel", fontWeight = FontWeight.Bold) },
            text = {
                Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(value = tName, onValueChange = { tName = it }, label = { Text("Tunnel Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())

                    Text("Entry (Iran) Server", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, fontWeight = FontWeight.Bold)
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        items(servers) { s ->
                            FilterChip(selected = iranId == s.id, onClick = { iranId = s.id }, label = { Text(s.name, fontSize = 11.sp) })
                        }
                    }

                    Text("Exit (Foreign) Server", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, fontWeight = FontWeight.Bold)
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        items(servers) { s ->
                            FilterChip(selected = foreignId == s.id, onClick = { foreignId = s.id }, label = { Text(s.name, fontSize = 11.sp) })
                        }
                    }

                    Text("Engine", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, fontWeight = FontWeight.Bold)
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        items(engines) { eng ->
                            FilterChip(selected = engine == eng, onClick = { engine = eng }, label = { Text(eng.uppercase(), fontSize = 11.sp) })
                        }
                    }

                    if (engine == "backhaul") {
                        Text("Backhaul Transport", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, fontWeight = FontWeight.Bold)
                        LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            items(bhTransports) { tr ->
                                FilterChip(selected = transport == tr, onClick = { transport = tr }, label = { Text(tr.uppercase(), fontSize = 11.sp) })
                            }
                        }
                    }

                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedTextField(
                            value = tPort,
                            onValueChange = { tPort = it.filter { c -> c.isDigit() }.take(5) },
                            label = { Text("Ctrl Port") },
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                            singleLine = true,
                            modifier = Modifier.weight(0.38f)
                        )
                        OutlinedTextField(
                            value = fwdPorts,
                            onValueChange = { fwdPorts = it },
                            label = { Text("Forward Ports") },
                            singleLine = true,
                            modifier = Modifier.weight(0.62f)
                        )
                    }
                }
            },
            confirmButton = {
                Button(
                    onClick = {
                        if (iranId > 0 && foreignId > 0 && iranId != foreignId && fwdPorts.isNotBlank()) {
                            onCreateTunnel(tName, engine, if (engine == "backhaul") transport else "raw", iranId, foreignId, tPort.toIntOrNull() ?: 3080, fwdPorts)
                            showCreate = false
                        }
                    }
                ) { Text("Deploy") }
            },
            dismissButton = { TextButton(onClick = { showCreate = false }) { Text("Cancel") } }
        )
    }

    if (showBench) {
        val iranList = servers.filter { it.role == "iran" }.ifEmpty { servers }
        val forList = servers.filter { it.role != "iran" }.ifEmpty { servers }
        var bIran by remember { mutableStateOf(iranList.firstOrNull()?.id ?: 0L) }
        var bFor by remember { mutableStateOf(forList.firstOrNull { it.id != bIran }?.id ?: forList.firstOrNull()?.id ?: 0L) }
        var bProf by remember { mutableStateOf("balanced") }

        AlertDialog(
            onDismissRequest = { showBench = false },
            title = { Text("⚡ MSmart All-Protocol Tester", fontWeight = FontWeight.Bold) },
            text = {
                Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("Iran Server", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        items(servers) { s -> FilterChip(selected = bIran == s.id, onClick = { bIran = s.id }, label = { Text(s.name, fontSize = 11.sp) }) }
                    }
                    Text("Foreign Server", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        items(servers) { s -> FilterChip(selected = bFor == s.id, onClick = { bFor = s.id }, label = { Text(s.name, fontSize = 11.sp) }) }
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        listOf("balanced" to "Balanced", "speed" to "Speed", "ping" to "Low Ping").forEach { (k, lbl) ->
                            FilterChip(selected = bProf == k, onClick = { bProf = k }, label = { Text(lbl, fontSize = 11.sp) })
                        }
                    }
                    Text("${benchJob.current} (${benchJob.percent}%)", fontSize = 12.sp, color = MDesignAccent, fontWeight = FontWeight.Bold)
                    LinearProgressIndicator(progress = { (benchJob.percent / 100f).coerceIn(0f, 1f) }, color = MDesignAccent, modifier = Modifier.fillMaxWidth())

                    benchJob.results.take(12).forEach { r ->
                        Surface(
                            shape = RoundedCornerShape(10.dp),
                            color = MaterialTheme.colorScheme.surfaceVariant,
                            modifier = Modifier.fillMaxWidth()
                        ) {
                            Row(
                                Modifier.padding(8.dp),
                                horizontalArrangement = Arrangement.SpaceBetween,
                                verticalAlignment = Alignment.CenterVertically
                            ) {
                                Column(Modifier.weight(1f)) {
                                    Text("#${r.rank} ${r.label}", fontWeight = FontWeight.Bold, fontSize = 12.sp)
                                    Text(
                                        if (r.ok) "${r.mbps} Mbps • ${r.pingMs} ms • Loss ${r.lossPct.toInt()}%" else r.error.ifBlank { "Failed" },
                                        fontSize = 11.sp,
                                        color = if (r.ok) MDesignGreen else MDesignRed
                                    )
                                }
                                if (r.ok) {
                                    Text("${r.score}/100", fontWeight = FontWeight.ExtraBold, color = MDesignAccent, fontSize = 12.sp)
                                }
                            }
                        }
                    }
                }
            },
            confirmButton = {
                Button(
                    enabled = !benchJob.running && bIran > 0 && bFor > 0 && bIran != bFor,
                    onClick = { onStartBenchmark(bIran, bFor, bProf) }
                ) { Text(if (benchJob.running) "Running..." else "Start Test") }
            },
            dismissButton = { TextButton(onClick = { showBench = false }) { Text("Close") } }
        )
    }

    confirmDelete?.let { target ->
        AlertDialog(
            onDismissRequest = { confirmDelete = null },
            title = { Text("Delete Tunnel?", color = MDesignRed, fontWeight = FontWeight.Bold) },
            text = { Text("Remove ${target.name} from both servers?") },
            confirmButton = {
                Button(
                    onClick = {
                        onDeleteTunnel(target.id)
                        confirmDelete = null
                    },
                    colors = ButtonDefaults.buttonColors(containerColor = MDesignRed)
                ) { Text("Delete") }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = null }) { Text("Cancel") } }
        )
    }
}
