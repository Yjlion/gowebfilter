package com.webfilter.app

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.TextView
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import mobile.Mobile

/**
 * Home screen: start/stop the filter, open the per-app picker and CA install
 * flow, and embed the management dashboard (served by the Go engine on
 * 127.0.0.1) in a WebView once filtering is running.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var statusText: TextView
    private lateinit var toggleButton: Button
    private lateinit var dashboard: WebView

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
        dashboard = findViewById(R.id.dashboard)

        toggleButton.setOnClickListener { onToggle() }
        findViewById<Button>(R.id.appsButton).setOnClickListener {
            startActivity(Intent(this, AppPickerActivity::class.java))
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

        if (running) {
            val url = Mobile.mgmtUrl()
            if (url.isNotEmpty() && dashboard.url != url) {
                dashboard.loadUrl(url)
            }
        } else {
            dashboard.loadUrl("about:blank")
        }
    }
}
