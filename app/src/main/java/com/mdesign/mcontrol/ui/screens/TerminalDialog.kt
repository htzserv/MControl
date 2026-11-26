package com.mdesign.mcontrol.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Send
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.launch

private val ANSI_RE = Regex("""\[[0-9;?]*[A-Za-z]""")

@Composable
fun TerminalDialog(
    title: String,
    onRunCommand: suspend (String) -> Pair<Boolean, String>,
    onDismiss: () -> Unit
) {
    val scope = rememberCoroutineScope()
    var output by remember { mutableStateOf("Connected to $title.\nType a bash command below.") }
    var input by remember { mutableStateOf("") }
    var running by remember { mutableStateOf(false) }
    val scroll = rememberScrollState()

    val snippets = listOf(
        "System" to "uptime && free -h && df -h /",
        "Ports" to "ss -tulpn | head -n 25",
        "MControl" to "systemctl status mcontrol --no-pager -l | head -n 20",
        "IPs" to "ip -br a"
    )

    fun runCmd(cmd: String) {
        val c = cmd.trim()
        if (c.isEmpty() || running) return
        running = true
        output += "\n$ $c\n..."
        scope.launch {
            val (_, res) = onRunCommand(c)
            val clean = ANSI_RE.replace(res, "")
            output = output.removeSuffix("\n...") + "\n" + clean
            running = false
            scroll.animateScrollTo(scroll.maxValue)
        }
    }

    Dialog(
        onDismissRequest = onDismiss,
        properties = DialogProperties(usePlatformDefaultWidth = false)
    ) {
        Surface(
            modifier = Modifier.fillMaxSize().padding(10.dp),
            shape = RoundedCornerShape(20.dp),
            color = Color(0xFF0B1019)
        ) {
            Column(Modifier.fillMaxSize().padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceBetween) {
                    Text(title, color = Color(0xFF38BDF8), fontWeight = FontWeight.Bold, fontSize = 15.sp)
                    IconButton(onClick = onDismiss) {
                        Icon(Icons.Default.Close, contentDescription = "Close", tint = Color(0xFFF87171))
                    }
                }

                LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    items(snippets) { (lbl, cmd) ->
                        AssistChip(
                            onClick = { runCmd(cmd) },
                            label = { Text(lbl, fontSize = 11.sp, color = Color(0xFFE2E8F0)) }
                        )
                    }
                }

                Box(
                    Modifier
                        .weight(1f)
                        .fillMaxWidth()
                        .background(Color(0xFF060A11), RoundedCornerShape(12.dp))
                        .padding(10.dp)
                        .verticalScroll(scroll)
                ) {
                    Text(
                        text = output,
                        color = Color(0xFF86EFAC),
                        fontFamily = FontFamily.Monospace,
                        fontSize = 12.sp,
                        lineHeight = 17.sp
                    )
                }

                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(
                        value = input,
                        onValueChange = { input = it },
                        placeholder = { Text("$ bash command...", fontFamily = FontFamily.Monospace, fontSize = 13.sp) },
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                        keyboardActions = KeyboardActions(onSend = {
                            val c = input
                            input = ""
                            runCmd(c)
                        }),
                        modifier = Modifier.weight(1f),
                        shape = RoundedCornerShape(12.dp)
                    )
                    FilledIconButton(
                        onClick = {
                            val c = input
                            input = ""
                            runCmd(c)
                        },
                        enabled = !running
                    ) {
                        Icon(Icons.Default.Send, contentDescription = "Run")
                    }
                }
            }
        }
    }
}
