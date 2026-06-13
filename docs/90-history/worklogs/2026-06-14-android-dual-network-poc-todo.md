# 2026-06-14 Android dual-network POC TODO

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
