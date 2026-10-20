package com.webfilter.app

import android.content.Context

/**
 * Thin wrapper over SharedPreferences for app-side state that is not engine
 * config: the per-app filtering selection, the start mode, the web UI
 * switch, and first-run bookkeeping.
 *
 * On a single-user phone the desktop build's source-IP policy tiers collapse
 * to a catch-all, so the meaningful axis here is *which apps* are routed
 * through the filter. An empty selection means "filter every app".
 */
object Prefs {
    private const val FILE = "webfilter_prefs"
    private const val KEY_SELECTED_APPS = "selected_apps"
    private const val KEY_APPS_INITIALIZED = "apps_initialized"
    private const val KEY_PROXY_ONLY = "proxy_only_mode"
    private const val KEY_WEB_UI = "web_ui_enabled"
    private const val KEY_ONBOARDING_DONE = "onboarding_done"

    private fun prefs(context: Context) =
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    /** Package names the user chose to route through the filter. */
    fun selectedApps(context: Context): Set<String> =
        prefs(context).getStringSet(KEY_SELECTED_APPS, emptySet()) ?: emptySet()

    fun setSelectedApps(context: Context, packages: Set<String>) {
        prefs(context).edit()
            .putStringSet(KEY_SELECTED_APPS, packages)
            .putBoolean(KEY_APPS_INITIALIZED, true)
            .apply()
    }

    /**
     * First-run default for the per-app selection: the installed web
     * browsers. Runs once (onboarding, or the first tunnel start if
     * onboarding was skipped); afterwards an empty selection keeps meaning
     * "filter every app". Nothing is written when no browser is detected,
     * which leaves the all-apps default in place.
     */
    fun ensureAppSelectionInitialized(context: Context) {
        val p = prefs(context)
        if (p.getBoolean(KEY_APPS_INITIALIZED, false)) return
        val browsers = BrowserApps.detect(context)
        p.edit()
            .putStringSet(KEY_SELECTED_APPS, browsers)
            .putBoolean(KEY_APPS_INITIALIZED, true)
            .apply()
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

    /** Effective start mode: the MDM restriction wins over the local switch. */
    fun effectiveProxyOnly(context: Context): Boolean =
        ManagedConfig.forcedProxyOnly(context) ?: proxyOnlyMode(context)

    /** Local "Web interface" switch (Settings › Advanced); off by default. */
    fun webUiEnabled(context: Context): Boolean =
        prefs(context).getBoolean(KEY_WEB_UI, false)

    fun setWebUiEnabled(context: Context, enabled: Boolean) {
        prefs(context).edit().putBoolean(KEY_WEB_UI, enabled).apply()
        applyWebUi(context)
    }

    /** Effective web UI switch: the MDM `web_ui_enabled` restriction wins. */
    fun effectiveWebUi(context: Context): Boolean =
        ManagedConfig.forcedWebUi(context) ?: webUiEnabled(context)

    /**
     * Pushes the effective web UI switch into the Go engine, which gates the
     * loopback management server per request (no restart needed).
     */
    fun applyWebUi(context: Context) {
        mobile.Mobile.setWebUiEnabled(effectiveWebUi(context))
    }

    fun onboardingDone(context: Context): Boolean =
        prefs(context).getBoolean(KEY_ONBOARDING_DONE, false)

    fun setOnboardingDone(context: Context) {
        prefs(context).edit().putBoolean(KEY_ONBOARDING_DONE, true).apply()
    }
}
