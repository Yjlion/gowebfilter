package com.webfilter.app

import android.content.Context
import android.widget.Toast
import androidx.preference.PreferenceDataStore

/**
 * Routes androidx.preference widgets to [PolicyJsonStore] instead of
 * SharedPreferences. Preference keys are the same identifiers the MDM
 * restriction schema uses (res/xml/app_restrictions.xml) — one mapping
 * table, two consumers.
 */
class PolicyPreferenceDataStore(
    private val context: Context,
    private val store: PolicyJsonStore,
) : PreferenceDataStore() {

    companion object {
        /** Sentinel entry value for the "Custom…" DoH preset choice. */
        const val DOH_CUSTOM = "__custom__"
    }

    private enum class Kind { BOOL, TEXT, LINES, NUMBER, INT, PRESET }

    private class Spec(val path: String, val kind: Kind)

    private val specs: Map<String, Spec> = buildMap {
        put("safesearch_enabled", Spec("safesearch.enabled", Kind.BOOL))
        for (engine in listOf("google", "bing", "duckduckgo", "yahoo", "youtube")) {
            put("safesearch_${engine}_enabled", Spec("safesearch.engines.$engine.enabled", Kind.BOOL))
            put("safesearch_${engine}_block_images_tab", Spec("safesearch.engines.$engine.block_images_tab", Kind.BOOL))
            put("safesearch_${engine}_block_videos_tab", Spec("safesearch.engines.$engine.block_videos_tab", Kind.BOOL))
            put("safesearch_${engine}_block_ai_tab", Spec("safesearch.engines.$engine.block_ai_tab", Kind.BOOL))
        }
        put("url_filter_enabled", Spec("url_filter.enabled", Kind.BOOL))
        put("url_filter_mode", Spec("url_filter.mode", Kind.TEXT))
        put("url_filter_allow", Spec("url_filter.allow", Kind.LINES))
        put("url_filter_block", Spec("url_filter.block", Kind.LINES))
        // url_filter_categories is edited by CategoriesActivity (checkbox
        // list), not a preference widget; the MDM restriction of the same
        // name still exists and is mapped by ManagedConfig.
        put("url_filter_block_quic", Spec("url_filter.block_quic", Kind.BOOL))
        put("youtube_enabled", Spec("youtube.enabled", Kind.BOOL))
        put("youtube_mode", Spec("youtube.mode", Kind.TEXT))
        put("youtube_channels", Spec("youtube.channels", Kind.LINES))
        put("youtube_block_home", Spec("youtube.block_home", Kind.BOOL))
        put("youtube_remove_comments", Spec("youtube.remove_comments", Kind.BOOL))
        put("youtube_remove_recommendations", Spec("youtube.remove_recommendations", Kind.BOOL))
        put("text_classifier_enabled", Spec("text_classifier.enabled", Kind.BOOL))
        put("text_classifier_threshold", Spec("text_classifier.threshold", Kind.NUMBER))
        put("image_classifier_enabled", Spec("image_classifier.enabled", Kind.BOOL))
        put("image_classifier_action", Spec("image_classifier.action", Kind.TEXT))
        put("image_classifier_threshold", Spec("image_classifier.threshold", Kind.NUMBER))
        put("image_classifier_min_dimension", Spec("image_classifier.min_dimension", Kind.INT))
        put("doh_enabled", Spec("doh.enabled", Kind.BOOL))
        put("doh_server", Spec("doh.server", Kind.TEXT))
        // Same JSON path as doh_server: the ListPreference writes a preset
        // URL through it, and reads back __custom__ when the stored server
        // is not one of the presets (the EditTextPreference then edits it).
        put("doh_server_preset", Spec("doh.server", Kind.PRESET))
        put("block_page_message", Spec("block_page.message", Kind.TEXT))
    }

    private fun spec(key: String) = specs[key] ?: error("unmapped preference key $key")

    private val dohPresetValues: Set<String> by lazy {
        context.resources.getStringArray(R.array.doh_preset_values)
            .filterNot { it == DOH_CUSTOM }
            .toSet()
    }

    // Defaults for absent paths come from the XML android:defaultValue —
    // per-engine `enabled` declares true there, matching the Go side's
    // NewSafeSearchEngineConfig for engines missing from the engines map.
    override fun getBoolean(key: String, defValue: Boolean): Boolean =
        store.getBool(spec(key).path, defValue)

    override fun putBoolean(key: String, value: Boolean) = write { store.set(spec(key).path, value) }

    override fun getString(key: String, defValue: String?): String {
        val s = spec(key)
        return when (s.kind) {
            Kind.LINES -> store.getLines(s.path)
            Kind.PRESET -> {
                val v = store.getString(s.path, defValue ?: "")
                if (v in dohPresetValues) v else DOH_CUSTOM
            }
            else -> store.getString(s.path, defValue ?: "")
        }
    }

    override fun putString(key: String, value: String?) {
        val s = spec(key)
        val v = value ?: ""
        // Picking "Custom…" only reveals the URL editor; it must not
        // overwrite the stored server.
        if (s.kind == Kind.PRESET && v == DOH_CUSTOM) return
        write {
            when (s.kind) {
                Kind.LINES -> store.setLines(s.path, v)
                Kind.NUMBER -> {
                    val f = v.toDoubleOrNull()
                    if (f == null || f < 0.0 || f > 1.0) {
                        Toast.makeText(context, R.string.invalid_threshold, Toast.LENGTH_SHORT).show()
                        return
                    }
                    store.set(s.path, f)
                }
                Kind.INT -> store.set(s.path, v.toIntOrNull() ?: return)
                else -> store.set(s.path, v)
            }
        }
    }

    private inline fun write(block: () -> Unit) {
        try {
            block()
        } catch (e: Exception) {
            Toast.makeText(
                context,
                context.getString(R.string.settings_save_failed, e.message ?: "unknown error"),
                Toast.LENGTH_LONG,
            ).show()
            // Drop the failed edit so the UI re-reads engine truth.
            store.load()
        }
    }
}

/**
 * Same idea for the mobile-relevant GlobalSettings keys. Each write is a
 * single-key partial update merged server-side; a toast reminds the user
 * that settings need a filter restart (policies, by contrast, hot-reload).
 */
class SettingsPreferenceDataStore(
    private val context: Context,
    private val store: SettingsJsonStore,
) : PreferenceDataStore() {

    private val intKeys = setOf("log_retention_days")

    override fun getBoolean(key: String, defValue: Boolean) = store.getBool(key, defValue)

    override fun putBoolean(key: String, value: Boolean) = write(key, value)

    override fun getString(key: String, defValue: String?): String = store.getString(key, defValue ?: "")

    override fun putString(key: String, value: String?) {
        val v = value ?: return
        if (key in intKeys) {
            write(key, v.toIntOrNull() ?: return)
        } else {
            write(key, v)
        }
    }

    private fun write(key: String, value: Any) {
        try {
            store.set(key, value)
            Toast.makeText(context, R.string.settings_restart_hint, Toast.LENGTH_SHORT).show()
        } catch (e: Exception) {
            Toast.makeText(
                context,
                context.getString(R.string.settings_save_failed, e.message ?: "unknown error"),
                Toast.LENGTH_LONG,
            ).show()
            store.load()
        }
    }
}
