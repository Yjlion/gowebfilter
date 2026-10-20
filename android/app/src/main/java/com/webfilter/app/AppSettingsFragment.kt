package com.webfilter.app

import android.content.Intent
import android.os.Bundle
import androidx.preference.Preference
import androidx.preference.PreferenceFragmentCompat
import androidx.preference.SwitchPreferenceCompat

/**
 * The Settings tab: app-wide, non-policy settings. Filter policies live on
 * the Filters tab; this screen covers how filtering runs (apps, mode, CA),
 * the opt-in management web interface, and the engine's general/security
 * settings. Keys here are local SharedPreferences or pure navigation — the
 * engine-backed screens open in [SettingsActivity].
 */
class AppSettingsFragment : PreferenceFragmentCompat(), MainActivity.EngineStateListener {

    override fun onCreatePreferences(savedInstanceState: Bundle?, rootKey: String?) {
        // Local prefs file, so the two switches below read/write [Prefs].
        preferenceManager.sharedPreferencesName = "webfilter_prefs"
        setPreferencesFromResource(R.xml.prefs_app, rootKey)

        findPreference<SwitchPreferenceCompat>("proxy_only_mode")?.setOnPreferenceChangeListener { _, _ ->
            // Persisted by the preference itself; takes effect on next start.
            view?.post { bind() }
            true
        }
        findPreference<SwitchPreferenceCompat>("web_ui_enabled")?.setOnPreferenceChangeListener { _, value ->
            Prefs.setWebUiEnabled(requireContext(), value as Boolean)
            view?.post { bind() }
            true
        }
    }

    override fun onResume() {
        super.onResume()
        bind()
    }

    override fun onHiddenChanged(hidden: Boolean) {
        super.onHiddenChanged(hidden)
        if (!hidden) bind()
    }

    override fun onEngineStateChanged() = bind()

    /** Summaries and MDM-forced states. */
    private fun bind() {
        val ctx = context ?: return
        val running = EngineState.running

        findPreference<Preference>("nav_ca")?.apply {
            setSummary(if (CaStatus.isInstalled(ctx)) R.string.home_ca_ok else R.string.home_ca_missing)
        }
        findPreference<Preference>("nav_apps")?.summary = Prefs.selectedApps(ctx).let {
            if (it.isEmpty()) getString(R.string.apps_all) else resources.getQuantityString(R.plurals.apps_selected, it.size, it.size)
        }

        val forcedProxy = ManagedConfig.forcedProxyOnly(ctx)
        findPreference<SwitchPreferenceCompat>("proxy_only_mode")?.apply {
            isChecked = forcedProxy ?: Prefs.proxyOnlyMode(ctx)
            isEnabled = forcedProxy == null && !running
            setSummary(
                when {
                    forcedProxy != null -> R.string.pref_forced_by_admin
                    running -> R.string.pref_proxy_only_running
                    else -> R.string.pref_proxy_only_summary
                },
            )
        }

        val forcedWeb = ManagedConfig.forcedWebUi(ctx)
        val webOn = Prefs.effectiveWebUi(ctx)
        findPreference<SwitchPreferenceCompat>("web_ui_enabled")?.apply {
            isChecked = webOn
            isEnabled = forcedWeb == null
            setSummary(if (forcedWeb != null) R.string.pref_forced_by_admin else R.string.pref_web_ui_summary)
        }
        findPreference<Preference>("open_web_ui")?.apply {
            isVisible = webOn
            summary = if (running) EngineState.mgmtUrl else getString(R.string.web_ui_needs_running)
            isEnabled = running
        }
        findPreference<Preference>("about_version")?.summary = try {
            ctx.packageManager.getPackageInfo(ctx.packageName, 0).versionName
        } catch (_: Exception) {
            ""
        }
    }

    override fun onPreferenceTreeClick(preference: Preference): Boolean {
        val ctx = requireContext()
        when (preference.key) {
            "nav_apps" -> startActivity(Intent(ctx, AppPickerActivity::class.java))
            "nav_ca" -> startActivity(Intent(ctx, CaInstallActivity::class.java))
            "nav_analytics" -> startActivity(Intent(ctx, AnalyticsActivity::class.java))
            "nav_general" -> startActivity(SettingsActivity.settingsScreen(ctx, R.xml.prefs_general, getString(R.string.cat_general)))
            "nav_security" -> startActivity(SettingsActivity.settingsScreen(ctx, R.xml.prefs_security, getString(R.string.cat_security)))
            "open_web_ui" -> startActivity(Intent(ctx, WebUiActivity::class.java))
            "about_enforcement" -> EnforcementInfo.show(ctx)
            "about_onboarding" -> startActivity(Intent(ctx, OnboardingActivity::class.java))
            else -> return super.onPreferenceTreeClick(preference)
        }
        return true
    }
}
