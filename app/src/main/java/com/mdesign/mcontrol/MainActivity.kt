package com.mdesign.mcontrol

import android.Manifest
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.widget.Toast
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Dashboard
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.MonitorHeart
import androidx.compose.material.icons.filled.OpenInBrowser
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import com.mdesign.mcontrol.data.*
import com.mdesign.mcontrol.ui.components.MControlAnimatedLogo
import com.mdesign.mcontrol.ui.screens.*
import com.mdesign.mcontrol.ui.theme.MDesignAccent
import com.mdesign.mcontrol.ui.theme.MDesignTheme
import com.mdesign.mcontrol.worker.OutageCheckWorker
import com.mdesign.mcontrol.worker.OutageMonitorService
import kotlinx.coroutines.launch

class MainActivity : FragmentActivity() {

    private val notifPermLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val store = PanelStore(this)

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            notifPermLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }

        setContent {
            var themeMode by remember { mutableStateOf(store.getThemeMode()) }
            var unlocked by remember { mutableStateOf(!store.isBiometricLockEnabled()) }

            LaunchedEffect(Unit) {
                if (!unlocked) {
                    promptBiometric { ok -> unlocked = ok }
                }
            }

            MDesignTheme(themeMode = themeMode) {
                if (!unlocked) {
                    Surface(Modifier.fillMaxSize()) {
                        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
                                MControlAnimatedLogo(modifier = Modifier.size(140.dp))
                                Button(onClick = { promptBiometric { ok -> unlocked = ok } }) {
                                    Text("Unlock MControl")
                                }
                            }
                        }
                    }
                } else {
                    MControlApp(
                        store = store,
                        themeMode = themeMode,
                        onThemeChange = {
                            themeMode = it
                            store.setThemeMode(it)
                        }
                    )
                }
            }
        }
    }

    private fun promptBiometric(onResult: (Boolean) -> Unit) {
        val bm = BiometricManager.from(this)
        val can = bm.canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_WEAK or BiometricManager.Authenticators.DEVICE_CREDENTIAL)
        if (can != BiometricManager.BIOMETRIC_SUCCESS) {
            onResult(true)
            return
        }
        val prompt = BiometricPrompt(
            this,
            ContextCompat.getMainExecutor(this),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) = onResult(true)
                override fun onAuthenticationError(errorCode: Int, errString: CharSequence) = onResult(false)
            }
        )
        val info = BiometricPrompt.PromptInfo.Builder()
            .setTitle("Unlock MControl")
            .setSubtitle("Authenticate to access your servers")
            .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_WEAK or BiometricManager.Authenticators.DEVICE_CREDENTIAL)
            .build()
        prompt.authenticate(info)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MControlApp(
    store: PanelStore,
    themeMode: String,
    onThemeChange: (String) -> Unit
) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()

    var panels by remember { mutableStateOf(store.getPanels()) }
    var activePanel by remember { mutableStateOf(store.getActivePanel()) }
    var showLogin by remember { mutableStateOf(activePanel == null || activePanel!!.token.isBlank()) }

    var tab by remember { mutableIntStateOf(0) }
    var loading by remember { mutableStateOf(false) }
    var localStats by remember { mutableStateOf(LocalStats()) }
    var servers by remember { mutableStateOf<List<ServerItem>>(emptyList()) }
    var tunnels by remember { mutableStateOf<List<TunnelItem>>(emptyList()) }
    var metricsMap by remember { mutableStateOf<Map<Long, TunnelMetrics>>(emptyMap()) }
    var monitors by remember { mutableStateOf<List<MonitorItem>>(emptyList()) }
    var benchJob by remember { mutableStateOf(BenchJobState()) }

    var bgAlerts by remember { mutableStateOf(store.isBgAlertsEnabled()) }
    var alertIntervalSec by remember { mutableIntStateOf(store.getAlertIntervalSec()) }
    var alertEveryN by remember { mutableIntStateOf(store.getAlertEveryN()) }
    var alertMaxPerOutage by remember { mutableIntStateOf(store.getAlertMaxPerOutage()) }
    var alertOnRecovery by remember { mutableStateOf(store.isAlertOnRecovery()) }
    var bioLock by remember { mutableStateOf(store.isBiometricLockEnabled()) }
    var apkBeta by remember { mutableStateOf(store.isApkBetaChannel()) }
    var panelUpdate by remember { mutableStateOf<PanelUpdateCheck?>(null) }

    var termTarget by remember { mutableStateOf<Pair<String, Long?>?>(null) }
    var webPanelOpen by remember { mutableStateOf(false) }
    var webPanelTab by remember { mutableStateOf<String?>(null) }

    val api = remember(activePanel) { activePanel?.let { MControlApi(it) } }

    fun showToast(msg: String) {
        Toast.makeText(ctx, msg, Toast.LENGTH_SHORT).show()
    }

    fun openWeb(section: String?) {
        webPanelTab = section
        webPanelOpen = true
    }

    fun refreshAll() {
        val client = api ?: return
        loading = true
        scope.launch {
            client.getLocalStats().onSuccess { localStats = it }
            client.getServers().onSuccess { servers = it }
            client.getTunnels().onSuccess { tunnels = it }
            client.getMonitors().onSuccess { monitors = it }
            loading = false
        }
    }

    LaunchedEffect(activePanel?.id, activePanel?.token) {
        if (!showLogin && activePanel != null) {
            refreshAll()
            if (bgAlerts) {
                OutageMonitorService.sync(ctx, true)
                OutageCheckWorker.schedule(ctx, true)
            }
        }
    }

    if (showLogin) {
        LoginScreen(
            savedPanels = panels,
            initialProfile = activePanel,
            onLoginSuccess = { prof ->
                store.upsertPanel(prof)
                panels = store.getPanels()
                activePanel = prof
                showLogin = false
                showToast("Connected to ${prof.name}")
            }
        )
        return
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        MControlAnimatedLogo(modifier = Modifier.size(32.dp))
                        Text(activePanel?.name ?: "MControl", fontSize = 17.sp)
                    }
                },
                actions = {
                    TextButton(onClick = { openWeb(null) }) {
                        Icon(Icons.Default.OpenInBrowser, null, Modifier.size(18.dp), tint = MDesignAccent)
                        Spacer(Modifier.width(4.dp))
                        Text("Full Panel", fontSize = 12.sp, color = MDesignAccent)
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface
                )
            )
        },
        bottomBar = {
            NavigationBar(containerColor = MaterialTheme.colorScheme.surface) {
                NavigationBarItem(selected = tab == 0, onClick = { tab = 0 }, icon = { Icon(Icons.Default.Dashboard, null) }, label = { Text("Overview") })
                NavigationBarItem(selected = tab == 1, onClick = { tab = 1 }, icon = { Icon(Icons.Default.Dns, null) }, label = { Text("Servers") })
                NavigationBarItem(selected = tab == 2, onClick = { tab = 2 }, icon = { Icon(Icons.Default.Hub, null) }, label = { Text("Tunnels") })
                NavigationBarItem(selected = tab == 3, onClick = { tab = 3 }, icon = { Icon(Icons.Default.MonitorHeart, null) }, label = { Text("Uptime") })
                NavigationBarItem(selected = tab == 4, onClick = { tab = 4 }, icon = { Icon(Icons.Default.Settings, null) }, label = { Text("Settings") })
            }
        }
    ) { pad ->
        Box(Modifier.fillMaxSize().padding(pad)) {
            when (tab) {
                0 -> OverviewScreen(
                    stats = localStats,
                    servers = servers,
                    loading = loading,
                    onRefresh = { refreshAll() },
                    onCleanRam = {
                        scope.launch {
                            val (_, msg) = api?.cleanMasterRam() ?: (false to "Offline")
                            showToast(msg)
                            refreshAll()
                        }
                    },
                    onRestartPanel = {
                        scope.launch {
                            val (_, msg) = api?.restartMasterPanel() ?: (false to "Offline")
                            showToast(msg)
                        }
                    },
                    onOpenMasterTerminal = { termTarget = "Master Server" to null },
                    onOpenWebSection = { openWeb(it) }
                )
                1 -> ServersScreen(
                    servers = servers,
                    onRefreshAll = {
                        scope.launch {
                            api?.probeAllServers()
                            api?.getServers()?.onSuccess { servers = it }
                            showToast("Servers refreshed")
                        }
                    },
                    onPingAll = {
                        scope.launch {
                            val (_, msg) = api?.pingAllServers() ?: (false to "Offline")
                            showToast(msg)
                            api?.getServers()?.onSuccess { servers = it }
                        }
                    },
                    onAddServer = { name, host, port, user, pass, useOb ->
                        scope.launch {
                            showToast("Adding server...")
                            val (ok, msg) = api?.addServer(name, host, port, user, pass, useOb) ?: (false to "Offline")
                            showToast(msg)
                            if (ok) api?.getServers()?.onSuccess { servers = it }
                        }
                    },
                    onDeleteServer = { id, mode ->
                        scope.launch {
                            val (_, msg) = api?.deleteServer(id, mode) ?: (false to "Offline")
                            showToast(msg)
                            api?.getServers()?.onSuccess { servers = it }
                        }
                    },
                    onPingServer = { id ->
                        scope.launch {
                            val (_, msg) = api?.pingServer(id) ?: (false to "Offline")
                            showToast(msg)
                            api?.getServers()?.onSuccess { servers = it }
                        }
                    },
                    onProbeServer = { id ->
                        scope.launch {
                            val (_, msg) = api?.probeServer(id) ?: (false to "Offline")
                            showToast(msg)
                            api?.getServers()?.onSuccess { servers = it }
                        }
                    },
                    onTweakServer = { id, act ->
                        scope.launch {
                            val (_, msg) = api?.serverTweak(id, act) ?: (false to "Offline")
                            showToast(msg)
                        }
                    },
                    onOpenTerminal = { srv -> termTarget = srv.name to srv.id }
                )
                2 -> TunnelsScreen(
                    tunnels = tunnels,
                    servers = servers,
                    metricsMap = metricsMap,
                    benchJob = benchJob,
                    onRefresh = { scope.launch { api?.getTunnels()?.onSuccess { tunnels = it } } },
                    onCheckHealth = { id ->
                        scope.launch {
                            api?.getTunnelMetrics(id)?.onSuccess { m ->
                                metricsMap = metricsMap + (id to m)
                            }?.onFailure { showToast(it.message ?: "Failed") }
                        }
                    },
                    onRotatePort = { id ->
                        scope.launch {
                            val (_, msg) = api?.rotateTunnelPort(id) ?: (false to "Offline")
                            showToast(msg)
                            api?.getTunnels()?.onSuccess { tunnels = it }
                        }
                    },
                    onAction = { id, act ->
                        scope.launch {
                            val (_, msg) = api?.tunnelAction(id, act) ?: (false to "Offline")
                            showToast(msg)
                            api?.getTunnels()?.onSuccess { tunnels = it }
                        }
                    },
                    onSpeedtest = { id ->
                        scope.launch {
                            showToast("Running in-tunnel speedtest...")
                            val (_, out) = api?.tunnelSpeedtest(id) ?: (false to "Offline")
                            showToast(out.lines().lastOrNull { it.isNotBlank() } ?: out)
                        }
                    },
                    onDeleteTunnel = { id ->
                        scope.launch {
                            val (_, msg) = api?.deleteTunnel(id) ?: (false to "Offline")
                            showToast(msg)
                            api?.getTunnels()?.onSuccess { tunnels = it }
                        }
                    },
                    onCreateTunnel = { name, eng, tr, irId, frId, tPort, ports ->
                        scope.launch {
                            showToast("Deploying tunnel...")
                            val (ok, msg) = api?.createTunnel(name, eng, tr, irId, frId, tPort, ports) ?: (false to "Offline")
                            showToast(msg)
                            if (ok) api?.getTunnels()?.onSuccess { tunnels = it }
                        }
                    },
                    onStartBenchmark = { irId, frId, prof ->
                        scope.launch {
                            val (ok, msg) = api?.startBenchmark(irId, frId, prof) ?: (false to "Offline")
                            showToast(msg)
                            if (ok) api?.getBenchmarkStatus()?.onSuccess { benchJob = it }
                        }
                    },
                    onPollBenchmark = {
                        scope.launch {
                            api?.getBenchmarkStatus()?.onSuccess { benchJob = it }
                        }
                    }
                )
                3 -> UptimeScreen(
                    monitors = monitors,
                    onRefresh = { scope.launch { api?.getMonitors()?.onSuccess { monitors = it } } },
                    onCheckNow = { id ->
                        scope.launch {
                            val (_, msg) = api?.checkMonitorNow(id) ?: (false to "Offline")
                            showToast(msg)
                            api?.getMonitors()?.onSuccess { monitors = it }
                        }
                    },
                    onAddMonitor = { name, type, target, interval ->
                        scope.launch {
                            val (ok, msg) = api?.createMonitor(name, type, target, interval) ?: (false to "Offline")
                            showToast(msg)
                            if (ok) api?.getMonitors()?.onSuccess { monitors = it }
                        }
                    },
                    onDeleteMonitor = { id ->
                        scope.launch {
                            val (_, msg) = api?.deleteMonitor(id) ?: (false to "Offline")
                            showToast(msg)
                            api?.getMonitors()?.onSuccess { monitors = it }
                        }
                    },
                    onImportMonitors = { kind ->
                        scope.launch {
                            val (_, msg) = api?.importMonitors(kind) ?: (false to "Offline")
                            showToast(msg)
                            api?.getMonitors()?.onSuccess { monitors = it }
                        }
                    }
                )
                4 -> SettingsScreen(
                    activePanel = activePanel,
                    themeMode = themeMode,
                    bgAlerts = bgAlerts,
                    alertIntervalSec = alertIntervalSec,
                    alertEveryN = alertEveryN,
                    alertMaxPerOutage = alertMaxPerOutage,
                    alertOnRecovery = alertOnRecovery,
                    bioLock = bioLock,
                    apkBeta = apkBeta,
                    panelUpdate = panelUpdate,
                    onChangeTheme = onThemeChange,
                    onToggleBgAlerts = { on ->
                        bgAlerts = on
                        store.setBgAlertsEnabled(on)
                        OutageMonitorService.sync(ctx, on)
                        OutageCheckWorker.schedule(ctx, on)
                        showToast(if (on) "Live alerts active (every ${alertIntervalSec}s, every $alertEveryN alert)" else "Outage alerts disabled")
                    },
                    onSaveAlertConfig = { sec, everyN, maxPer, onRec ->
                        alertIntervalSec = sec
                        alertEveryN = everyN
                        alertMaxPerOutage = maxPer
                        alertOnRecovery = onRec
                        store.setAlertIntervalSec(sec)
                        store.setAlertEveryN(everyN)
                        store.setAlertMaxPerOutage(maxPer)
                        store.setAlertOnRecovery(onRec)
                        if (bgAlerts) OutageMonitorService.sync(ctx, true)
                        showToast("Saved: every ${sec}s • every $everyN alert(s)")
                    },
                    onTestAlert = {
                        OutageMonitorService.sendTestNotification(ctx, alertIntervalSec, alertEveryN)
                        showToast("Test notification sent")
                    },
                    onToggleBioLock = { on ->
                        bioLock = on
                        store.setBiometricLockEnabled(on)
                    },
                    onToggleApkBeta = { on ->
                        apkBeta = on
                        store.setApkBetaChannel(on)
                        showToast(if (on) "Switched to Rolling Beta APK channel" else "Switched to Stable APK channel")
                    },
                    onCheckPanelUpdate = {
                        scope.launch {
                            showToast("Checking Stable & Beta updates...")
                            api?.checkPanelUpdate()?.onSuccess {
                                panelUpdate = it
                                showToast(
                                    when {
                                        it.betaAvailable -> "Beta update available: ${it.betaTag}"
                                        it.stableAvailable -> "Stable update available: v${it.stableVersion}"
                                        else -> "Panel v${it.currentVersion} is up to date"
                                    }
                                )
                            }?.onFailure { showToast(it.message ?: "Check failed") }
                        }
                    },
                    onApplyPanelUpdate = { src ->
                        scope.launch {
                            val (_, msg) = api?.applyPanelUpdate(src) ?: (false to "Offline")
                            showToast(msg)
                        }
                    },
                    onDownloadApk = { src, beta ->
                        val url = when {
                            src == "mirror" && beta -> "https://c107328.parspack.net/c107328/MControl/beta-latest/MControl-Android-beta.apk"
                            src == "mirror" -> "https://c107328.parspack.net/c107328/MControl/MControl-Android-latest.apk"
                            beta -> "https://github.com/htzserv/MControl/releases/download/beta-latest/MControl-Android-beta.apk"
                            else -> "https://github.com/htzserv/MControl/releases/latest/download/MControl-Android-latest.apk"
                        }
                        try {
                            ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
                        } catch (e: Exception) {
                            showToast("Cannot open browser: ${e.message}")
                        }
                    },
                    onOpenWebSection = { openWeb(it) },
                    onSwitchPanel = { showLogin = true },
                    onLogout = {
                        activePanel?.let { p ->
                            store.upsertPanel(p.copy(token = ""))
                            activePanel = p.copy(token = "")
                        }
                        showLogin = true
                    }
                )
            }
        }
    }

    termTarget?.let { (title, srvId) ->
        TerminalDialog(
            title = "Terminal — $title",
            onRunCommand = { cmd -> api?.execCommand(srvId, cmd) ?: (false to "Not connected") },
            onDismiss = { termTarget = null }
        )
    }

    if (webPanelOpen && activePanel != null) {
        FullWebPanelDialog(
            profile = activePanel!!,
            initialTab = webPanelTab,
            onDismiss = {
                webPanelOpen = false
                refreshAll()
            }
        )
    }
}
