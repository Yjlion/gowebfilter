package com.webfilter.app

import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.BaseAdapter
import android.widget.Button
import android.widget.EditText
import android.widget.ListView
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import org.json.JSONArray
import org.json.JSONObject
import mobile.Mobile

/**
 * Native multi-policy manager. On device every flow arrives from
 * 127.0.0.1, so all policies sit in the catch-all matching tier: "default"
 * is the schedule-less always-on fallback, and an additional policy whose
 * schedule is enabled and currently inside a window outranks it (the Go
 * side's schedule precedence). Rows expose an Active switch (= !inactive);
 * tap opens the policy's own settings screens; long-press offers
 * rename/delete. "default" is protected: no switch, no rename, no delete —
 * the Go exports refuse those anyway.
 */
class PoliciesActivity : AppCompatActivity() {

    private lateinit var adapter: PolicyAdapter
    private var policies = JSONArray()
    private var locked = false

    private val dataDir get() = filesDir.absolutePath

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_policies)

        adapter = PolicyAdapter()
        findViewById<ListView>(R.id.policyList).adapter = adapter
        findViewById<Button>(R.id.addPolicyButton).setOnClickListener { promptCreate() }
    }

    override fun onResume() {
        super.onResume()
        locked = ManagedState.isLocked(this)
        findViewById<Button>(R.id.addPolicyButton).isEnabled = !locked
        reload()
    }

    private fun reload() {
        policies = try {
            JSONArray(Mobile.listPoliciesJson(dataDir))
        } catch (e: Exception) {
            toastError(e)
            JSONArray()
        }
        adapter.notifyDataSetChanged()
    }

    private fun toastError(e: Exception) {
        Toast.makeText(this, getString(R.string.settings_save_failed, e.message ?: "error"), Toast.LENGTH_LONG).show()
    }

    private fun statusFor(p: JSONObject): String {
        val name = p.optString("name")
        val schedule = p.optJSONObject("schedule")
        val windows = schedule?.optJSONArray("active_windows")?.length() ?: 0
        val scheduled = schedule?.optBoolean("enabled", false) == true && windows > 0
        return when {
            name == "default" -> getString(R.string.policy_status_default)
            p.optBoolean("inactive", false) -> getString(R.string.policy_status_inactive)
            scheduled -> getString(R.string.policy_status_scheduled, windows)
            // Active without a schedule: competes with default by filename
            // sort — almost never what the user wants; call it out.
            else -> getString(R.string.policy_status_unscheduled_warning)
        }
    }

    private fun promptCreate() {
        val input = EditText(this).apply {
            inputType = InputType.TYPE_CLASS_TEXT
            hint = getString(R.string.policy_name_hint)
        }
        AlertDialog.Builder(this)
            .setTitle(R.string.policy_add)
            .setView(input)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val name = input.text.toString().trim()
                if (name.isEmpty()) return@setPositiveButton
                try {
                    Mobile.createPolicyJson(dataDir, JSONObject().put("name", name).toString())
                    Toast.makeText(this, R.string.policy_created_hint, Toast.LENGTH_LONG).show()
                    reload()
                } catch (e: Exception) {
                    toastError(e)
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun promptRename(p: JSONObject) {
        val oldName = p.optString("name")
        val input = EditText(this).apply {
            inputType = InputType.TYPE_CLASS_TEXT
            setText(oldName)
        }
        AlertDialog.Builder(this)
            .setTitle(R.string.policy_rename)
            .setView(input)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val newName = input.text.toString().trim()
                if (newName.isEmpty() || newName == oldName) return@setPositiveButton
                try {
                    // Full-document write with the changed name renames the
                    // policy file server-side.
                    val doc = JSONObject(Mobile.getPolicyJson(dataDir, oldName))
                    doc.put("name", newName)
                    Mobile.updatePolicyJson(dataDir, oldName, doc.toString())
                    reload()
                } catch (e: Exception) {
                    toastError(e)
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun promptDelete(p: JSONObject) {
        val name = p.optString("name")
        AlertDialog.Builder(this)
            .setTitle(R.string.policy_delete)
            .setMessage(getString(R.string.policy_delete_confirm, name))
            .setPositiveButton(android.R.string.ok) { _, _ ->
                try {
                    Mobile.deletePolicy(dataDir, name)
                    reload()
                } catch (e: Exception) {
                    toastError(e)
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun setActive(p: JSONObject, active: Boolean) {
        val name = p.optString("name")
        try {
            val doc = JSONObject(Mobile.getPolicyJson(dataDir, name))
            doc.put("inactive", !active)
            Mobile.updatePolicyJson(dataDir, name, doc.toString())
        } catch (e: Exception) {
            toastError(e)
        }
        reload()
    }

    private inner class PolicyAdapter : BaseAdapter() {
        override fun getCount() = policies.length()
        override fun getItem(position: Int): JSONObject = policies.getJSONObject(position)
        override fun getItemId(position: Int) = position.toLong()

        override fun getView(position: Int, convertView: View?, parent: ViewGroup?): View {
            val view = convertView ?: LayoutInflater.from(this@PoliciesActivity)
                .inflate(R.layout.row_policy, parent, false)
            val p = getItem(position)
            val name = p.optString("name")
            val isDefault = name == "default"

            view.findViewById<TextView>(R.id.policyName).text = name
            view.findViewById<TextView>(R.id.policyStatus).text = statusFor(p)

            val activeSwitch = view.findViewById<Switch>(R.id.policyActive)
            activeSwitch.setOnCheckedChangeListener(null)
            activeSwitch.visibility = if (isDefault) View.GONE else View.VISIBLE
            activeSwitch.isChecked = !p.optBoolean("inactive", false)
            activeSwitch.isEnabled = !locked
            activeSwitch.setOnCheckedChangeListener { _, checked -> setActive(p, checked) }

            view.setOnClickListener {
                startActivity(
                    Intent(this@PoliciesActivity, SettingsActivity::class.java)
                        .putExtra(SettingsActivity.EXTRA_POLICY_NAME, name),
                )
            }
            view.setOnLongClickListener {
                if (isDefault || locked) return@setOnLongClickListener false
                AlertDialog.Builder(this@PoliciesActivity)
                    .setTitle(name)
                    .setItems(
                        arrayOf(getString(R.string.policy_rename), getString(R.string.policy_delete)),
                    ) { _, which ->
                        when (which) {
                            0 -> promptRename(p)
                            1 -> promptDelete(p)
                        }
                    }
                    .show()
                true
            }
            return view
        }
    }
}
