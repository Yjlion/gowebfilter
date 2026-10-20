package com.webfilter.app

import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.graphics.drawable.Drawable
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.Editable
import android.text.TextWatcher
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.ImageView
import android.widget.ListView
import android.widget.TextView
import com.google.android.material.materialswitch.MaterialSwitch
import com.google.android.material.snackbar.Snackbar
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors

/**
 * Lets the user pick which installed apps are routed through the filter.
 * Browsers are listed first (and are the first-run default, see
 * [Prefs.ensureAppSelectionInitialized]); "Filter all apps" is the empty
 * selection. Selection is persisted in [Prefs] and applied by
 * [WebFilterVpnService] the next time the VPN is established (Android does
 * not allow changing the allowed-app set of a live VpnService, so a restart
 * of the tunnel is needed — surfaced to the user with a snackbar).
 */
class AppPickerActivity : BaseActivity() {

    private data class AppRow(val label: String, val pkg: String, val browser: Boolean)

    private sealed class Item {
        data class Section(val title: Int, val browsers: Boolean) : Item()
        data class App(val row: AppRow) : Item()
    }

    private var apps: List<AppRow> = emptyList()
    private var items: List<Item> = emptyList()
    private val selected = mutableSetOf<String>()
    private var query = ""
    private var notifiedRestart = false

    private lateinit var list: ListView
    private lateinit var allSwitch: MaterialSwitch
    private val adapter = AppAdapter()

    // App icons load lazily off the main thread: loadIcon() does binder/disk
    // I/O per package, and a few hundred rows loaded synchronously in
    // getView would visibly jank the list. Loaded drawables are cached by
    // package name for the lifetime of the activity.
    private val iconCache = ConcurrentHashMap<String, Drawable>()
    private val iconExecutor = Executors.newSingleThreadExecutor()
    private val mainHandler = Handler(Looper.getMainLooper())

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_app_picker)

        selected.addAll(Prefs.selectedApps(this))
        apps = loadLaunchableApps(BrowserApps.detect(this))

        list = findViewById(R.id.appList)
        list.adapter = adapter
        list.setOnItemClickListener { _, _, position, _ ->
            val item = items[position] as? Item.App ?: return@setOnItemClickListener
            val pkg = item.row.pkg
            if (!selected.remove(pkg)) selected.add(pkg)
            save()
        }

        allSwitch = findViewById(R.id.allAppsSwitch)
        allSwitch.setOnCheckedChangeListener { _, checked ->
            if (checked == selected.isEmpty()) return@setOnCheckedChangeListener
            selected.clear()
            // Leaving "all apps" starts from the browsers, not from nothing
            // (an empty selection would just mean "all apps" again).
            if (!checked) selected.addAll(apps.filter { it.browser }.map { it.pkg })
            save()
        }

        findViewById<EditText>(R.id.searchInput).addTextChangedListener(object : TextWatcher {
            override fun afterTextChanged(s: Editable?) {
                query = s?.toString()?.trim()?.lowercase() ?: ""
                rebuild()
            }

            override fun beforeTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
            override fun onTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
        })
        rebuild()
    }

    override fun onDestroy() {
        iconExecutor.shutdownNow()
        super.onDestroy()
    }

    private fun save() {
        Prefs.setSelectedApps(this, selected.toSet())
        rebuild()
        if (EngineState.running && !notifiedRestart) {
            notifiedRestart = true
            Snackbar.make(list, R.string.apps_restart_hint, Snackbar.LENGTH_LONG).show()
        }
    }

    private fun rebuild() {
        val all = selected.isEmpty()
        allSwitch.isChecked = all
        findViewById<TextView>(R.id.allAppsSummary).text = if (all) {
            getString(R.string.apps_all_summary)
        } else {
            resources.getQuantityString(R.plurals.apps_selected, selected.size, selected.size)
        }

        val matching = apps.filter {
            query.isEmpty() || it.label.lowercase().contains(query) || it.pkg.lowercase().contains(query)
        }
        val browsers = matching.filter { it.browser }
        val others = matching.filter { !it.browser }
        items = buildList {
            if (browsers.isNotEmpty()) {
                add(Item.Section(R.string.apps_section_browsers, true))
                browsers.forEach { add(Item.App(it)) }
            }
            if (others.isNotEmpty()) {
                add(Item.Section(R.string.apps_section_other, false))
                others.forEach { add(Item.App(it)) }
            }
        }
        adapter.notifyDataSetChanged()
    }

    /** Installed apps that are not this app, browsers flagged, sorted by label. */
    private fun loadLaunchableApps(browsers: Set<String>): List<AppRow> {
        val pm = packageManager
        return pm.getInstalledApplications(PackageManager.GET_META_DATA)
            .filter { it.packageName != packageName }
            .filter { it.flags and ApplicationInfo.FLAG_SYSTEM == 0 || pm.getLaunchIntentForPackage(it.packageName) != null }
            .map { AppRow(pm.getApplicationLabel(it).toString(), it.packageName, it.packageName in browsers) }
            .sortedBy { it.label.lowercase() }
    }

    private inner class AppAdapter : BaseAdapter() {
        override fun getCount() = items.size
        override fun getItem(position: Int) = items[position]
        override fun getItemId(position: Int) = position.toLong()
        override fun getViewTypeCount() = 2
        override fun getItemViewType(position: Int) = if (items[position] is Item.Section) 0 else 1
        override fun isEnabled(position: Int) = items[position] is Item.App && selected.isNotEmpty()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup): View =
            when (val item = items[position]) {
                is Item.Section -> sectionView(item, convertView, parent)
                is Item.App -> appView(item.row, convertView, parent)
            }

        private fun sectionView(item: Item.Section, convertView: View?, parent: ViewGroup): View {
            val view = convertView ?: LayoutInflater.from(parent.context).inflate(R.layout.row_section, parent, false)
            view.findViewById<TextView>(R.id.sectionTitle).setText(item.title)
            view.findViewById<Button>(R.id.sectionAction).apply {
                visibility = if (item.browsers) View.VISIBLE else View.GONE
                setText(R.string.apps_select_browsers)
                setOnClickListener {
                    selected.addAll(apps.filter { it.browser }.map { it.pkg })
                    save()
                }
            }
            return view
        }

        private fun appView(row: AppRow, convertView: View?, parent: ViewGroup): View {
            val view = convertView ?: LayoutInflater.from(parent.context).inflate(R.layout.row_app, parent, false)
            val all = selected.isEmpty()
            view.findViewById<TextView>(R.id.appLabel).text = row.label
            view.findViewById<TextView>(R.id.appPackage).text = row.pkg
            // In "all apps" mode every app is filtered; show that, greyed.
            view.findViewById<CheckBox>(R.id.appCheck).apply {
                isChecked = all || selected.contains(row.pkg)
                isEnabled = !all
            }
            view.alpha = if (all) 0.6f else 1f
            bindIcon(view.findViewById(R.id.appIcon), row.pkg)
            return view
        }

        private fun bindIcon(icon: ImageView, pkg: String) {
            val cached = iconCache[pkg]
            if (cached != null) {
                icon.tag = pkg
                icon.setImageDrawable(cached)
                return
            }
            // Tag the view so a recycled row that has moved on to another
            // package doesn't get this (late) icon.
            icon.tag = pkg
            icon.setImageDrawable(null)
            iconExecutor.execute {
                val drawable = try {
                    packageManager.getApplicationIcon(pkg)
                } catch (_: Exception) {
                    return@execute
                }
                iconCache[pkg] = drawable
                mainHandler.post {
                    if (icon.tag == pkg) icon.setImageDrawable(drawable)
                }
            }
        }
    }
}
