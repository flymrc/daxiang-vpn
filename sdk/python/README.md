# zongheng-vpn Python SDK

用 Python 控制纵横 VPN。连接后，本机代理地址是 `127.0.0.1:7890`。

## 安装

当前开发分支尚未提供通过平台/签名验收的原生 wheel，`build.ps1 -Wheel` 和直接 `pip wheel` 均拒绝。可使用源码 SDK 配合自己明确选择、已核对的 CLI：

```powershell
# 在 sdk/python 目录使用源码；正式包安装另行验收
$env:PYTHONPATH = (Resolve-Path .\src).Path
```

如果要用 `vpn.get()` / `vpn.request()`：

```powershell
python -m pip install requests
```

## 使用

```python
from zongheng_vpn import Client

vpn = Client(exe_path=r"C:\verified-tools\zhvpn.exe")

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

`vpn.proxies()`、`vpn.proxy_url()` 和未显式指定代理的 `vpn.request()` 现在要求
当前引擎认证就绪、代理 TCP 可达且没有状态错误。旧 CLI 状态仍可读取，但缺少
实例身份时不会自动将请求发送到配置端口。调用者主动传入 `proxy` 或 `proxies`
仍是显式选择，SDK 不将该选择解释为已验证健康；拒绝路径不会改成直接联网。

默认本地命令等待预算为 30 秒，`rotate_ip()` 的默认预算为至少 180 秒，显式
`wait_seconds` 较大时增加到该值加 90 秒。构造函数或方法指定的 `timeout`
优先，必须为有限正数。换 IP 超时可能发生在手机已经执行之后：

```python
from zongheng_vpn import ZHVpnTimeout

try:
    result = vpn.rotate_ip()
    print(result.status, result.before, result.after)
except ZHVpnTimeout as error:
    print(error.result_unknown, error.error_code)
    # 只读观察，不自动重复换 IP。
    print(vpn.status_ip())
```

`result_unknown=True` 表示修改命令结果未知，不表示没有执行。SDK 不自动重试；
当前 CLI 尚无持久 operation ID/查询协议，所以状态或 IP 观察不能确认某一次
超时操作的最终结果。`status="triggered"` 只表示触发请求已返回，不代表 IP
已经改变。

Windows 用户级系统代理由 CLI 管理；SDK 不直接读写注册表：

```python
lease = vpn.system_proxy_acquire()  # 必须已有认证 ready 引擎
print(lease.lease_id, lease.noop)
vpn.system_proxy_release(lease.lease_id)
record = vpn.system_proxy_inspect()  # recorded 只代表持久记录，不是 OS/出口健康
vpn.system_proxy_recover()           # 仅同 SID/home 的原实例已停止时允许恢复
```

释放需要 acquire 返回的 lease ID；复用已有租约时 `noop=True`，不能将其当作
当前调用者新接管的系统配置。恢复、范围或权限失败保留引擎与恢复材料，不能
先删除记录。旧 GUI v1 journal 需要按旧 GUI 的检查恢复流程处理，SDK 不自动
填补归属或收养成 v2。Mac 系统代理 adapter 尚未实现；不将 unsupported 当成功。

## 升级

旧 SDK 和 CLI 的升级须保留原安装和代理恢复记录；本开发分支未验收原生 wheel 升级，不提供自动覆盖流程。需要停止现有认证实例时先使用原 SDK/CLI 的受控恢复路径：

```powershell
python -c "from zongheng_vpn import Client; Client().disconnect()"
```

## Steelman 开发协议（2026-10-07）

新版 SDK 登录调用 `login --token-stdin --json`，授权码只进入子进程标准输入，不进入 argv；输入上限 4096 UTF-8 字节，异常中的输出与私密字段脱敏。此消费者要求本开发版本 CLI 支持该参数；旧 CLI 拒绝时不自动回退把 token 放回进程参数。系统代理命令消费 typed lease receipt，自动选代理须 ready/可达且无诊断；本地 ready 不证明 WireGuard/出口业务成功。

`build.ps1` 默认 release：必须干净源码、真实签名条件和统一行为/安全门禁，完整 SHA 与 source state 写进 sidecar；开发需显式 `-Development`。已有 bundled CLI 不覆盖，GOOS/GOARCH/CGO 环境构建后恢复。原生 bundle 的 wheel 平台/签名验收仍待实现，目前脚本 `-Wheel` 与 setuptools 的直接 wheel 命令均拒绝，不能由另一入口绕过。

自动发现 bundled CLI 要求 `build-manifest.json`：匹配 Windows 宿主架构、实际 PE machine、CLI/control/sidecar 协议、完整源码 SHA、source state 与 artifact SHA256；缺失、损坏、错平台和未知身份均拒绝，不回落 PATH。未签名 development bundle 还需进程环境显式 `ZHVPN_ALLOW_DEVELOPMENT_BUNDLE=1`。这些检查建立构建对应关系，不等于可信发布者签名或更新链验收；正式 wheel 分发仍关闭。显式 `exe_path`、`command` 或 `ZHVPN_EXE` 由调用者选择可信工具。
