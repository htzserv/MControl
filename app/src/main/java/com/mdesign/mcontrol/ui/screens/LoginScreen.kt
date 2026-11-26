package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.mdesign.mcontrol.data.MControlApi
import com.mdesign.mcontrol.data.PanelProfile
import com.mdesign.mcontrol.ui.theme.MDesignAccent
import kotlinx.coroutines.launch
import java.util.UUID

@Composable
fun LoginScreen(
    savedPanels: List<PanelProfile>,
    initialProfile: PanelProfile?,
    onLoginSuccess: (PanelProfile) -> Unit
) {
    val scope = rememberCoroutineScope()
    var name by remember { mutableStateOf(initialProfile?.name ?: "My MControl") }
    var baseUrl by remember { mutableStateOf(initialProfile?.baseUrl ?: "https://") }
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
        Text(
            text = "MControl",
            fontSize = 32.sp,
            fontWeight = FontWeight.ExtraBold,
            color = MDesignAccent
        )
        Text(
            text = "MDesign Mobile Control Center",
            fontSize = 13.sp,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Spacer(Modifier.height(20.dp))

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
                            editingId = p.id
                            name = p.name
                            baseUrl = p.baseUrl
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
                    value = baseUrl,
                    onValueChange = { baseUrl = it },
                    label = { Text("Panel URL (with secret path)") },
                    placeholder = { Text("https://ip:2053/webpath") },
                    leadingIcon = { Icon(Icons.Default.Public, null) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    shape = RoundedCornerShape(14.dp)
                )
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
                        val cleanUrl = baseUrl.trim().trimEnd('/')
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
