package com.example.xproxy

import android.os.Bundle
import androidx.activity.ComponentActivity
import xproxy.mobile.ClientConfig
import xproxy.mobile.Mobile

class MainActivity : ComponentActivity() {
    private var thread: Thread? = null
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val cfg = ClientConfig().apply {
            deviceID = "phone-1"
            serverAddr = "nddtech.cn:443"
            heartbeatIntervalNs = 10_000_000_000
            reconnectMinNs = 1_000_000_000
            reconnectMaxNs = 60_000_000_000
            proxyIdleTimeout = 30_000_000_000
            maxConcurrent = 128
            dnsCacheTTL = 30_000_000_000
        }
        thread = Thread { Mobile.run(cfg, AndroidDns()) }.also { it.start() }
    }
    override fun onDestroy() {
        Mobile.stop()
        super.onDestroy()
    }
}