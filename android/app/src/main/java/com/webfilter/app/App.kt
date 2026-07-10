package com.webfilter.app

import android.app.Application
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.util.Log
import mobile.Mobile

/**
 * Application entry point: applies MDM managed configuration on process
 * start and listens for pushes while the process lives.
 *
 * ACTION_APPLICATION_RESTRICTIONS_CHANGED is runtime-register-only (a
 * manifest receiver never fires for it), and registering here covers both
 * activity-driven and VpnService-only process lifetimes. If the process is
 * dead when the EMM pushes, the change lands at the next app or service
 * start instead ([WebFilterVpnService] re-applies right before engine
 * start; re-application is an idempotent no-op when nothing changed).
 */
class App : Application() {

    companion object {
        private const val TAG = "WebFilterApp"
    }

    private val restrictionsReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            val changed = ManagedConfig.applyRestrictions(context)
            Log.i(TAG, "managed configuration push received, changed=$changed")
            if (changed && Mobile.isRunning()) {
                restartEngine()
            }
        }
    }

    override fun onCreate() {
        super.onCreate()
        ManagedConfig.applyRestrictions(this)
        registerReceiver(
            restrictionsReceiver,
            IntentFilter(Intent.ACTION_APPLICATION_RESTRICTIONS_CHANGED),
        )
    }

    /**
     * Stop/start cycle so pushed settings (ports, auth) take effect.
     * Policy-only pushes hot-reload and would not strictly need this, but
     * the apply API reports a single changed flag, and a brief tunnel blip
     * on an (infrequent) EMM push is an acceptable price for never running
     * on stale settings. VPN consent persists across the cycle, so no user
     * prompt appears.
     */
    private fun restartEngine() {
        startService(
            Intent(this, WebFilterVpnService::class.java)
                .setAction(WebFilterVpnService.ACTION_STOP),
        )
        // Give the engine a moment to release 127.0.0.1:1080/:8000 before
        // rebinding; Mobile.stop() is synchronous but the service teardown
        // (stopSelf, notification) is not.
        android.os.Handler(mainLooper).postDelayed({
            startForegroundService(
                Intent(this, WebFilterVpnService::class.java)
                    .setAction(WebFilterVpnService.ACTION_START),
            )
        }, 500)
    }
}
