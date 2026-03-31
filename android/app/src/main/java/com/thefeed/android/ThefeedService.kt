package com.thefeed.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import java.io.File
import java.io.FileOutputStream

class ThefeedService : Service() {
    private var process: Process? = null
    private var readerThread: Thread? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        startForeground(NOTIFICATION_ID, buildNotification("Starting local service..."))
        startClientProcessAsync()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // If the process died, restart it
        if (process == null || !isProcessAlive()) {
            startClientProcessAsync()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        readerThread?.interrupt()
        readerThread = null
        process?.destroy()
        process?.waitFor()
        process = null
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun isProcessAlive(): Boolean {
        return try {
            process?.exitValue()
            false // exitValue() returned, so the process has exited
        } catch (_: IllegalThreadStateException) {
            true // still running
        }
    }

    private fun startClientProcessAsync() {
        // Don't spawn a second process
        if (process != null && isProcessAlive()) return

        Thread {
            try {
                val bin = ensureBinary()
                val dataDir = File(filesDir, "thefeeddata")
                if (!dataDir.exists()) dataDir.mkdirs()

                val env = mutableMapOf<String, String>()
                env["HOME"] = filesDir.absolutePath
                env["TMPDIR"] = cacheDir.absolutePath

                val pb = ProcessBuilder(
                    bin.absolutePath,
                    "--data-dir", dataDir.absolutePath,
                    "--port", PORT.toString()
                )
                pb.directory(dataDir)
                pb.redirectErrorStream(true)
                pb.environment().putAll(env)

                process = pb.start()

                readerThread = Thread {
                    try {
                        process?.inputStream?.bufferedReader()?.use { reader ->
                            while (!Thread.currentThread().isInterrupted) {
                                val line = reader.readLine() ?: break
                                updateForegroundNotification(line)
                            }
                        }
                    } catch (_: Exception) {
                    }
                }
                readerThread?.isDaemon = true
                readerThread?.start()

                updateForegroundNotification("Running on http://127.0.0.1:$PORT")
            } catch (e: Exception) {
                updateForegroundNotification("Failed: ${e.message}")
            }
        }.start()
    }

    private fun ensureBinary(): File {
        val target = File(filesDir, "thefeed-client")

        // If already extracted and executable, verify it's still valid
        if (target.exists() && target.length() > 0L && target.canExecute()) {
            return target
        }

        // Extract fresh copy from assets
        if (target.exists()) target.delete()

        assets.open("thefeed-client").use { input ->
            FileOutputStream(target).use { out ->
                input.copyTo(out)
            }
        }

        if (!target.setExecutable(true, true)) {
            throw IllegalStateException("Could not set executable bit on bundled binary")
        }

        return target
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "thefeed background service",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Keeps thefeed client running"
                setShowBadge(false)
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(message: String): Notification {
        val openIntent = Intent(this, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP
        }
        val pendingIntent = PendingIntent.getActivity(
            this, 0, openIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("thefeed")
            .setContentText(message)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(true)
            .setContentIntent(pendingIntent)
            .setSilent(true)
            .build()
    }

    private fun updateForegroundNotification(message: String) {
        try {
            val manager = getSystemService(NotificationManager::class.java)
            manager.notify(NOTIFICATION_ID, buildNotification(message))
        } catch (_: Exception) {
            // Notification permission may not be granted; service still runs
        }
    }

    companion object {
        const val CHANNEL_ID = "thefeed_service"
        const val NOTIFICATION_ID = 1201
        const val PORT = 8080
    }
}
