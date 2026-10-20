package com.webfilter.app

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import mobile.Mobile
import java.io.ByteArrayInputStream
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate

/**
 * The intercepting CA as the app sees it: its PEM (read straight from the
 * Go engine, so it works with filtering stopped), whether Android trusts
 * it yet, and a one-tap save into Downloads for the Settings installer.
 */
object CaStatus {

    const val FILE_NAME = "webfilter-ca.crt"
    const val MIME = "application/x-x509-ca-cert"

    fun pem(context: Context): String? =
        Mobile.caCertPem(context.filesDir.absolutePath).takeIf { it.isNotEmpty() }

    fun certificate(context: Context): X509Certificate? = try {
        pem(context)?.let {
            CertificateFactory.getInstance("X.509")
                .generateCertificate(ByteArrayInputStream(it.toByteArray())) as X509Certificate
        }
    } catch (_: Exception) {
        null
    }

    /**
     * True when the CA is in the system's trusted credential store — user-
     * installed or pushed by a device policy controller (both surface in
     * AndroidCAStore with a "user:" alias). Never throws.
     */
    fun isInstalled(context: Context): Boolean = try {
        val cert = certificate(context)
        if (cert == null) {
            false
        } else {
            val ks = KeyStore.getInstance("AndroidCAStore")
            ks.load(null)
            ks.getCertificateAlias(cert) != null
        }
    } catch (_: Exception) {
        false
    }

    /**
     * Writes the certificate into the public Downloads folder without any
     * permission or picker (MediaStore, API 29+). Returns null on older
     * releases, where the caller falls back to the SAF save dialog.
     */
    fun saveToDownloads(context: Context): Uri? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return null
        val pem = pem(context) ?: throw IllegalStateException("CA certificate unavailable")
        val resolver = context.contentResolver
        val collection = MediaStore.Downloads.EXTERNAL_CONTENT_URI
        // Replace an earlier copy we wrote rather than piling up "(1)" files.
        resolver.delete(
            collection,
            "${MediaStore.MediaColumns.DISPLAY_NAME}=? AND ${MediaStore.MediaColumns.OWNER_PACKAGE_NAME}=?",
            arrayOf(FILE_NAME, context.packageName),
        )
        val values = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, FILE_NAME)
            put(MediaStore.MediaColumns.MIME_TYPE, MIME)
            put(MediaStore.MediaColumns.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS)
        }
        val uri = resolver.insert(collection, values) ?: throw IllegalStateException("cannot create download")
        resolver.openOutputStream(uri)?.use { it.write(pem.toByteArray()) }
            ?: throw IllegalStateException("no output stream")
        return uri
    }
}
