package com.webfilter.app

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.Settings
import android.security.KeyChain
import android.widget.Button
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.FileProvider
import mobile.Mobile
import java.io.ByteArrayInputStream
import java.io.File
import java.security.cert.CertificateFactory

/**
 * Guides the user through installing the intercepting CA certificate. Reads
 * the public CA PEM straight from the Go engine (Mobile.caCertPem) so it works
 * even before the VPN is started, writes it to a shareable cache file, and
 * offers both the KeyChain install intent and a fallback to the security
 * settings. The on-screen copy states the honest limits (Chrome/CT bypass).
 */
class CaInstallActivity : AppCompatActivity() {

    /**
     * SAF "create document" picker for saving the certificate wherever the
     * user chooses (typically Downloads) — no storage permission needed on
     * any supported API level.
     */
    private val saveCertLauncher =
        registerForActivityResult(ActivityResultContracts.CreateDocument("application/x-x509-ca-cert")) { uri ->
            if (uri != null) saveCertTo(uri)
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_ca_install)

        findViewById<Button>(R.id.saveButton).setOnClickListener { saveCertLauncher.launch("webfilter-ca.crt") }
        findViewById<Button>(R.id.exportButton).setOnClickListener { exportCert() }
        findViewById<Button>(R.id.settingsButton).setOnClickListener { openCertInstall() }
    }

    private fun writeCertToCache(): File? {
        val pem = Mobile.caCertPem(filesDir.absolutePath)
        if (pem.isNullOrEmpty()) {
            Toast.makeText(this, "Failed to read CA certificate", Toast.LENGTH_LONG).show()
            return null
        }
        val dir = File(cacheDir, "ca").apply { mkdirs() }
        val file = File(dir, "webfilter-ca.crt")
        file.writeText(pem)
        return file
    }

    /** Write the PEM to the user-picked document (download-style save). */
    private fun saveCertTo(uri: Uri) {
        val pem = Mobile.caCertPem(filesDir.absolutePath)
        if (pem.isNullOrEmpty()) {
            Toast.makeText(this, R.string.ca_save_failed, Toast.LENGTH_LONG).show()
            return
        }
        try {
            contentResolver.openOutputStream(uri)?.use { it.write(pem.toByteArray()) }
                ?: throw IllegalStateException("no output stream")
            Toast.makeText(this, R.string.ca_saved, Toast.LENGTH_SHORT).show()
        } catch (e: Exception) {
            Toast.makeText(this, R.string.ca_save_failed, Toast.LENGTH_LONG).show()
        }
    }

    /** Share the .crt so the user can save it or open it with an installer. */
    private fun exportCert() {
        val file = writeCertToCache() ?: return
        val uri: Uri = FileProvider.getUriForFile(
            this,
            "$packageName.fileprovider",
            file,
        )
        val share = Intent(Intent.ACTION_SEND).apply {
            type = "application/x-x509-ca-cert"
            putExtra(Intent.EXTRA_STREAM, uri)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        startActivity(Intent.createChooser(share, getString(R.string.ca_export)))
    }

    /**
     * Prefer the KeyChain "install CA" intent; fall back to the security
     * settings screen if the OEM does not surface it.
     */
    private fun openCertInstall() {
        val file = writeCertToCache() ?: return
        try {
            // KeyChain.EXTRA_CERTIFICATE wants DER-encoded bytes, so decode the
            // PEM the engine produced into an X.509 certificate first.
            val der = CertificateFactory.getInstance("X.509")
                .generateCertificate(ByteArrayInputStream(file.readBytes()))
                .encoded
            val intent = KeyChain.createInstallIntent().apply {
                putExtra(KeyChain.EXTRA_CERTIFICATE, der)
                putExtra(KeyChain.EXTRA_NAME, "WebFilter CA")
            }
            startActivity(intent)
        } catch (e: Exception) {
            try {
                startActivity(Intent(Settings.ACTION_SECURITY_SETTINGS))
            } catch (_: Exception) {
                Toast.makeText(this, "Open Settings > Security > install a certificate", Toast.LENGTH_LONG).show()
            }
        }
    }
}
