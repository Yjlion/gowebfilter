package com.webfilter.app

import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.View
import android.widget.EditText
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.preference.Preference
import androidx.preference.PreferenceFragmentCompat
import org.json.JSONObject
import mobile.Mobile

/**
 * Native settings UI: a root screen of categories, each opening a
 * [PrefsFragment] whose widgets read/write the Go engine's JSON config via
 * the gomobile API (see [ConfigStores]). When the device is MDM-managed
 * with the settings lock set, everything renders read-only and a banner
 * explains why (the Go engine enforces the lock regardless — this is just
 * honest UI).
 */
class SettingsActivity : AppCompatActivity() {

    lateinit var policyStore: PolicyJsonStore
    lateinit var settingsStore: SettingsJsonStore
    var locked = false
        private set

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_settings)

        policyStore = PolicyJsonStore.forApp(this)
        settingsStore = SettingsJsonStore.forApp(this)

        if (savedInstanceState == null) {
            supportFragmentManager.beginTransaction()
                .replace(R.id.settingsContainer, PrefsFragment.newInstance(R.xml.prefs_root, PrefsFragment.KIND_ROOT))
                .commit()
        }
    }

    override fun onResume() {
        super.onResume()
        locked = ManagedState.isLocked(this)
        findViewById<TextView>(R.id.managedBanner).visibility = if (locked) View.VISIBLE else View.GONE
    }

    fun openScreen(xmlRes: Int, kind: String) {
        supportFragmentManager.beginTransaction()
            .replace(R.id.settingsContainer, PrefsFragment.newInstance(xmlRes, kind))
            .addToBackStack(null)
            .commit()
    }
}

class PrefsFragment : PreferenceFragmentCompat() {

    companion object {
        const val KIND_ROOT = "root"
        const val KIND_POLICY = "policy"
        const val KIND_SETTINGS = "settings"
        private const val ARG_XML = "xml"
        private const val ARG_KIND = "kind"

        fun newInstance(xmlRes: Int, kind: String) = PrefsFragment().apply {
            arguments = Bundle().apply {
                putInt(ARG_XML, xmlRes)
                putString(ARG_KIND, kind)
            }
        }
    }

    private val xmlRes get() = requireArguments().getInt(ARG_XML)
    private val kind get() = requireArguments().getString(ARG_KIND) ?: KIND_ROOT
    private val host get() = requireActivity() as SettingsActivity
    private var inflatedOnce = false

    override fun onCreatePreferences(savedInstanceState: Bundle?, rootKey: String?) {
        inflateScreen(rootKey)
        inflatedOnce = true
    }

    override fun onResume() {
        super.onResume()
        // Re-read engine truth on every return to the screen: the WebView
        // dashboard (or an MDM push) can rewrite the config behind our back.
        if (inflatedOnce) {
            preferenceScreen?.removeAll()
            inflateScreen(null)
        } else {
            inflatedOnce = true
        }
        applyLockState()
        bindDynamicSummaries()
    }

    private fun inflateScreen(rootKey: String?) {
        try {
            when (kind) {
                KIND_POLICY -> {
                    host.policyStore.load()
                    preferenceManager.preferenceDataStore =
                        PolicyPreferenceDataStore(requireContext(), host.policyStore)
                }
                KIND_SETTINGS -> {
                    host.settingsStore.load()
                    preferenceManager.preferenceDataStore =
                        SettingsPreferenceDataStore(requireContext(), host.settingsStore)
                }
            }
        } catch (e: Exception) {
            Toast.makeText(
                requireContext(),
                getString(R.string.settings_save_failed, e.message ?: "load error"),
                Toast.LENGTH_LONG,
            ).show()
        }
        setPreferencesFromResource(xmlRes, rootKey)
    }

    private fun applyLockState() {
        // The root screen stays navigable when locked (viewing is allowed);
        // value screens render disabled.
        if (kind != KIND_ROOT) {
            preferenceScreen.isEnabled = !host.locked
        }
    }

    override fun onPreferenceTreeClick(preference: Preference): Boolean {
        when (preference.key) {
            "nav_safesearch" -> host.openScreen(R.xml.prefs_safesearch, KIND_POLICY)
            "nav_url_filter" -> host.openScreen(R.xml.prefs_url_filter, KIND_POLICY)
            "nav_youtube" -> host.openScreen(R.xml.prefs_youtube, KIND_POLICY)
            "nav_classifiers" -> host.openScreen(R.xml.prefs_classifiers, KIND_POLICY)
            "nav_doh" -> host.openScreen(R.xml.prefs_doh, KIND_POLICY)
            "nav_block_page" -> host.openScreen(R.xml.prefs_block_page, KIND_POLICY)
            "nav_general" -> host.openScreen(R.xml.prefs_general, KIND_SETTINGS)
            "nav_security" -> host.openScreen(R.xml.prefs_security, KIND_SETTINGS)
            "nav_schedule" -> startActivity(Intent(requireContext(), ScheduleActivity::class.java))
            "nav_apps" -> startActivity(Intent(requireContext(), AppPickerActivity::class.java))
            "change_password" -> promptNewPassword()
            else -> return super.onPreferenceTreeClick(preference)
        }
        return true
    }

    private fun promptNewPassword() {
        val input = EditText(requireContext()).apply {
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
            hint = getString(R.string.password_hint)
        }
        AlertDialog.Builder(requireContext())
            .setTitle(R.string.pref_change_password)
            .setView(input)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val pw = input.text.toString()
                if (pw.isEmpty()) return@setPositiveButton
                try {
                    host.settingsStore.update(JSONObject().put("new_password", pw))
                    Toast.makeText(requireContext(), R.string.password_set, Toast.LENGTH_SHORT).show()
                } catch (e: Exception) {
                    Toast.makeText(
                        requireContext(),
                        getString(R.string.settings_save_failed, e.message ?: "error"),
                        Toast.LENGTH_LONG,
                    ).show()
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    /** Read-only info rows on the General screen (engine status). */
    private fun bindDynamicSummaries() {
        val mgmtPref = findPreference<Preference>("info_mgmt_url") ?: return
        val listenersPref = findPreference<Preference>("info_listeners")
        try {
            val st = JSONObject(Mobile.status())
            val running = st.optBoolean("running", false)
            mgmtPref.summary = if (running) st.optString("mgmtUrl", "") else getString(R.string.status_stopped)
            val listeners = st.optJSONArray("listeners")
            listenersPref?.summary = if (running && listeners != null && listeners.length() > 0) {
                (0 until listeners.length()).joinToString(", ") { listeners.optString(it) }
            } else {
                getString(R.string.status_stopped)
            }
        } catch (_: Exception) {
            mgmtPref.summary = getString(R.string.status_stopped)
        }
    }
}
