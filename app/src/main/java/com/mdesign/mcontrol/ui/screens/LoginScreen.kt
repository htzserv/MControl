package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.animation.core.*
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.PathMeasure
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.res.painterResource
import com.mdesign.mcontrol.R
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.mdesign.mcontrol.data.MControlApi
import com.mdesign.mcontrol.data.PanelProfile
import com.mdesign.mcontrol.ui.theme.MDesignAccent
import kotlinx.coroutines.launch
import java.net.URI
import java.util.UUID

private data class UrlParts(
    val address: String,
    val port: String,
    val webPath: String
)

private fun parseBaseUrlParts(rawUrl: String?): UrlParts {
    val cleaned = (rawUrl ?: "").trim().trimEnd('/')
    if (cleaned.isEmpty() || cleaned == "http://" || cleaned == "https://") {
        return UrlParts(address = "", port = "", webPath = "")
    }
    return try {
        val withScheme = if (cleaned.startsWith("http://") || cleaned.startsWith("https://")) cleaned else "https://$cleaned"
        val uri = URI(withScheme)
        val scheme = uri.scheme ?: "https"
        val host = uri.host ?: ""
        if (host.isEmpty()) {
            UrlParts(address = cleaned, port = "", webPath = "")
        } else {
            val portStr = if (uri.port > 0) uri.port.toString() else ""
            val pathStr = (uri.path ?: "").trim('/')
            UrlParts(address = "$scheme://$host", port = portStr, webPath = pathStr)
        }
    } catch (_: Exception) {
        UrlParts(address = cleaned, port = "", webPath = "")
    }
}

private fun buildPanelBaseUrl(addressInput: String, portInput: String, webPathInput: String): String {
    var addr = addressInput.trim().trimEnd('/')
    if (addr.isEmpty()) return ""
    if (!addr.startsWith("http://") && !addr.startsWith("https://")) {
        addr = "http://$addr"
    }
    val port = portInput.trim()
    val path = webPathInput.trim().trim('/')
    val baseWithPort = if (port.isNotEmpty()) {
        try {
            val uri = URI(addr)
            if (uri.port <= 0 && !uri.host.isNullOrEmpty()) {
                val hostPart = if (uri.host.contains(":") && !uri.host.startsWith("[")) "[${uri.host}]" else uri.host
                val existingPath = (uri.path ?: "").trim('/')
                val prefix = "${uri.scheme}://$hostPart:$port"
                if (existingPath.isNotEmpty() && path.isEmpty()) "$prefix/$existingPath" else prefix
            } else {
                addr
            }
        } catch (_: Exception) {
            "$addr:$port"
        }
    } else {
        addr
    }
    return if (path.isNotEmpty()) "${baseWithPort.trimEnd('/')}/$path" else baseWithPort.trimEnd('/')
}


private fun easeOutCubic(x: Float): Float {
    val c = x.coerceIn(0f, 1f)
    val inv = 1f - c
    return 1f - inv * inv * inv
}

private fun buildKntrPath(w: Float, h: Float): Path {
    val sx = w / 960f
    val sy = h / 559f
    return Path().apply {
        moveTo(900f * sx, 68f * sy)
        quadraticBezierTo(825f * sx, 108f * sy, 779f * sx, 152f * sy)
        quadraticBezierTo(775f * sx, 175f * sy, 818f * sx, 212f * sy)
        quadraticBezierTo(882f * sx, 265f * sy, 874f * sx, 306f * sy)
        quadraticBezierTo(865f * sx, 354f * sy, 795f * sx, 355f * sy)
        quadraticBezierTo(725f * sx, 355f * sy, 684f * sx, 344f * sy)
        quadraticBezierTo(678f * sx, 300f * sy, 678f * sx, 252f * sy)
        quadraticBezierTo(678f * sx, 302f * sy, 672f * sx, 344f * sy)
        quadraticBezierTo(625f * sx, 348f * sy, 576f * sx, 344f * sy)
        quadraticBezierTo(568f * sx, 298f * sy, 568f * sx, 250f * sy)
        quadraticBezierTo(568f * sx, 300f * sy, 562f * sx, 344f * sy)
        quadraticBezierTo(508f * sx, 348f * sy, 446f * sx, 344f * sy)
        quadraticBezierTo(434f * sx, 298f * sy, 434f * sx, 254f * sy)
        quadraticBezierTo(434f * sx, 320f * sy, 428f * sx, 368f * sy)
        quadraticBezierTo(418f * sx, 422f * sy, 365f * sx, 458f * sy)
        quadraticBezierTo(338f * sx, 476f * sy, 316f * sx, 486f * sy)
    }
}

private fun buildLamPath(w: Float, h: Float): Path {
    val sx = w / 960f
    val sy = h / 559f
    return Path().apply {
        moveTo(288f * sx, 78f * sy)
        lineTo(290f * sx, 235f * sy)
        quadraticBezierTo(290f * sx, 325f * sy, 275f * sx, 368f * sy)
        quadraticBezierTo(250f * sx, 412f * sy, 185f * sx, 414f * sy)
        quadraticBezierTo(112f * sx, 414f * sy, 86f * sx, 360f * sy)
        quadraticBezierTo(74f * sx, 314f * sy, 96f * sx, 268f * sy)
        quadraticBezierTo(112f * sx, 246f * sy, 140f * sx, 245f * sy)
    }
}

private fun buildDotPath(cx: Float, cy: Float, w: Float, h: Float): Path {
    val sx = w / 960f
    val sy = h / 559f
    return Path().apply {
        moveTo((cx + 10f) * sx, (cy - 4f) * sy)
        quadraticBezierTo((cx + 11f) * sx, (cy + 10f) * sy, cx * sx, (cy + 10f) * sy)
        quadraticBezierTo((cx - 11f) * sx, (cy + 10f) * sy, (cx - 10f) * sx, (cy - 2f) * sy)
        quadraticBezierTo((cx - 8f) * sx, (cy - 11f) * sy, (cx + 4f) * sx, (cy - 8f) * sy)
    }
}

@Composable
private fun KontrolAnimatedLogo(modifier: Modifier = Modifier) {
    // Dynamic theme pigment color; drop shadows & 3D studio bevel highlights stay 100% fixed!
    val logoTint = ColorFilter.tint(MaterialTheme.colorScheme.primary)

    val transition = rememberInfiniteTransition(label = "kontrol_pen_3s")
    val t by transition.animateFloat(
        initialValue = 0f,
        targetValue = 3f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 3000, easing = LinearEasing),
            repeatMode = RepeatMode.Restart
        ),
        label = "t"
    )

    val loopAlpha = if (t > 2.70f) ((2.94f - t) / 0.24f).coerceIn(0f, 1f) else 1f
    val kntrProg = easeOutCubic(t / 1.35f)
    val lamProg  = easeOutCubic((t - 1.32f) / 0.63f)
    val d1Prog   = easeOutCubic((t - 1.95f) / 0.18f)
    val d2Prog   = easeOutCubic((t - 2.07f) / 0.18f)
    val d3Prog   = easeOutCubic((t - 2.19f) / 0.18f)

    Box(modifier = modifier.alpha(loopAlpha), contentAlignment = Alignment.Center) {
        // 1. "کنتر" — fixed shadow + dynamic theme pigment + fixed 3D studio bevel/gloss
        if (kntrProg > 0.005f) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }
                    .drawWithContent {
                        drawContent()
                        val full = buildKntrPath(size.width, size.height)
                        val pm = PathMeasure().apply { setPath(full, false) }
                        val seg = Path()
                        pm.getSegment(0f, pm.length * kntrProg, seg, true)
                        drawPath(
                            path = seg,
                            color = Color.White,
                            style = Stroke(
                                width = size.width * (156f / 960f),
                                cap = StrokeCap.Round,
                                join = StrokeJoin.Round
                            ),
                            blendMode = BlendMode.DstIn
                        )
                    }
            ) {
                Image(painter = painterResource(id = R.drawable.logo_kntr_shadow), contentDescription = null, modifier = Modifier.fillMaxSize())
                Image(painter = painterResource(id = R.drawable.logo_kntr_sil), contentDescription = "کنترل", colorFilter = logoTint, modifier = Modifier.fillMaxSize())
                Image(painter = painterResource(id = R.drawable.logo_kntr_shade), contentDescription = null, modifier = Modifier.fillMaxSize())
            }
        }
        // 2. "ل" — fixed shadow + dynamic theme pigment + fixed 3D studio bevel/gloss
        if (lamProg > 0.005f) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }
                    .drawWithContent {
                        drawContent()
                        val full = buildLamPath(size.width, size.height)
                        val pm = PathMeasure().apply { setPath(full, false) }
                        val seg = Path()
                        pm.getSegment(0f, pm.length * lamProg, seg, true)
                        drawPath(
                            path = seg,
                            color = Color.White,
                            style = Stroke(
                                width = size.width * (152f / 960f),
                                cap = StrokeCap.Round,
                                join = StrokeJoin.Round
                            ),
                            blendMode = BlendMode.DstIn
                        )
                    }
            ) {
                Image(painter = painterResource(id = R.drawable.logo_lam_shadow), contentDescription = null, modifier = Modifier.fillMaxSize())
                Image(painter = painterResource(id = R.drawable.logo_lam_sil), contentDescription = null, colorFilter = logoTint, modifier = Modifier.fillMaxSize())
                Image(painter = painterResource(id = R.drawable.logo_lam_shade), contentDescription = null, modifier = Modifier.fillMaxSize())
            }
        }
        // 3. Three dots — fixed shadow + dynamic theme pigment + fixed 3D studio bevel/gloss
        if (d1Prog > 0.005f) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }
                    .drawWithContent {
                        drawContent()
                        val strokeW = size.width * (84f / 960f)
                        val stroke = Stroke(width = strokeW, cap = StrokeCap.Round, join = StrokeJoin.Round)
                        val combined = Path()
                        listOf(
                            Triple(659f, 138f, d1Prog),
                            Triple(567f, 137f, d2Prog),
                            Triple(477f, 137f, d3Prog)
                        ).forEach { (cx, cy, p) ->
                            if (p > 0.005f) {
                                val dp = buildDotPath(cx, cy, size.width, size.height)
                                val pm = PathMeasure().apply { setPath(dp, false) }
                                val seg = Path()
                                pm.getSegment(0f, pm.length * p, seg, true)
                                combined.addPath(seg)
                            }
                        }
                        drawPath(path = combined, color = Color.White, style = stroke, blendMode = BlendMode.DstIn)
                    }
            ) {
                Image(painter = painterResource(id = R.drawable.logo_dots_shadow), contentDescription = null, modifier = Modifier.fillMaxSize())
                Image(painter = painterResource(id = R.drawable.logo_dots_sil), contentDescription = null, colorFilter = logoTint, modifier = Modifier.fillMaxSize())
                Image(painter = painterResource(id = R.drawable.logo_dots_shade), contentDescription = null, modifier = Modifier.fillMaxSize())
            }
        }
    }
}

@Composable
fun LoginScreen(
    savedPanels: List<PanelProfile>,
    initialProfile: PanelProfile?,
    onLoginSuccess: (PanelProfile) -> Unit
) {
    val scope = rememberCoroutineScope()
    val initParts = remember(initialProfile?.id) { parseBaseUrlParts(initialProfile?.baseUrl) }
    var name by remember { mutableStateOf(initialProfile?.name ?: "My MControl") }
    var address by remember { mutableStateOf(initParts.address) }
    var port by remember { mutableStateOf(initParts.port) }
    var webPath by remember { mutableStateOf(initParts.webPath) }
    var username by remember { mutableStateOf(initialProfile?.username ?: "admin") }
    var password by remember { mutableStateOf(initialProfile?.password ?: "") }
    var totpCode by remember { mutableStateOf("") }
    var need2fa by remember { mutableStateOf(false) }
    var trustSsl by remember { mutableStateOf(initialProfile?.trustAllSsl ?: true) }
    var showPass by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var errMsg by remember { mutableStateOf("") }
    var editingId by remember { mutableStateOf(initialProfile?.id ?: UUID.randomUUID().toString()) }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            .verticalScroll(rememberScrollState())
            .padding(22.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        KontrolAnimatedLogo(
            modifier = Modifier.width(260.dp).height(152.dp)
        )

        Spacer(Modifier.height(18.dp))

        if (savedPanels.isNotEmpty()) {
            LazyRow(
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                modifier = Modifier.fillMaxWidth().padding(bottom = 14.dp)
            ) {
                items(savedPanels) { p ->
                    Surface(
                        shape = RoundedCornerShape(99.dp),
                        color = if (p.id == editingId) MaterialTheme.colorScheme.primary.copy(alpha = 0.2f) else MaterialTheme.colorScheme.surface,
                        border = BorderStroke(1.dp, if (p.id == editingId) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outline),
                        modifier = Modifier.clickable {
                            val parts = parseBaseUrlParts(p.baseUrl)
                            editingId = p.id
                            name = p.name
                            address = parts.address
                            port = parts.port
                            webPath = parts.webPath
                            username = p.username
                            password = p.password
                            trustSsl = p.trustAllSsl
                            need2fa = false
                            errMsg = ""
                        }
                    ) {
                        Text(
                            text = p.name,
                            modifier = Modifier.padding(horizontal = 14.dp, vertical = 7.dp),
                            fontSize = 12.sp,
                            fontWeight = FontWeight.Bold
                        )
                    }
                }
            }
        }

        Card(
            shape = RoundedCornerShape(22.dp),
            colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline),
            modifier = Modifier.fillMaxWidth()
        ) {
            Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Panel Name") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    shape = RoundedCornerShape(14.dp)
                )
                OutlinedTextField(
                    value = address,
                    onValueChange = { address = it },
                    label = { Text("Server Address (IP / Domain)") },
                    placeholder = { Text("1.2.3.4 or panel.example.com") },
                    leadingIcon = { Icon(Icons.Default.Public, null) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    shape = RoundedCornerShape(14.dp)
                )
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.spacedBy(10.dp)
                ) {
                    OutlinedTextField(
                        value = port,
                        onValueChange = { v -> port = v.filter { it.isDigit() }.take(5) },
                        label = { Text("Port") },
                        placeholder = { Text("2053") },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.weight(0.42f),
                        shape = RoundedCornerShape(14.dp)
                    )
                    OutlinedTextField(
                        value = webPath,
                        onValueChange = { webPath = it.trim() },
                        label = { Text("Web Path") },
                        placeholder = { Text("mc_panel") },
                        singleLine = true,
                        modifier = Modifier.weight(0.58f),
                        shape = RoundedCornerShape(14.dp)
                    )
                }
                OutlinedTextField(
                    value = username,
                    onValueChange = { username = it },
                    label = { Text("Username") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    shape = RoundedCornerShape(14.dp)
                )
                OutlinedTextField(
                    value = password,
                    onValueChange = { password = it },
                    label = { Text("Password") },
                    leadingIcon = { Icon(Icons.Default.Lock, null) },
                    trailingIcon = {
                        IconButton(onClick = { showPass = !showPass }) {
                            Icon(if (showPass) Icons.Default.VisibilityOff else Icons.Default.Visibility, null)
                        }
                    },
                    visualTransformation = if (showPass) VisualTransformation.None else PasswordVisualTransformation(),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    shape = RoundedCornerShape(14.dp)
                )
                if (need2fa) {
                    OutlinedTextField(
                        value = totpCode,
                        onValueChange = { totpCode = it.take(6) },
                        label = { Text("6-Digit 2FA Code") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth(),
                        shape = RoundedCornerShape(14.dp)
                    )
                }

                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween
                ) {
                    Text("Allow Self-Signed SSL", fontSize = 13.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Switch(checked = trustSsl, onCheckedChange = { trustSsl = it })
                }

                if (errMsg.isNotBlank()) {
                    Text(errMsg, color = MaterialTheme.colorScheme.error, fontSize = 12.sp)
                }

                Button(
                    onClick = {
                        val trimmedAddr = address.trim()
                        if (trimmedAddr.isEmpty() || trimmedAddr == "http://" || trimmedAddr == "https://") {
                            errMsg = "Enter a valid server address (IP or domain)"
                            return@Button
                        }
                        val portInt = port.trim().toIntOrNull()
                        if (port.isNotBlank() && (portInt == null || portInt !in 1..65535)) {
                            errMsg = "Port must be between 1 and 65535"
                            return@Button
                        }
                        val cleanUrl = buildPanelBaseUrl(trimmedAddr, port, webPath)
                        if (!cleanUrl.startsWith("http://") && !cleanUrl.startsWith("https://")) {
                            errMsg = "URL must start with http:// or https://"
                            return@Button
                        }
                        busy = true
                        errMsg = ""
                        scope.launch {
                            val draft = PanelProfile(
                                id = editingId,
                                name = name.ifBlank { "MControl" },
                                baseUrl = cleanUrl,
                                username = username.trim(),
                                password = password,
                                trustAllSsl = trustSsl
                            )
                            val res = MControlApi(draft).login(draft.username, draft.password, totpCode)
                            busy = false
                            if (res.ok && res.token.isNotBlank()) {
                                onLoginSuccess(draft.copy(token = res.token))
                            } else if (res.require2fa) {
                                need2fa = true
                                errMsg = "Enter your 6-digit 2FA code."
                            } else {
                                errMsg = res.error.ifBlank { "Login failed" }
                            }
                        }
                    },
                    enabled = !busy,
                    shape = RoundedCornerShape(14.dp),
                    modifier = Modifier.fillMaxWidth().height(50.dp)
                ) {
                    if (busy) {
                        CircularProgressIndicator(modifier = Modifier.size(20.dp), strokeWidth = 2.dp)
                    } else {
                        Text("Sign In", fontWeight = FontWeight.Bold, fontSize = 15.sp)
                    }
                }
            }
        }
    }
}
