# Android reverse egress

`egress/reverse` is the production replacement data plane for the Android
mobile egress node. Android actively dials the Hub and the Hub exposes a local
HTTP CONNECT proxy for the egress router/client layer.

The legacy Android `zhandroid-egress` / sing-box proxy has been removed from
the production path. Android production data must use `zhreverse`.

## Shape

```text
Hub egress router/client
  -> 10.66.0.1:18081 HTTP CONNECT proxy
  -> zhreverse server
  -> TCP + yamux reverse session
  -> zhreverse client on Android
  -> public target from Android network
```

The Android side actively dials the Hub, so no inbound port is required on the
phone. The server can either resolve target hostnames and send `IP:port` to
Android, or ask Android to resolve with a public DNS fallback.

The proxy can also expose an experimental application-layer fetch endpoint when
`server.enable_fetch: true` is set:

```text
Hub curl/client
  -> GET http://10.66.0.1:18081/fetch?url=<absolute-http-or-https-url>
  -> zhreverse server
  -> FETCH <base64url(url)>
  -> zhreverse client on Android
  -> Android-side HTTP GET
  -> status, headers, and body streamed back to Hub
```

`/fetch` is disabled by default and is not a transparent proxy replacement. It
is a diagnostic path for testing recoverable Android-side downloads without
tunneling the target TLS connection byte-for-byte. The Android binary uses the
same public-DNS dialer as CONNECT and explicitly loads Android system CA certificates from
`/system/etc/security/cacerts` / `/apex/com.android.conscrypt/cacerts`.

## Build

```sh
GOOS=linux GOARCH=amd64 go build -o dist/reverse/zhreverse-linux-amd64 ./egress/reverse
GOOS=linux GOARCH=arm64 go build -o dist/reverse/zhreverse-linux-arm64 ./egress/reverse
```

## Configs

Example configs:

- Hub: `docs/20-operations/configs/egress/hub-reverse-server.yaml.example`
- Android: `docs/20-operations/configs/egress/android-reverse-client.yaml.example`

Use `token_file` in production and keep the real token out of git. Production
currently uses TCP/yamux because Rakuten mobile UDP paths showed repeated QUIC
idle timeouts. QUIC clients must pin the Hub certificate with
`client.server_cert_sha256`; the unsafe `--insecure-skip-verify` flag is only
for temporary lab runs.

## Production services

Hub systemd template:

```sh
install -d /opt/zongheng/zhreverse /etc/zongheng/zhreverse
install -m 755 dist/reverse/zhreverse-linux-amd64 /opt/zongheng/zhreverse/zhreverse
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -subj /CN=zhreverse \
  -keyout /etc/zongheng/zhreverse/server.key \
  -out /etc/zongheng/zhreverse/server.crt
chmod 600 /etc/zongheng/zhreverse/server.key
openssl x509 -in /etc/zongheng/zhreverse/server.crt -outform DER \
  | sha256sum | awk '{print $1}'
install -m 600 docs/20-operations/configs/egress/hub-reverse-server.yaml.example /etc/zongheng/zhreverse/server.yaml
install -m 644 egress/reverse/systemd/zhreverse-hub.service /etc/systemd/system/zhreverse-hub.service
systemctl daemon-reload
systemctl enable --now zhreverse-hub.service
```

Android Magisk service.d template:

```sh
mkdir -p /data/adb/zhreverse/bin
cp /data/local/tmp/zhreverse /data/adb/zhreverse/bin/zhreverse
cp /data/local/tmp/android-reverse-client.yaml /data/adb/zhreverse/client.yaml
cp /data/local/tmp/99-zhreverse-egress.sh /data/adb/service.d/99-zhreverse-egress.sh
chmod 700 /data/adb/zhreverse/bin/zhreverse /data/adb/service.d/99-zhreverse-egress.sh
chmod 600 /data/adb/zhreverse/client.yaml /data/adb/zhreverse/token
```

The Android service script disables the legacy `zhandroid-egress` supervisor,
keeps Wi-Fi off for mobile-SIM egress, applies conservative UDP/socket buffer
tuning, then keeps `zhreverse client --config /data/adb/zhreverse/client.yaml`
running.

## Manual test run

Server:

```sh
/tmp/zhreverse server \
  --transport tcp \
  --resolve server \
  --listen 0.0.0.0:39093 \
  --proxy 10.66.0.1:18081 \
  --tls-cert-file /etc/zongheng/zhreverse/server.crt \
  --tls-key-file /etc/zongheng/zhreverse/server.key \
  --token <shared-token>
```

Android client:

```sh
/data/local/tmp/zhreverse client \
  --transport tcp \
  --connections 2 \
  --server <hub-public-ip>:39093 \
  --token <shared-token>
```

Use `--transport quic` for the UDP/QUIC experiment; when using QUIC, also pass
`--server-cert-sha256 <hub-cert-sha256>`. Use `--connections N` to have Android
maintain multiple reverse sessions; the server prefers idle sessions with lower
observed command RTT instead of blindly round-robining CONNECT requests across
them. Production keeps `connections: 2` as the current balance between lower
TCP/yamux head-of-line blocking and mobile-network half-dead session risk.
Use `--resolve client` to have the Android side resolve target
hostnames via public DNS, which helps test CDN/DNS selection effects.

The Hub server applies a deadline while sending `CONNECT`/`FETCH` commands and
waiting for the Android-side response. If a reverse session is half-dead,
the server removes that session from the pool and retries another session
instead of letting the HTTP proxy request hang behind a stale tunnel. Successful
commands update a per-session RTT estimate and active stream count, so new
CONNECT requests avoid busy or slower tunnel sessions when another session is
healthier.
Hub-side diagnostics are exposed at `GET /debug/session-health` on the same
proxy listener. The endpoint is protected by `allowed_proxy_cidrs` and returns
the current reverse session count, per-session active stream count, consecutive
failure count, command RTT EWMA, scheduler score, and active proxy concurrency.
`GET /debug/tunnel-bench?bytes=<total>&streams=<n>` measures only Android ->
Hub reverse tunnel throughput. It splits the requested byte count across up to
8 reverse streams, asks Android to send synthetic bytes with the `BENCH`
command, and returns per-stream plus aggregate Mbps. This avoids DNS, target
TLS, CDN, and public-site variability; it is not a replacement for real egress
download tests.
The Hub server can also cap concurrent `CONNECT` sessions with
`max_proxy_connections` and `max_proxy_connections_per_client`; production uses
these as a fast-fail guard so browser burst concurrency does not pile up inside
the mobile reverse tunnel.

Hub-side probe:

```sh
scripts/check-android-reverse-egress.sh
curl -s http://10.66.0.1:18081/debug/session-health
curl -s 'http://10.66.0.1:18081/debug/tunnel-bench?bytes=20000000&streams=1'
curl -s 'http://10.66.0.1:18081/debug/tunnel-bench?bytes=20000000&streams=2'
curl --proxy http://10.66.0.1:18081 https://api.ipify.org
curl -L --proxy http://10.66.0.1:18081 -o /dev/null \
  'https://speed.cloudflare.com/__down?bytes=20000000'
# Requires server.enable_fetch: true.
curl -L -o /dev/null \
  'http://10.66.0.1:18081/fetch?url=https%3A%2F%2Fspeed.cloudflare.com%2F__down%3Fbytes%3D2000000'
```

Remove any temporary public firewall rule after manual testing. The production
Hub service needs the configured UDP listener open.
