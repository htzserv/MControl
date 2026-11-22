package ir.mcontrol.mobile

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONException
import org.json.JSONObject
import java.net.SocketTimeoutException
import java.net.URL
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLException

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Rtl) {
                MaterialTheme(colorScheme = darkColorScheme()) { PanelScreen() }
            }
        }
    }
}

@Composable
private fun PanelScreen() {
    var address by rememberSaveable { mutableStateOf("") }
    var result by rememberSaveable { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    Surface(modifier = Modifier.fillMaxSize()) {
        Column(Modifier.safeDrawingPadding().imePadding().verticalScroll(rememberScrollState())
            .padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("MControl", style = MaterialTheme.typography.displaySmall)
            Text("اتصال به پنل", style = MaterialTheme.typography.headlineSmall)
            Text("آدرس کامل HTTPS پنل را همراه پورت و مسیر اختصاصی آن وارد کنید.")
            OutlinedTextField(value = address, onValueChange = { address = it; result = "" },
                enabled = !busy, modifier = Modifier.fillMaxWidth(), singleLine = true,
                textStyle = LocalTextStyle.current.copy(textDirection = TextDirection.Ltr),
                label = { Text("آدرس پنل") }, placeholder = { Text("https://example.org/panel") })
            Button(enabled = !busy && address.isNotBlank(), modifier = Modifier.fillMaxWidth(),
                onClick = {
                    val url = runCatching { statusAddress(address) }.getOrNull()
                    if (url == null) {
                        result = "آدرس معتبر HTTPS وارد کنید."
                    } else {
                        busy = true
                        result = ""
                        scope.launch {
                            try {
                                result = withContext(Dispatchers.IO) { checkPanel(url) }
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: SSLException) {
                                result = "اتصال امن برقرار نشد. نام دامنه و گواهی HTTPS را بررسی کنید."
                            } catch (e: SocketTimeoutException) {
                                result = "زمان اتصال تمام شد. دوباره تلاش کنید."
                            } catch (e: JSONException) {
                                result = "پاسخ این آدرس با MControl سازگار نیست. مسیر پنل را بررسی کنید."
                            } catch (e: java.io.IOException) {
                                result = "دسترسی به پنل ممکن نشد. شبکه، دامنه و پورت را بررسی کنید."
                            } finally {
                                busy = false
                            }
                        }
                    }
                }) { Text(if (busy) "در حال بررسی…" else "تست اتصال") }
            if (result.isNotEmpty()) {
                Card { Text(result, modifier = Modifier.fillMaxWidth().padding(16.dp)) }
            }
        }
    }
}

private fun checkPanel(url: URL): String {
    val connection = url.openConnection() as HttpsURLConnection
    try {
        connection.connectTimeout = 8000
        connection.readTimeout = 8000
        connection.instanceFollowRedirects = false
        connection.setRequestProperty("Accept", "application/json")
        val code = connection.responseCode
        if (code != 200) return "پاسخ سرور HTTP $code بود. آدرس و مسیر پنل را بررسی کنید."
        val body = connection.inputStream.bufferedReader().use { reader ->
            val buffer = CharArray(4097)
            var size = 0
            while (size < buffer.size) {
                val count = reader.read(buffer, size, buffer.size - size)
                if (count < 0) break
                size += count
            }
            if (size == buffer.size) throw JSONException("Oversized response")
            String(buffer, 0, size)
        }
        val json = JSONObject(body)
        if (json.opt("ok") != true || json.opt("setup_done") !is Boolean)
            throw JSONException("Unexpected response")
        return if (json.getBoolean("setup_done")) "اتصال موفق بود. پنل راه‌اندازی شده است."
        else "اتصال موفق بود. راه‌اندازی اولیهٔ پنل هنوز تکمیل نشده است."
    } finally {
        connection.disconnect()
    }
}
