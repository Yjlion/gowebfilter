package com.webfilter.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.view.View
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import org.json.JSONObject
import mobile.Mobile

/**
 * Home screen: start/stop the filter, open the per-app picker and CA install
 * flow, and embed the management dashboard (served by the Go engine on
 * 127.0.0.1) in a WebView once filtering is running.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var statusText: TextView
    private lateinit var toggleButton: Button
    private lateinit var proxyOnlySwitch: Switch
    private lateinit var proxyInfo: TextView
    private lateinit var dashboard: WebView

    /**
     * Effective start mode. The MDM `proxy_only_mode` restriction (when the
     * device is managed) wins over the local switch.
     */
    private fun proxyOnlyMode(): Boolean =
        ManagedConfig.forcedProxyOnly(this) ?: Prefs.proxyOnlyMode(this)

    private val vpnConsent =
        registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
            if (result.resultCode == RESULT_OK) {
                startVpn()
            }
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        statusText = findViewById(R.id.statusText)
        toggleButton = findViewById(R.id.toggleButton)
        proxyOnlySwitch = findViewById(R.id.proxyOnlySwitch)
        proxyInfo = findViewById(R.id.proxyInfo)
        dashboard = findViewById(R.id.dashboard)

        toggleButton.setOnClickListener { onToggle() }
        proxyOnlySwitch.setOnCheckedChangeListener { _, checked ->
            Prefs.setProxyOnlyMode(this, checked)
        }
        findViewById<Button>(R.id.appsButton).setOnClickListener {
            startActivity(Intent(this, AppPickerActivity::class.java))
        }
        findViewById<Button>(R.id.settingsButton).setOnClickListener {
            startActivity(Intent(this, SettingsActivity::class.java))
        }
        findViewById<Button>(R.id.caButton).setOnClickListener {
            startActivity(Intent(this, CaInstallActivity::class.java))
        }

        dashboard.settings.javaScriptEnabled = true
        dashboard.settings.domStorageEnabled = true
        dashboard.webViewClient = WebViewClient()
    }

    override fun onResume() {
        super.onResume()
        refreshState()
    }

    private fun onToggle() {
        if (Mobile.isRunning()) {
            stopVpn()
        } else if (proxyOnlyMode()) {
            // No TUN is established, so no VPN consent dialog is needed.
            startProxyOnly()
        } else {
            // Ask the user for VPN consent; startVpn() runs on OK.
            val prepare = VpnService.prepare(this)
            if (prepare != null) {
                vpnConsent.launch(prepare)
            } else {
                startVpn()
            }
        }
    }

    private fun startVpn() {
        val intent = Intent(this, WebFilterVpnService::class.java)
            .setAction(WebFilterVpnService.ACTION_START)
        startForegroundService(intent)
        // The engine binds asynchronously; give the UI a moment then refresh.
        dashboard.postDelayed({ refreshState() }, 800)
    }

    private fun startProxyOnly() {
        val intent = Intent(this, WebFilterVpnService::class.java)
            .setAction(WebFilterVpnService.ACTION_START_PROXY_ONLY)
        startForegroundService(intent)
        dashboard.postDelayed({ refreshState() }, 800)
    }

    private fun stopVpn() {
        val intent = Intent(this, WebFilterVpnService::class.java)
            .setAction(WebFilterVpnService.ACTION_STOP)
        startService(intent)
        dashboard.postDelayed({ refreshState() }, 400)
    }

    private fun refreshState() {
        val running = Mobile.isRunning()
        statusText.setText(if (running) R.string.status_running else R.string.status_stopped)
        toggleButton.setText(if (running) R.string.stop_filter else R.string.start_filter)
        refreshModeControls(running)

        if (running) {
            val url = Mobile.mgmtUrl()
            if (url.isNotEmpty() && dashboard.url != url) {
                dashboard.loadUrl(url)
            }
        } else {
            dashboard.loadUrl("about:blank")
        }
    }

    /**
     * Mode switch state + the proxy-only info row (PAC URL / HTTP proxy
     * address with a tap-to-copy on the PAC URL). The switch is locked to
     * the pushed value on MDM-managed devices, and disabled while running
     * (switching modes requires a stop/start).
     */
    private fun refreshModeControls(running: Boolean) {
        val forced = ManagedConfig.forcedProxyOnly(this)
        proxyOnlySwitch.isChecked = forced ?: Prefs.proxyOnlyMode(this)
        proxyOnlySwitch.isEnabled = forced == null && !running

        var pacUrl = ""
        var proxyPort = 0
        var mode = ""
        try {
            val st = JSONObject(Mobile.status())
            mode = st.optString("mode", "")
            pacUrl = st.optString("pacUrl", "")
            proxyPort = st.optInt("proxyPort", 0)
        } catch (_: Exception) {
        }

        if (running && mode == "proxy" && pacUrl.isNotEmpty()) {
            proxyInfo.visibility = View.VISIBLE
            proxyInfo.text = getString(R.string.proxy_info, "127.0.0.1:$proxyPort", pacUrl)
            proxyInfo.setOnClickListener {
                val cm = getSystemService(ClipboardManager::class.java)
                cm.setPrimaryClip(ClipData.newPlainText("PAC URL", pacUrl))
                Toast.makeText(this, R.string.pac_copied, Toast.LENGTH_SHORT).show()
            }
        } else {
            proxyInfo.visibility = View.GONE
        }
    }
}
