# Zongheng Innernet Status

macOS status bar helper for the Zongheng management innernet.

It intentionally stores no WireGuard private key. It only checks local route/status and calls the installed helper scripts:

- `~/.zhvpn/bin/zhvpn-admin-innernet-up.sh`
- `~/.zhvpn/bin/zhvpn-admin-innernet-down.sh`

It falls back to `/usr/local/sbin/` if the user-local helper scripts are missing.

The helper works with either:

- `admin-innernet` client IP `10.66.0.40`.
- Existing Mac egress IP `10.66.0.100`.

Do not run both on the same Mac. They both own routes for the `10.66.0.0/24` WireGuard management subnet, so the helper disables `admin-innernet` connect while `mac-egress` is active.

Build:

```bash
swiftc -O -framework AppKit -o local/apps/ZonghengInnernetStatus clients/admin-menubar/ZonghengInnernetStatus.swift
```
