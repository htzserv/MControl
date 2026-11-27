package com.mdesign.mcontrol.ui.screens

import android.annotation.SuppressLint
import android.net.http.SslError
import android.webkit.SslErrorHandler
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.mdesign.mcontrol.data.PanelProfile
import com.mdesign.mcontrol.ui.theme.MDesignAccent
import org.json.JSONObject

/**
 * Full MControl Web Panel embedded inside the Android app with automatic token injection
 * so every Web feature (Domains/Cloudflare/ArvanCloud, Datacenter, Outbound Xray, Telegram Bot,
 * Guard Firewall, File Manager, Topology, Clean IP Scanner, Backup/Restore, Tenants, Theme Studio)
 * works seamlessly without logging in a second time.
 */
@SuppressLint("SetJavaScriptEnabled")
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FullWebPanelDialog(
    profile: PanelProfile,
    initialTab: String? = null,
    onDismiss: () -> Unit
) {
    var webViewRef by remember { mutableStateOf<WebView?>(null) }
    var loadingProgress by remember { mutableIntStateOf(0) }
    var canGoBack by remember { mutableStateOf(false) }

    val quickTabs = remember {
        listOf(
            "domains" to "🌐 Domains",
            "datacenter" to "☁️ Datacenter",
            "settings:outbound" to "⚡ Outbound Xray",
            "settings:telegram" to "🤖 Telegram",
            "settings:ops" to "⏰ Ops & Vault",
            "settings:themes" to "🎨 Themes",
            "tunnels" to "🚇 Tunnels",
            "servers" to "🖥 Servers"
        )
    }

    fun jumpToSection(key: String) {
        val wv = webViewRef ?: return
        val js = if (key.startsWith("settings:")) {
            val sub = key.removePrefix("settings:")
            "if(typeof switchTab==='function'){switchTab('settings');} setTimeout(function(){if(typeof switchSubTab==='function'){switchSubTab('$sub', document.getElementById('stab-$sub'));}},150);"
        } else {
            "if(typeof switchTab==='function'){switchTab('$key');}"
        }
        wv.evaluateJavascript(js, null)
    }

    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false, dismissOnBackPress = true)
    ) {
        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text("${profile.name} — Full Panel", fontSize = 15.sp, fontWeight = FontWeight.Bold) },
                    navigationIcon = {
                        IconButton(onClick = {
                            if (canGoBack) webViewRef?.goBack() else onDismiss()
                        }) {
                            Icon(if (canGoBack) Icons.Default.ArrowBack else Icons.Default.Close, contentDescription = "Back")
                        }
                    },
                    actions = {
                        IconButton(onClick = { webViewRef?.reload() }) {
                            Icon(Icons.Default.Refresh, contentDescription = "Reload", tint = MDesignAccent)
                        }
                        IconButton(onClick = onDismiss) {
                            Icon(Icons.Default.Close, contentDescription = "Close")
                        }
                    },
                    colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.surface)
                )
            }
        ) { pad ->
            Column(Modifier.fillMaxSize().padding(pad)) {
                LazyRow(
                    modifier = Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 6.dp),
                    horizontalArrangement = Arrangement.spacedBy(6.dp)
                ) {
                    items(quickTabs) { (k, lbl) ->
                        AssistChip(
                            onClick = { jumpToSection(k) },
                            label = { Text(lbl, fontSize = 11.sp) }
                        )
                    }
                }
                if (loadingProgress in 1..99) {
                    LinearProgressIndicator(
                        progress = { loadingProgress / 100f },
                        color = MDesignAccent,
                        modifier = Modifier.fillMaxWidth().height(3.dp)
                    )
                }
                AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { ctx ->
                        WebView(ctx).apply {
                            settings.javaScriptEnabled = true
                            settings.domStorageEnabled = true
                            settings.databaseEnabled = true
                            settings.useWideViewPort = true
                            settings.loadWithOverviewMode = true
                            settings.mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
                            settings.setSupportZoom(true)
                            settings.builtInZoomControls = true
                            settings.displayZoomControls = false

                            webChromeClient = object : WebChromeClient() {
                                override fun onProgressChanged(view: WebView?, newProgress: Int) {
                                    loadingProgress = newProgress
                                }
                            }
                            webViewClient = object : WebViewClient() {
                                private var injected = false

                                @SuppressLint("WebViewClientOnReceivedSslError")
                                override fun onReceivedSslError(view: WebView?, handler: SslErrorHandler?, error: SslError?) {
                                    if (profile.trustAllSsl) handler?.proceed() else handler?.cancel()
                                }

                                override fun onPageFinished(view: WebView?, url: String?) {
                                    canGoBack = view?.canGoBack() == true
                                    if (!injected && profile.token.isNotBlank()) {
                                        injected = true
                                        val quotedTok = JSONObject.quote(profile.token)
                                        val initJs = buildString {
                                            append("try{")
                                            append("var cur=localStorage.getItem('mc_token');")
                                            append("if(cur!==$quotedTok){localStorage.setItem('mc_token',$quotedTok);location.reload();}")
                                            if (!initialTab.isNullOrBlank()) {
                                                append("else{setTimeout(function(){")
                                                if (initialTab.startsWith("settings:")) {
                                                    val sub = initialTab.removePrefix("settings:")
                                                    append("if(typeof switchTab==='function')switchTab('settings');")
                                                    append("setTimeout(function(){if(typeof switchSubTab==='function')switchSubTab('$sub',document.getElementById('stab-$sub'));},200);")
                                                } else {
                                                    append("if(typeof switchTab==='function')switchTab('$initialTab');")
                                                }
                                                append("},350);}")
                                            }
                                            append("}catch(e){}")
                                        }
                                        view?.evaluateJavascript(initJs, null)
                                    }
                                }
                            }
                            val targetUrl = profile.baseUrl.trimEnd('/') + "/"
                            loadUrl(targetUrl)
                            webViewRef = this
                        }
                    }
                )
            }
        }
    }
}
