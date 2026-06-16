# zongheng-vpn Python SDK

Python SDK for controlling Zongheng VPN from scripts and applications.

The SDK is a thin wrapper around `zhvpn.exe`. It does not talk to the GUI, does not call Hub APIs directly, and does not reimplement WireGuard or sing-box runtime logic.

```text
Python SDK -> zhvpn.exe --json -> local proxy 127.0.0.1:7890
```

## Install for development

```powershell
python -m pip install -e sdk/python
```

If you want the convenience `get()` / `request()` helpers:

```powershell
python -m pip install -e "sdk/python[requests]"
```

After publishing to PyPI, the target install command is:

```powershell
python -m pip install zongheng-vpn
python -m pip install "zongheng-vpn[requests]"
```

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
4. `PATH`
5. Common Windows install locations

Set `ZHVPN_EXE` if the desktop GUI installed sidecar is not on `PATH`.
