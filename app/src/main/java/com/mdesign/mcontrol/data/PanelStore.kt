package com.mdesign.mcontrol.data

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject

class PanelStore(context: Context) {
    private val prefs = context.getSharedPreferences("mcontrol_prefs", Context.MODE_PRIVATE)

    fun getPanels(): List<PanelProfile> {
        val raw = prefs.getString("panels_json", "[]") ?: "[]"
        val out = mutableListOf<PanelProfile>()
        try {
            val arr = JSONArray(raw)
            for (i in 0 until arr.length()) {
                val o = arr.getJSONObject(i)
                out.add(
                    PanelProfile(
                        id = o.optString("id"),
                        name = o.optString("name", "MControl"),
                        baseUrl = o.optString("baseUrl"),
                        username = o.optString("username", "admin"),
                        password = o.optString("password", ""),
                        token = o.optString("token", ""),
                        trustAllSsl = o.optBoolean("trustAllSsl", true)
                    )
                )
            }
        } catch (_: Exception) {}
        return out
    }

    fun savePanels(list: List<PanelProfile>) {
        val arr = JSONArray()
        for (p in list) {
            arr.put(JSONObject().apply {
                put("id", p.id)
                put("name", p.name)
                put("baseUrl", p.baseUrl)
                put("username", p.username)
                put("password", p.password)
                put("token", p.token)
                put("trustAllSsl", p.trustAllSsl)
            })
        }
        prefs.edit().putString("panels_json", arr.toString()).apply()
    }

    fun upsertPanel(profile: PanelProfile) {
        val list = getPanels().toMutableList()
        val idx = list.indexOfFirst { it.id == profile.id }
        if (idx >= 0) list[idx] = profile else list.add(0, profile)
        savePanels(list)
        setActivePanelId(profile.id)
    }

    fun deletePanel(id: String) {
        val list = getPanels().filterNot { it.id == id }
        savePanels(list)
        if (getActivePanelId() == id) {
            setActivePanelId(list.firstOrNull()?.id ?: "")
        }
    }

    fun getActivePanelId(): String = prefs.getString("active_panel_id", "") ?: ""
    fun setActivePanelId(id: String) { prefs.edit().putString("active_panel_id", id).apply() }

    fun getActivePanel(): PanelProfile? {
        val id = getActivePanelId()
        val all = getPanels()
        return all.firstOrNull { it.id == id } ?: all.firstOrNull()
    }

    // Theme: "dark" | "light" | "auto"
    fun getThemeMode(): String = prefs.getString("theme_mode", "dark") ?: "dark"
    fun setThemeMode(m: String) { prefs.edit().putString("theme_mode", m).apply() }

    fun isBgAlertsEnabled(): Boolean = prefs.getBoolean("bg_alerts_enabled", false)
    fun setBgAlertsEnabled(on: Boolean) { prefs.edit().putBoolean("bg_alerts_enabled", on).apply() }

    fun isBiometricLockEnabled(): Boolean = prefs.getBoolean("bio_lock", false)
    fun setBiometricLockEnabled(on: Boolean) { prefs.edit().putBoolean("bio_lock", on).apply() }

    fun isApkBetaChannel(): Boolean = prefs.getBoolean("apk_beta_channel", false)
    fun setApkBetaChannel(on: Boolean) = prefs.edit().putBoolean("apk_beta_channel", on).apply()

    fun getAlertIntervalSec(): Int = prefs.getInt("alert_interval_sec", 30).coerceIn(5, 3600)
    fun setAlertIntervalSec(sec: Int) = prefs.edit().putInt("alert_interval_sec", sec.coerceIn(5, 3600)).apply()

    fun getAlertEveryN(): Int = prefs.getInt("alert_every_n", 1).coerceIn(1, 100)
    fun setAlertEveryN(n: Int) = prefs.edit().putInt("alert_every_n", n.coerceIn(1, 100)).apply()

    fun getAlertMaxPerOutage(): Int = prefs.getInt("alert_max_per_outage", 0).coerceIn(0, 100)
    fun setAlertMaxPerOutage(n: Int) = prefs.edit().putInt("alert_max_per_outage", n.coerceIn(0, 100)).apply()

    fun isAlertOnRecovery(): Boolean = prefs.getBoolean("alert_on_recovery", true)
    fun setAlertOnRecovery(on: Boolean) = prefs.edit().putBoolean("alert_on_recovery", on).apply()
}
