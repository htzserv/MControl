package com.mdesign.mcontrol.data

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

class MControlApi(private var profile: PanelProfile) {
    private val jsonType = "application/json; charset=utf-8".toMediaType()
    private val client: OkHttpClient = buildClient(profile.trustAllSsl)

    fun updateProfile(p: PanelProfile) {
        profile = p
    }

    private fun buildClient(trustAll: Boolean): OkHttpClient {
        val b = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(45, TimeUnit.SECONDS)
            .writeTimeout(30, TimeUnit.SECONDS)
        if (trustAll) {
            try {
                val tm = object : X509TrustManager {
                    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {}
                    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {}
                    override fun getAcceptedIssuers(): Array<X509Certificate> = arrayOf()
                }
                val sc = SSLContext.getInstance("TLS")
                sc.init(null, arrayOf<TrustManager>(tm), SecureRandom())
                b.sslSocketFactory(sc.socketFactory, tm)
                b.hostnameVerifier { _, _ -> true }
            } catch (_: Exception) {}
        }
        return b.build()
    }

    private fun url(path: String): String {
        val base = profile.baseUrl.trim().trimEnd('/')
        val clean = path.trimStart('/')
        return "$base/$clean"
    }

    private suspend fun requestJson(method: String, path: String, body: JSONObject? = null): JSONObject =
        withContext(Dispatchers.IO) {
            try {
                val rb = Request.Builder().url(url(path))
                if (profile.token.isNotBlank()) {
                    rb.header("Authorization", "Bearer ${profile.token}")
                }
                when (method.uppercase()) {
                    "GET" -> rb.get()
                    "DELETE" -> rb.delete()
                    else -> {
                        val payload = (body ?: JSONObject()).toString().toRequestBody(jsonType)
                        rb.method(method.uppercase(), payload)
                    }
                }
                client.newCall(rb.build()).execute().use { resp ->
                    val raw = resp.body?.string().orEmpty()
                    if (raw.startsWith("{")) {
                        JSONObject(raw).apply {
                            if (!has("http_code")) put("http_code", resp.code)
                        }
                    } else {
                        JSONObject().apply {
                            put("ok", resp.isSuccessful)
                            put("http_code", resp.code)
                            if (!resp.isSuccessful) put("error", "HTTP ${resp.code}: ${raw.take(180)}")
                        }
                    }
                }
            } catch (e: Exception) {
                JSONObject().apply {
                    put("ok", false)
                    put("error", e.localizedMessage ?: e.toString())
                }
            }
        }

    suspend fun login(username: String, password: String, totpCode: String = ""): LoginResult {
        val body = JSONObject().apply {
            put("username", username.trim())
            put("password", password)
            if (totpCode.isNotBlank()) put("totp_code", totpCode.trim())
        }
        val r = requestJson("POST", "api/login", body)
        return LoginResult(
            ok = r.optBoolean("ok", false),
            token = r.optString("token", ""),
            require2fa = r.optBoolean("require_2fa", false),
            error = r.optString("error", "")
        )
    }

    suspend fun getLocalStats(): Result<LocalStats> {
        val r = requestJson("GET", "api/local/stats")
        if (!r.optBoolean("ok", false)) return Result.failure(Exception(r.optString("error", "Failed")))
        val s = r.optJSONObject("stats") ?: return Result.success(LocalStats())
        return Result.success(
            LocalStats(
                hostname = s.optString("hostname", "Master"),
                os = s.optString("os", ""),
                ipv4 = s.optString("ipv4", "N/A"),
                ipv6 = s.optString("ipv6", "N/A"),
                cpuPercent = s.optDouble("cpu_percent", 0.0),
                loadAvg = s.optString("load_avg", "0.00"),
                ramUsedMb = s.optLong("ram_used_mb", 0),
                ramTotalMb = s.optLong("ram_total_mb", 0),
                ramPercent = s.optDouble("ram_percent", 0.0),
                diskPercent = s.optDouble("disk_percent", 0.0),
                netRxSpeed = s.optLong("net_rx_speed", 0),
                netTxSpeed = s.optLong("net_tx_speed", 0),
                netRxTotal = s.optLong("net_rx_total", 0),
                netTxTotal = s.optLong("net_tx_total", 0),
                uptime = s.optString("uptime", ""),
                bbr = s.optString("bbr", "")
            )
        )
    }

    suspend fun cleanMasterRam(): Pair<Boolean, String> {
        val r = requestJson("POST", "api/local/clean-cache")
        return r.optBoolean("ok") to (r.optString("message").ifBlank { r.optString("error", "Done") })
    }

    suspend fun restartMasterPanel(): Pair<Boolean, String> {
        val r = requestJson("POST", "api/local/restart")
        return r.optBoolean("ok") to (r.optString("message").ifBlank { r.optString("error", "Restarting...") })
    }

    suspend fun getServers(): Result<List<ServerItem>> {
        val r = requestJson("GET", "api/servers")
        if (!r.optBoolean("ok", false)) return Result.failure(Exception(r.optString("error", "Failed")))
        val arr = r.optJSONArray("servers") ?: return Result.success(emptyList())
        val out = mutableListOf<ServerItem>()
        for (i in 0 until arr.length()) {
            val o = arr.getJSONObject(i)
            out.add(
                ServerItem(
                    id = o.optLong("id"),
                    name = o.optString("name"),
                    role = o.optString("role", "foreign"),
                    host = o.optString("host"),
                    port = o.optInt("port", 22),
                    status = o.optString("status", "unknown"),
                    country = o.optString("country", ""),
                    countryCode = o.optString("country_code", ""),
                    cpu = o.optString("cpu", ""),
                    ram = o.optString("ram", ""),
                    uptime = o.optString("uptime", ""),
                    ipv4 = o.optString("ipv4", ""),
                    ipv6 = o.optString("ipv6", ""),
                    authMode = o.optString("auth_mode", "ssh"),
                    pingMs = o.optDouble("ping_ms", 0.0),
                    outboundPingMs = o.optDouble("outbound_ping_ms", 0.0),
                    reversePingMs = o.optDouble("reverse_ping_ms", 0.0),
                    useOutbound = o.optBoolean("use_outbound", false),
                    lastError = o.optString("last_error", "")
                )
            )
        }
        return Result.success(out.sortedBy { it.name.lowercase() })
    }

    suspend fun pingServer(id: Long): Pair<Boolean, String> {
        val r = requestJson("POST", "api/servers/$id/ping")
        if (!r.optBoolean("ok")) return false to r.optString("error", "Ping failed")
        val p = r.optDouble("ping_ms", 0.0)
        return true to (if (p > 0) String.format("Ping: %.1f ms", p) else "Ping updated")
    }

    suspend fun probeServer(id: Long): Pair<Boolean, String> {
        val r = requestJson("POST", "api/servers/$id/probe")
        return r.optBoolean("ok") to (r.optString("message").ifBlank { if (r.optBoolean("ok")) "Server refreshed" else r.optString("error", "Failed") })
    }

    suspend fun serverTweak(id: Long, action: String): Pair<Boolean, String> {
        val r = requestJson("POST", "api/servers/$id/tweak", JSONObject().put("action", action))
        return r.optBoolean("ok") to (r.optString("output").ifBlank { r.optString("message").ifBlank { r.optString("error", "Done") } })
    }

    suspend fun execCommand(serverId: Long?, command: String): Pair<Boolean, String> {
        val path = if (serverId == null) "api/local/exec" else "api/servers/$serverId/exec"
        val r = requestJson("POST", path, JSONObject().put("command", command))
        val out = r.optString("output", "")
        val err = r.optString("error", "")
        val combined = buildString {
            if (out.isNotBlank()) append(out)
            if (!r.optBoolean("ok") && err.isNotBlank()) {
                if (isNotEmpty()) append("\n")
                append("ERROR: ").append(err)
            }
        }.ifBlank { "(no output)" }
        return r.optBoolean("ok") to combined
    }

    suspend fun getTunnels(): Result<List<TunnelItem>> {
        val r = requestJson("GET", "api/tunnels")
        if (!r.optBoolean("ok", false)) return Result.failure(Exception(r.optString("error", "Failed")))
        val arr = r.optJSONArray("tunnels") ?: return Result.success(emptyList())
        val out = mutableListOf<TunnelItem>()
        for (i in 0 until arr.length()) {
            val o = arr.getJSONObject(i)
            out.add(
                TunnelItem(
                    id = o.optLong("id"),
                    name = o.optString("name"),
                    engine = o.optString("engine"),
                    transport = o.optString("transport"),
                    iranName = o.optString("iran_name"),
                    iranHost = o.optString("iran_host"),
                    foreignName = o.optString("foreign_name"),
                    foreignHost = o.optString("foreign_host"),
                    tunnelPort = o.optInt("tunnel_port"),
                    ports = o.optString("ports"),
                    status = o.optString("status"),
                    lastError = o.optString("last_error", "")
                )
            )
        }
        return Result.success(out)
    }

    suspend fun getTunnelMetrics(id: Long): Result<TunnelMetrics> {
        val r = requestJson("GET", "api/tunnels/$id/metrics")
        if (!r.optBoolean("ok", false)) return Result.failure(Exception(r.optString("error", "Failed")))
        val bh = r.optJSONObject("bh_health")
        return Result.success(
            TunnelMetrics(
                iranStatus = r.optString("iran_status", "?"),
                foreignStatus = r.optString("foreign_status", "?"),
                ping = r.optString("ping", "N/A"),
                conns = r.optString("conns", "0"),
                bhConnected = bh?.optBoolean("connected"),
                bhStatus = bh?.optString("status", "") ?: "",
                bhTrafficBytes = bh?.optLong("traffic_bytes", 0L) ?: 0L,
                bhUptimeSec = bh?.optLong("uptime_sec", 0L) ?: 0L,
                bhGoroutines = bh?.optInt("goroutines", 0) ?: 0
            )
        )
    }

    suspend fun tunnelAction(id: Long, action: String): Pair<Boolean, String> {
        val r = requestJson("POST", "api/tunnels/$id/action", JSONObject().put("action", action))
        return r.optBoolean("ok") to (r.optString("message").ifBlank { if (r.optBoolean("ok")) "Action '$action' done" else r.optString("error", "Failed") })
    }

    suspend fun rotateTunnelPort(id: Long): Pair<Boolean, String> {
        val r = requestJson("POST", "api/tunnels/$id/rotate-port")
        return r.optBoolean("ok") to (r.optString("message").ifBlank { r.optString("error", "Failed") })
    }

    suspend fun getMonitors(): Result<List<MonitorItem>> {
        val r = requestJson("GET", "api/monitors")
        if (!r.optBoolean("ok", false)) return Result.failure(Exception(r.optString("error", "Failed")))
        val arr = r.optJSONArray("monitors") ?: return Result.success(emptyList())
        val out = mutableListOf<MonitorItem>()
        for (i in 0 until arr.length()) {
            val o = arr.getJSONObject(i)
            out.add(
                MonitorItem(
                    id = o.optLong("id"),
                    name = o.optString("name"),
                    type = o.optString("type"),
                    target = o.optString("target"),
                    status = o.optString("status", "pending"),
                    lastLatencyMs = o.optInt("last_latency_ms", 0),
                    uptime24h = o.optDouble("uptime_24h", 100.0),
                    enabled = o.optBoolean("enabled", true)
                )
            )
        }
        return Result.success(out)
    }

    suspend fun checkMonitorNow(id: Long): Pair<Boolean, String> {
        val r = requestJson("POST", "api/monitors/$id/check")
        return r.optBoolean("ok") to (if (r.optBoolean("ok")) "Checked" else r.optString("error", "Failed"))
    }
}
