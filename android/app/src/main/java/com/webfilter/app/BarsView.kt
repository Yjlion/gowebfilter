package com.webfilter.app

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.RectF
import android.view.View
import org.json.JSONArray

/** Minimal bar chart: one rounded bar per bucket, heights normalized. */
class BarsView(context: Context, private val counts: IntArray, color: Int) : View(context) {
    private val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply { this.color = color }
    private val rect = RectF()
    private val maxBarWidth = 16 * resources.displayMetrics.density

    companion object {
        /**
         * The analytics `blocks_timeline` is sparse (only hours with blocks,
         * each `{ts, count}` at the start of its hour). Spread it over one
         * bucket per hour for the last [hours] hours, oldest first, so empty
         * hours show as gaps instead of the bars stretching to fill them.
         */
        fun hourly(timeline: JSONArray, hours: Int, nowMillis: Long = System.currentTimeMillis()): IntArray {
            val counts = IntArray(hours)
            val currentHour = nowMillis / 1000 / 3600
            for (i in 0 until timeline.length()) {
                val b = timeline.optJSONObject(i) ?: continue
                val age = currentHour - b.optLong("ts") / 3600
                if (age in 0 until hours) counts[(hours - 1 - age).toInt()] += b.optInt("count")
            }
            return counts
        }
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        if (counts.isEmpty()) return
        val max = counts.max().coerceAtLeast(1)
        val slot = width.toFloat() / counts.size
        val barWidth = (slot * 0.7f).coerceIn(1f, maxBarWidth)
        val radius = barWidth / 3
        for (i in counts.indices) {
            // A sliver for empty buckets keeps the time axis readable.
            val h = (height * counts[i].toFloat() / max).coerceAtLeast(2f)
            val left = i * slot + (slot - barWidth) / 2
            rect.set(left, height - h, left + barWidth, height.toFloat())
            canvas.drawRoundRect(rect, radius, radius, paint)
        }
    }
}
