package xproxy.android

import java.net.InetAddress
import xproxy.mobile.HostResolver

class AndroidDns : HostResolver {
    override fun lookupHost(hostname: String): String {
        val addrs = InetAddress.getAllByName(hostname)
        return addrs.joinToString("\n") { it.hostAddress ?: "" }
    }
}
