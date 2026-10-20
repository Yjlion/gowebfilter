package com.webfilter.app

import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.res.ColorStateList
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.security.KeyChain
import android.view.View
import android.widget.Button
import android.widget.ImageView
import android.widget.TextView
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.FileProvider
import com.google.android.material.card.MaterialCardView
import java.io.File

/**
 * Guided install of the intercepting CA certificate, with a live
 * "installed?" check (CaStatus.isInstalled, re-run on every resume — i.e.
 * when the user comes back from Settings).
 *
 * Why it is a guided flow and not one button: since Android 11 the
 * KeyChain install intent refuses CA certificates ("must be installed in
 * Settings"), so the only path is Settings › … › Install a certificate ›
 * CA certificate, picking the file from storage. Step 1 therefore saves the
 * file straight into Downloads, step 2 sends the user to Settings with the
 * exact path, step 3 confirms. On Android 8–10 step 2 still uses the
 * KeyChain installer directly.
 */
class CaInstallActivity : BaseActivity() {

    private var saved = false

    /** API 26–28 fallback: SAF "create document" (no storage permission). */
    private val saveCertLauncher =
        registerForActivityResult(ActivityResultContracts.CreateDocument(CaStatus.MIME)) { uri ->
            if (uri != null) saveCertTo(uri)
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_ca_install)
        saved = savedInstanceState?.getBoolean(STATE_SAVED) ?: false

        findViewById<Button>(R.id.shareButton).setOnClickListener { shareCert() }
        val limitsBody = findViewById<View>(R.id.limitsBody)
        val chevron = findViewById<View>(R.id.limitsChevron)
        findViewById<View>(R.id.limitsHeader).setOnClickListener {
            val open = limitsBody.visibility != View.VISIBLE
            limitsBody.visibility = if (open) View.VISIBLE else View.GONE
            chevron.rotation = if (open) 180f else 0f
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putBoolean(STATE_SAVED, saved)
    }

    override fun onResume() {
        super.onResume()
        render()
    }

    private fun render() {
        val installed = CaStatus.isInstalled(this)
        val tone = if (installed) Tone.OK else Tone.WARN
        val (fg, bg) = Rows.toneColors(this, tone)
        findViewById<MaterialCardView>(R.id.statusCard).setCardBackgroundColor(bg)
        findViewById<ImageView>(R.id.statusIcon).apply {
            setImageResource(if (installed) R.drawable.ic_check_circle else R.drawable.ic_warning)
            imageTintList = ColorStateList.valueOf(fg)
        }
        findViewById<TextView>(R.id.statusTitle).apply {
            setText(if (installed) R.string.ca_status_installed else R.string.ca_status_missing)
            setTextColor(fg)
        }
        findViewById<TextView>(R.id.statusBody).setText(
            if (installed) R.string.ca_status_installed_body else R.string.ca_status_missing_body,
        )

        val modern = Build.VERSION.SDK_INT >= Build.VERSION_CODES.R
        bindStep(
            R.id.stepSave, 1, R.string.ca_step_save,
            getString(if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) R.string.ca_step_save_body else R.string.ca_step_save_body_legacy),
            R.string.ca_save, done = saved || installed,
        ) { saveCert() }
        bindStep(
            R.id.stepInstall, 2, R.string.ca_step_install,
            getString(if (modern) R.string.ca_step_install_body else R.string.ca_step_install_body_legacy),
            if (modern) R.string.ca_open_settings else R.string.ca_install_now, done = installed,
        ) { if (modern) openSecuritySettings() else keyChainInstall() }
        bindStep(
            R.id.stepVerify, 3, R.string.ca_step_verify,
            getString(if (installed) R.string.ca_step_verify_done else R.string.ca_step_verify_body),
            R.string.ca_check_again, done = installed,
        ) {
            render()
            if (!CaStatus.isInstalled(this)) {
                Toast.makeText(this, R.string.ca_status_missing, Toast.LENGTH_SHORT).show()
            }
        }
    }

    private fun bindStep(id: Int, n: Int, title: Int, body: String, button: Int, done: Boolean, action: () -> Unit) {
        val card = findViewById<View>(id)
        card.findViewById<TextView>(R.id.stepNumber).apply {
            text = n.toString()
            visibility = if (done) View.INVISIBLE else View.VISIBLE
        }
        card.findViewById<ImageView>(R.id.stepDone).visibility = if (done) View.VISIBLE else View.GONE
        card.findViewById<TextView>(R.id.stepTitle).setText(title)
        card.findViewById<TextView>(R.id.stepBody).text = body
        card.findViewById<Button>(R.id.stepButton).apply {
            setText(button)
            setOnClickListener { action() }
        }
    }

    private fun saveCert() {
        try {
            if (CaStatus.saveToDownloads(this) != null) {
                saved = true
                Toast.makeText(this, getString(R.string.ca_saved_downloads, CaStatus.FILE_NAME), Toast.LENGTH_LONG).show()
                render()
            } else {
                saveCertLauncher.launch(CaStatus.FILE_NAME)
            }
        } catch (e: Exception) {
            Toast.makeText(this, R.string.ca_save_failed, Toast.LENGTH_LONG).show()
        }
    }

    /** Write the PEM to the user-picked document (pre-Q save). */
    private fun saveCertTo(uri: Uri) {
        val pem = CaStatus.pem(this)
        if (pem == null) {
            Toast.makeText(this, R.string.ca_save_failed, Toast.LENGTH_LONG).show()
            return
        }
        try {
            contentResolver.openOutputStream(uri)?.use { it.write(pem.toByteArray()) }
                ?: throw IllegalStateException("no output stream")
            saved = true
            Toast.makeText(this, R.string.ca_saved, Toast.LENGTH_SHORT).show()
            render()
        } catch (e: Exception) {
            Toast.makeText(this, R.string.ca_save_failed, Toast.LENGTH_LONG).show()
        }
    }

    private fun writeCertToCache(): File? {
        val pem = CaStatus.pem(this)
        if (pem == null) {
            Toast.makeText(this, R.string.ca_save_failed, Toast.LENGTH_LONG).show()
            return null
        }
        val dir = File(cacheDir, "ca").apply { mkdirs() }
        return File(dir, CaStatus.FILE_NAME).apply { writeText(pem) }
    }

    /** Share the .crt (e.g. to another device, or a file manager). */
    private fun shareCert() {
        val file = writeCertToCache() ?: return
        val uri: Uri = FileProvider.getUriForFile(this, "$packageName.fileprovider", file)
        val share = Intent(Intent.ACTION_SEND).apply {
            type = CaStatus.MIME
            putExtra(Intent.EXTRA_STREAM, uri)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        startActivity(Intent.createChooser(share, getString(R.string.ca_export)))
    }

    /** Android 8–10: the KeyChain installer still accepts CA certificates. */
    private fun keyChainInstall() {
        val cert = CaStatus.certificate(this)
        if (cert == null) {
            Toast.makeText(this, R.string.ca_save_failed, Toast.LENGTH_LONG).show()
            return
        }
        try {
            startActivity(
                KeyChain.createInstallIntent().apply {
                    putExtra(KeyChain.EXTRA_CERTIFICATE, cert.encoded)
                    putExtra(KeyChain.EXTRA_NAME, "WebFilter CA")
                },
            )
        } catch (_: ActivityNotFoundException) {
            openSecuritySettings()
        }
    }

    /**
     * Android 11+: there is no public intent for the CA install screen
     * itself, so open the closest stable one (Security settings), falling
     * back to Settings home. The step text carries the rest of the path.
     */
    private fun openSecuritySettings() {
        for (action in listOf(Settings.ACTION_SECURITY_SETTINGS, Settings.ACTION_SETTINGS)) {
            try {
                startActivity(Intent(action))
                Toast.makeText(this, R.string.ca_settings_hint, Toast.LENGTH_LONG).show()
                return
            } catch (_: ActivityNotFoundException) {
            }
        }
        Toast.makeText(this, R.string.ca_settings_hint, Toast.LENGTH_LONG).show()
    }

    companion object {
        private const val STATE_SAVED = "saved"
    }
}
