package com.mdesign.mcontrol.worker

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import com.mdesign.mcontrol.MainActivity
import com.mdesign.mcontrol.R
import com.mdesign.mcontrol.data.MControlApi
import com.mdesign.mcontrol.data.PanelStore
import kotlinx.coroutines.*

/**
 * Lightweight Foreground Service that monitors servers & uptime monitors at the exact
 * second-level interval configured by the user (e.g. every 10s, 30s, 60s), tracks
 * consecutive outage counts per node, notifies every N alerts (with optional max cap),
 * and notifies when an offline node recovers.
 */
class OutageMonitorService : Service() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var watchJob: Job? = null

    // Per-key state: consecutive outage checks & how many notifications were already sent
    private val downChecks = mutableMapOf<String, Int>()
    private val sentCount = mutableMapOf<String, Int>()

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        val store = PanelStore(applicationContext)
        if (!store.isBgAlertsEnabled()) {
            stopSelf()
            return START_NOT_STICKY
        }

        val fgNotif = buildForegroundNotification(store.getAlertIntervalSec(), store.getAlertEveryN())
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(FG_NOTIF_ID, fgNotif, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(FG_NOTIF_ID, fgNotif)
        }

        startWatchLoop()
        return START_STICKY
    }

    private fun startWatchLoop() {
        watchJob?.cancel()
        watchJob = scope.launch {
            while (isActive) {
                val store = PanelStore(applicationContext)
                if (!store.isBgAlertsEnabled()) {
                    stopSelf()
                    break
                }
                val intervalSec = store.getAlertIntervalSec().coerceIn(5, 3600)
                val everyN = store.getAlertEveryN().coerceIn(1, 100)
                val maxPer = store.getAlertMaxPerOutage().coerceIn(0, 100)
                val onRecovery = store.isAlertOnRecovery()

                try {
                    val panel = store.getActivePanel()
                    if (panel != null && panel.token.isNotBlank()) {
                        val api = MControlApi(panel)
                        val currentlyDown = linkedMapOf<String, String>()

                        api.getServers().getOrNull()?.forEach { s ->
                            val key = "srv:${s.id}"
                            if (s.status.equals("offline", ignoreCase = true)) {
                                currentlyDown[key] = "🖥 ${s.name} (${s.host})"
                            }
                        }
                        api.getMonitors().getOrNull()?.forEach { m ->
                            val key = "mon:${m.id}"
                            if (m.enabled && m.status.equals("down", ignoreCase = true)) {
                                currentlyDown[key] = "💓 ${m.name} (${m.target})"
                            }
                        }

                        // Detect recoveries
                        val recovered = mutableListOf<String>()
                        val prevKeys = downChecks.keys.toList()
                        for (k in prevKeys) {
                            if (!currentlyDown.containsKey(k)) {
                                if ((sentCount[k] ?: 0) > 0 && onRecovery) {
                                    recovered.add(k)
                                }
                                downChecks.remove(k)
                                sentCount.remove(k)
                            }
                        }
                        if (recovered.isNotEmpty()) {
                            sendRecoveryNotification(applicationContext, panel.name, recovered.size)
                        }

                        // Evaluate outage alerts using everyN & maxPerOutage
                        val toNotify = mutableListOf<String>()
                        for ((k, label) in currentlyDown) {
                            val c = (downChecks[k] ?: 0) + 1
                            downChecks[k] = c
                            val alreadySent = sentCount[k] ?: 0
                            val underCap = (maxPer == 0) || (alreadySent < maxPer)
                            val hitCadence = (c % everyN == 0)
                            if (underCap && hitCadence) {
                                sentCount[k] = alreadySent + 1
                                toNotify.add("$label [#$c]")
                            }
                        }

                        if (toNotify.isNotEmpty()) {
                            sendAlertNotification(applicationContext, panel.name, toNotify)
                        }
                    }
                } catch (_: Exception) {
                }

                delay(intervalSec * 1000L)
            }
        }
    }

    override fun onDestroy() {
        watchJob?.cancel()
        scope.cancel()
        super.onDestroy()
    }

    private fun buildForegroundNotification(intervalSec: Int, everyN: Int): Notification {
        ensureChannels(applicationContext)
        val openIntent = Intent(this, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        val pi = PendingIntent.getActivity(
            this, 0, openIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        return NotificationCompat.Builder(this, CHANNEL_WATCHER)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentTitle("MControl Live Monitor Active")
            .setContentText("Checking every ${intervalSec}s • Alert every $everyN failure(s)")
            .setOngoing(true)
            .setSilent(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setContentIntent(pi)
            .build()
    }

    companion object {
        const val CHANNEL_ALERTS = "mcontrol_outages"
        const val CHANNEL_WATCHER = "mcontrol_watcher_fg"
        private const val FG_NOTIF_ID = 1041
        private const val ALERT_NOTIF_ID = 1042
        private const val RECOVERY_NOTIF_ID = 1043
        private const val ACTION_STOP = "com.mdesign.mcontrol.STOP_MONITOR"

        fun ensureChannels(ctx: Context) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                val nm = ctx.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
                nm.createNotificationChannel(
                    NotificationChannel(
                        CHANNEL_ALERTS,
                        "MControl Outage Alerts",
                        NotificationManager.IMPORTANCE_HIGH
                    ).apply {
                        enableVibration(true)
                        description = "Immediate alerts when a server or monitor goes offline"
                    }
                )
                nm.createNotificationChannel(
                    NotificationChannel(
                        CHANNEL_WATCHER,
                        "MControl Live Watcher",
                        NotificationManager.IMPORTANCE_LOW
                    ).apply {
                        setShowBadge(false)
                        description = "Keeps second-level server outage monitoring active"
                    }
                )
            }
        }

        fun sendAlertNotification(ctx: Context, panelName: String, items: List<String>) {
            ensureChannels(ctx)
            val nm = ctx.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
            val body = items.take(4).joinToString(" • ") + if (items.size > 4) " (+${items.size - 4} more)" else ""
            val notif = NotificationCompat.Builder(ctx, CHANNEL_ALERTS)
                .setSmallIcon(R.drawable.ic_launcher_foreground)
                .setContentTitle("⚠️ $panelName: ${items.size} Offline")
                .setContentText(body)
                .setStyle(NotificationCompat.BigTextStyle().bigText(items.joinToString("
")))
                .setPriority(NotificationCompat.PRIORITY_HIGH)
                .setAutoCancel(true)
                .build()
            nm.notify(ALERT_NOTIF_ID, notif)
        }

        fun sendRecoveryNotification(ctx: Context, panelName: String, count: Int) {
            ensureChannels(ctx)
            val nm = ctx.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
            val notif = NotificationCompat.Builder(ctx, CHANNEL_ALERTS)
                .setSmallIcon(R.drawable.ic_launcher_foreground)
                .setContentTitle("✅ $panelName: $count Back Online")
                .setContentText("$count previously offline node(s) recovered.")
                .setPriority(NotificationCompat.PRIORITY_DEFAULT)
                .setAutoCancel(true)
                .build()
            nm.notify(RECOVERY_NOTIF_ID, notif)
        }

        fun sendTestNotification(ctx: Context, intervalSec: Int, everyN: Int) {
            ensureChannels(ctx)
            val nm = ctx.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
            val notif = NotificationCompat.Builder(ctx, CHANNEL_ALERTS)
                .setSmallIcon(R.drawable.ic_launcher_foreground)
                .setContentTitle("🔔 MControl Alert Test")
                .setContentText("Active: check every ${intervalSec}s • notify every $everyN alert(s)")
                .setPriority(NotificationCompat.PRIORITY_HIGH)
                .setAutoCancel(true)
                .build()
            nm.notify(1049, notif)
        }

        fun sync(ctx: Context, enabled: Boolean) {
            val intent = Intent(ctx, OutageMonitorService::class.java)
            if (!enabled) {
                intent.action = ACTION_STOP
                try { ctx.stopService(intent) } catch (_: Exception) {}
                return
            }
            try {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    ctx.startForegroundService(intent)
                } else {
                    ctx.startService(intent)
                }
            } catch (_: Exception) {
            }
        }
    }
}
