package com.webfilter.app

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri

/** Detects installed web browsers: apps that can VIEW a browsable https URL. */
object BrowserApps {

    fun detect(context: Context): Set<String> {
        val pm = context.packageManager
        val intent = Intent(Intent.ACTION_VIEW, Uri.parse("https://example.com/"))
            .addCategory(Intent.CATEGORY_BROWSABLE)
        @Suppress("DEPRECATION")
        val matches = pm.queryIntentActivities(intent, PackageManager.MATCH_ALL)
        return matches
            .mapNotNull { it.activityInfo?.packageName }
            .filter { it != context.packageName }
            .toSet()
    }
}
