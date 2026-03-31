package com.thefeed.android

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat

class MainActivity : ComponentActivity() {
    private lateinit var webView: WebView
    private lateinit var txtStatus: TextView
    private val handler = Handler(Looper.getMainLooper())
    private var retryCount = 0

    private val notificationPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { /* granted or not, service still works */ }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        webView = findViewById(R.id.webView)
        txtStatus = findViewById(R.id.txtStatus)

        findViewById<Button>(R.id.btnStart).setOnClickListener {
            startThefeedService()
            txtStatus.text = "Service started. Opening local UI..."
            retryCount = 0
            loadLocalWebWithRetry()
        }

        findViewById<Button>(R.id.btnStop).setOnClickListener {
            stopService(Intent(this, ThefeedService::class.java))
            txtStatus.text = "Service stopped"
        }

        findViewById<Button>(R.id.btnReload).setOnClickListener {
            retryCount = 0
            loadLocalWebWithRetry()
        }

        requestNotificationPermission()
        configureWebView()
        startThefeedService()
        retryCount = 0
        loadLocalWebWithRetry()
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED
            ) {
                notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
    }

    private fun startThefeedService() {
        val intent = Intent(this, ThefeedService::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
    }

    private fun configureWebView() {
        webView.webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView?, url: String?) {
                if (url != null && url.startsWith("http://127.0.0.1")) {
                    txtStatus.text = "Connected"
                    retryCount = 0
                }
            }

            override fun onReceivedError(
                view: WebView?,
                request: WebResourceRequest?,
                error: WebResourceError?
            ) {
                if (request?.isForMainFrame == true && retryCount < MAX_RETRIES) {
                    retryCount++
                    txtStatus.text = "Waiting for service to start... (attempt $retryCount)"
                    handler.postDelayed({ loadLocalWebWithRetry() }, RETRY_DELAY_MS)
                } else if (retryCount >= MAX_RETRIES) {
                    txtStatus.text = "Could not connect. Tap Reload to try again."
                }
            }
        }

        with(webView.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            cacheMode = WebSettings.LOAD_NO_CACHE
            allowFileAccess = false
            allowContentAccess = false
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
        }
    }

    private fun loadLocalWebWithRetry() {
        val url = "http://127.0.0.1:${ThefeedService.PORT}"
        txtStatus.text = "Loading $url ..."
        // Give the Go binary time to bind the port on first launch
        val delay = if (retryCount == 0) INITIAL_DELAY_MS else 0L
        handler.postDelayed({ webView.loadUrl(url) }, delay)
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        webView.destroy()
        super.onDestroy()
    }

    companion object {
        private const val MAX_RETRIES = 15
        private const val RETRY_DELAY_MS = 2000L
        private const val INITIAL_DELAY_MS = 1500L
    }
}
