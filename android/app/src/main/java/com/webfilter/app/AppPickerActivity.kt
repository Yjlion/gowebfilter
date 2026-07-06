package com.webfilter.app

import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.CheckBox
import android.widget.ListView
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity

/**
 * Lets the user pick which installed apps are routed through the filter.
 * Selection is persisted in [Prefs] and applied by [WebFilterVpnService] the
 * next time the VPN is established (Android does not allow changing the
 * allowed-app set of a live VpnService, so a restart of the tunnel is needed
 * for changes to take effect — surfaced to the user in the UI copy).
 */
class AppPickerActivity : AppCompatActivity() {

    private data class AppRow(val label: String, val pkg: String)

    private lateinit var rows: List<AppRow>
    private val selected = mutableSetOf<String>()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_app_picker)

        selected.addAll(Prefs.selectedApps(this))
        rows = loadLaunchableApps()

        val list = findViewById<ListView>(R.id.appList)
        list.adapter = AppAdapter()
        list.setOnItemClickListener { _, _, position, _ ->
            val pkg = rows[position].pkg
            if (selected.contains(pkg)) selected.remove(pkg) else selected.add(pkg)
            Prefs.setSelectedApps(this, selected)
            (list.adapter as AppAdapter).notifyDataSetChanged()
        }
    }

    /** Installed apps that are not this app, sorted by display label. */
    private fun loadLaunchableApps(): List<AppRow> {
        val pm = packageManager
        return pm.getInstalledApplications(PackageManager.GET_META_DATA)
            .filter { it.packageName != packageName }
            .filter { it.flags and ApplicationInfo.FLAG_SYSTEM == 0 || pm.getLaunchIntentForPackage(it.packageName) != null }
            .map { AppRow(pm.getApplicationLabel(it).toString(), it.packageName) }
            .sortedBy { it.label.lowercase() }
    }

    private inner class AppAdapter : BaseAdapter() {
        override fun getCount() = rows.size
        override fun getItem(position: Int) = rows[position]
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup?): View {
            val view = convertView ?: LayoutInflater.from(this@AppPickerActivity)
                .inflate(R.layout.row_app, parent, false)
            val row = rows[position]
            view.findViewById<TextView>(R.id.appLabel).text = row.label
            view.findViewById<CheckBox>(R.id.appCheck).isChecked = selected.contains(row.pkg)
            return view
        }
    }
}
