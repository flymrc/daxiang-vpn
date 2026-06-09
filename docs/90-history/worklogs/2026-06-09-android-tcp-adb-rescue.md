# 2026-06-09 Android 受控 TCP ADB 救援入口

## 背景

USB ADB 拔掉后,需要保留一个远程救援入口。此前确认 `10.66.0.101:5555` 未监听,`service.adb.tcp.port` 和 `persist.adb.tcp.port` 均为空,没有留下远程 ADB 后门。

## 方案

新增 [97-dxadb-tcp-wg-only.sh](../../../egress/android-control/service.d/97-dxadb-tcp-wg-only.sh):

- 开启 `adbd` TCP 监听 `5555`。
- 通过 iptables 只允许 `tun0` 上的 `10.66.0.0/24` 访问。
- 非 WireGuard 接口和 IPv6 的 `5555/tcp` 直接 drop。
- 不设置 `persist.adb.tcp.port`;持久性由 Magisk `service.d` 脚本负责。
- ADB 仍使用 Android host key 授权,不是免认证入口。

## 使用方式

从本机经 Hub 转发:

```powershell
ssh -L 127.0.0.1:15555:10.66.0.101:5555 root@36.50.84.68
adb connect 127.0.0.1:15555
adb -s 127.0.0.1:15555 shell id
```

Hub 侧可直接检查:

```bash
nc -zv 10.66.0.101 5555
ssh -i /root/.ssh/dxandroid_control_hub -p 2022 root@10.66.0.101 \
  'getprop service.adb.tcp.port; ss -lntup | grep 5555; iptables -S DXADB_TCP'
```

## 验证

```powershell
powershell -ExecutionPolicy Bypass -File scripts/check-android-shell-baseline.ps1
```

部署后需确认:

- `service.adb.tcp.port=5555`。
- `persist.adb.tcp.port` 为空。
- `10.66.0.101:5555` 只经 WireGuard 可达。
- `iptables -S DXADB_TCP` 包含允许 `tun0/10.66.0.0/24` 和默认 drop。
