package com.webfilter.app

import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity

/** Secondary screens: action-bar title with an up arrow that closes the screen. */
open class BaseActivity : AppCompatActivity() {

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        supportActionBar?.setDisplayHomeAsUpEnabled(true)
    }

    override fun onSupportNavigateUp(): Boolean {
        finish()
        return true
    }
}
