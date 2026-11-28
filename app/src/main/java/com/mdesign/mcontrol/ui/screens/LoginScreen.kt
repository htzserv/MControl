package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.animation.core.*
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.PathMeasure
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.luminance
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
import com.mdesign.mcontrol.ui.components.MControlAnimatedLogo
import com.mdesign.mcontrol.ui.components.MControlWordmark
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
    val sx = w / 656f
    val sy = h / 370f
    return Path().apply {
        moveTo(606f * sx, 48f * sy)
        quadraticTo(562f * sx, 68f * sy, 529f * sx, 98f * sy)
        quadraticTo(526f * sx, 118f * sy, 566f * sx, 146f * sy)
        quadraticTo(598f * sx, 178f * sy, 594f * sx, 204f * sy)
        quadraticTo(588f * sx, 236f * sy, 535f * sx, 235f * sy)
        quadraticTo(488f * sx, 234f * sy, 464f * sx, 224f * sy)
        quadraticTo(460f * sx, 194f * sy, 460f * sx, 162f * sy)
        quadraticTo(460f * sx, 196f * sy, 456f * sx, 226f * sy)
        quadraticTo(418f * sx, 231f * sy, 389f * sx, 226f * sy)
        quadraticTo(384f * sx, 195f * sy, 384f * sx, 165f * sy)
        quadraticTo(384f * sx, 198f * sy, 380f * sx, 227f * sy)
        quadraticTo(340f * sx, 232f * sy, 300f * sx, 228f * sy)
        quadraticTo(292f * sx, 196f * sy, 292f * sx, 170f * sy)
        quadraticTo(292f * sx, 214f * sy, 286f * sx, 248f * sy)
        quadraticTo(274f * sx, 288f * sy, 242f * sx, 310f * sy)
        quadraticTo(228f * sx, 318f * sy, 218f * sx, 322f * sy)
    }
}

private fun buildLamPath(w: Float, h: Float): Path {
    val sx = w / 656f
    val sy = h / 370f
    return Path().apply {
        moveTo(192f * sx, 52f * sy)
        lineTo(193f * sx, 168f * sy)
        quadraticTo(193f * sx, 218f * sy, 182f * sx, 246f * sy)
        quadraticTo(162f * sx, 276f * sy, 114f * sx, 277f * sy)
        quadraticTo(66f * sx, 274f * sy, 52f * sx, 230f * sy)
        quadraticTo(47f * sx, 196f * sy, 65f * sx, 168f * sy)
        quadraticTo(74f * sx, 156f * sy, 86f * sx, 155f * sy)
    }
}

private fun buildDotPath(cx: Float, cy: Float, w: Float, h: Float): Path {
    val sx = w / 656f
    val sy = h / 370f
    return Path().apply {
        moveTo((cx + 7.5f) * sx, (cy - 3.3f) * sy)
        quadraticTo((cx + 8.5f) * sx, (cy + 6.7f) * sy, cx * sx, (cy + 6.7f) * sy)
        quadraticTo((cx - 8.5f) * sx, (cy + 6.7f) * sy, (cx - 7.5f) * sx, (cy - 1.3f) * sy)
        quadraticTo((cx - 5.5f) * sx, (cy - 8.3f) * sy, (cx + 2.5f) * sx, (cy - 6.3f) * sy)
    }
}

@Composable
private fun KontrolAnimatedLogo(modifier: Modifier = Modifier) {
    val isDark = MaterialTheme.colorScheme.background.luminance() < 0.5f
    val baseAccent = MaterialTheme.colorScheme.primary
    // Two-tone flat ribbon palette derived smoothly from theme accent
    val toneLight = if (isDark) lerp(baseAccent, Color.White, 0.36f) else lerp(baseAccent, Color.White, 0.22f)
    val toneDark  = if (isDark) lerp(baseAccent, Color(0xFF0A1426), 0.30f) else lerp(baseAccent, Color(0xFF0B1628), 0.44f)
    val lightTint = ColorFilter.tint(toneLight)
    val darkTint  = ColorFilter.tint(toneDark)

    val transition = rememberInfiniteTransition(label = "kontrol_ribbon_3s")
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
        // 1. "کنتر" — Pure Vector Two-Tone Flat Ribbon revealed by seamless pen stroke
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
                                width = size.width * (104f / 656f),
                                cap = StrokeCap.Round,
                                join = StrokeJoin.Round
                            ),
                            blendMode = BlendMode.DstIn
                        )
                    }
            ) {
                Image(
                    painter = painterResource(id = R.drawable.logo_kntr_base),
                    contentDescription = "کنترل",
                    colorFilter = darkTint,
                    modifier = Modifier.fillMaxSize()
                )
                Image(
                    painter = painterResource(id = R.drawable.logo_kntr_light),
                    contentDescription = null,
                    colorFilter = lightTint,
                    modifier = Modifier.fillMaxSize()
                )
            }
        }
        // 2. "ل" — Pure Vector Two-Tone Flat Ribbon revealed by seamless pen stroke
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
                                width = size.width * (104f / 656f),
                                cap = StrokeCap.Round,
                                join = StrokeJoin.Round
                            ),
                            blendMode = BlendMode.DstIn
                        )
                    }
            ) {
                Image(
                    painter = painterResource(id = R.drawable.logo_lam_base),
                    contentDescription = null,
                    colorFilter = darkTint,
                    modifier = Modifier.fillMaxSize()
                )
                Image(
                    painter = painterResource(id = R.drawable.logo_lam_light),
                    contentDescription = null,
                    colorFilter = lightTint,
                    modifier = Modifier.fillMaxSize()
                )
            }
        }
        // 3. Three dots: ن (light) + two unified ت dots (both dark)
        if (d1Prog > 0.005f) {
            Canvas(
                modifier = Modifier
                    .fillMaxSize()
                    .graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }
            ) {
                val sx = size.width / 656f
                val sy = size.height / 370f
                val r = 29.3f * sx
                drawCircle(color = toneLight, radius = r, center = Offset(447.5f * sx, 86.3f * sy))
                drawCircle(color = toneDark,  radius = r, center = Offset(384.1f * sx, 86.3f * sy))
                drawCircle(color = toneDark,  radius = r, center = Offset(320.4f * sx, 86.3f * sy))

                val stroke = Stroke(width = 66f * sx, cap = StrokeCap.Round, join = StrokeJoin.Round)
                val combined = Path()
                listOf(
                    Triple(447.5f, 86.3f, d1Prog),
                    Triple(384.1f, 86.3f, d2Prog),
                    Triple(320.4f, 86.3f, d3Prog)
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
