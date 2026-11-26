package com.mdesign.mcontrol

import android.Manifest
import android.os.Build
import android.os.Bundle
import android.widget.Toast
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Dashboard
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.MonitorHeart
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import com.mdesign.mcontrol.data.*
import com.mdesign.mcontrol.ui.screens.*
import com.mdesign.mcontrol.ui.theme.MDesignTheme
import com.mdesign.mcontrol.worker.OutageCheckWorker
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
                        Box(Modifier.fillMaxSize(), contentAlignment = androidx.compose.ui.Alignment.Center) {
                            Button(onClick = { promptBiometric { ok -> unlocked = ok } }) {
                                Text("Unlock MControl")
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

    var bgAlerts by remember { mutableStateOf(store.isBgAlertsEnabled()) }
    var bioLock by remember { mutableStateOf(store.isBiometricLockEnabled()) }

    var termTarget by remember { mutableStateOf<Pair<String, Long?>?>(null) }

    val api = remember(activePanel) { activePanel?.let { MControlApi(it) } }

    fun showToast(msg: String) {
        Toast.makeText(ctx, msg, Toast.LENGTH_SHORT).show()
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
                title = { Text(activePanel?.name ?: "MControl") },
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
                    onOpenMasterTerminal = { termTarget = "Master Server" to null }
                )
                1 -> ServersScreen(
                    servers = servers,
                    onRefreshAll = { refreshAll() },
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
                    metricsMap = metricsMap,
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
                    }
                )
                4 -> SettingsScreen(
                    activePanel = activePanel,
                    themeMode = themeMode,
                    bgAlerts = bgAlerts,
                    bioLock = bioLock,
                    onChangeTheme = onThemeChange,
                    onToggleBgAlerts = { on ->
                        bgAlerts = on
                        store.setBgAlertsEnabled(on)
                        OutageCheckWorker.schedule(ctx, on)
                        showToast(if (on) "Background alerts enabled (15m)" else "Background alerts disabled")
                    },
                    onToggleBioLock = { on ->
                        bioLock = on
                        store.setBiometricLockEnabled(on)
                    },
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
}
