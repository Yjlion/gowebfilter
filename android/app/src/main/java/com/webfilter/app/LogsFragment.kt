package com.webfilter.app

import android.os.Bundle
import android.text.Editable
import android.text.TextWatcher
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.EditText
import android.widget.ListView
import android.widget.TextView
import android.widget.Toast
import androidx.fragment.app.Fragment
import com.google.android.material.chip.ChipGroup
import org.json.JSONArray
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import mobile.Mobile

/**
 * Native request/block/audit log viewer (the Logs tab). Reads the SQLite
 * log store through Mobile.queryLogsJson (a write-free reader), so it works
 * whether or not the filter is running. Rows come newest-first from the Go
 * side; a client-side substring filter narrows them.
 */
class LogsFragment : Fragment(), MainActivity.EngineStateListener {

    private lateinit var adapter: LogAdapter
    private var entries = JSONArray()
    private var shown: List<JSONObject> = emptyList()
    private var kind = "blocks"
    private var filter = ""

    private val timeFmt = SimpleDateFormat("MM-dd HH:mm:ss", Locale.getDefault())

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View =
        inflater.inflate(R.layout.fragment_logs, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        adapter = LogAdapter()
        view.findViewById<ListView>(R.id.logList).apply {
            adapter = this@LogsFragment.adapter
            emptyView = view.findViewById(R.id.logEmpty)
        }

        view.findViewById<ChipGroup>(R.id.kindChips).setOnCheckedStateChangeListener { _, ids ->
            kind = when (ids.firstOrNull()) {
                R.id.chipRequests -> "requests"
                R.id.chipPolicyChanges -> "policy_changes"
                else -> "blocks"
            }
            refresh()
        }

        view.findViewById<EditText>(R.id.filterInput).addTextChangedListener(object : TextWatcher {
            override fun afterTextChanged(s: Editable?) {
                filter = s?.toString()?.trim()?.lowercase() ?: ""
                applyFilter()
            }

            override fun beforeTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
            override fun onTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
        })

        view.findViewById<View>(R.id.refreshButton).setOnClickListener { refresh() }
    }

    override fun onResume() {
        super.onResume()
        if (!isHidden) refresh()
    }

    override fun onHiddenChanged(hidden: Boolean) {
        super.onHiddenChanged(hidden)
        if (!hidden) refresh()
    }

    override fun onEngineStateChanged() {
        if (!isHidden) refresh()
    }

    private fun refresh() {
        val ctx = context?.applicationContext ?: return
        val k = kind
        Thread {
            var error: Exception? = null
            val result = try {
                JSONObject(Mobile.queryLogsJson(ctx.filesDir.absolutePath, k, 500L))
                    .optJSONArray("entries") ?: JSONArray()
            } catch (e: Exception) {
                error = e
                JSONArray()
            }
            activity?.runOnUiThread {
                if (!isAdded || k != kind) return@runOnUiThread
                error?.let {
                    Toast.makeText(ctx, getString(R.string.settings_save_failed, it.message ?: "error"), Toast.LENGTH_LONG).show()
                }
                entries = result
                applyFilter()
            }
        }.start()
    }

    private fun applyFilter() {
        val list = mutableListOf<JSONObject>()
        for (i in 0 until entries.length()) {
            val e = entries.optJSONObject(i) ?: continue
            if (filter.isEmpty() || matches(e)) list.add(e)
        }
        shown = list
        view?.findViewById<TextView>(R.id.logCount)?.text = getString(R.string.log_count, shown.size)
        adapter.notifyDataSetChanged()
    }

    private fun matches(e: JSONObject): Boolean {
        for (key in e.keys()) {
            val v = e.opt(key) ?: continue
            if (v.toString().lowercase().contains(filter)) return true
        }
        return false
    }

    private fun titleFor(e: JSONObject): String = when (kind) {
        "requests" -> e.optString("host")
        "blocks" -> e.optString("domain")
        else -> e.optString("policy_name")
    }

    private fun subtitleFor(e: JSONObject): String {
        val ts = timeFmt.format(Date(e.optLong("ts") * 1000))
        val parts = mutableListOf(ts)
        when (kind) {
            "requests" -> {
                parts.add(e.optString("action").ifEmpty { "ok" })
                e.optString("component").takeIf { it.isNotEmpty() }?.let { parts.add(it) }
                e.optString("policy").takeIf { it.isNotEmpty() }?.let { parts.add(it) }
                parts.add(e.optString("method") + " " + e.optString("path"))
            }
            "blocks" -> {
                e.optString("reason").takeIf { it.isNotEmpty() }?.let { parts.add(it) }
                e.optString("component").takeIf { it.isNotEmpty() }?.let { parts.add(it) }
                e.optString("policy").takeIf { it.isNotEmpty() }?.let { parts.add(it) }
            }
            else -> {
                parts.add(e.optString("action"))
                e.optString("old_name").takeIf { it.isNotEmpty() }?.let { parts.add(getString(R.string.log_renamed_from, it)) }
                e.optString("client_ip").takeIf { it.isNotEmpty() }?.let { parts.add(it) }
            }
        }
        return parts.joinToString(" · ")
    }

    /** Icon and tone per row: blocked red, modified amber, allowed neutral. */
    private fun styleFor(e: JSONObject): Pair<Int, Tone> = when (kind) {
        "blocks" -> R.drawable.ic_block to Tone.OFF
        "requests" -> when (e.optString("action")) {
            "blocked" -> R.drawable.ic_block to Tone.OFF
            "modified" -> R.drawable.ic_tune to Tone.WARN
            else -> R.drawable.ic_globe to Tone.NEUTRAL
        }
        else -> R.drawable.ic_policy to Tone.NEUTRAL
    }

    private inner class LogAdapter : BaseAdapter() {
        override fun getCount() = shown.size
        override fun getItem(position: Int) = shown[position]
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup): View {
            val view = convertView ?: Rows.inflate(parent)
            val e = getItem(position)
            val (icon, tone) = styleFor(e)
            return Rows.bind(view, icon, titleFor(e), subtitleFor(e), tone)
        }
    }
}
