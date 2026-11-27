package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.OpenInBrowser
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.mdesign.mcontrol.data.MonitorItem
import com.mdesign.mcontrol.data.PanelProfile
import com.mdesign.mcontrol.data.PanelUpdateCheck
import com.mdesign.mcontrol.ui.theme.*

@Composable
fun UptimeScreen(
    monitors: List<MonitorItem>,
    onRefresh: () -> Unit,
    onCheckNow: (Long) -> Unit,
    onAddMonitor: (name: String, type: String, target: String, intervalSec: Int) -> Unit,
    onDeleteMonitor: (Long) -> Unit,
    onImportMonitors: (String) -> Unit
) {
    var showAdd by remember { mutableStateOf(false) }

    Column(Modifier.fillMaxSize().padding(horizontal = 14.dp, vertical = 10.dp)) {
        Row(
            Modifier.fillMaxWidth().padding(bottom = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween
        ) {
            Text("Uptime (${monitors.size})", fontWeight = FontWeight.ExtraBold, fontSize = 18.sp)
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                FilledTonalButton(
                    onClick = { showAdd = true },
                    contentPadding = PaddingValues(horizontal = 10.dp, vertical = 4.dp),
                    modifier = Modifier.height(34.dp)
                ) {
                    Icon(Icons.Default.Add, null, Modifier.size(15.dp))
                    Spacer(Modifier.width(3.dp))
                    Text("Add", fontSize = 12.sp)
                }
                IconButton(onClick = onRefresh, modifier = Modifier.size(34.dp)) {
                    Icon(Icons.Default.Refresh, contentDescription = "Refresh", tint = MDesignAccent)
                }
            }
        }

        Row(Modifier.fillMaxWidth().padding(bottom = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(
                onClick = { onImportMonitors("servers") },
                contentPadding = PaddingValues(horizontal = 10.dp, vertical = 4.dp),
                modifier = Modifier.weight(1f).height(34.dp)
            ) { Text("+ Import Servers", fontSize = 11.sp) }
            OutlinedButton(
                onClick = { onImportMonitors("tunnels") },
                contentPadding = PaddingValues(horizontal = 10.dp, vertical = 4.dp),
                modifier = Modifier.weight(1f).height(34.dp)
            ) { Text("+ Import Tunnels", fontSize = 11.sp) }
        }

        if (monitors.isEmpty()) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("No uptime monitors configured.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else {
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
                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                                OutlinedButton(
                                    onClick = { onCheckNow(m.id) },
                                    contentPadding = PaddingValues(horizontal = 9.dp, vertical = 4.dp)
                                ) {
                                    Text("Check", fontSize = 11.sp)
                                }
                                IconButton(onClick = { onDeleteMonitor(m.id) }, modifier = Modifier.size(32.dp)) {
                                    Icon(Icons.Default.Delete, contentDescription = "Delete", tint = MDesignRed, modifier = Modifier.size(18.dp))
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    if (showAdd) {
        var mName by remember { mutableStateOf("") }
        var mType by remember { mutableStateOf("http") }
        var mTarget by remember { mutableStateOf("") }
        var mInt by remember { mutableStateOf("60") }

        AlertDialog(
            onDismissRequest = { showAdd = false },
            title = { Text("+ Add Monitor", fontWeight = FontWeight.Bold) },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(value = mName, onValueChange = { mName = it }, label = { Text("Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        listOf("http", "tcp", "ping", "dns").forEach { t ->
                            FilterChip(selected = mType == t, onClick = { mType = t }, label = { Text(t.uppercase(), fontSize = 11.sp) })
                        }
                    }
                    OutlinedTextField(
                        value = mTarget,
                        onValueChange = { mTarget = it },
                        label = { Text("Target (URL / IP / host:port)") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth()
                    )
                    OutlinedTextField(
                        value = mInt,
                        onValueChange = { mInt = it.filter { c -> c.isDigit() }.take(4) },
                        label = { Text("Interval (sec)") },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth()
                    )
                }
            },
            confirmButton = {
                Button(onClick = {
                    if (mName.isNotBlank() && mTarget.isNotBlank()) {
                        onAddMonitor(mName, mType, mTarget, mInt.toIntOrNull() ?: 60)
                        showAdd = false
                    }
                }) { Text("Create") }
            },
            dismissButton = { TextButton(onClick = { showAdd = false }) { Text("Cancel") } }
        )
    }
}

@Composable
fun SettingsScreen(
    activePanel: PanelProfile?,
    themeMode: String,
    bgAlerts: Boolean,
    alertIntervalSec: Int,
    alertEveryN: Int,
    alertMaxPerOutage: Int,
    alertOnRecovery: Boolean,
    bioLock: Boolean,
    apkBeta: Boolean,
    panelUpdate: PanelUpdateCheck?,
    onChangeTheme: (String) -> Unit,
    onToggleBgAlerts: (Boolean) -> Unit,
    onSaveAlertConfig: (intervalSec: Int, everyN: Int, maxPerOutage: Int, onRecovery: Boolean) -> Unit,
    onTestAlert: () -> Unit,
    onToggleBioLock: (Boolean) -> Unit,
    onToggleApkBeta: (Boolean) -> Unit,
    onCheckPanelUpdate: () -> Unit,
    onApplyPanelUpdate: (String) -> Unit,
    onDownloadApk: (source: String, beta: Boolean) -> Unit,
    onOpenWebSection: (String?) -> Unit,
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
                Button(
                    onClick = { onOpenWebSection(null) },
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Icon(Icons.Default.OpenInBrowser, null, Modifier.size(16.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Open Full Web Panel (All Features)", fontSize = 13.sp, fontWeight = FontWeight.Bold)
                }
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

        // Dedicated Outage Notifications Card
        var secInput by remember(alertIntervalSec) { mutableStateOf(alertIntervalSec.toString()) }
        var everyNInput by remember(alertEveryN) { mutableStateOf(alertEveryN.toString()) }
        var maxInput by remember(alertMaxPerOutage) { mutableStateOf(alertMaxPerOutage.toString()) }
        var recSwitch by remember(alertOnRecovery) { mutableStateOf(alertOnRecovery) }

        Card(
            shape = RoundedCornerShape(18.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
            modifier = Modifier.fillMaxWidth()
        ) {
            Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Row(
                    Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween
                ) {
                    Text("🔔 Outage Notifications", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MDesignAccent)
                    TextButton(onClick = onTestAlert, contentPadding = PaddingValues(horizontal = 8.dp, vertical = 2.dp)) {
                        Text("Test Alert", fontSize = 12.sp)
                    }
                }
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                    Column(Modifier.weight(1f)) {
                        Text("Server Outage Push Alerts", fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Text(
                            if (bgAlerts) "Active • Every ${alertIntervalSec}s • Notify every $alertEveryN alert(s)" else "Disabled",
                            fontSize = 11.sp,
                            color = if (bgAlerts) MDesignGreen else MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                    Switch(checked = bgAlerts, onCheckedChange = onToggleBgAlerts)
                }

                HorizontalDivider(color = MaterialTheme.colorScheme.outline.copy(alpha = 0.35f))

                Text("Check & Alert Interval (Seconds)", fontSize = 11.sp, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    listOf(15, 30, 60, 120, 300).forEach { s ->
                        FilterChip(
                            selected = secInput == s.toString(),
                            onClick = {
                                secInput = s.toString()
                                onSaveAlertConfig(s, everyNInput.toIntOrNull() ?: 1, maxInput.toIntOrNull() ?: 0, recSwitch)
                            },
                            label = { Text("${s}s", fontSize = 11.sp) }
                        )
                    }
                }

                Text("Notify Every N Alerts (هر چند الرت خبر بده)", fontSize = 11.sp, fontWeight = FontWeight.Bold, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    listOf(1, 2, 3, 5, 10).forEach { n ->
                        FilterChip(
                            selected = everyNInput == n.toString(),
                            onClick = {
                                everyNInput = n.toString()
                                onSaveAlertConfig(secInput.toIntOrNull() ?: 30, n, maxInput.toIntOrNull() ?: 0, recSwitch)
                            },
                            label = { Text(if (n == 1) "1 (All)" else "Every $n", fontSize = 11.sp) }
                        )
                    }
                }

                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(
                        value = secInput,
                        onValueChange = { secInput = it.filter { c -> c.isDigit() }.take(4) },
                        label = { Text("Seconds", fontSize = 11.sp) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.weight(1f)
                    )
                    OutlinedTextField(
                        value = everyNInput,
                        onValueChange = { everyNInput = it.filter { c -> c.isDigit() }.take(3) },
                        label = { Text("Every N Alerts", fontSize = 11.sp) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.weight(1f)
                    )
                    OutlinedTextField(
                        value = maxInput,
                        onValueChange = { maxInput = it.filter { c -> c.isDigit() }.take(3) },
                        label = { Text("Max (0=∞)", fontSize = 11.sp) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.weight(1f)
                    )
                }

                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                    Column(Modifier.weight(1f)) {
                        Text("Notify on Server Recovery", fontWeight = FontWeight.Bold, fontSize = 13.sp)
                        Text("Send notification when an offline server comes back online", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Switch(checked = recSwitch, onCheckedChange = {
                        recSwitch = it
                        onSaveAlertConfig(secInput.toIntOrNull() ?: 30, everyNInput.toIntOrNull() ?: 1, maxInput.toIntOrNull() ?: 0, it)
                    })
                }

                FilledTonalButton(
                    onClick = {
                        onSaveAlertConfig(
                            (secInput.toIntOrNull() ?: 30).coerceIn(5, 3600),
                            (everyNInput.toIntOrNull() ?: 1).coerceIn(1, 100),
                            (maxInput.toIntOrNull() ?: 0).coerceIn(0, 100),
                            recSwitch
                        )
                    },
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("Save Notification Settings", fontSize = 12.sp, fontWeight = FontWeight.Bold)
                }
            }
        }

        // Security Card
        Card(
            shape = RoundedCornerShape(18.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
            modifier = Modifier.fillMaxWidth()
        ) {
            Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("Security", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MDesignAccent)
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                    Column(Modifier.weight(1f)) {
                        Text("Biometric App Lock", fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Text("Require fingerprint / face unlock on app open", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Switch(checked = bioLock, onCheckedChange = onToggleBioLock)
                }
            }
        }

        // Panel Update & Beta Channel Card
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
                    Text("Panel Updates & Beta Channel", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MDesignAccent)
                    TextButton(onClick = onCheckPanelUpdate, contentPadding = PaddingValues(horizontal = 8.dp, vertical = 2.dp)) {
                        Text("Check Now", fontSize = 12.sp)
                    }
                }
                if (panelUpdate != null) {
                    Text(
                        "Current: v${panelUpdate.currentVersion.ifBlank { "1.8.1.1" }}" +
                            (if (panelUpdate.stableAvailable) " • Stable: v${panelUpdate.stableVersion}" else "") +
                            (if (panelUpdate.betaAvailable) " • Beta: ${panelUpdate.betaTag}" else ""),
                        fontSize = 12.sp,
                        color = if (panelUpdate.betaAvailable || panelUpdate.stableAvailable) MDesignGreen else MaterialTheme.colorScheme.onSurfaceVariant
                    )
                } else {
                    Text("Tap Check Now to inspect Stable & Rolling Beta builds on your panel.", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    FilledTonalButton(
                        onClick = { onApplyPanelUpdate("beta_mirror") },
                        contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                        modifier = Modifier.weight(1f)
                    ) { Text("Beta (Iran)", fontSize = 11.sp) }
                    FilledTonalButton(
                        onClick = { onApplyPanelUpdate("beta") },
                        contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                        modifier = Modifier.weight(1f)
                    ) { Text("Beta (GitHub)", fontSize = 11.sp) }
                }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    OutlinedButton(
                        onClick = { onApplyPanelUpdate("mirror") },
                        contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                        modifier = Modifier.weight(1f)
                    ) { Text("Stable (Iran)", fontSize = 11.sp) }
                    OutlinedButton(
                        onClick = { onApplyPanelUpdate("github") },
                        contentPadding = PaddingValues(horizontal = 6.dp, vertical = 4.dp),
                        modifier = Modifier.weight(1f)
                    ) { Text("Stable (GitHub)", fontSize = 11.sp) }
                }
            }
        }

        // Android App Beta Channel Card
        Card(
            shape = RoundedCornerShape(18.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
            modifier = Modifier.fillMaxWidth()
        ) {
            Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Android App Channel (APK)", fontWeight = FontWeight.Bold, fontSize = 13.sp, color = MDesignAccent)
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                    Column(Modifier.weight(1f)) {
                        Text("Receive Beta APK Builds", fontWeight = FontWeight.Bold, fontSize = 14.sp)
                        Text(
                            if (apkBeta) "Channel: Rolling Beta (beta-latest)" else "Channel: Official Stable",
                            fontSize = 11.sp,
                            color = if (apkBeta) MDesignAmber else MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                    Switch(checked = apkBeta, onCheckedChange = onToggleApkBeta)
                }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(
                        onClick = { onDownloadApk("mirror", apkBeta) },
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 6.dp),
                        modifier = Modifier.weight(1f)
                    ) { Text(if (apkBeta) "Download Beta (Iran)" else "Download APK (Iran)", fontSize = 11.sp) }
                    OutlinedButton(
                        onClick = { onDownloadApk("github", apkBeta) },
                        contentPadding = PaddingValues(horizontal = 8.dp, vertical = 6.dp),
                        modifier = Modifier.weight(1f)
                    ) { Text(if (apkBeta) "Beta (GitHub)" else "APK (GitHub)", fontSize = 11.sp) }
                }
            }
        }

        Text(
            "MControl Android v1.8.1.1 • MDesign",
            fontSize = 11.sp,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.align(Alignment.CenterHorizontally).padding(vertical = 4.dp)
        )
    }
}
