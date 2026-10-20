package com.webfilter.app

import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import org.json.JSONObject

/**
 * The filter modules a policy is made of, as the Filters tab and the home
 * dashboard present them. [enabledPath] is the policy JSON switch the
 * inline toggle writes (null = no single switch, the row only navigates);
 * [screen] is the preference XML for the detail screen (null = a dedicated
 * activity, see [Kind]).
 */
data class PolicyModule(
    val key: String,
    @StringRes val title: Int,
    @StringRes val summary: Int,
    @DrawableRes val icon: Int,
    val enabledPath: String?,
    val screen: Int?,
    val kind: Kind = Kind.PREFS,
    val tintIcon: Boolean = true,
) {
    enum class Kind { PREFS, CATEGORIES, SCHEDULE }

    fun isEnabled(policy: JSONObject): Boolean {
        val path = enabledPath ?: return false
        var cur: Any? = policy
        for (part in path.split('.')) cur = (cur as? JSONObject)?.opt(part)
        return cur as? Boolean ?: false
    }
}

object PolicyModules {

    val all: List<PolicyModule> = listOf(
        PolicyModule("safesearch", R.string.cat_safesearch, R.string.mod_safesearch, R.drawable.ic_search, "safesearch.enabled", R.xml.prefs_safesearch),
        PolicyModule("adblock", R.string.cat_adblock, R.string.mod_adblock, R.drawable.ic_block, "adblock.enabled", R.xml.prefs_adblock),
        PolicyModule("doh", R.string.cat_doh, R.string.mod_doh, R.drawable.ic_dns, "doh.enabled", R.xml.prefs_doh),
        PolicyModule("url_filter", R.string.cat_url_filter, R.string.mod_url_filter, R.drawable.ic_globe, "url_filter.enabled", R.xml.prefs_url_filter),
        PolicyModule("categories", R.string.title_categories, R.string.mod_categories, R.drawable.ic_category, null, null, PolicyModule.Kind.CATEGORIES),
        PolicyModule("youtube", R.string.cat_youtube, R.string.mod_youtube, R.drawable.ic_engine_youtube, "youtube.enabled", R.xml.prefs_youtube, tintIcon = false),
        PolicyModule("text_classifier", R.string.pref_text_classifier, R.string.mod_text_classifier, R.drawable.ic_text, "text_classifier.enabled", R.xml.prefs_classifiers),
        PolicyModule("image_classifier", R.string.pref_image_classifier, R.string.mod_image_classifier, R.drawable.ic_image, "image_classifier.enabled", R.xml.prefs_classifiers),
        PolicyModule("block_page", R.string.cat_block_page, R.string.mod_block_page, R.drawable.ic_web, null, R.xml.prefs_block_page),
        PolicyModule("schedule", R.string.title_schedule, R.string.pref_schedule_summary, R.drawable.ic_schedule, null, null, PolicyModule.Kind.SCHEDULE),
    )

    /** Modules with an on/off switch that is currently on. */
    fun enabled(policy: JSONObject): List<PolicyModule> =
        all.filter { it.enabledPath != null && it.isEnabled(policy) }

    /** Number of categories this policy blocks (url_filter.category_actions). */
    fun blockedCategories(policy: JSONObject): Int {
        val actions = policy.optJSONObject("url_filter")?.optJSONObject("category_actions") ?: return 0
        return actions.keys().asSequence().count { actions.optString(it) == "block" }
    }
}
