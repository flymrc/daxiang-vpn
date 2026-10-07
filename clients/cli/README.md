# zhvpn CLI

Windows / macOS 客户端 CLI。正式 CLI 签名、公证和发行验证尚未完成，构建入口默认拒绝 release；当前只生成显式未签名的开发产物。

## 构建

```powershell
# Windows：必须使用不存在的输出目录
.\build.ps1 -Development -Version dev -OutputDirectory C:\artifacts\zhvpn-cli-dev-unique
```

```bash
# macOS：需要 PowerShell 7；门禁与 Windows 使用同一入口
./build-macos.sh -Development -Version dev -OutputDirectory /tmp/zhvpn-cli-dev-unique
```

两个旧入口均委托 `scripts/build-cli.ps1`。它先执行 `check-steelman.ps1`，固定完整 Git SHA、干净/dirty 状态、源码 hash 清册与工具链，编译两种 CPU 架构，并恢复调用前的 Go 环境变量。输出目录中保留门禁证据、源码清册、每个产物的 SHA-256 和 `build-manifest.json`；源码在检查或编译期间变化、旧目录存在、任一门禁失败都会拒绝成功清单。开发产物始终标记 `signed=false`、`release_ready=false`、`compile_only`，交叉编译不代表对应 OS 实测。

针对构建拒绝路径的合成回归：

```bash
pwsh -NoProfile -File ../../scripts/test-cli-build.ps1
```

手动 `go build` 仅供本地开发，不能作为正式发布结果。实际 macOS 代理、权限、签名/公证与 Windows CLI 签名仍需要独立完成和验收。

## 使用

```powershell
# Windows
.\zhvpn.exe login ZH-DEV-TOKEN
.\zhvpn.exe start
.\zhvpn.exe status
.\zhvpn.exe rotate-ip
.\zhvpn.exe stop
```

```bash
# macOS
./zhvpn login ZH-DEV-TOKEN
./zhvpn start
./zhvpn status
./zhvpn rotate-ip
./zhvpn stop
```

机器接口：

```powershell
.\zhvpn.exe login ZH-DEV-TOKEN --json
.\zhvpn.exe start --json
.\zhvpn.exe status --json --no-ip-check
.\zhvpn.exe status --json
.\zhvpn.exe rotate-ip --json
.\zhvpn.exe stop --json
.\zhvpn.exe logout --json
.\zhvpn.exe version --json
```

`login` / `start` 会写入本地状态缓存，供 `status` 和桌面 GUI 高频轮询读取；`status` 不会重复请求 Hub bootstrap。`start` 仍会重新 bootstrap 获取最新运行配置。WireGuard 私钥由客户端本地生成并保存在 `ZHVPN_HOME/wireguard/client.key`，bootstrap 只上报公钥；状态缓存不持久化 WireGuard 私钥。

## Android 出口换 IP

CLI 可以让当前手机卡出口重注册并尝试更换公网出口 IP：

```powershell
.\zhvpn.exe rotate-ip
.\zhvpn.exe rotate-ip --down-seconds 12 --wait-seconds 90
```

`--wait-seconds` 是最大等待时间,CLI 会轮询到出口恢复或超时。

## 出口节点说明

Android root 出口节点生产数据面在 `egress/reverse`，安卓状态 App 在 `egress/android-status`。

相关文档：`docs/30-implementation/android-egress-agent.md`
