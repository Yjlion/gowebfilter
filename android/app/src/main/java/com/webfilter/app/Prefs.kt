package com.webfilter.app

import android.content.Context

/**
 * Thin wrapper over SharedPreferences for the per-app filtering selection.
 *
 * On a single-user phone the desktop build's source-IP policy tiers collapse
 * to a catch-all, so the meaningful axis here is *which apps* are routed
 * through the filter. An empty selection means "filter every app".
 */
object Prefs {
    private const val FILE = "webfilter_prefs"
    private const val KEY_SELECTED_APPS = "selected_apps"
    private const val KEY_PROXY_ONLY = "proxy_only_mode"

    private fun prefs(context: Context) =
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    /** Package names the user chose to route through the filter. */
    fun selectedApps(context: Context): Set<String> =
        prefs(context).getStringSet(KEY_SELECTED_APPS, emptySet()) ?: emptySet()

    fun setSelectedApps(context: Context, packages: Set<String>) {
        prefs(context).edit().putStringSet(KEY_SELECTED_APPS, packages).apply()
    }

    /**
     * Local proxy-only-mode choice (no VPN; only explicitly configured
     * clients are filtered). An MDM `proxy_only_mode` restriction
     * (ManagedConfig.forcedProxyOnly) overrides this.
     */
    fun proxyOnlyMode(context: Context): Boolean =
        prefs(context).getBoolean(KEY_PROXY_ONLY, false)

    fun setProxyOnlyMode(context: Context, enabled: Boolean) {
        prefs(context).edit().putBoolean(KEY_PROXY_ONLY, enabled).apply()
    }
}
