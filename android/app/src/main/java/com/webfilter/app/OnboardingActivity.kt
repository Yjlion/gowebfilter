package com.webfilter.app

import android.content.Intent
import android.content.res.ColorStateList
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AppCompatActivity
import com.google.android.material.progressindicator.LinearProgressIndicator

/**
 * First-run walkthrough: welcome, the "no enforcement without an EMM/MDM"
 * notice, the filtered-apps default (installed browsers), CA certificate
 * setup, and a final "start protection". Finishing marks onboarding done;
 * [EXTRA_START] in the result asks MainActivity to start filtering.
 */
class OnboardingActivity : AppCompatActivity() {

    companion object {
        const val EXTRA_START = "start_protection"
        private const val STATE_PAGE = "page"
    }

    private enum class Page { WELCOME, ENFORCEMENT, APPS, CA, DONE }

    private var page = Page.WELCOME

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_onboarding)
        page = Page.entries[savedInstanceState?.getInt(STATE_PAGE) ?: 0]

        findViewById<Button>(R.id.backButton).setOnClickListener { go(-1) }
        findViewById<Button>(R.id.nextButton).setOnClickListener {
            if (page == Page.DONE) finishOnboarding(start = true) else go(+1)
        }
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (page == Page.WELCOME) finishOnboarding(start = false) else go(-1)
            }
        })
    }

    override fun onResume() {
        super.onResume()
        // Re-render: the user may be back from the app picker or CA flow.
        render()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putInt(STATE_PAGE, page.ordinal)
    }

    private fun go(delta: Int) {
        val next = page.ordinal + delta
        if (next < 0 || next >= Page.entries.size) return
        page = Page.entries[next]
        render()
    }

    private fun finishOnboarding(start: Boolean) {
        Prefs.ensureAppSelectionInitialized(this)
        Prefs.setOnboardingDone(this)
        setResult(RESULT_OK, Intent().putExtra(EXTRA_START, start))
        finish()
    }

    private fun render() {
        val progress = findViewById<LinearProgressIndicator>(R.id.progress)
        progress.setProgressCompat(100 * (page.ordinal + 1) / Page.entries.size, true)
        findViewById<View>(R.id.backButton).visibility = if (page == Page.WELCOME) View.INVISIBLE else View.VISIBLE
        val next = findViewById<Button>(R.id.nextButton)
        next.setText(
            when (page) {
                Page.ENFORCEMENT -> R.string.onb_understand
                Page.DONE -> R.string.onb_start
                else -> R.string.onb_next
            },
        )

        val extra = findViewById<LinearLayout>(R.id.pageExtra)
        extra.removeAllViews()
        when (page) {
            Page.WELCOME -> header(R.drawable.ic_shield, Tone.NEUTRAL, R.string.onb_welcome_title, getString(R.string.onb_welcome_body))
            Page.ENFORCEMENT -> renderEnforcement(extra)
            Page.APPS -> renderApps(extra)
            Page.CA -> renderCa(extra)
            Page.DONE -> renderDone(extra)
        }
    }

    private fun header(icon: Int, tone: Tone, title: Int, body: CharSequence) {
        val (fg, bg) = Rows.toneColors(this, tone)
        findViewById<ImageView>(R.id.pageIcon).apply {
            setImageResource(icon)
            imageTintList = ColorStateList.valueOf(fg)
            (background.mutate() as? GradientDrawable)?.setColor(bg)
        }
        findViewById<TextView>(R.id.pageTitle).setText(title)
        findViewById<TextView>(R.id.pageBody).text = body
    }

    private fun renderEnforcement(extra: LinearLayout) {
        if (ManagedState.isManaged(this)) {
            header(R.drawable.ic_business, Tone.OK, R.string.enforce_managed_title, getString(R.string.enforce_managed_body))
            return
        }
        header(R.drawable.ic_info, Tone.WARN, R.string.enforce_title, getString(R.string.onb_enforce_body))
        for ((title, body) in listOf(
            R.string.onb_enforce_point_vpn to R.string.onb_enforce_point_vpn_body,
            R.string.onb_enforce_point_settings to R.string.onb_enforce_point_settings_body,
            R.string.onb_enforce_point_uninstall to R.string.onb_enforce_point_uninstall_body,
        )) {
            Rows.add(extra, R.drawable.ic_warning, getString(title), getString(body), Tone.WARN)
        }
        Rows.add(extra, R.drawable.ic_business, getString(R.string.onb_enforce_mdm), getString(R.string.onb_enforce_mdm_body), Tone.OK)
    }

    private fun renderApps(extra: LinearLayout) {
        Prefs.ensureAppSelectionInitialized(this)
        val selected = Prefs.selectedApps(this)
        val body = if (selected.isEmpty()) getString(R.string.onb_apps_body_all) else getString(R.string.onb_apps_body)
        header(R.drawable.ic_apps, Tone.NEUTRAL, R.string.onb_apps_title, body)
        val pm = packageManager
        for (pkg in selected.sorted().take(8)) {
            val label = try {
                pm.getApplicationLabel(pm.getApplicationInfo(pkg, 0)).toString()
            } catch (_: Exception) {
                continue
            }
            val row = Rows.add(extra, R.drawable.ic_globe, label, pkg, tintIcon = false)
            try {
                row.findViewById<ImageView>(R.id.rowIcon).apply {
                    setImageDrawable(pm.getApplicationIcon(pkg))
                    background = null
                    setPadding(0, 0, 0, 0)
                }
            } catch (_: Exception) {
            }
        }
        if (selected.size > 8) {
            extra.addView(TextView(this).apply {
                text = getString(R.string.onb_apps_more, selected.size - 8)
                setPadding(Rows.dp(context, 16), 0, 0, 0)
            })
        }
        tonalButton(extra, R.string.onb_apps_change) {
            startActivity(Intent(this, AppPickerActivity::class.java))
        }
    }

    private fun renderCa(extra: LinearLayout) {
        header(R.drawable.ic_lock, Tone.NEUTRAL, R.string.onb_ca_title, getString(R.string.onb_ca_body))
        if (CaStatus.isInstalled(this)) {
            Rows.add(extra, R.drawable.ic_check_circle, getString(R.string.ca_status_installed), getString(R.string.home_ca_ok_sub), Tone.OK)
        } else {
            Rows.add(extra, R.drawable.ic_warning, getString(R.string.ca_status_missing), getString(R.string.onb_ca_optional), Tone.WARN)
            tonalButton(extra, R.string.onb_ca_setup) {
                startActivity(Intent(this, CaInstallActivity::class.java))
            }
        }
    }

    private fun tonalButton(parent: LinearLayout, text: Int, onClick: () -> Unit) {
        val button = layoutInflater.inflate(R.layout.button_tonal, parent, false) as Button
        button.setText(text)
        button.setOnClickListener { onClick() }
        parent.addView(button)
    }

    private fun renderDone(extra: LinearLayout) {
        header(R.drawable.ic_verified_user, Tone.OK, R.string.onb_done_title, getString(R.string.onb_done_body))
        // What the default policy actually has on (the preconfigured
        // baseline on a fresh install, or whatever an MDM/earlier edit set).
        val enabled = try {
            PolicyModules.enabled(org.json.JSONObject(mobile.Mobile.getPolicyJson(filesDir.absolutePath, "default")))
        } catch (_: Exception) {
            emptyList()
        }
        for (m in enabled) {
            Rows.add(extra, m.icon, getString(m.title), getString(m.summary), Tone.OK, tintIcon = m.tintIcon)
        }
    }
}
