package com.mdesign.mcontrol.ui.screens

import android.annotation.SuppressLint
import android.app.DownloadManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.net.http.SslError
import android.os.Environment
import android.webkit.CookieManager
import android.webkit.JsPromptResult
import android.webkit.JsResult
import android.webkit.SslErrorHandler
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Toast
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Fullscreen
import androidx.compose.material.icons.filled.FullscreenExit
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.mdesign.mcontrol.data.PanelProfile
import com.mdesign.mcontrol.ui.theme.MDesignAccent
import org.json.JSONObject

private sealed class JsDialogState {
    data class Alert(val message: String, val result: JsResult) : JsDialogState()
    data class Confirm(val message: String, val result: JsResult) : JsDialogState()
    data class Prompt(val message: String, val defaultVal: String, val result: JsPromptResult) : JsDialogState()
}

/**
 * Complete MControl Web App inside Android with:
 * - Automatic mc_token injection & self-signed SSL support
 * - Native hardware Back button navigation
 * - Full File Upload (Backup .zip restore, Brand logo, File Manager)
 * - Full File Download via Android DownloadManager
 * - Native Material 3 JS Alert / Confirm / Prompt dialogs
 * - Collapsible quick-nav chips for all panel modules (including Update & Beta Center)
 */
@SuppressLint("SetJavaScriptEnabled")
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FullWebPanelDialog(
    profile: PanelProfile,
    initialTab: String? = null,
    onDismiss: () -> Unit
) {
    val ctx = LocalContext.current
    var webViewRef by remember { mutableStateOf<WebView?>(null) }
    var loadingProgress by remember { mutableIntStateOf(0) }
    var canGoBack by remember { mutableStateOf(false) }
    var showQuickBar by remember { mutableStateOf(true) }
    var jsDialog by remember { mutableStateOf<JsDialogState?>(null) }
    var filePathCallback by remember { mutableStateOf<ValueCallback<Array<Uri>>?>(null) }

    val filePicker = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.GetContent()
    ) { uri: Uri? ->
        val cb = filePathCallback
        filePathCallback = null
        if (cb != null) {
            cb.onReceiveValue(if (uri != null) arrayOf(uri) else null)
        }
    }

    val quickTabs = remember {
        listOf(
            "overview" to "📊 Overview",
            "servers" to "🖥 Servers",
            "tunnels" to "🚇 Tunnels",
            "monitor" to "💓 Uptime",
            "domains" to "🌐 Domains",
            "datacenter" to "☁️ Datacenter",
            "settings:outbound" to "⚡ Outbound",
            "settings:telegram" to "🤖 Telegram",
            "settings:ops" to "⏰ Ops & Vault",
            "settings:profiles" to "📦 Backup",
            "settings:themes" to "🎨 Themes",
            "settings:update" to "🚀 Updates / Beta"
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
        BackHandler(enabled = canGoBack) {
            webViewRef?.goBack()
        }

        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text("${profile.name} — Web App", fontSize = 15.sp, fontWeight = FontWeight.Bold) },
                    navigationIcon = {
                        IconButton(onClick = {
                            if (canGoBack) webViewRef?.goBack() else onDismiss()
                        }) {
                            Icon(if (canGoBack) Icons.Default.ArrowBack else Icons.Default.Close, contentDescription = "Back")
                        }
                    },
                    actions = {
                        IconButton(onClick = { showQuickBar = !showQuickBar }) {
                            Icon(
                                if (showQuickBar) Icons.Default.Fullscreen else Icons.Default.FullscreenExit,
                                contentDescription = "Toggle Quick Bar",
                                tint = MDesignAccent
                            )
                        }
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
                if (showQuickBar) {
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
                    factory = { context ->
                        WebView(context).apply {
                            CookieManager.getInstance().setAcceptCookie(true)
                            CookieManager.getInstance().setAcceptThirdPartyCookies(this, true)
                            settings.javaScriptEnabled = true
                            settings.domStorageEnabled = true
                            settings.databaseEnabled = true
                            settings.allowFileAccess = true
                            settings.allowContentAccess = true
                            settings.javaScriptCanOpenWindowsAutomatically = true
                            settings.useWideViewPort = true
                            settings.loadWithOverviewMode = true
                            settings.mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
                            settings.setSupportZoom(true)
                            settings.builtInZoomControls = true
                            settings.displayZoomControls = false

                            setDownloadListener { url, userAgent, contentDisposition, mimeType, _ ->
                                try {
                                    if (url.startsWith("http://") || url.startsWith("https://")) {
                                        val dm = context.getSystemService(Context.DOWNLOAD_SERVICE) as DownloadManager
                                        val req = DownloadManager.Request(Uri.parse(url))
                                        val fname = URLUtil.guessFileName(url, contentDisposition, mimeType)
                                        req.setMimeType(mimeType)
                                        val cookies = CookieManager.getInstance().getCookie(url)
                                        if (!cookies.isNullOrBlank()) req.addRequestHeader("Cookie", cookies)
                                        if (!userAgent.isNullOrBlank()) req.addRequestHeader("User-Agent", userAgent)
                                        if (profile.token.isNotBlank()) req.addRequestHeader("X-MC-Token", profile.token)
                                        req.setTitle(fname)
                                        req.setDescription("Downloading from MControl...")
                                        req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                                        req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, fname)
                                        dm.enqueue(req)
                                        Toast.makeText(context, "Downloading $fname...", Toast.LENGTH_SHORT).show()
                                    } else {
                                        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
                                    }
                                } catch (e: Exception) {
                                    Toast.makeText(context, "Download error: ${e.message}", Toast.LENGTH_SHORT).show()
                                }
                            }

                            webChromeClient = object : WebChromeClient() {
                                override fun onProgressChanged(view: WebView?, newProgress: Int) {
                                    loadingProgress = newProgress
                                }

                                override fun onShowFileChooser(
                                    webView: WebView?,
                                    callback: ValueCallback<Array<Uri>>?,
                                    fileChooserParams: FileChooserParams?
                                ): Boolean {
                                    filePathCallback?.onReceiveValue(null)
                                    filePathCallback = callback
                                    val accept = fileChooserParams?.acceptTypes?.firstOrNull { it.isNotBlank() } ?: "*/*"
                                    filePicker.launch(if (accept.contains("/")) accept else "*/*")
                                    return true
                                }

                                override fun onJsAlert(view: WebView?, url: String?, message: String?, result: JsResult?): Boolean {
                                    if (result != null) {
                                        jsDialog = JsDialogState.Alert(message ?: "", result)
                                        return true
                                    }
                                    return super.onJsAlert(view, url, message, result)
                                }

                                override fun onJsConfirm(view: WebView?, url: String?, message: String?, result: JsResult?): Boolean {
                                    if (result != null) {
                                        jsDialog = JsDialogState.Confirm(message ?: "", result)
                                        return true
                                    }
                                    return super.onJsConfirm(view, url, message, result)
                                }

                                override fun onJsPrompt(
                                    view: WebView?,
                                    url: String?,
                                    message: String?,
                                    defaultValue: String?,
                                    result: JsPromptResult?
                                ): Boolean {
                                    if (result != null) {
                                        jsDialog = JsDialogState.Prompt(message ?: "", defaultValue ?: "", result)
                                        return true
                                    }
                                    return super.onJsPrompt(view, url, message, defaultValue, result)
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
                            val targetUrl = profile.baseUrl.trimEnd('/') + "/?source=android_webapp"
                            loadUrl(targetUrl)
                            webViewRef = this
                        }
                    }
                )
            }
        }

        // Native Material 3 JS Dialogs (Alert / Confirm / Prompt)
        when (val dlg = jsDialog) {
            is JsDialogState.Alert -> {
                AlertDialog(
                    onDismissRequest = { dlg.result.confirm(); jsDialog = null },
                    title = { Text("MControl", fontWeight = FontWeight.Bold) },
                    text = { Text(dlg.message) },
                    confirmButton = {
                        Button(onClick = { dlg.result.confirm(); jsDialog = null }) { Text("OK") }
                    }
                )
            }
            is JsDialogState.Confirm -> {
                AlertDialog(
                    onDismissRequest = { dlg.result.cancel(); jsDialog = null },
                    title = { Text("Confirm", fontWeight = FontWeight.Bold) },
                    text = { Text(dlg.message) },
                    confirmButton = {
                        Button(onClick = { dlg.result.confirm(); jsDialog = null }) { Text("Confirm") }
                    },
                    dismissButton = {
                        TextButton(onClick = { dlg.result.cancel(); jsDialog = null }) { Text("Cancel") }
                    }
                )
            }
            is JsDialogState.Prompt -> {
                var inputVal by remember(dlg) { mutableStateOf(dlg.defaultVal) }
                AlertDialog(
                    onDismissRequest = { dlg.result.cancel(); jsDialog = null },
                    title = { Text("Input", fontWeight = FontWeight.Bold) },
                    text = {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Text(dlg.message)
                            OutlinedTextField(
                                value = inputVal,
                                onValueChange = { inputVal = it },
                                singleLine = true,
                                modifier = Modifier.fillMaxWidth()
                            )
                        }
                    },
                    confirmButton = {
                        Button(onClick = { dlg.result.confirm(inputVal); jsDialog = null }) { Text("OK") }
                    },
                    dismissButton = {
                        TextButton(onClick = { dlg.result.cancel(); jsDialog = null }) { Text("Cancel") }
                    }
                )
            }
            null -> {}
        }
    }
}
