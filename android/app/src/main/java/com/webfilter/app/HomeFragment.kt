package com.webfilter.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.res.ColorStateList
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.fragment.app.Fragment
import org.json.JSONArray
import org.json.JSONObject
import mobile.Mobile
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * The integrated dashboard: protection toggle, 24h stats, hourly blocks
 * chart, setup checklist (CA, apps, policy, enforcement), recent blocks and
 * top blocked domains. Everything is read through the gomobile API (the
 * same aggregates the web dashboard uses), so it works with the web UI off.
 */
class HomeFragment : Fragment(), MainActivity.EngineStateListener {

    /** Everything the screen shows, gathered off the main thread. */
    private class Snapshot(
        val running: Boolean,
        val status: JSONObject,
        val analytics: JSONObject,
        val recent: JSONArray,
        val caInstalled: Boolean,
        val managed: Boolean,
        val activePolicy: String,
        val policy: JSONObject?,
    )

    private val handler = Handler(Looper.getMainLooper())
    private val timeFmt = SimpleDateFormat("HH:mm", Locale.getDefault())
    private var loading = false

    private val poll = object : Runnable {
        override fun run() {
            refresh()
            handler.postDelayed(this, POLL_MS)
        }
    }

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View =
        inflater.inflate(R.layout.fragment_home, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        view.findViewById<View>(R.id.powerButton).setOnClickListener {
            (activity as? MainActivity)?.toggleFilter()
        }
        view.findViewById<View>(R.id.seeAllButton).setOnClickListener {
            (activity as? MainActivity)?.selectTab(R.id.nav_logs)
        }
        view.findViewById<View>(R.id.analyticsButton).setOnClickListener {
            startActivity(Intent(requireContext(), AnalyticsActivity::class.java))
        }
        setupTile(view.findViewById(R.id.statRequests), R.drawable.ic_globe, R.string.home_stat_requests)
        setupTile(view.findViewById(R.id.statBlocked), R.drawable.ic_block, R.string.home_stat_blocked)
        setupTile(view.findViewById(R.id.statRate), R.drawable.ic_bar_chart, R.string.home_stat_rate)
    }

    override fun onResume() {
        super.onResume()
        if (!isHidden) startPolling()
    }

    override fun onPause() {
        handler.removeCallbacks(poll)
        super.onPause()
    }

    override fun onHiddenChanged(hidden: Boolean) {
        super.onHiddenChanged(hidden)
        if (hidden) handler.removeCallbacks(poll) else startPolling()
    }

    override fun onEngineStateChanged() = refresh()

    private fun startPolling() {
        handler.removeCallbacks(poll)
        poll.run()
    }

    private fun setupTile(tile: View, icon: Int, label: Int) {
        tile.findViewById<ImageView>(R.id.statIcon).apply {
            setImageResource(icon)
            imageTintList = ColorStateList.valueOf(Rows.attrColor(context, androidx.appcompat.R.attr.colorPrimary))
        }
        tile.findViewById<TextView>(R.id.statLabel).setText(label)
    }

    private fun refresh() {
        if (loading || context == null) return
        loading = true
        val ctx = requireContext().applicationContext
        val dir = ctx.filesDir.absolutePath
        Thread {
            val running = EngineState.running
            // Status takes the engine lock, which a start holds throughout.
            val status = if (EngineState.busy) JSONObject() else parse { JSONObject(Mobile.status()) } ?: JSONObject()
            val analytics = parse { JSONObject(Mobile.analyticsJson(dir, 24)) } ?: JSONObject()
            val recent = parse {
                JSONObject(Mobile.queryLogsJson(dir, "blocks", RECENT_ROWS.toLong())).optJSONArray("entries")
            } ?: JSONArray()
            val active = (if (EngineState.busy) "" else Mobile.activePolicyName()).ifEmpty { "default" }
            val snap = Snapshot(
                running = running,
                status = status,
                analytics = analytics,
                recent = recent,
                caInstalled = CaStatus.isInstalled(ctx),
                managed = ManagedState.isManaged(ctx),
                activePolicy = active,
                policy = parse { JSONObject(Mobile.getPolicyJson(dir, active)) },
            )
            handler.post {
                loading = false
                if (isAdded && view != null) render(snap)
            }
        }.start()
    }

    private inline fun <T> parse(block: () -> T?): T? = try {
        block()
    } catch (_: Exception) {
        null
    }

    private fun render(s: Snapshot) {
        val root = requireView()
        renderHero(root, s)
        renderStats(root, s.analytics)
        renderChart(root, s.analytics)
        renderStatusRows(root.findViewById(R.id.statusRows), s)
        renderRecent(root.findViewById(R.id.recentRows), s.recent)
        renderTopDomains(root.findViewById(R.id.topDomains), s.analytics.optJSONArray("top_blocked_domains") ?: JSONArray())
    }

    private fun renderHero(root: View, s: Snapshot) {
        val ctx = root.context
        val tone = if (s.running) Tone.OK else Tone.OFF
        val (fg, bg) = Rows.toneColors(ctx, tone)
        val ring = GradientDrawable().apply {
            shape = GradientDrawable.OVAL
            setColor(bg)
            setStroke(Rows.dp(ctx, 6), fg)
        }
        root.findViewById<FrameLayout>(R.id.powerButton).background = ring
        root.findViewById<ImageView>(R.id.powerIcon).apply {
            setImageResource(if (s.running) R.drawable.ic_verified_user else R.drawable.ic_power)
            imageTintList = ColorStateList.valueOf(fg)
        }
        root.findViewById<TextView>(R.id.statusTitle).apply {
            setText(if (s.running) R.string.home_protected else R.string.home_not_protected)
            setTextColor(fg)
        }
        root.findViewById<TextView>(R.id.statusSubtitle).setText(
            when {
                EngineState.busy -> R.string.home_working
                s.running -> R.string.home_tap_to_stop
                else -> R.string.home_tap_to_start
            },
        )

        val proxyMode = if (s.running) s.status.optString("mode") == "proxy" else Prefs.effectiveProxyOnly(ctx)
        root.findViewById<TextView>(R.id.modeChip).apply {
            setText(if (proxyMode) R.string.home_mode_proxy else R.string.home_mode_vpn)
            setCompoundDrawablesRelativeWithIntrinsicBounds(
                if (proxyMode) R.drawable.ic_globe else R.drawable.ic_vpn, 0, 0, 0,
            )
            androidx.core.widget.TextViewCompat.setCompoundDrawableTintList(this, ColorStateList.valueOf(currentTextColor))
        }

        val proxyInfo = root.findViewById<TextView>(R.id.proxyInfo)
        val pacUrl = s.status.optString("pacUrl", "")
        if (s.running && proxyMode && pacUrl.isNotEmpty()) {
            val port = s.status.optInt("proxyPort", 0)
            proxyInfo.visibility = View.VISIBLE
            proxyInfo.text = getString(R.string.proxy_info, "127.0.0.1:$port", pacUrl)
            proxyInfo.setOnClickListener {
                val cm = ctx.getSystemService(ClipboardManager::class.java)
                cm.setPrimaryClip(ClipData.newPlainText("PAC URL", pacUrl))
                Toast.makeText(ctx, R.string.pac_copied, Toast.LENGTH_SHORT).show()
            }
        } else {
            proxyInfo.visibility = View.GONE
        }
    }

    private fun renderStats(root: View, a: JSONObject) {
        val requests = a.optInt("total_requests")
        val blocks = a.optInt("total_blocks")
        root.findViewById<View>(R.id.statRequests).findViewById<TextView>(R.id.statValue).text = compact(requests)
        root.findViewById<View>(R.id.statBlocked).findViewById<TextView>(R.id.statValue).text = compact(blocks)
        root.findViewById<View>(R.id.statRate).findViewById<TextView>(R.id.statValue).text =
            if (requests > 0) String.format(Locale.getDefault(), "%.1f%%", 100.0 * blocks / requests) else "—"
    }

    private fun compact(n: Int): String = when {
        n >= 1_000_000 -> String.format(Locale.getDefault(), "%.1fM", n / 1_000_000.0)
        n >= 10_000 -> String.format(Locale.getDefault(), "%.1fk", n / 1_000.0)
        else -> n.toString()
    }

    private fun renderChart(root: View, a: JSONObject) {
        val container = root.findViewById<FrameLayout>(R.id.chartContainer)
        container.removeAllViews()
        val timeline = a.optJSONArray("blocks_timeline") ?: JSONArray()
        val counts = BarsView.hourly(timeline, 24)
        if (counts.isEmpty() || counts.all { it == 0 }) {
            container.addView(TextView(root.context).apply {
                setText(R.string.analytics_no_data)
                setTextAppearance(R.style.TextAppearance_WebFilter_Caption)
            })
        } else {
            val color = Rows.attrColor(root.context, androidx.appcompat.R.attr.colorPrimary)
            container.addView(BarsView(root.context, counts, color))
        }
    }

    private fun renderStatusRows(container: LinearLayout, s: Snapshot) {
        val ctx = container.context
        container.removeAllViews()

        if (s.caInstalled) {
            Rows.add(container, R.drawable.ic_lock, getString(R.string.home_ca_ok), getString(R.string.home_ca_ok_sub), Tone.OK, chevron = true) {
                startActivity(Intent(ctx, CaInstallActivity::class.java))
            }
        } else {
            Rows.add(container, R.drawable.ic_lock, getString(R.string.home_ca_missing), getString(R.string.home_ca_missing_sub), Tone.WARN, chevron = true) {
                startActivity(Intent(ctx, CaInstallActivity::class.java))
            }
        }

        val proxyMode = Prefs.effectiveProxyOnly(ctx)
        val selected = Prefs.selectedApps(ctx)
        val appsSub = when {
            proxyMode -> getString(R.string.home_apps_proxy_mode)
            selected.isEmpty() -> getString(R.string.apps_all)
            else -> resources.getQuantityString(R.plurals.apps_selected, selected.size, selected.size)
        }
        Rows.add(container, R.drawable.ic_apps, getString(R.string.title_app_picker), appsSub, chevron = true) {
            startActivity(Intent(ctx, AppPickerActivity::class.java))
        }

        val modules = s.policy?.let { PolicyModules.enabled(it) }.orEmpty()
        val policySub = if (modules.isEmpty()) {
            getString(R.string.home_policy_nothing_on)
        } else {
            modules.joinToString(", ") { getString(it.title) }
        }
        Rows.add(
            container,
            R.drawable.ic_policy,
            getString(R.string.home_policy_active, s.activePolicy),
            policySub,
            if (modules.isEmpty()) Tone.WARN else Tone.NEUTRAL,
            chevron = true,
        ) {
            (activity as? MainActivity)?.selectTab(R.id.nav_filters)
        }

        if (s.managed) {
            Rows.add(container, R.drawable.ic_business, getString(R.string.enforce_managed_title), getString(R.string.home_managed_sub), Tone.OK) {
                EnforcementInfo.show(ctx)
            }
        } else {
            Rows.add(container, R.drawable.ic_info, getString(R.string.home_not_enforced), getString(R.string.home_not_enforced_sub), Tone.WARN, chevron = true) {
                EnforcementInfo.show(ctx)
            }
        }
    }

    private fun renderRecent(container: LinearLayout, recent: JSONArray) {
        container.removeAllViews()
        if (recent.length() == 0) {
            Rows.add(container, R.drawable.ic_check_circle, getString(R.string.home_recent_empty), null, Tone.OK)
            return
        }
        for (i in 0 until minOf(recent.length(), RECENT_ROWS)) {
            val e = recent.optJSONObject(i) ?: continue
            val sub = listOf(
                timeFmt.format(Date(e.optLong("ts") * 1000)),
                e.optString("component"),
                e.optString("reason"),
            ).filter { it.isNotEmpty() }.joinToString(" · ")
            Rows.add(container, R.drawable.ic_block, e.optString("domain"), sub, Tone.OFF)
        }
    }

    private fun renderTopDomains(container: LinearLayout, arr: JSONArray) {
        val ctx = container.context
        container.removeAllViews()
        if (arr.length() == 0) {
            container.addView(TextView(ctx).apply {
                setText(R.string.analytics_no_data)
                setTextAppearance(R.style.TextAppearance_WebFilter_Caption)
            })
            return
        }
        var max = 1
        for (i in 0 until arr.length()) max = maxOf(max, arr.optJSONObject(i)?.optInt("count") ?: 0)
        val barColor = Rows.attrColor(ctx, androidx.appcompat.R.attr.colorPrimary)
        val trackColor = Rows.attrColor(ctx, com.google.android.material.R.attr.colorSurfaceContainerHigh)
        for (i in 0 until minOf(arr.length(), TOP_DOMAINS)) {
            val entry = arr.optJSONObject(i) ?: continue
            val count = entry.optInt("count")
            val row = LinearLayout(ctx).apply { orientation = LinearLayout.HORIZONTAL }
            row.addView(
                TextView(ctx).apply {
                    text = entry.optString("domain")
                    maxLines = 1
                    ellipsize = android.text.TextUtils.TruncateAt.MIDDLE
                },
                LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f),
            )
            row.addView(TextView(ctx).apply {
                text = count.toString()
                setTextAppearance(R.style.TextAppearance_WebFilter_Caption)
            })
            container.addView(row)
            val track = LinearLayout(ctx).apply {
                orientation = LinearLayout.HORIZONTAL
                background = GradientDrawable().apply {
                    cornerRadius = Rows.dp(ctx, 3).toFloat()
                    setColor(trackColor)
                }
                addView(
                    View(ctx).apply {
                        background = GradientDrawable().apply {
                            cornerRadius = Rows.dp(ctx, 3).toFloat()
                            setColor(barColor)
                        }
                    },
                    LinearLayout.LayoutParams(0, Rows.dp(ctx, 6), count.toFloat()),
                )
                addView(View(ctx), LinearLayout.LayoutParams(0, Rows.dp(ctx, 6), (max - count).toFloat()))
            }
            container.addView(
                track,
                LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, Rows.dp(ctx, 6)).apply {
                    topMargin = Rows.dp(ctx, 4)
                    bottomMargin = Rows.dp(ctx, 10)
                },
            )
        }
    }

    companion object {
        private const val POLL_MS = 5000L
        private const val RECENT_ROWS = 5
        private const val TOP_DOMAINS = 5
    }
}
