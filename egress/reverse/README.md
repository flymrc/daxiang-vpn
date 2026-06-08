# Android reverse egress

`egress/reverse` is the production replacement data plane for the Android
mobile egress node. Android actively dials the Hub and the Hub exposes a local
HTTP CONNECT proxy for the egress router/client layer.

The legacy `dxandroid-egress` / sing-box proxy is kept only as a rollback path.

## Shape

```text
Hub egress router/client
  -> 127.0.0.1:18081 HTTP CONNECT proxy
  -> dxreverse server
  -> UDP + QUIC reverse session
  -> dxreverse client on Android
  -> public target from Android network
```

The Android side actively dials the Hub, so no inbound port is required on the
phone. The server can either resolve target hostnames and send `IP:port` to
Android, or ask Android to resolve with a public DNS fallback.

The proxy also exposes an experimental application-layer fetch endpoint:

```text
Hub curl/client
  -> GET http://127.0.0.1:18081/fetch?url=<absolute-http-or-https-url>
  -> dxreverse server
  -> FETCH <base64url(url)>
  -> dxreverse client on Android
  -> Android-side HTTP GET
  -> status, headers, and body streamed back to Hub
```

`/fetch` is not a transparent proxy replacement. It is a diagnostic path for
testing recoverable Android-side downloads without tunneling the target TLS
connection byte-for-byte. The Android binary uses the same public-DNS dialer as
CONNECT and explicitly loads Android system CA certificates from
`/system/etc/security/cacerts` / `/apex/com.android.conscrypt/cacerts`.

## Build

```sh
GOOS=linux GOARCH=amd64 go build -o dist/reverse/dxreverse-linux-amd64 ./egress/reverse
GOOS=linux GOARCH=arm64 go build -o dist/reverse/dxreverse-linux-arm64 ./egress/reverse
```

## Configs

Example configs:

- Hub: `docs/20-operations/configs/egress/hub-reverse-server.yaml.example`
- Android: `docs/20-operations/configs/egress/android-reverse-client.yaml.example`

Use `token_file` in production and keep the real token out of git.

## Production services

Hub systemd template:

```sh
install -d /opt/daxiang/dxreverse /etc/daxiang/dxreverse
install -m 755 dist/reverse/dxreverse-linux-amd64 /opt/daxiang/dxreverse/dxreverse
install -m 600 docs/20-operations/configs/egress/hub-reverse-server.yaml.example /etc/daxiang/dxreverse/server.yaml
install -m 644 egress/reverse/systemd/dxreverse-hub.service /etc/systemd/system/dxreverse-hub.service
systemctl daemon-reload
systemctl enable --now dxreverse-hub.service
```

Android Magisk service.d template:

```sh
mkdir -p /data/adb/dxreverse/bin
cp /data/local/tmp/dxreverse /data/adb/dxreverse/bin/dxreverse
cp /data/local/tmp/android-reverse-client.yaml /data/adb/dxreverse/client.yaml
cp /data/local/tmp/99-dxreverse-egress.sh /data/adb/service.d/99-dxreverse-egress.sh
chmod 700 /data/adb/dxreverse/bin/dxreverse /data/adb/service.d/99-dxreverse-egress.sh
chmod 600 /data/adb/dxreverse/client.yaml /data/adb/dxreverse/token
```

The Android service script stops the legacy `dxandroid-egress` supervisor by
default, then keeps `dxreverse client --config /data/adb/dxreverse/client.yaml`
running.

## Manual test run

Server:

```sh
/tmp/dxreverse server \
  --transport quic \
  --resolve server \
  --listen 0.0.0.0:39093 \
  --proxy 127.0.0.1:18081 \
  --token <shared-token>
```

Android client:

```sh
/data/local/tmp/dxreverse client \
  --transport quic \
  --connections 4 \
  --server <hub-public-ip>:39093 \
  --token <shared-token>
```

Use `--transport tcp` for the older TCP/yamux experiment. Use
`--connections N` to have Android maintain multiple reverse sessions; the
server round-robins CONNECT requests across them. Use `--resolve client` to have
the Android side resolve target hostnames via public DNS, which helps test
CDN/DNS selection effects.

Hub-side probe:

```sh
scripts/check-android-reverse-egress.sh
curl --proxy http://127.0.0.1:18081 https://api.ipify.org
curl -L --proxy http://127.0.0.1:18081 -o /dev/null \
  'https://speed.cloudflare.com/__down?bytes=20000000'
curl -L -o /dev/null \
  'http://127.0.0.1:18081/fetch?url=https%3A%2F%2Fspeed.cloudflare.com%2F__down%3Fbytes%3D2000000'
```

Remove any temporary public firewall rule after manual testing. The production
Hub service needs the configured UDP listener open.
