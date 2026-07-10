package com.webfilter.app

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.Button
import android.widget.CheckBox
import android.widget.ListView
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import org.json.JSONArray
import org.json.JSONObject
import mobile.Mobile

/**
 * Per-category blocklist screen: the ipfire lists downloadable from
 * dbl.ipfire.org (Mobile.listCategoriesJson's "available") merged with
 * whatever is already on disk ("installed" — which may include extra names
 * installed by the desktop tarball update). The checkbox binds a category
 * name into THIS policy's url_filter.categories; download/update/delete
 * manage the shared on-disk list itself (shared across all policies).
 *
 * Downloads block for seconds-to-minutes (the largest list is ~15 MB), so
 * they run on a plain background Thread — this app deliberately has no
 * coroutines. gomobile calls are thread-safe here (the Go side locks).
 */
class CategoriesActivity : AppCompatActivity() {

    companion object {
        /** Optional intent extra naming the policy whose selection to edit. */
        const val EXTRA_POLICY_NAME = "policy_name"
    }

    private class Row(
        val name: String,
        var installedCount: Int, // -1 = not on disk
        var updated: String,
        val remote: Boolean, // downloadable from the list server
        var busy: Boolean = false,
    )

    private lateinit var store: PolicyJsonStore
    private lateinit var adapter: CategoryAdapter
    private var rows: List<Row> = emptyList()
    private var selected = mutableSetOf<String>()
    private var locked = false

    private val dataDir get() = filesDir.absolutePath

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_categories)
        val policyName = intent.getStringExtra(EXTRA_POLICY_NAME) ?: "default"
        store = PolicyJsonStore.forApp(this, policyName)

        adapter = CategoryAdapter()
        findViewById<ListView>(R.id.categoryList).adapter = adapter
    }

    override fun onResume() {
        super.onResume()
        locked = ManagedState.isLocked(this)
        try {
            store.load()
        } catch (e: Exception) {
            toastError(e)
            finish()
            return
        }
        selected = mutableSetOf()
        val arr = store.get("url_filter.categories") as? JSONArray ?: JSONArray()
        for (i in 0 until arr.length()) selected.add(arr.optString(i))
        reloadRows()
    }

    /** Re-queries the on-disk state, keeping in-flight (busy) markers. */
    private fun reloadRows() {
        val busyNames = rows.filter { it.busy }.map { it.name }.toSet()
        val byName = linkedMapOf<String, Row>()
        try {
            val listed = JSONObject(Mobile.listCategoriesJson(dataDir))
            val available = listed.optJSONArray("available") ?: JSONArray()
            for (i in 0 until available.length()) {
                val name = available.optString(i)
                byName[name] = Row(name, -1, "", remote = true)
            }
            val installed = listed.optJSONArray("installed") ?: JSONArray()
            for (i in 0 until installed.length()) {
                val m = installed.optJSONObject(i) ?: continue
                val name = m.optString("name")
                val row = byName[name] ?: Row(name, -1, "", remote = false).also { byName[name] = it }
                row.installedCount = m.optInt("count", 0)
                row.updated = m.optString("updated", "")
            }
        } catch (e: Exception) {
            toastError(e)
        }
        // Selected-but-unknown names (e.g. hand-edited policies) still show,
        // so the checkbox state is never hidden from the user.
        for (name in selected) {
            if (name !in byName) byName[name] = Row(name, -1, "", remote = false)
        }
        byName.values.forEach { it.busy = it.name in busyNames }
        rows = byName.values.sortedBy { it.name }
        adapter.notifyDataSetChanged()
    }

    private fun toastError(e: Exception) {
        Toast.makeText(this, getString(R.string.settings_save_failed, e.message ?: "error"), Toast.LENGTH_LONG).show()
    }

    private fun persistSelection() {
        val arr = JSONArray()
        selected.sorted().forEach { arr.put(it) }
        try {
            store.set("url_filter.categories", arr)
        } catch (e: Exception) {
            toastError(e)
            store.load()
        }
    }

    private fun download(row: Row) {
        row.busy = true
        adapter.notifyDataSetChanged()
        Thread {
            val err = try {
                Mobile.downloadCategoryJson(dataDir, row.name)
                null
            } catch (e: Exception) {
                e
            }
            runOnUiThread {
                row.busy = false
                if (err != null) {
                    Toast.makeText(
                        this,
                        getString(R.string.category_download_failed, row.name, err.message ?: "error"),
                        Toast.LENGTH_LONG,
                    ).show()
                }
                reloadRows()
            }
        }.start()
    }

    private fun promptDelete(row: Row) {
        AlertDialog.Builder(this)
            .setTitle(R.string.category_delete)
            .setMessage(getString(R.string.category_delete_confirm, row.name))
            .setPositiveButton(android.R.string.ok) { _, _ ->
                try {
                    Mobile.deleteCategory(dataDir, row.name)
                } catch (e: Exception) {
                    toastError(e)
                }
                reloadRows()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private inner class CategoryAdapter : BaseAdapter() {
        override fun getCount() = rows.size
        override fun getItem(position: Int) = rows[position]
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup?): View {
            val view = convertView ?: LayoutInflater.from(this@CategoriesActivity)
                .inflate(R.layout.row_category, parent, false)
            val row = getItem(position)

            view.findViewById<TextView>(R.id.categoryName).text = row.name
            view.findViewById<TextView>(R.id.categorySummary).text = when {
                row.busy -> getString(R.string.category_downloading)
                row.installedCount >= 0 ->
                    getString(R.string.category_installed, row.installedCount, row.updated.take(10))
                row.remote -> getString(R.string.category_not_downloaded)
                else -> getString(R.string.category_missing)
            }

            val check = view.findViewById<CheckBox>(R.id.categoryCheck)
            check.setOnCheckedChangeListener(null)
            check.isChecked = row.name in selected
            check.isEnabled = !locked
            check.setOnCheckedChangeListener { _, checked ->
                if (checked) selected.add(row.name) else selected.remove(row.name)
                persistSelection()
                if (checked && row.installedCount < 0 && !row.busy) {
                    Toast.makeText(
                        this@CategoriesActivity,
                        R.string.category_selected_not_installed,
                        Toast.LENGTH_SHORT,
                    ).show()
                }
            }

            val action = view.findViewById<Button>(R.id.categoryAction)
            when {
                row.busy -> {
                    action.visibility = View.VISIBLE
                    action.isEnabled = false
                    action.setText(R.string.category_downloading)
                }
                row.remote -> {
                    action.visibility = View.VISIBLE
                    action.isEnabled = !locked
                    action.setText(if (row.installedCount >= 0) R.string.category_update else R.string.category_download)
                    action.setOnClickListener { download(row) }
                }
                else -> action.visibility = View.GONE
            }

            view.setOnLongClickListener {
                if (locked || row.installedCount < 0 || row.busy) return@setOnLongClickListener false
                promptDelete(row)
                true
            }
            return view
        }
    }
}
