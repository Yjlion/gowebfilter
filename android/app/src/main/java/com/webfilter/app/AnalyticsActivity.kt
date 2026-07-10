package com.webfilter.app

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.os.Bundle
import android.view.View
import android.widget.AdapterView
import android.widget.ArrayAdapter
import android.widget.LinearLayout
import android.widget.Spinner
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import org.json.JSONArray
import org.json.JSONObject
import mobile.Mobile

/**
 * Native analytics screen over Mobile.analyticsJson (the same aggregates
 * as the dashboard's /api/analytics): summary counts, top blocked domains,
 * blocks by component as proportional bars, and an hourly blocks timeline
 * drawn by a minimal custom view. No chart library — plain views, matching
 * the rest of the app.
 */
class AnalyticsActivity : AppCompatActivity() {

    private val windows = intArrayOf(24, 7 * 24, 30 * 24)
    private var hours = 24

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_analytics)

        val spinner = findViewById<Spinner>(R.id.windowSpinner)
        spinner.adapter = ArrayAdapter(
            this,
            android.R.layout.simple_spinner_dropdown_item,
            resources.getStringArray(R.array.analytics_window_labels),
        )
        spinner.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) {
                if (windows[position] != hours) {
                    hours = windows[position]
                    refresh()
                }
            }

            override fun onNothingSelected(parent: AdapterView<*>?) {}
        }
    }

    override fun onResume() {
        super.onResume()
        refresh()
    }

    private fun refresh() {
        Thread {
            val result = try {
                JSONObject(Mobile.analyticsJson(filesDir.absolutePath, hours))
            } catch (e: Exception) {
                runOnUiThread {
                    Toast.makeText(this, getString(R.string.settings_save_failed, e.message ?: "error"), Toast.LENGTH_LONG).show()
                }
                JSONObject()
            }
            runOnUiThread { render(result) }
        }.start()
    }

    private fun render(a: JSONObject) {
        findViewById<TextView>(R.id.totalRequests).text = a.optInt("total_requests").toString()
        findViewById<TextView>(R.id.totalBlocks).text = a.optInt("total_blocks").toString()

        val actions = a.optJSONObject("request_actions") ?: JSONObject()
        findViewById<TextView>(R.id.actionsBreakdown).text =
            actions.keys().asSequence()
                .map { "$it: ${actions.optInt(it)}" }
                .sorted()
                .joinToString("   ")
                .ifEmpty { getString(R.string.analytics_no_data) }

        renderCounts(
            findViewById(R.id.topDomains),
            a.optJSONArray("top_blocked_domains") ?: JSONArray(),
            "domain",
        )
        renderCounts(
            findViewById(R.id.byComponent),
            a.optJSONArray("blocks_by_component") ?: JSONArray(),
            "component",
        )

        val timeline = a.optJSONArray("blocks_timeline") ?: JSONArray()
        val counts = IntArray(timeline.length())
        for (i in 0 until timeline.length()) {
            counts[i] = timeline.optJSONObject(i)?.optInt("count") ?: 0
        }
        val container = findViewById<LinearLayout>(R.id.timelineContainer)
        container.removeAllViews()
        if (counts.isEmpty()) {
            container.addView(TextView(this).apply { setText(R.string.analytics_no_data) })
        } else {
            container.addView(
                BarsView(this, counts),
                LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, dp(96)),
            )
        }
    }

    /** Labeled rows with a proportional bar under each label. */
    private fun renderCounts(container: LinearLayout, arr: JSONArray, labelKey: String) {
        container.removeAllViews()
        if (arr.length() == 0) {
            container.addView(TextView(this).apply { setText(R.string.analytics_no_data) })
            return
        }
        var max = 1
        for (i in 0 until arr.length()) {
            max = maxOf(max, arr.optJSONObject(i)?.optInt("count") ?: 0)
        }
        for (i in 0 until arr.length()) {
            val entry = arr.optJSONObject(i) ?: continue
            val count = entry.optInt("count")
            container.addView(TextView(this).apply {
                text = getString(R.string.analytics_count_row, entry.optString(labelKey), count)
                textSize = 13f
            })
            val bar = View(this).apply { setBackgroundColor(0xFF6750A4.toInt()) }
            val track = LinearLayout(this).apply {
                orientation = LinearLayout.HORIZONTAL
                addView(bar, LinearLayout.LayoutParams(0, dp(6), count.toFloat()))
                addView(View(context), LinearLayout.LayoutParams(0, dp(6), (max - count).toFloat()))
            }
            container.addView(
                track,
                LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, dp(6)).apply {
                    bottomMargin = dp(6)
                },
            )
        }
    }

    private fun dp(v: Int): Int = (v * resources.displayMetrics.density).toInt()

    /** Minimal bar chart: one bar per hourly bucket, heights normalized. */
    private class BarsView(context: Context, private val counts: IntArray) : View(context) {
        private val paint = Paint().apply { color = 0xFF6750A4.toInt() }

        override fun onDraw(canvas: Canvas) {
            super.onDraw(canvas)
            if (counts.isEmpty()) return
            val max = counts.max().coerceAtLeast(1)
            val slot = width.toFloat() / counts.size
            val barWidth = (slot * 0.7f).coerceAtLeast(1f)
            for (i in counts.indices) {
                val h = height * counts[i].toFloat() / max
                val left = i * slot + (slot - barWidth) / 2
                canvas.drawRect(left, height - h, left + barWidth, height.toFloat(), paint)
            }
        }
    }
}
