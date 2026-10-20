package com.webfilter.app

import android.content.Context
import com.google.android.material.dialog.MaterialAlertDialogBuilder

/**
 * The honest "this app does not enforce anything" explanation, shown on
 * first run (onboarding) and reachable later from Home and Settings.
 */
object EnforcementInfo {

    fun show(context: Context) {
        val managed = ManagedState.isManaged(context)
        MaterialAlertDialogBuilder(context)
            .setIcon(if (managed) R.drawable.ic_business else R.drawable.ic_info)
            .setTitle(if (managed) R.string.enforce_managed_title else R.string.enforce_title)
            .setMessage(if (managed) R.string.enforce_managed_body else R.string.enforce_body)
            .setPositiveButton(android.R.string.ok, null)
            .show()
    }
}
