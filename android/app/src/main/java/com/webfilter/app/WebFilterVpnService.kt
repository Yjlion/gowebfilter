package com.webfilter.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import mobile.Mobile

/**
 * Establishes the system VPN, hands its TUN file descriptor to the embedded
 * Go engine, and keeps the process alive as a foreground service.
 *
 * The TUN parameters mirror models.NewTun2SocksConfig on the Go side
 * (198.18.0.0/15, MTU 1500) so tun2socks and the interface agree. Whole-device
 * traffic → TUN → gVisor netstack (in Go) → in-process SOCKS5 → the existing
 * MITM/addon pipeline. No root; VpnService owns the interface.
 */
class WebFilterVpnService : VpnService() {

    private var tunInterface: ParcelFileDescriptor? = null

    companion object {
        private const val TAG = "WebFilterVpn"
        private const val CHANNEL_ID = "webfilter_vpn"
        private const val NOTIF_ID = 1

        const val ACTION_START = "com.webfilter.app.START"
        const val ACTION_START_PROXY_ONLY = "com.webfilter.app.START_PROXY_ONLY"
        const val ACTION_STOP = "com.webfilter.app.STOP"

        // Matches models.NewTun2SocksConfig defaults (a 198.18.0.0/15 TUN).
        private const val TUN_ADDRESS = "198.18.0.1"
        private const val TUN_PREFIX = 15
        private const val TUN_MTU = 1500
        private const val DNS_SERVER = "1.1.1.1"
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                stopFilter()
                return START_NOT_STICKY
            }
            ACTION_START_PROXY_ONLY -> startProxyOnlyFilter()
            else -> startFilter()
        }
        return START_STICKY
    }

    private fun startFilter() {
        if (Mobile.isRunning()) {
            Log.i(TAG, "engine already running")
            return
        }

        // Apply any pending MDM managed configuration before the engine
        // boots (idempotent no-op when nothing changed). Combined with the
        // self-bootstrapping apply path on the Go side, a managed device
        // never runs its first session on unmanaged defaults.
        ManagedConfig.applyRestrictions(this)

        val builder = Builder()
            .setSession(getString(R.string.app_name))
            .setMtu(TUN_MTU)
            .addAddress(TUN_ADDRESS, TUN_PREFIX)
            .addDnsServer(DNS_SERVER)
            .addRoute("0.0.0.0", 0)

        applyPerAppSelection(builder)

        val pfd = builder.establish()
        if (pfd == null) {
            Log.e(TAG, "VpnService.establish() returned null")
            stopSelf()
            return
        }
        tunInterface = pfd

        startForegroundWithNotification()

        try {
            // Ownership of the fd transfers to Go, which closes it on Stop().
            Mobile.start(filesDir.absolutePath, pfd.detachFd().toLong())
        } catch (e: Exception) {
            Log.e(TAG, "Mobile.start failed", e)
            stopFilter()
        }
    }

    /**
     * Proxy-only mode: no TUN, nothing is captured system-wide. Only
     * clients explicitly configured against the loopback HTTP proxy (or
     * its PAC file — e.g. Chrome via an MDM ProxySettings policy) are
     * filtered. Still a foreground service so the engine outlives the
     * activity. A VpnService runs fine as a plain service when establish()
     * is never called.
     */
    private fun startProxyOnlyFilter() {
        if (Mobile.isRunning()) {
            Log.i(TAG, "engine already running")
            return
        }
        ManagedConfig.applyRestrictions(this)
        startForegroundWithNotification(proxyOnly = true)
        try {
            Mobile.startProxyOnly(filesDir.absolutePath)
        } catch (e: Exception) {
            Log.e(TAG, "Mobile.startProxyOnly failed", e)
            stopFilter()
        }
    }

    /**
     * Route only the user-selected apps through the filter, or every app when
     * the selection is empty. This app itself is always excluded so its own
     * mgmt/loopback traffic never loops back through the TUN.
     */
    private fun applyPerAppSelection(builder: Builder) {
        val selected = Prefs.selectedApps(this)
        try {
            builder.addDisallowedApplication(packageName)
        } catch (_: Exception) {
        }
        if (selected.isEmpty()) return
        for (pkg in selected) {
            if (pkg == packageName) continue
            try {
                builder.addAllowedApplication(pkg)
            } catch (e: Exception) {
                Log.w(TAG, "cannot add allowed app $pkg", e)
            }
        }
    }

    private fun stopFilter() {
        try {
            Mobile.stop()
        } catch (e: Exception) {
            Log.w(TAG, "Mobile.stop failed", e)
        }
        tunInterface?.close()
        tunInterface = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onRevoke() {
        Log.i(TAG, "VPN revoked by system/user")
        stopFilter()
        super.onRevoke()
    }

    override fun onDestroy() {
        stopFilter()
        super.onDestroy()
    }

    private fun startForegroundWithNotification(proxyOnly: Boolean = false) {
        val nm = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                getString(R.string.notif_channel_name),
                NotificationManager.IMPORTANCE_LOW,
            )
            nm.createNotificationChannel(channel)
        }

        val openIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE,
        )

        val notification: Notification = Notification.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(if (proxyOnly) R.string.notif_title_proxy else R.string.notif_title))
            .setContentText(getString(R.string.notif_text))
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentIntent(openIntent)
            .setOngoing(true)
            .build()

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            // API 34 validates the FGS type: systemExempted is only allowed
            // while this app is the active VPN (establish() ran), so
            // proxy-only mode must declare itself specialUse instead.
            val type = if (proxyOnly) {
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE
            } else {
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED
            }
            startForeground(NOTIF_ID, notification, type)
        } else {
            startForeground(NOTIF_ID, notification)
        }
    }
}
