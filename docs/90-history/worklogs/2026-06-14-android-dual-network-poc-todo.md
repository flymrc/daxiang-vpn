# 2026-06-14 Android dual-network POC

## Idea

Use residential WiFi only for the Android -> Hub reverse tunnel, while keeping
target website connections on the phone's cellular IPv6 path.

Desired split:

```text
zhreverse tunnel socket -> wlan0 / home WiFi IPv4 -> home relay -> Hub
zhreverse target socket -> rmnet_data* / cellular IPv6 -> target website
```

## Why It Could Work

The phone is rooted and Android is Linux underneath. With root we can
experiment with interface binding, policy routing, fwmark, or Android netd
network handles. The hard part is not the home TCP relay; the hard part is
forcing different sockets from the same `zhreverse` process onto different
networks.

## Tomorrow POC

1. Enable WiFi and cellular together. Enable "mobile data always on".
2. Read-only inspect Android network state:

```sh
ip -6 addr show
ip route
ip -6 route
dumpsys connectivity | head -200
```

3. Run a home LAN relay such as:

```text
192.168.1.x:39093 -> 36.50.84.68:39093
```

4. Point a temporary Android `zhreverse client` at the LAN relay.
5. Check Hub `/debug/session-health`.
6. Success if Hub sees the residential public IPv4 as `remote_addr`.
7. Then check proxy egress:

```bash
curl -x http://10.66.0.1:18081 -s https://api6.ipify.org; echo
```

Success if the returned IP is still the phone's `240b:...` cellular IPv6.

## If Default Routing Steals Target Traffic

Add explicit split-routing support:

- `tunnel_bind_interface: wlan0`
- `target_bind_interface: rmnet_dataX`
- possible `SO_BINDTODEVICE`
- possible fwmark / policy route
- possible Android netd network handle
- prefer cellular-side DNS for target resolution

## Success Criteria

- Hub reverse session `remote_addr` = residential public IPv4.
- Proxy `api6.ipify.org` = phone cellular `240b:...` IPv6.
- `check-android-egress-health.ps1` passes.
- `measure-android-tail-latency.ps1 -Runs 30` p95/p99 is not worse than the
  current cellular-tunnel baseline.

## Rollback

Restore Android `client.yaml` to `server=36.50.84.68:39093` or turn WiFi off,
then `pkill zhreverse` and let the service script restart the production path.

## WiFi Connected Inspection

After WiFi was connected, read-only checks showed:

- `wlan0` is up with `192.168.3.3/24` and residential IPv6
  `2400:2412:...`.
- Cellular remains up on `rmnet1` with IPv4 `10.1.7.154/32` and IPv6
  `240b:c010:...`.
- Android's active default network is WiFi. The WireGuard VPN also reports WiFi
  as its underlying network.
- Hub `/debug/session-health` now sees the Android reverse sessions from the
  residential public IPv4 `60.124.42.38:*`, so the tunnel leg already moved to
  WiFi.
- A proxy request to `https://api6.ipify.org` returned residential WiFi IPv6
  `2400:2412:...`, proving target website sockets also follow WiFi by default.
- `ip -6 route get ... oif rmnet1` can still produce a cellular IPv6 route,
  so explicit socket binding is worth testing before moving to heavier Android
  netd/fwmark work.

## Code POC

Implemented an opt-in client-side bind-interface POC:

- `tunnel_bind_interface`: bind reverse tunnel TCP dials, intended for `wlan0`.
- `target_bind_interface`: bind target TCP dials and target DNS dials, intended
  for `rmnet1`.
- Linux uses `SO_BINDTODEVICE`; non-Linux builds keep default behavior unless
  the option is explicitly set.
- `tunnel_bind_interface` is intentionally TCP-only in this POC and is rejected
  with QUIC tunnel transport.

Validation:

```powershell
go test ./egress/reverse
$env:GOOS='linux'; $env:GOARCH='arm64'; go build ./egress/reverse
```

## Deployment

Deployed the bind-interface POC to Android only.

- Android binary SHA256:
  `7d813b5d2f42548e4210ce3c6595034b071cccebfe23e4b9d4d5e80e55e990af`
- Android binary backup:
  `/data/adb/zhreverse/bin/zhreverse.bak-20260614-bind-iface-poc`
- Android config backup:
  `/data/adb/zhreverse/client.yaml.bak-20260614-bind-iface-poc`
- Live Android config adds:

```yaml
tunnel_bind_interface: wlan0
target_bind_interface: rmnet1
```

After `pkill zhreverse`, supervisor restarted the client as PID `12042`.

## POC Result

The split works:

- Hub `/debug/session-health` shows two reverse sessions from residential public
  IPv4 `60.124.42.38:*`.
- Android `ss -ntp` shows reverse tunnel sockets:
  `192.168.3.3%wlan0 -> 36.50.84.68:39093`.
- A proxy request to `https://api6.ipify.org` returns cellular IPv6
  `240b:c010:420:43fc:0:22:6ab2:8701`.
- During the request, Android `ss -ntp` showed target socket:
  `[240b:c010:420:43fc:0:22:6ab2:8701]%rmnet1 -> [2607:f2d8:...]:443`.
- `scripts/check-android-egress-health.ps1` passes. v4 egress also remains
  phone-side (`133.106.32.25`), not Hub or residential WiFi.

Tail-latency smoke after deploy, `measure-android-tail-latency.ps1 -Runs 30`:

```text
api64.ipify.org                 ok=30/30 total_ms p50=630.84 p95=911.73 p99=974.89
speed.cloudflare.com 1KB        ok=30/30 total_ms p50=331.79 p95=695.05 p99=706.02
www.cloudflare.com/cdn-cgi/trace ok=30/30 total_ms p50=306.29 p95=636.27 p99=785.95
Hub proxy_metrics first_byte_ms p50=783 p95=1276 p99=1510
```

This is materially better than the earlier cellular-tunnel baseline for small
Cloudflare requests.

## Fallback Behavior

There is no automatic fallback in this POC. Because the tunnel socket is
explicitly bound to `wlan0`, WiFi/home broadband loss makes the reverse tunnel
fail and reconnect attempts keep targeting `wlan0`.

Manual rollback:

```sh
cp /data/adb/zhreverse/client.yaml.bak-20260614-bind-iface-poc /data/adb/zhreverse/client.yaml
cp /data/adb/zhreverse/bin/zhreverse.bak-20260614-bind-iface-poc /data/adb/zhreverse/bin/zhreverse.staged
chmod 700 /data/adb/zhreverse/bin/zhreverse.staged
mv /data/adb/zhreverse/bin/zhreverse.staged /data/adb/zhreverse/bin/zhreverse
pkill zhreverse
```
