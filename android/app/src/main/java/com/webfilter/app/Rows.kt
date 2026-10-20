package com.webfilter.app

import android.content.Context
import android.content.res.ColorStateList
import android.graphics.drawable.GradientDrawable
import android.util.TypedValue
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.TextView
import androidx.annotation.AttrRes
import androidx.annotation.ColorInt
import androidx.annotation.ColorRes
import androidx.annotation.DrawableRes
import androidx.core.content.ContextCompat
import com.google.android.material.materialswitch.MaterialSwitch

/** Status tones used for row icons and the protection ring. */
enum class Tone { NEUTRAL, OK, WARN, OFF }

/** Builds and binds `row_status.xml` rows, the app's one list-row style. */
object Rows {

    fun inflate(parent: ViewGroup): View =
        LayoutInflater.from(parent.context).inflate(R.layout.row_status, parent, false)

    fun bind(
        row: View,
        @DrawableRes icon: Int,
        title: CharSequence,
        subtitle: CharSequence? = null,
        tone: Tone = Tone.NEUTRAL,
        chevron: Boolean = false,
        tintIcon: Boolean = true,
        onClick: (() -> Unit)? = null,
    ): View {
        val ctx = row.context
        val iconView = row.findViewById<ImageView>(R.id.rowIcon)
        iconView.setImageResource(icon)
        val (fg, bg) = toneColors(ctx, tone)
        iconView.imageTintList = if (tintIcon) ColorStateList.valueOf(fg) else null
        (iconView.background.mutate() as? GradientDrawable)?.setColor(bg)

        row.findViewById<TextView>(R.id.rowTitle).text = title
        row.findViewById<TextView>(R.id.rowSubtitle).apply {
            text = subtitle
            visibility = if (subtitle.isNullOrEmpty()) View.GONE else View.VISIBLE
        }
        row.findViewById<ImageView>(R.id.rowTrailing).visibility = if (chevron) View.VISIBLE else View.GONE
        row.findViewById<MaterialSwitch>(R.id.rowSwitch).visibility = View.GONE
        if (onClick != null) {
            row.isClickable = true
            row.setOnClickListener { onClick() }
        } else {
            row.isClickable = false
            row.setOnClickListener(null)
        }
        return row
    }

    /** Adds a bound row to [parent] and returns it. */
    fun add(
        parent: ViewGroup,
        @DrawableRes icon: Int,
        title: CharSequence,
        subtitle: CharSequence? = null,
        tone: Tone = Tone.NEUTRAL,
        chevron: Boolean = false,
        tintIcon: Boolean = true,
        onClick: (() -> Unit)? = null,
    ): View {
        val row = bind(inflate(parent), icon, title, subtitle, tone, chevron, tintIcon, onClick)
        parent.addView(row)
        return row
    }

    /** Foreground (icon) and background (circle) colors for a tone. */
    fun toneColors(ctx: Context, tone: Tone): Pair<Int, Int> = when (tone) {
        Tone.OK -> color(ctx, R.color.status_ok) to color(ctx, R.color.status_ok_container)
        Tone.WARN -> color(ctx, R.color.status_warn) to color(ctx, R.color.status_warn_container)
        Tone.OFF -> color(ctx, R.color.status_off) to color(ctx, R.color.status_off_container)
        Tone.NEUTRAL -> attrColor(ctx, com.google.android.material.R.attr.colorOnSecondaryContainer) to
            attrColor(ctx, com.google.android.material.R.attr.colorSecondaryContainer)
    }

    @ColorInt
    fun color(ctx: Context, @ColorRes res: Int): Int = ContextCompat.getColor(ctx, res)

    @ColorInt
    fun attrColor(ctx: Context, @AttrRes attr: Int): Int {
        val tv = TypedValue()
        ctx.theme.resolveAttribute(attr, tv, true)
        return if (tv.resourceId != 0) ContextCompat.getColor(ctx, tv.resourceId) else tv.data
    }

    fun dp(ctx: Context, v: Int): Int = (v * ctx.resources.displayMetrics.density).toInt()
}
