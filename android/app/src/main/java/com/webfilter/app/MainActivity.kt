package com.webfilter.app

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.fragment.app.Fragment
import com.google.android.material.appbar.MaterialToolbar
import com.google.android.material.bottomnavigation.BottomNavigationView

/**
 * The app shell: a toolbar, four tabs (Home dashboard, Filters, Logs,
 * Settings) and the filter start/stop plumbing they share — VPN consent,
 * VPN vs proxy-only start, stop. First launch routes through
 * [OnboardingActivity].
 *
 * The management web UI is deliberately not embedded here any more; it is
 * opt-in under Settings › Advanced and opens in [WebUiActivity].
 */
class MainActivity : AppCompatActivity() {

    /** Tabs that redraw when the engine starts or stops. */
    interface EngineStateListener {
        fun onEngineStateChanged()
    }

    private val handler = Handler(Looper.getMainLooper())
    private lateinit var bottomNav: BottomNavigationView

    private val vpnConsent =
        registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
            if (result.resultCode == RESULT_OK) startVpn()
        }

    private val onboarding =
        registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
            if (result.resultCode == RESULT_OK &&
                result.data?.getBooleanExtra(OnboardingActivity.EXTRA_START, false) == true &&
                !EngineState.running
            ) {
                toggleFilter()
            }
            notifyEngineState()
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        setSupportActionBar(findViewById<MaterialToolbar>(R.id.toolbar))

        bottomNav = findViewById(R.id.bottomNav)
        bottomNav.setOnItemSelectedListener { item ->
            showTab(item.itemId)
            true
        }
        if (savedInstanceState == null) {
            showTab(R.id.nav_home)
            if (!Prefs.onboardingDone(this)) {
                onboarding.launch(Intent(this, OnboardingActivity::class.java))
            }
        }
    }

    override fun onRestoreInstanceState(savedInstanceState: Bundle) {
        super.onRestoreInstanceState(savedInstanceState)
        // Fragments (and their hidden state) are restored for us; the
        // selected tab is restored by now, the toolbar title is not.
        supportActionBar?.title = titleFor(bottomNav.selectedItemId)
    }

    private fun titleFor(itemId: Int): String = getString(
        when (itemId) {
            R.id.nav_filters -> R.string.nav_filters
            R.id.nav_logs -> R.string.nav_logs
            R.id.nav_settings -> R.string.nav_settings
            else -> R.string.app_name
        },
    )

    /** Switches tabs programmatically (e.g. Home's "See all" → Logs). */
    fun selectTab(itemId: Int) {
        bottomNav.selectedItemId = itemId
    }

    /**
     * Tab fragments are created once and shown/hidden, so a tab keeps its
     * scroll position and expanded cards when the user switches back.
     */
    private fun showTab(itemId: Int) {
        val tag = "tab_$itemId"
        val fm = supportFragmentManager
        val tx = fm.beginTransaction()
        for (f in fm.fragments) {
            if (f.tag?.startsWith("tab_") == true && f.tag != tag) tx.hide(f)
        }
        val existing = fm.findFragmentByTag(tag)
        if (existing != null) {
            tx.show(existing)
        } else {
            tx.add(R.id.tabContainer, newTab(itemId), tag)
        }
        tx.commit()
        supportActionBar?.title = titleFor(itemId)
    }

    private fun newTab(itemId: Int): Fragment = when (itemId) {
        R.id.nav_filters -> FiltersFragment()
        R.id.nav_logs -> LogsFragment()
        R.id.nav_settings -> AppSettingsFragment()
        else -> HomeFragment()
    }

    /** Start or stop filtering, in the effective mode (MDM wins). */
    fun toggleFilter() {
        // A start/stop is still in flight on the engine worker.
        if (EngineState.busy) return
        if (EngineState.running) {
            stopFilter()
        } else if (Prefs.effectiveProxyOnly(this)) {
            // No TUN is established, so no VPN consent dialog is needed.
            startFilterService(WebFilterVpnService.ACTION_START_PROXY_ONLY)
        } else {
            // Ask the user for VPN consent; startVpn() runs on OK.
            val prepare = VpnService.prepare(this)
            if (prepare != null) vpnConsent.launch(prepare) else startVpn()
        }
    }

    private fun startVpn() = startFilterService(WebFilterVpnService.ACTION_START)

    private fun stopFilter() {
        startService(Intent(this, WebFilterVpnService::class.java).setAction(WebFilterVpnService.ACTION_STOP))
        handler.postDelayed({ notifyEngineState() }, 400)
    }

    private fun startFilterService(action: String) {
        startForegroundService(Intent(this, WebFilterVpnService::class.java).setAction(action))
        // The engine binds asynchronously; refresh shortly, then again once
        // a slow first start (CA generation, sqlite schema) has finished.
        handler.postDelayed({ notifyEngineState() }, 800)
        handler.postDelayed({ notifyEngineState() }, 2500)
    }

    fun notifyEngineState() {
        if (isFinishing || isDestroyed) return
        for (f in supportFragmentManager.fragments) {
            if (f.isAdded) (f as? EngineStateListener)?.onEngineStateChanged()
        }
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        super.onDestroy()
    }
}
