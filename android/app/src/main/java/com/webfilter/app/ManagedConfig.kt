package com.webfilter.app

import android.content.Context
import android.content.RestrictionsManager
import android.os.Bundle
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import mobile.Mobile

/**
 * Bridges Android managed configurations (RestrictionsManager) to the Go
 * engine: translates the EMM's restrictions bundle into the canonical
 * document consumed by Mobile.applyManagedConfigJson (schema documented in
 * res/xml/app_restrictions.xml and internal/settingsvc/managed.go).
 *
 * Only keys PRESENT in the bundle are emitted — that containsKey gate is
 * what guarantees a partial EMM push never clobbers user edits to
 * unmanaged fields. Re-application is idempotent (the Go side hashes the
 * document), so calling [applyRestrictions] on every app/service start is
 * the intended usage.
 */
object ManagedConfig {

    private const val TAG = "ManagedConfig"

    private val SETTINGS_BOOL_KEYS = listOf("log_blocks", "log_requests", "auth_enabled")
    private val ENGINES = listOf("google", "bing", "duckduckgo", "yahoo", "youtube")

    /**
     * Reads the current application restrictions and applies them to the
     * engine config. Returns true when anything actually changed (the
     * caller should restart a running engine so settings/lock changes take
     * effect; policy changes hot-reload). Never throws: a malformed EMM
     * push is logged and ignored, leaving the previous state in force.
     */
    fun applyRestrictions(context: Context): Boolean {
        return try {
            val rm = context.getSystemService(Context.RESTRICTIONS_SERVICE) as RestrictionsManager
            val doc = buildDocFromBundle(rm.applicationRestrictions)
            Mobile.applyManagedConfigJson(context.filesDir.absolutePath, doc.toString())
        } catch (e: Exception) {
            Log.e(TAG, "failed to apply managed configuration", e)
            false
        }
    }

    /**
     * MDM-forced start mode: true/false when the EMM sets the
     * `proxy_only_mode` restriction, null when unmanaged. This key is
     * consumed by the Kotlin layer ONLY (it selects how the service starts,
     * not engine config) — deliberately absent from [buildDocFromBundle],
     * the one documented exception to the restriction-key == preference-key
     * sync rule.
     */
    fun forcedProxyOnly(context: Context): Boolean? = try {
        val rm = context.getSystemService(Context.RESTRICTIONS_SERVICE) as RestrictionsManager
        val b = rm.applicationRestrictions
        if (b.containsKey("proxy_only_mode")) b.getBoolean("proxy_only_mode") else null
    } catch (_: Exception) {
        null
    }

    /**
     * Pure Bundle -> canonical-document mapping (no Android services, no
     * engine calls) so it stays unit-testable without instrumentation.
     * An empty bundle produces an empty document, which the Go side treats
     * as "not managed" (clears any previous managed state).
     */
    fun buildDocFromBundle(b: Bundle): JSONObject {
        val doc = JSONObject()
        if (b.isEmpty) return doc

        if (b.containsKey("settings_locked")) {
            doc.put("settings_locked", b.getBoolean("settings_locked"))
        }
        stringIfSet(b, "policy_json")?.let { doc.put("policy_json", it) }

        val settings = JSONObject()
        for (key in SETTINGS_BOOL_KEYS) {
            if (b.containsKey(key)) settings.put(key, b.getBoolean(key))
        }
        if (b.containsKey("log_retention_days")) {
            settings.put("log_retention_days", b.getInt("log_retention_days"))
        }
        stringIfSet(b, "ui_language")?.let { settings.put("ui_language", it) }
        // mgmt_password maps to the merge path's new_password (hash-on-write;
        // the plaintext never lands on disk).
        stringIfSet(b, "mgmt_password")?.let { settings.put("new_password", it) }
        if (settings.length() > 0) doc.put("settings", settings)

        val policy = buildPolicyPatch(b)
        if (policy.length() > 0) doc.put("policy", policy)

        return doc
    }

    private fun buildPolicyPatch(b: Bundle): JSONObject {
        val policy = JSONObject()

        // SafeSearch. The per-tab flags expand to ALL engines explicitly:
        // the Go model's legacy flat-schema migration only fires when the
        // engines map is absent, which is never true when merging onto an
        // existing policy.
        val safesearch = JSONObject()
        if (b.containsKey("safesearch_enabled")) safesearch.put("enabled", b.getBoolean("safesearch_enabled"))
        val engines = JSONObject()
        for (tab in listOf("block_images_tab", "block_videos_tab", "block_ai_tab")) {
            if (!b.containsKey("safesearch_$tab")) continue
            val value = b.getBoolean("safesearch_$tab")
            for (engine in ENGINES) {
                val e = engines.optJSONObject(engine) ?: JSONObject().also { engines.put(engine, it) }
                e.put(tab, value)
            }
        }
        if (engines.length() > 0) safesearch.put("engines", engines)
        if (safesearch.length() > 0) policy.put("safesearch", safesearch)

        val urlFilter = JSONObject()
        boolIfSet(b, "url_filter_enabled") { urlFilter.put("enabled", it) }
        stringIfSet(b, "url_filter_mode")?.let { urlFilter.put("mode", it) }
        linesIfSet(b, "url_filter_allow")?.let { urlFilter.put("allow", it) }
        linesIfSet(b, "url_filter_block")?.let { urlFilter.put("block", it) }
        linesIfSet(b, "url_filter_categories")?.let { urlFilter.put("categories", it) }
        boolIfSet(b, "url_filter_block_quic") { urlFilter.put("block_quic", it) }
        if (urlFilter.length() > 0) policy.put("url_filter", urlFilter)

        val youtube = JSONObject()
        boolIfSet(b, "youtube_enabled") { youtube.put("enabled", it) }
        stringIfSet(b, "youtube_mode")?.let { youtube.put("mode", it) }
        linesIfSet(b, "youtube_channels")?.let { youtube.put("channels", it) }
        boolIfSet(b, "youtube_block_home") { youtube.put("block_home", it) }
        boolIfSet(b, "youtube_remove_comments") { youtube.put("remove_comments", it) }
        boolIfSet(b, "youtube_remove_recommendations") { youtube.put("remove_recommendations", it) }
        if (youtube.length() > 0) policy.put("youtube", youtube)

        val text = JSONObject()
        boolIfSet(b, "text_classifier_enabled") { text.put("enabled", it) }
        stringIfSet(b, "text_classifier_threshold")?.let { text.put("threshold", it) }
        if (text.length() > 0) policy.put("text_classifier", text)

        val image = JSONObject()
        boolIfSet(b, "image_classifier_enabled") { image.put("enabled", it) }
        stringIfSet(b, "image_classifier_action")?.let { image.put("action", it) }
        stringIfSet(b, "image_classifier_threshold")?.let { image.put("threshold", it) }
        if (b.containsKey("image_classifier_min_dimension")) {
            image.put("min_dimension", b.getInt("image_classifier_min_dimension"))
        }
        if (image.length() > 0) policy.put("image_classifier", image)

        val doh = JSONObject()
        boolIfSet(b, "doh_enabled") { doh.put("enabled", it) }
        stringIfSet(b, "doh_server")?.let { doh.put("server", it) }
        if (doh.length() > 0) policy.put("doh", doh)

        stringIfSet(b, "schedule_json")?.let {
            try {
                policy.put("schedule", JSONObject(it))
            } catch (e: Exception) {
                Log.w(TAG, "ignoring malformed schedule_json restriction", e)
            }
        }

        // Empty string is a deliberate clear for the block message (unlike
        // list/choice keys, where EMM consoles send "" for "unset").
        if (b.containsKey("block_page_message")) {
            policy.put("block_page", JSONObject().put("message", b.getString("block_page_message") ?: ""))
        }

        return policy
    }

    /** Non-blank string restriction, else null (EMM consoles send "" for unset). */
    private fun stringIfSet(b: Bundle, key: String): String? {
        if (!b.containsKey(key)) return null
        val v = b.getString(key) ?: return null
        return v.ifBlank { null }
    }

    private inline fun boolIfSet(b: Bundle, key: String, put: (Boolean) -> Unit) {
        if (b.containsKey(key)) put(b.getBoolean(key))
    }

    /**
     * Newline-separated (comma tolerated) list restriction as a JSON array;
     * null when absent or blank. Split newlines first: URL patterns may
     * contain commas-in-paths, and newline is the documented canonical
     * separator.
     */
    private fun linesIfSet(b: Bundle, key: String): JSONArray? {
        val raw = stringIfSet(b, key) ?: return null
        val arr = JSONArray()
        raw.split('\n')
            .flatMap { if (it.contains(',')) it.split(',') else listOf(it) }
            .map { it.trim() }
            .filter { it.isNotEmpty() }
            .forEach { arr.put(it) }
        return arr
    }
}
