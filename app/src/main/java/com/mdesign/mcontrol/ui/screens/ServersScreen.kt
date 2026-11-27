package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.mdesign.mcontrol.data.ServerItem
import com.mdesign.mcontrol.data.flagEmojiFor
import com.mdesign.mcontrol.ui.theme.*

@Composable
fun ServersScreen(
    servers: List<ServerItem>,
    onRefreshAll: () -> Unit,
    onPingAll: () -> Unit,
    onAddServer: (name: String, host: String, port: Int, user: String, pass: String, useOutbound: Boolean) -> Unit,
    onDeleteServer: (id: Long, mode: String) -> Unit,
    onPingServer: (Long) -> Unit,
    onProbeServer: (Long) -> Unit,
    onTweakServer: (Long, String) -> Unit,
    onOpenTerminal: (ServerItem) -> Unit
) {
    var selectedCC by remember { mutableStateOf("ALL") }
    var expandedId by remember { mutableStateOf<Long?>(null) }
    var showAddDialog by remember { mutableStateOf(false) }
    var deleteTarget by remember { mutableStateOf<ServerItem?>(null) }

    val ccGroups = remember(servers) {
        servers.groupBy { it.countryCode.ifBlank { if (it.role == "iran") "IR" else "XX" } }
            .entries.sortedByDescending { it.value.size }
    }
    val filtered = remember(servers, selectedCC) {
        if (selectedCC == "ALL") servers
        else servers.filter { (it.countryCode.ifBlank { if (it.role == "iran") "IR" else "XX" }) == selectedCC }
    }

    Column(Modifier.fillMaxSize().padding(horizontal = 14.dp, vertical = 10.dp)) {
        // Header actions
        Row(
            Modifier.fillMaxWidth().padding(bottom = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text("Servers (${servers.size})", fontWeight = FontWeight.ExtraBold, fontSize = 18.sp)
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                OutlinedButton(
                    onClick = onPingAll,
                    contentPadding = PaddingValues(horizontal = 9.dp, vertical = 4.dp),
                    modifier = Modifier.height(34.dp)
                ) { Text("Ping All", fontSize = 11.sp) }
                FilledTonalButton(
                    onClick = { showAddDialog = true },
                    contentPadding = PaddingValues(horizontal = 10.dp, vertical = 4.dp),
                    modifier = Modifier.height(34.dp)
                ) {
                    Icon(Icons.Default.Add, null, Modifier.size(15.dp))
                    Spacer(Modifier.width(3.dp))
                    Text("Add", fontSize = 12.sp)
                }
                IconButton(onClick = onRefreshAll, modifier = Modifier.size(34.dp)) {
                    Icon(Icons.Default.Refresh, contentDescription = "Refresh", tint = MDesignAccent)
                }
            }
        }

        // Country Filter Dock
        LazyRow(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            item {
                FilterChip(
                    selected = selectedCC == "ALL",
                    onClick = { selectedCC = "ALL" },
                    label = { Text("🌐 All (${servers.size})", fontSize = 12.sp) }
                )
            }
            items(ccGroups) { (cc, list) ->
                FilterChip(
                    selected = selectedCC == cc,
                    onClick = { selectedCC = cc },
                    label = { Text("${flagEmojiFor(cc)} $cc (${list.size})", fontSize = 12.sp) }
                )
            }
        }

        Spacer(Modifier.height(8.dp))

        LazyColumn(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            items(filtered, key = { it.id }) { srv ->
                val isOpen = expandedId == srv.id
                val online = srv.status == "online"
                val accent = if (srv.role == "iran") MDesignGreen else MDesignAccent

                Card(
                    shape = RoundedCornerShape(18.dp),
                    colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
                    border = BorderStroke(1.2.dp, if (isOpen) MDesignAccent else MaterialTheme.colorScheme.outline),
                    modifier = Modifier.fillMaxWidth().clickable {
                        expandedId = if (isOpen) null else srv.id
                    }
                ) {
                    Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Row(
                            Modifier.fillMaxWidth(),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.SpaceBetween
                        ) {
                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Box(
                                    Modifier.size(10.dp).clip(CircleShape)
                                        .background(if (online) MDesignGreen else MDesignRed)
                                )
                                Text(flagEmojiFor(srv.countryCode.ifBlank { if (srv.role == "iran") "IR" else "" }), fontSize = 16.sp)
                                Column {
                                    Text(srv.name, fontWeight = FontWeight.Bold, fontSize = 15.sp)
                                    Text(srv.host, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                            }
                            Surface(
                                shape = RoundedCornerShape(99.dp),
                                color = accent.copy(alpha = 0.16f)
                            ) {
                                Text(
                                    text = if (srv.authMode == "peer") "PEER V4" else "SSH",
                                    color = accent,
                                    fontSize = 10.sp,
                                    fontWeight = FontWeight.ExtraBold,
                                    modifier = Modifier.padding(horizontal = 9.dp, vertical = 3.dp)
                                )
                            }
                        }

                        Row(
                            Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            if (srv.pingMs > 0) {
                                PingBadge("🇮🇷 ${String.format("%.1fms", srv.pingMs)}", MDesignGreen)
                            }
                            if (srv.reversePingMs > 0) {
                                PingBadge("↩ ${String.format("%.1fms", srv.reversePingMs)}", MDesignAccent)
                            }
                            Spacer(Modifier.weight(1f))
                            if (srv.cpu.isNotBlank() || srv.ram.isNotBlank()) {
                                Text(
                                    "CPU ${srv.cpu.ifBlank { "-" }} • RAM ${srv.ram.ifBlank { "-" }}",
                                    fontSize = 11.sp,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                            }
                        }

                        if (isOpen) {
                            HorizontalDivider(color = MaterialTheme.colorScheme.outline.copy(alpha = 0.5f))
                            if (srv.uptime.isNotBlank()) {
                                Text("Uptime: ${srv.uptime}", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                            if (srv.lastError.isNotBlank()) {
                                Text(srv.lastError, fontSize = 11.sp, color = MDesignRed)
                            }
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Button(
                                    onClick = { onOpenTerminal(srv) },
                                    contentPadding = PaddingValues(horizontal = 10.dp, vertical = 6.dp),
                                    modifier = Modifier.weight(1f)
                                ) {
                                    Icon(Icons.Default.Terminal, null, Modifier.size(15.dp))
                                    Spacer(Modifier.width(4.dp))
                                    Text("Terminal", fontSize = 12.sp)
                                }
                                OutlinedButton(
                                    onClick = { onPingServer(srv.id) },
                                    contentPadding = PaddingValues(horizontal = 8.dp, vertical = 6.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("Ping", fontSize = 12.sp) }
                                OutlinedButton(
                                    onClick = { onProbeServer(srv.id) },
                                    contentPadding = PaddingValues(horizontal = 8.dp, vertical = 6.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("Probe", fontSize = 12.sp) }
                            }
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                OutlinedButton(
                                    onClick = { onTweakServer(srv.id, "net_opt") },
                                    contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("Optimize", fontSize = 11.sp) }
                                OutlinedButton(
                                    onClick = { onTweakServer(srv.id, "clear_cache") },
                                    contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("RAM", fontSize = 11.sp) }
                                OutlinedButton(
                                    onClick = { onTweakServer(srv.id, "reboot") },
                                    contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                                    modifier = Modifier.weight(1f)
                                ) { Text("Reboot", fontSize = 11.sp, color = MDesignAmber) }
                                IconButton(
                                    onClick = { deleteTarget = srv },
                                    modifier = Modifier.size(36.dp)
                                ) {
                                    Icon(Icons.Default.Delete, contentDescription = "Delete", tint = MDesignRed)
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    if (showAddDialog) {
        var sName by remember { mutableStateOf("") }
        var sHost by remember { mutableStateOf("") }
        var sPort by remember { mutableStateOf("22") }
        var sUser by remember { mutableStateOf("root") }
        var sPass by remember { mutableStateOf("") }
        var sOutbound by remember { mutableStateOf(false) }

        AlertDialog(
            onDismissRequest = { showAddDialog = false },
            title = { Text("+ Add Server", fontWeight = FontWeight.Bold) },
            text = {
                Column(
                    Modifier.verticalScroll(rememberScrollState()),
                    verticalArrangement = Arrangement.spacedBy(8.dp)
                ) {
                    OutlinedTextField(value = sName, onValueChange = { sName = it }, label = { Text("Server Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    OutlinedTextField(value = sHost, onValueChange = { sHost = it }, label = { Text("IP / Host") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedTextField(
                            value = sPort,
                            onValueChange = { sPort = it.filter { c -> c.isDigit() }.take(5) },
                            label = { Text("Port") },
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                            singleLine = true,
                            modifier = Modifier.weight(0.38f)
                        )
                        OutlinedTextField(value = sUser, onValueChange = { sUser = it }, label = { Text("SSH User") }, singleLine = true, modifier = Modifier.weight(0.62f))
                    }
                    OutlinedTextField(
                        value = sPass,
                        onValueChange = { sPass = it },
                        label = { Text("SSH Password") },
                        visualTransformation = PasswordVisualTransformation(),
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth()
                    )
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                        Text("Via Master Outbound (Xray)", fontSize = 12.sp)
                        Switch(checked = sOutbound, onCheckedChange = { sOutbound = it })
                    }
                }
            },
            confirmButton = {
                Button(
                    onClick = {
                        if (sHost.isNotBlank() && sPass.isNotBlank()) {
                            onAddServer(sName.ifBlank { sHost }, sHost, sPort.toIntOrNull() ?: 22, sUser, sPass, sOutbound)
                            showAddDialog = false
                        }
                    }
                ) { Text("Add Server") }
            },
            dismissButton = {
                TextButton(onClick = { showAddDialog = false }) { Text("Cancel") }
            }
        )
    }

    deleteTarget?.let { target ->
        var delMode by remember { mutableStateOf("unpair_keep_tunnels") }
        val modes = listOf(
            "unpair_keep_tunnels" to "Unpair & Keep Tunnels (Recommended)",
            "unpair_only" to "Unpair Only (Keep in Panel)",
            "panel_only" to "Remove from Panel Only",
            "uninstall_all" to "Full Uninstall (Agent + All Tunnels)"
        )
        AlertDialog(
            onDismissRequest = { deleteTarget = null },
            title = { Text("Remove ${target.name}", color = MDesignRed, fontWeight = FontWeight.Bold) },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    modes.forEach { (key, lbl) ->
                        Row(
                            Modifier.fillMaxWidth().clickable { delMode = key }.padding(vertical = 4.dp),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            RadioButton(selected = delMode == key, onClick = { delMode = key })
                            Spacer(Modifier.width(6.dp))
                            Text(lbl, fontSize = 13.sp, color = if (key == "uninstall_all") MDesignRed else MaterialTheme.colorScheme.onSurface)
                        }
                    }
                }
            },
            confirmButton = {
                Button(
                    onClick = {
                        onDeleteServer(target.id, delMode)
                        deleteTarget = null
                    },
                    colors = ButtonDefaults.buttonColors(containerColor = MDesignRed)
                ) { Text("Confirm") }
            },
            dismissButton = {
                TextButton(onClick = { deleteTarget = null }) { Text("Cancel") }
            }
        )
    }
}

@Composable
private fun PingBadge(text: String, color: androidx.compose.ui.graphics.Color) {
    Surface(
        shape = RoundedCornerShape(99.dp),
        color = color.copy(alpha = 0.14f),
        border = BorderStroke(1.dp, color.copy(alpha = 0.35f))
    ) {
        Text(text, fontSize = 11.sp, fontWeight = FontWeight.Bold, color = color, modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp))
    }
}
