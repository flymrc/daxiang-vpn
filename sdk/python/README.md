# zongheng-vpn Python SDK

Python SDK for controlling Zongheng VPN from scripts and applications.

The SDK is a thin wrapper around `zhvpn.exe`. It does not talk to the GUI, does not call Hub APIs directly, and does not reimplement WireGuard or sing-box runtime logic.

Naming:

- Install package: `zongheng-vpn`
- Python import: `zongheng_vpn`
- CLI executable: `zhvpn` / `zhvpn.exe`

```text
Python SDK -> zhvpn.exe --json -> local proxy 127.0.0.1:7890
```

## Local install

```powershell
.\sdk\python\build.ps1 -Install
```

This builds the Go CLI into the Python package as `zongheng_vpn/bin/zhvpn.exe`,
then installs the SDK in editable mode. After this, `Client()` works without
manually setting `ZHVPN_EXE`.

If you want the convenience `get()` / `request()` helpers:

```powershell
.\sdk\python\build.ps1 -Install -WithRequests
```

After publishing Windows wheels to PyPI, the target install command is:

```powershell
python -m pip install zongheng-vpn
python -m pip install "zongheng-vpn[requests]"
```

The PyPI wheel should include the matching `zhvpn.exe`, so users do not need a
separate CLI install.

## Install and upgrade behavior

Installing the SDK does not overwrite an existing desktop GUI or standalone
`zhvpn.exe`. The bundled executable is installed inside Python's own
`site-packages/zongheng_vpn/bin/` directory, and the SDK does not register a
global `zhvpn` command.

Normal cases:

- First install on a machine that already has the GUI: no conflict.
- First install on a machine that already has a standalone CLI in `PATH`: no conflict.
- SDK, GUI, and standalone CLI can share the same runtime state through the default `ZHVPN_HOME`.

The only case that can fail is upgrading or reinstalling the SDK while the
SDK-packaged `zhvpn.exe` is currently running. On Windows, a running executable
can lock the old file and make `pip install --upgrade` fail while replacing it.
Disconnect first, then upgrade:

```powershell
python -c "from zongheng_vpn import Client; Client().disconnect()"
python -m pip install --upgrade zongheng-vpn
```

If the VPN engine was started from the GUI or a separate CLI install, that
process does not lock the SDK's packaged executable. It still shares runtime
state, so disconnecting from either side is reflected in the other side.

## Build a release wheel

```powershell
.\sdk\python\build.ps1 -Wheel
```

The wheel is written to `sdk/python/dist/` and includes
`zongheng_vpn/bin/zhvpn.exe`. Windows wheels are platform-specific, for example
`zongheng_vpn-0.1.0-py3-none-win_amd64.whl`.

## Usage

```python
from zongheng_vpn import Client

vpn = Client()
vpn.login("ZH-XXXX")
vpn.connect()

status = vpn.status()
print(status.running, status.proxy, status.egress)

response = vpn.get("https://api64.ipify.org", timeout=10)
print(response.text)

vpn.rotate_ip()
vpn.disconnect()
```

For libraries that already manage HTTP clients, use the proxy helpers directly:

```python
proxies = vpn.proxies()
# {"http": "http://127.0.0.1:7890", "https": "http://127.0.0.1:7890"}
```

## CLI discovery

The SDK looks for `zhvpn.exe` in this order:

1. `Client(exe_path="...")`
2. `Client(command=[...])` for advanced/testing use
3. `ZHVPN_EXE`
4. Bundled package binary: `zongheng_vpn/bin/zhvpn.exe`
5. `PATH`
6. Common Windows install locations

Set `ZHVPN_EXE` only when you intentionally want to override the packaged CLI.

## Existing GUI or CLI installs

If the machine already has the desktop GUI or a standalone `zhvpn.exe`, the SDK
still uses its packaged CLI by default. This keeps the SDK and CLI JSON contract
version-matched.

All CLIs share the same default runtime directory (`%LOCALAPPDATA%\ZonghengVPN`),
so GUI and SDK operations see the same login/status/proxy state. Calling
`vpn.disconnect()` from Python will stop the same local VPN engine that the GUI
shows as connected.

To force the SDK to use an existing CLI instead:

```powershell
$env:ZHVPN_EXE="C:\Path\To\zhvpn.exe"
```

or:

```python
vpn = Client(exe_path=r"C:\Path\To\zhvpn.exe")
```
