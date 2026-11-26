package com.mdesign.mcontrol.worker

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.mdesign.mcontrol.R
import com.mdesign.mcontrol.data.MControlApi
import com.mdesign.mcontrol.data.PanelStore
import java.util.concurrent.TimeUnit

class OutageCheckWorker(
    appContext: Context,
    params: WorkerParameters
) : CoroutineWorker(appContext, params) {

    override suspend fun doWork(): Result {
        val store = PanelStore(applicationContext)
        if (!store.isBgAlertsEnabled()) return Result.success()
        val panel = store.getActivePanel() ?: return Result.success()
        if (panel.token.isBlank()) return Result.success()

        val api = MControlApi(panel)
        val offlineItems = mutableListOf<String>()

        api.getServers().getOrNull()?.forEach { s ->
            if (s.status.equals("offline", ignoreCase = true)) {
                offlineItems.add("Server: ${s.name}")
            }
        }
        api.getMonitors().getOrNull()?.forEach { m ->
            if (m.enabled && m.status.equals("down", ignoreCase = true)) {
                offlineItems.add("Monitor: ${m.name}")
            }
        }

        if (offlineItems.isNotEmpty()) {
            sendAlertNotification(panel.name, offlineItems)
        }
        return Result.success()
    }

    private fun sendAlertNotification(panelName: String, items: List<String>) {
        val nm = applicationContext.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        val channelId = "mcontrol_outages"
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val ch = NotificationChannel(
                channelId,
                "MControl Outage Alerts",
                NotificationManager.IMPORTANCE_HIGH
            )
            nm.createNotificationChannel(ch)
        }
        val body = items.take(4).joinToString(" • ") + if (items.size > 4) " (+${items.size - 4} more)" else ""
        val notif = NotificationCompat.Builder(applicationContext, channelId)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentTitle("⚠️ $panelName: ${items.size} Offline")
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(items.joinToString("\n")))
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .build()
        nm.notify(1042, notif)
    }

    companion object {
        private const val WORK_NAME = "mcontrol_outage_watcher"

        fun schedule(context: Context, enabled: Boolean) {
            val wm = WorkManager.getInstance(context)
            if (!enabled) {
                wm.cancelUniqueWork(WORK_NAME)
                return
            }
            val req = PeriodicWorkRequestBuilder<OutageCheckWorker>(15, TimeUnit.MINUTES).build()
            wm.enqueueUniquePeriodicWork(WORK_NAME, ExistingPeriodicWorkPolicy.UPDATE, req)
        }
    }
}
