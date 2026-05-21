package com.example.xproxy

import android.os.Bundle
import android.util.Log
import androidx.activity.ComponentActivity
import xproxy.mobile.ClientConfig
import xproxy.mobile.Mobile

class MainActivity : ComponentActivity() {
    private var thread: Thread? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Log.i(TAG, "MainActivity.onCreate")

        val cfg = ClientConfig().apply {
            deviceID = "phone-1"
            serverAddr = "nddtech.cn:443"
            heartbeatIntervalNs = 10_000_000_000L
            reconnectMinNs = 1_000_000_000L
            reconnectMaxNs = 60_000_000_000L
            proxyIdleTimeout = 30_000_000_000L
            maxConcurrent = 1000
            dnsCacheTTL = 30_000_000_000L
        }

        thread = Thread {
            Log.i(TAG, "calling Mobile.run")
            try {
                Mobile.run(cfg, AndroidDns())
                Log.i(TAG, "Mobile.run returned normally")
            } catch (t: Throwable) {
                Log.e(TAG, "Mobile.run threw", t)
            }
        }.apply {
            name = "xproxy-run"
            // Critical: catch errors here instead of letting the default
            // handler kill the whole process.
            setUncaughtExceptionHandler { th, ex ->
                Log.e(TAG, "uncaught in ${th.name}", ex)
            }
            start()
        }
    }

    override fun onDestroy() {
        Log.i(TAG, "MainActivity.onDestroy")
        try { Mobile.stop() } catch (t: Throwable) { Log.e(TAG, "stop", t) }
        thread?.interrupt()
        super.onDestroy()
    }

    companion object { private const val TAG = "xproxy" }
}