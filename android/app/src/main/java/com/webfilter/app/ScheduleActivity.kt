package com.webfilter.app

import android.app.TimePickerDialog
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.Button
import android.widget.ListView
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import org.json.JSONArray
import org.json.JSONObject

/**
 * Hand-rolled editor for the policy schedule (too structured for
 * androidx.preference): an enabled switch plus a list of time windows, each
 * with a day-of-week multi-select and start/end time pickers.
 *
 * Day indices follow the Go model's convention: 0 = Monday ... 6 = Sunday
 * (internal/models/schedule.go), NOT java.util.Calendar's Sunday-first.
 * Schedules fail open on the engine side: disabled or empty-window
 * schedules mean "always active".
 */
class ScheduleActivity : AppCompatActivity() {

    private lateinit var store: PolicyJsonStore
    private lateinit var schedule: JSONObject
    private lateinit var adapter: WindowAdapter
    private var locked = false

    private val dayNames by lazy {
        arrayOf("Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun")
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_schedule)
        store = PolicyJsonStore.forApp(this)
    }

    override fun onResume() {
        super.onResume()
        locked = ManagedState.isLocked(this)
        try {
            store.load()
        } catch (e: Exception) {
            Toast.makeText(this, getString(R.string.settings_save_failed, e.message ?: "load error"), Toast.LENGTH_LONG).show()
            finish()
            return
        }
        schedule = store.scheduleJson()
        if (!schedule.has("active_windows")) schedule.put("active_windows", JSONArray())

        val enabledSwitch = findViewById<Switch>(R.id.scheduleEnabled)
        enabledSwitch.isChecked = schedule.optBoolean("enabled", false)
        enabledSwitch.isEnabled = !locked
        enabledSwitch.setOnCheckedChangeListener { _, checked ->
            schedule.put("enabled", checked)
            persist()
        }

        val addButton = findViewById<Button>(R.id.addWindowButton)
        addButton.isEnabled = !locked
        addButton.setOnClickListener {
            windows().put(
                JSONObject()
                    .put("days", JSONArray(listOf(0, 1, 2, 3, 4, 5, 6)))
                    .put("start", "00:00")
                    .put("end", "23:59"),
            )
            persist()
            adapter.notifyDataSetChanged()
        }

        adapter = WindowAdapter()
        findViewById<ListView>(R.id.windowList).adapter = adapter
    }

    private fun windows(): JSONArray = schedule.getJSONArray("active_windows")

    private fun persist() {
        try {
            store.setSchedule(schedule)
        } catch (e: Exception) {
            Toast.makeText(this, getString(R.string.settings_save_failed, e.message ?: "error"), Toast.LENGTH_LONG).show()
            store.load()
            schedule = store.scheduleJson()
            adapter.notifyDataSetChanged()
        }
    }

    private fun daysSummary(days: JSONArray): String {
        if (days.length() == 0) return "—"
        if (days.length() == 7) return getString(R.string.schedule_days) + ": Mon–Sun"
        return (0 until days.length())
            .map { days.optInt(it) }
            .filter { it in 0..6 }
            .sorted()
            .joinToString(", ") { dayNames[it] }
    }

    private inner class WindowAdapter : BaseAdapter() {
        override fun getCount() = windows().length()
        override fun getItem(position: Int): JSONObject = windows().getJSONObject(position)
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup?): View {
            val view = convertView ?: LayoutInflater.from(this@ScheduleActivity)
                .inflate(R.layout.row_time_window, parent, false)
            val window = getItem(position)

            view.findViewById<TextView>(R.id.windowSummary).text =
                "${daysSummary(window.optJSONArray("days") ?: JSONArray())}  ${window.optString("start", "00:00")}–${window.optString("end", "23:59")}"

            val daysButton = view.findViewById<Button>(R.id.daysButton)
            val startButton = view.findViewById<Button>(R.id.startButton)
            val endButton = view.findViewById<Button>(R.id.endButton)
            val removeButton = view.findViewById<Button>(R.id.removeButton)
            startButton.text = window.optString("start", "00:00")
            endButton.text = window.optString("end", "23:59")
            for (b in listOf(daysButton, startButton, endButton, removeButton)) b.isEnabled = !locked

            daysButton.setOnClickListener { pickDays(window) }
            startButton.setOnClickListener { pickTime(window, "start") }
            endButton.setOnClickListener { pickTime(window, "end") }
            removeButton.setOnClickListener {
                val remaining = JSONArray()
                for (i in 0 until windows().length()) {
                    if (i != position) remaining.put(windows().get(i))
                }
                schedule.put("active_windows", remaining)
                persist()
                notifyDataSetChanged()
            }
            return view
        }
    }

    private fun pickDays(window: JSONObject) {
        val current = window.optJSONArray("days") ?: JSONArray()
        val checked = BooleanArray(7)
        for (i in 0 until current.length()) {
            val d = current.optInt(i, -1)
            if (d in 0..6) checked[d] = true
        }
        AlertDialog.Builder(this)
            .setTitle(R.string.schedule_days)
            .setMultiChoiceItems(dayNames, checked) { _, which, isChecked -> checked[which] = isChecked }
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val days = JSONArray()
                checked.forEachIndexed { day, on -> if (on) days.put(day) }
                window.put("days", days)
                persist()
                adapter.notifyDataSetChanged()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun pickTime(window: JSONObject, field: String) {
        val parts = window.optString(field, "00:00").split(':')
        val hour = parts.getOrNull(0)?.toIntOrNull() ?: 0
        val minute = parts.getOrNull(1)?.toIntOrNull() ?: 0
        TimePickerDialog(
            this,
            { _, h, m ->
                window.put(field, String.format("%02d:%02d", h, m))
                persist()
                adapter.notifyDataSetChanged()
            },
            hour,
            minute,
            true,
        ).show()
    }
}
