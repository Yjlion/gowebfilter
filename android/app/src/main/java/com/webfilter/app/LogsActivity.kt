package com.webfilter.app

import android.os.Bundle
import android.text.Editable
import android.text.TextWatcher
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.AdapterView
import android.widget.ArrayAdapter
import android.widget.BaseAdapter
import android.widget.Button
import android.widget.EditText
import android.widget.ListView
import android.widget.Spinner
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import org.json.JSONArray
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import mobile.Mobile

/**
 * Native request/block/audit log viewer. Reads the SQLite log store
 * through Mobile.queryLogsJson (a write-free reader), so it works whether
 * or not the filter is running. Rows come newest-first from the Go side; a
 * client-side substring filter narrows them.
 */
class LogsActivity : AppCompatActivity() {

    private val kinds = arrayOf("blocks", "requests", "policy_changes")

    private lateinit var adapter: LogAdapter
    private var entries = JSONArray()
    private var shown: List<JSONObject> = emptyList()
    private var kind = "blocks"
    private var filter = ""

    private val timeFmt = SimpleDateFormat("MM-dd HH:mm:ss", Locale.US)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_logs)

        adapter = LogAdapter()
        findViewById<ListView>(R.id.logList).adapter = adapter

        val kindSpinner = findViewById<Spinner>(R.id.kindSpinner)
        kindSpinner.adapter = ArrayAdapter(
            this,
            android.R.layout.simple_spinner_dropdown_item,
            resources.getStringArray(R.array.log_kind_labels),
        )
        kindSpinner.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onItemSelected(parent: AdapterView<*>?, view: View?, position: Int, id: Long) {
                if (kinds[position] != kind) {
                    kind = kinds[position]
                    refresh()
                }
            }

            override fun onNothingSelected(parent: AdapterView<*>?) {}
        }

        findViewById<EditText>(R.id.filterInput).addTextChangedListener(object : TextWatcher {
            override fun afterTextChanged(s: Editable?) {
                filter = s?.toString()?.trim()?.lowercase() ?: ""
                applyFilter()
            }

            override fun beforeTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
            override fun onTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
        })

        findViewById<Button>(R.id.refreshButton).setOnClickListener { refresh() }
    }

    override fun onResume() {
        super.onResume()
        refresh()
    }

    private fun refresh() {
        Thread {
            val result = try {
                JSONObject(Mobile.queryLogsJson(filesDir.absolutePath, kind, 500))
                    .optJSONArray("entries") ?: JSONArray()
            } catch (e: Exception) {
                runOnUiThread {
                    Toast.makeText(this, getString(R.string.settings_save_failed, e.message ?: "error"), Toast.LENGTH_LONG).show()
                }
                JSONArray()
            }
            runOnUiThread {
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
        findViewById<TextView>(R.id.logCount).text = getString(R.string.log_count, shown.size)
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

    private inner class LogAdapter : BaseAdapter() {
        override fun getCount() = shown.size
        override fun getItem(position: Int) = shown[position]
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup?): View {
            val view = convertView ?: LayoutInflater.from(this@LogsActivity)
                .inflate(R.layout.row_log, parent, false)
            val e = getItem(position)
            view.findViewById<TextView>(R.id.logTitle).text = titleFor(e)
            view.findViewById<TextView>(R.id.logSubtitle).text = subtitleFor(e)
            return view
        }
    }
}
