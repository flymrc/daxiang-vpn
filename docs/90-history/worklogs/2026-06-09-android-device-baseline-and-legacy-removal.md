# 2026-06-09 Android 当前设备底层优化与旧数据面拆除

## 背景

当前只针对现有 rooted Android 出口机做底层优化,不讨论换设备/多出口。目标顺序:

1. 锁定生产数据面走手机卡蜂窝网络。
2. 保持 Android 唤醒,减少 Doze/后台策略影响。
3. 尝试放大 Android UDP/socket buffer,减少 zhreverse 回传侧额外损耗。
4. 保留 WireGuard App 作为控制面,所有 Android 出口生产数据面只走 `zhreverse`。
5. 拆掉旧 `zhandroid-egress` / `10.66.0.101:1080` Android 入口。

## 代码改动

- [egress/reverse/service.d/99-zhreverse-egress.sh](../../../egress/reverse/service.d/99-zhreverse-egress.sh)
  - 启动前执行网络基线:
    - `settings put global stay_on_while_plugged_in 3`
    - `dumpsys deviceidle disable`
    - 默认 `svc wifi disable`
    - 尝试设置:
      - `net.core.rmem_max=8388608`
      - `net.core.wmem_max=8388608`
      - `net.ipv4.udp_rmem_min=65536`
      - `net.ipv4.udp_wmem_min=65536`
  - 记录 `ip route get 1.1.1.1`,若默认路由仍是 `wlan*` 则写 WARN。
  - 自动禁用 `/data/adb/service.d/99-zhandroid-egress.sh`,并停止旧 `zhandroid-egress` 进程。
- [egress/android-control/watchdog.sh](../../../egress/android-control/watchdog.sh)
  - 每 300 秒重放一次网络基线。
  - 持续禁用旧 `zhandroid-egress` service 和进程。
  - 继续保证 WireGuard 控制面、`zhandroid-control` 和 `zhreverse` supervisor 存活。
- [scripts/check-android-shell-baseline.ps1](../../../scripts/check-android-shell-baseline.ps1)
  - 在 Windows 本地检查 Android shell 脚本的关键基线逻辑与基础结构配对。

## 旧入口拆除

- 删除旧 Android egress 配置示例:
  - `docs/20-operations/configs/egress/android-egress-01.yaml.example`
- 更新 Android 实现文档,生产入口只保留 `egress/reverse`。
- 更新运维手册:若看到 `zhandroid-egress` 进程,视为旧服务残留/异常。
- `egress/proxy` 保留为 Mac/PC 出口预留代码,不再列 Android 构建目标。
- `egress/proxy` 内部帮助文本和注释改为中性 `zhegress-proxy`,避免继续暴露旧 Android 程序名。

## 验证

```powershell
powershell -ExecutionPolicy Bypass -File scripts/check-android-shell-baseline.ps1
git diff --check
go test ./...
```

结果:通过。

本 Windows 环境只有 WSL `bash.exe`,执行 `bash -n` 会挂住超时;本次未能在本机完成 shell `-n` 语法检查。已用 PowerShell 静态检查脚本覆盖关键基线,并人工检查两个 `/system/bin/sh` 脚本。

## 部署注意

部署到 Android 后检查:

```sh
ps -A -o PID,PPID,ARGS | grep -E 'zhreverse|zhandroid-egress|99-zh'
ip route get 1.1.1.1
tail -80 /data/local/tmp/zhreverse-egress.log
tail -80 /data/local/tmp/zhandroid-control.log
```

正常状态:

- 只应看到 `99-zhreverse-egress.sh` 和 `zhreverse client`。
- 不应看到 `zhandroid-egress`。
- 默认公网路由应走 `rmnet_data*`,不是 `wlan0`。
