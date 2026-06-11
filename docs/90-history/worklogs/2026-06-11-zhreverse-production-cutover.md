# 2026-06-11 zhreverse 线上切换

## 背景

仓库已完成品牌与内部标识切换为 Zongheng / 纵横,但线上 Hub 和 Pixel 7a 仍在运行旧 `dxreverse` 路径。用户要求将线上全部切到新命名后再测试。

## 执行

- 本地确认 `origin/main` 最新提交为 `bc8363a Rename project branding to Zongheng VPN`。
- 构建并测试 `zhreverse`:
  - `go test ./egress/reverse` 通过。
  - `GOOS=linux GOARCH=amd64` 构建 Hub 二进制。
  - `GOOS=linux GOARCH=arm64` 构建 Pixel 二进制。
- Hub:
  - 安装 `/opt/zongheng/zhreverse/zhreverse`。
  - 创建 `/etc/zongheng/zhreverse/`,从旧 `/etc/daxiang/dxreverse/` 复制 token、证书和配置,配置内路径替换为 `/etc/zongheng/zhreverse`。
  - 安装并启用 `/etc/systemd/system/zhreverse-hub.service`。
  - 停用 `dxreverse-hub.service`。
- Pixel 7a:
  - 安装 `/data/adb/zhreverse/bin/zhreverse`。
  - 从旧 `/data/adb/dxreverse/` 复制 token 和 client 配置,配置内路径替换为 `/data/adb/zhreverse`。
  - 安装 `/data/adb/service.d/99-zhreverse-egress.sh`。
  - 停止旧 `99-dxreverse-egress.sh` / `dxreverse client`,并将旧 service.d 脚本改名为 `99-dxreverse-egress.sh.disabled`。
  - 手动拉起 `99-zhreverse-egress.sh` supervisor。

## 验证

- Hub:
  - `zhreverse-hub.service` active/enabled。
  - `dxreverse-hub.service` inactive。
  - `10.66.0.1:18081` 与 `0.0.0.0:39093` 由 `zhreverse` 监听。
  - Hub 日志显示 `reverse tcp client connected from 133.106.34.62:*`。
- Pixel:
  - 进程仅剩 `99-zhreverse-egress.sh` supervisor 和 `zhreverse client --config /data/adb/zhreverse/client.yaml`。
  - `99-dxreverse-egress.sh` 已 disabled。
- 出口:
  - `api64.ipify.org` / `ifconfig.me` / `ident.me` / Cloudflare trace 均返回 IPv6 `240b:c010:421:d18c:0:42:e654:1701`。
  - Cloudflare trace 显示 `loc=JP`, `colo=NRT`。
  - `measure-android-egress.ps1 -Runs 2 -Url https://speed.cloudflare.com/__down?bytes=1000000`:
    - run 1: `400637 B/s`,约 `3.21 Mbps`。
    - run 2: `343943 B/s`,约 `2.75 Mbps`。
    - 平均约 `2.98 Mbps`。
- 健康脚本:
  - `check-android-egress-health.ps1 -HubIdentityFile ~/.ssh/daxiang_server` 通过 proxy、Hub route MTU、TCPMSS 检查。
  - WireGuard 控制面仍提示 stale,符合 Pixel 控制面尚未迁移现状。

## 后续

- Pixel 控制面 WireGuard / `zhandroid-control` 仍未迁移,`10.66.0.101:2022` 暂不可作为 Pixel 控制面使用。
- 当前 Pixel 生产 `connections: 1`,保持与 2026-06-10 稳定性结论一致。
