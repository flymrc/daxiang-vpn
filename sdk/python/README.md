# zongheng-vpn Python SDK

用 Python 控制纵横 VPN。连接后，本机代理地址是 `127.0.0.1:7890`。

## 安装

```powershell
python -m pip install .\dist\zongheng_vpn-0.1.2-py3-none-win_amd64.whl
```

如果要用 `vpn.get()` / `vpn.request()`：

```powershell
python -m pip install requests
```

## 使用

```python
from zongheng_vpn import Client

vpn = Client()

vpn.login("ZH-XXXX")  # 第一次使用需要
vpn.connect()

print(vpn.status())
print(vpn.get("https://api64.ipify.org", timeout=10).text)

vpn.disconnect()
```

自定义本地代理端口：

```python
vpn.connect(port=7891)
```

如果不用 SDK 发请求，只取代理配置：

```python
proxies = vpn.proxies()
# {"http": "http://127.0.0.1:7890", "https": "http://127.0.0.1:7890"}
```

## 常用

```python
vpn.connect()
vpn.disconnect()
vpn.status()
vpn.status_ip()
vpn.rotate_ip()
vpn.logout()
```

`status()` 默认只读取本地状态，不探测出口 IP；`status_ip()` 才启用这次
状态查询的 IP 探测。`running` 表示本地引擎状态，`proxy_reachable` 表示
本地 TCP 端口可达，两者都不能单独证明 WireGuard 或住宅出口健康。

当前 SDK 校验公开 CLI v1 合同，允许兼容的附加字段和没有版本字段的旧
CLI 输出，拒绝不兼容版本、错误类型、身份缺失和矛盾状态。旧 `Status`
字段继续保留；新增 `engine_state`、实例/配置标识、`error_code` 和
`contract_version` 可用于定位。

```python
from zongheng_vpn import ZHVpnContractError

try:
    status = vpn.status()
    print(status.engine_state, status.error_code)
    print(status.evidence.engine_ready)
    print(status.evidence.tunnel_healthy)  # 当前合同没有此证据，始终 None
    print(status.evidence.verified_at)    # 当前合同没有验证时间，始终 None
except ZHVpnContractError as error:
    print(error.field_path, error.error_code, error.returncode)
```

`None` 表示未知。旧输出缺少认证身份时，`evidence.engine_ready` 为
`None`，旧 `running` 仍按原字段返回。没有 IPv4/IPv6 观察结果不表示该
协议族不健康；`egress` 是缓存的配置名称，IP 是这次查询的观察值，SDK
不从接收时间伪造验证时间，也不把未知扩展字段当成健康证据。

错误保留命令、退出码、公开诊断码和字段位置，并对已知凭据脱敏。
CLI 输出不是有效 JSON 或不是响应对象时，SDK 不在异常里保存原始
stdout/stderr，防止损坏输出里的私钥或登录参数泄漏。

合同维护源与确定性生成命令见 [`shared/contracts`](../../shared/contracts/README.md)。

## 升级

如果当前 VPN 是 Python SDK 连上的，升级前先断开：

```powershell
python -c "from zongheng_vpn import Client; Client().disconnect()"
python -m pip install --upgrade .\dist\zongheng_vpn-0.1.2-py3-none-win_amd64.whl
```
