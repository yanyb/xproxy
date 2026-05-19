# xproxy

Minimal phone-egress SOCKS5 proxy. New codebase; extend here instead of growing the legacy tree.

## Layout

```
cmd/server/          process entry
cmd/device/          phone client (desktop / debug)
cmd/                 (no android binary — use mobile AAR)
mobile/              gomobile API for Android
mobile/android/      Kotlin HostResolver example
internal/protocol/   NDJSON control frames
internal/tunnel/     device registry, serve, dial
internal/socks/      SOCKS5 wiring
internal/device/     TLS client session
internal/config/     YAML config
scripts/             gomobile-bind-android.sh
```

## Run

```bash
cd xproxy
cp configs/server.example.yaml configs/server.yaml
cp configs/device.example.yaml configs/device.yaml
# point tls_cert/tls_key at real PEM files

go run ./cmd/server -config configs/server.yaml
go run ./cmd/device -config configs/device.yaml
```

SOCKS5: `curl --socks5-hostname phone-1:secret@127.0.0.1:1080 https://ifconfig.me`

- Username = `device_id` when multiple phones are online.
- Empty `socks_password` allows no-auth only with a single device.

## Android

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
export ANDROID_HOME=...
./scripts/gomobile-bind-android.sh
```

AAR imports package `xproxy.mobile` (override with `JAVAPKG=...`).

```kotlin
import xproxy.mobile.ClientConfig
import xproxy.mobile.Mobile
import xproxy.android.AndroidDns

val cfg = ClientConfig().apply {
    deviceID = "phone-1"
    serverAddr = "your.server:8443"
    heartbeatIntervalNs = 10_000_000_000L
    reconnectMinNs = 1_000_000_000L
    reconnectMaxNs = 60_000_000_000L
}
Mobile.setNetworkType("wifi")
Thread { Mobile.run(cfg, AndroidDns()) }.start()
// Mobile.stop() on destroy
```

`HostResolver.lookupHost` returns newline-separated IPs (gomobile cannot bind `[]string`).

## Extend later

| Feature | Suggested package |
|---------|-------------------|
| Encrypted register / admin API | `internal/admin` or separate service |
| Pick device by country / RTT | `internal/schedule` |
| Traffic metering | wrap `net.Conn` in `internal/meter` |
| NSQ / Mongo heartbeat | `internal/pipeline` |
