package com.webfilter.app

import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.ImageButton
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.widget.PopupMenu
import androidx.fragment.app.Fragment
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.floatingactionbutton.ExtendedFloatingActionButton
import com.google.android.material.materialswitch.MaterialSwitch
import org.json.JSONArray
import org.json.JSONObject
import mobile.Mobile

/**
 * Policy-centric filter settings. Every policy is a card; tapping it
 * expands the policy's filter modules, each with an inline on/off switch
 * and a tap-through to its detail screen.
 *
 * On device every flow arrives from 127.0.0.1, so all policies sit in the
 * catch-all matching tier: "default" is the schedule-less always-on
 * fallback, and an additional policy whose schedule is enabled and
 * currently inside a window outranks it (the Go side's schedule
 * precedence). "default" is protected: no Active switch, no rename, no
 * delete — the Go exports refuse those anyway.
 */
class FiltersFragment : Fragment() {

    private var policies = JSONArray()
    private var locked = false
    private val expanded = mutableSetOf("default")

    private val dataDir get() = requireContext().filesDir.absolutePath

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View =
        inflater.inflate(R.layout.fragment_filters, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        savedInstanceState?.getStringArrayList(STATE_EXPANDED)?.let {
            expanded.clear()
            expanded.addAll(it)
        }
        view.findViewById<ExtendedFloatingActionButton>(R.id.addPolicyFab).setOnClickListener { promptCreate() }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putStringArrayList(STATE_EXPANDED, ArrayList(expanded))
    }

    override fun onResume() {
        super.onResume()
        if (!isHidden) reload()
    }

    override fun onHiddenChanged(hidden: Boolean) {
        super.onHiddenChanged(hidden)
        if (!hidden) reload()
    }

    private fun reload() {
        val view = view ?: return
        locked = ManagedState.isLocked(requireContext())
        view.findViewById<View>(R.id.managedBanner).visibility = if (locked) View.VISIBLE else View.GONE
        view.findViewById<View>(R.id.addPolicyFab).visibility = if (locked) View.GONE else View.VISIBLE
        policies = try {
            JSONArray(Mobile.listPoliciesJson(dataDir))
        } catch (e: Exception) {
            toastError(e)
            JSONArray()
        }
        render(view.findViewById(R.id.policyCards))
    }

    private fun render(container: LinearLayout) {
        container.removeAllViews()
        // default first, then the overrides in file order.
        val list = (0 until policies.length()).mapNotNull { policies.optJSONObject(it) }
            .sortedBy { if (it.optString("name") == "default") 0 else 1 }
        for (p in list) container.addView(policyCard(container, p))
    }

    private fun policyCard(parent: ViewGroup, p: JSONObject): View {
        val ctx = parent.context
        val card = LayoutInflater.from(ctx).inflate(R.layout.card_policy, parent, false)
        val name = p.optString("name")
        val isDefault = name == "default"
        val inactive = p.optBoolean("inactive", false)

        card.findViewById<TextView>(R.id.policyName).text =
            if (isDefault) getString(R.string.policy_default_title) else name
        card.findViewById<TextView>(R.id.policyStatus).text = statusFor(p)
        val enabled = PolicyModules.enabled(p)
        card.findViewById<TextView>(R.id.policySummary).text = if (enabled.isEmpty()) {
            getString(R.string.home_policy_nothing_on)
        } else {
            getString(R.string.policy_summary_on, enabled.joinToString(", ") { getString(it.title) })
        }
        val tone = when {
            !isDefault && inactive -> Tone.NEUTRAL
            enabled.isEmpty() -> Tone.WARN
            else -> Tone.OK
        }
        val (fg, bg) = Rows.toneColors(ctx, tone)
        card.findViewById<ImageView>(R.id.policyIcon).apply {
            imageTintList = android.content.res.ColorStateList.valueOf(fg)
            (background.mutate() as? android.graphics.drawable.GradientDrawable)?.setColor(bg)
        }

        card.findViewById<MaterialSwitch>(R.id.policyActive).apply {
            visibility = if (isDefault) View.GONE else View.VISIBLE
            isChecked = !inactive
            isEnabled = !locked
            setOnCheckedChangeListener { _, checked -> setActive(name, checked) }
        }
        card.findViewById<ImageButton>(R.id.policyMenu).apply {
            visibility = if (isDefault || locked) View.GONE else View.VISIBLE
            setOnClickListener { showMenu(it, name) }
        }

        val body = card.findViewById<LinearLayout>(R.id.policyModules)
        val chevron = card.findViewById<ImageView>(R.id.policyExpand)
        fun applyExpanded(open: Boolean) {
            body.visibility = if (open) View.VISIBLE else View.GONE
            chevron.rotation = if (open) 180f else 0f
            if (open && body.childCount == 0) fillModules(body, p)
        }
        applyExpanded(name in expanded)
        card.findViewById<View>(R.id.policyHeader).setOnClickListener {
            val open = name !in expanded
            if (open) expanded.add(name) else expanded.remove(name)
            applyExpanded(open)
        }
        return card
    }

    private fun fillModules(body: LinearLayout, p: JSONObject) {
        val name = p.optString("name")
        val isDefault = name == "default"
        val urlFilterOn = p.optJSONObject("url_filter")?.optBoolean("enabled", false) == true
        for (m in PolicyModules.all) {
            if (m.kind == PolicyModule.Kind.SCHEDULE && isDefault) continue
            var tone = Tone.NEUTRAL
            val subtitle = when (m.kind) {
                PolicyModule.Kind.CATEGORIES -> {
                    val n = PolicyModules.blockedCategories(p)
                    when {
                        n == 0 -> getString(m.summary)
                        // Categories are evaluated by the URL filter addon,
                        // which does nothing while url_filter is off.
                        !urlFilterOn -> {
                            tone = Tone.WARN
                            getString(R.string.mod_categories_inactive, resources.getQuantityString(R.plurals.categories_blocked, n, n))
                        }
                        else -> resources.getQuantityString(R.plurals.categories_blocked, n, n)
                    }
                }
                PolicyModule.Kind.SCHEDULE -> scheduleSummary(p)
                else -> getString(m.summary)
            }
            val row = Rows.add(body, m.icon, getString(m.title), subtitle, tone, chevron = m.enabledPath == null, tintIcon = m.tintIcon) {
                openModule(name, m)
            }
            if (m.enabledPath != null) {
                row.findViewById<MaterialSwitch>(R.id.rowSwitch).apply {
                    visibility = View.VISIBLE
                    isChecked = m.isEnabled(p)
                    isEnabled = !locked
                    setOnCheckedChangeListener { _, checked -> setModule(name, m.enabledPath, checked) }
                }
            }
        }
    }

    private fun scheduleSummary(p: JSONObject): String {
        val schedule = p.optJSONObject("schedule")
        val windows = schedule?.optJSONArray("active_windows")?.length() ?: 0
        return if (schedule?.optBoolean("enabled", false) == true && windows > 0) {
            getString(R.string.policy_status_scheduled, windows)
        } else {
            getString(R.string.pref_schedule_summary)
        }
    }

    private fun openModule(policy: String, m: PolicyModule) {
        val ctx = requireContext()
        val intent = when (m.kind) {
            PolicyModule.Kind.CATEGORIES -> Intent(ctx, CategoriesActivity::class.java)
                .putExtra(CategoriesActivity.EXTRA_POLICY_NAME, policy)
            PolicyModule.Kind.SCHEDULE -> Intent(ctx, ScheduleActivity::class.java)
                .putExtra(ScheduleActivity.EXTRA_POLICY_NAME, policy)
            PolicyModule.Kind.PREFS -> SettingsActivity.policyScreen(ctx, policy, m.screen ?: return, getString(m.title))
        }
        startActivity(intent)
    }

    private fun setModule(policy: String, path: String, enabled: Boolean) {
        try {
            val store = PolicyJsonStore.forApp(requireContext(), policy)
            store.load()
            store.set(path, enabled)
        } catch (e: Exception) {
            toastError(e)
        }
        // Rebuild after the switch's own callback has returned.
        view?.post { reload() }
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

    private fun showMenu(anchor: View, name: String) {
        PopupMenu(requireContext(), anchor).apply {
            menu.add(0, 1, 0, R.string.policy_rename)
            menu.add(0, 2, 1, R.string.policy_delete)
            setOnMenuItemClickListener {
                when (it.itemId) {
                    1 -> promptRename(name)
                    2 -> promptDelete(name)
                }
                true
            }
        }.show()
    }

    private fun nameInput(initial: String = ""): Pair<View, EditText> {
        val ctx = requireContext()
        val input = EditText(ctx).apply {
            inputType = InputType.TYPE_CLASS_TEXT
            hint = getString(R.string.policy_name_hint)
            setText(initial)
        }
        val pad = Rows.dp(ctx, 24)
        val frame = FrameLayout(ctx).apply {
            setPadding(pad, Rows.dp(ctx, 8), pad, 0)
            addView(input)
        }
        return frame to input
    }

    private fun promptCreate() {
        val (frame, input) = nameInput()
        MaterialAlertDialogBuilder(requireContext())
            .setTitle(R.string.policy_add)
            .setView(frame)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val name = input.text.toString().trim()
                if (name.isEmpty()) return@setPositiveButton
                try {
                    val created = JSONObject(Mobile.createPolicyJson(dataDir, JSONObject().put("name", name).toString()))
                    expanded.add(created.optString("name", name))
                    Toast.makeText(requireContext(), R.string.policy_created_hint, Toast.LENGTH_LONG).show()
                } catch (e: Exception) {
                    toastError(e)
                }
                reload()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun promptRename(oldName: String) {
        val (frame, input) = nameInput(oldName)
        MaterialAlertDialogBuilder(requireContext())
            .setTitle(R.string.policy_rename)
            .setView(frame)
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val newName = input.text.toString().trim()
                if (newName.isEmpty() || newName == oldName) return@setPositiveButton
                try {
                    // Full-document write with the changed name renames the
                    // policy file server-side.
                    val doc = JSONObject(Mobile.getPolicyJson(dataDir, oldName))
                    doc.put("name", newName)
                    Mobile.updatePolicyJson(dataDir, oldName, doc.toString())
                    if (expanded.remove(oldName)) expanded.add(newName)
                } catch (e: Exception) {
                    toastError(e)
                }
                reload()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun promptDelete(name: String) {
        MaterialAlertDialogBuilder(requireContext())
            .setTitle(R.string.policy_delete)
            .setMessage(getString(R.string.policy_delete_confirm, name))
            .setPositiveButton(R.string.policy_delete) { _, _ ->
                try {
                    Mobile.deletePolicy(dataDir, name)
                    expanded.remove(name)
                } catch (e: Exception) {
                    toastError(e)
                }
                reload()
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun setActive(name: String, active: Boolean) {
        try {
            val doc = JSONObject(Mobile.getPolicyJson(dataDir, name))
            doc.put("inactive", !active)
            Mobile.updatePolicyJson(dataDir, name, doc.toString())
        } catch (e: Exception) {
            toastError(e)
        }
        view?.post { reload() }
    }

    private fun toastError(e: Exception) {
        Toast.makeText(requireContext(), getString(R.string.settings_save_failed, e.message ?: "error"), Toast.LENGTH_LONG).show()
    }

    companion object {
        private const val STATE_EXPANDED = "expanded"
    }
}
