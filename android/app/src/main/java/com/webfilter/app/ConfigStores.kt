package com.webfilter.app

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import mobile.Mobile

/**
 * JSON-backed stores for the native settings screens. Both talk to the Go
 * engine's config files through the gomobile API (works whether or not the
 * VPN is running) instead of SharedPreferences:
 *
 *  - [PolicyJsonStore] holds one named policy as a [JSONObject]; every
 *    [set] writes the FULL document back through Mobile.updatePolicyJson —
 *    the Go side's sub-config unmarshalers reset unmentioned sibling fields
 *    to defaults on partial bodies, so full-document write is the only safe
 *    shape. Policy changes hot-reload; no engine restart needed.
 *  - [SettingsJsonStore] sends PARTIAL bodies (just the changed key) through
 *    Mobile.updateSettingsJson, which merges server-side. Settings changes
 *    need an engine restart to take effect.
 *
 * Both surface write failures to the caller (the preference layer shows a
 * toast and re-loads) rather than caching unsynced state.
 */
class PolicyJsonStore(private val dataDir: String, var policyName: String = "default") {

    private var doc: JSONObject = JSONObject()

    fun load() {
        doc = JSONObject(Mobile.getPolicyJson(dataDir, policyName))
    }

    /** Walks a dot path ("safesearch.engines.google.enabled"). Null if absent. */
    fun get(path: String): Any? {
        var cur: Any? = doc
        for (part in path.split('.')) {
            cur = (cur as? JSONObject)?.opt(part) ?: return null
        }
        return cur
    }

    fun getBool(path: String, def: Boolean): Boolean = get(path) as? Boolean ?: def

    fun getString(path: String, def: String): String = get(path)?.toString() ?: def

    /** JSON array at path rendered one-entry-per-line for multiline editors. */
    fun getLines(path: String): String {
        val arr = get(path) as? JSONArray ?: return ""
        return (0 until arr.length()).joinToString("\n") { arr.optString(it) }
    }

    /**
     * Sets a value at a dot path (creating intermediate objects) and writes
     * the full policy document back. Throws on engine rejection (e.g. MDM
     * lock, validation) — caller must catch, surface, and re-[load].
     */
    fun set(path: String, value: Any) {
        val parts = path.split('.')
        var cur = doc
        for (part in parts.dropLast(1)) {
            cur = cur.optJSONObject(part) ?: JSONObject().also { cur.put(part, it) }
        }
        cur.put(parts.last(), value)
        save()
    }

    fun setLines(path: String, lines: String) {
        val arr = JSONArray()
        lines.split('\n', ',')
            .map { it.trim() }
            .filter { it.isNotEmpty() }
            .forEach { arr.put(it) }
        set(path, arr)
    }

    /** Returns the schedule sub-object (for ScheduleActivity's editor). */
    fun scheduleJson(): JSONObject = doc.optJSONObject("schedule") ?: JSONObject()

    fun setSchedule(schedule: JSONObject) = set("schedule", schedule)

    private fun save() {
        val updated = Mobile.updatePolicyJson(dataDir, policyName, doc.toString())
        doc = JSONObject(updated)
        // Adopt a rename (the doc's name wins server-side) so subsequent
        // loads/saves address the new file.
        policyName = doc.optString("name", policyName)
    }

    companion object {
        fun forApp(context: Context, policyName: String = "default") =
            PolicyJsonStore(context.filesDir.absolutePath, policyName)
    }
}

class SettingsJsonStore(private val dataDir: String) {

    private var doc: JSONObject = JSONObject()

    fun load() {
        doc = JSONObject(Mobile.getSettingsJson(dataDir))
    }

    fun getBool(key: String, def: Boolean): Boolean = doc.optBoolean(key, def)

    fun getString(key: String, def: String): String =
        if (doc.has(key) && !doc.isNull(key)) doc.get(key).toString() else def

    /**
     * Sends a single-key partial update; the Go side merges over the current
     * file. Throws on rejection (MDM lock, validation) — caller surfaces it.
     */
    fun set(key: String, value: Any) {
        update(JSONObject().put(key, value))
    }

    fun update(partial: JSONObject) {
        val updated = Mobile.updateSettingsJson(dataDir, partial.toString())
        doc = JSONObject(updated)
    }

    companion object {
        fun forApp(context: Context) = SettingsJsonStore(context.filesDir.absolutePath)
    }
}

/** Read-only view of the MDM lock state for gating the settings UI. */
object ManagedState {
    fun isLocked(context: Context): Boolean = try {
        val st = JSONObject(Mobile.getManagedStateJson(context.filesDir.absolutePath))
        st.optBoolean("managed", false) && st.optBoolean("settings_locked", false)
    } catch (_: Exception) {
        // Fail closed, same stance as the Go side: never offer edits the
        // engine would reject anyway.
        true
    }
}
