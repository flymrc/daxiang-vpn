# 2026-06-10 Pixel 7a Android 出口初始接入

## 背景

用 Google Pixel 7a 替换原 Motorola Android 出口,验证是否能改善 Rakuten 手机卡出口速度和稳定性。

## 执行

- Pixel 7a 已开启 OEM unlock,执行 `fastboot flashing unlock` 解锁 bootloader。
- 当前系统为 `google/lynx/lynx:16/BP3A.251105.015/14339231:user/release-keys`。
- 从 Google factory image `lynx-bp3a.251105.015-factory-32237bad.zip` 提取 `init_boot.img`。
  - factory zip SHA-256: `32237BAD0C65D7D731A3DB2906A365E5EDC2391220CF3CD6A2B65C2AE7B2E7B9`
  - `init_boot.img` SHA-256: `3CB48759CDE478CC03C192EAFA964B086F6032F625D99E886EAAD528990E8236`
- 安装 Magisk v30.7,用 Magisk patch `init_boot.img`,刷入当前 slot `_b` 的 `init_boot_b`。
- ADB root 验证通过:
  - `su -c id` -> `uid=0(root) gid=0(root) groups=0(root) context=u:r:magisk:s0`
- 部署 `zhreverse-linux-arm64` 到 `/data/adb/zhreverse/bin/zhreverse`。
- 复制 Hub `/etc/zongheng/zhreverse/token` 到 Pixel `/data/adb/zhreverse/token`。
- 部署 `/data/adb/zhreverse/client.yaml` 与 `/data/adb/service.d/99-zhreverse-egress.sh`。
- 手动拉起 `99-zhreverse-egress.sh` supervisor。

## 当前配置

- Hub 保持不变:
  - `/etc/zongheng/zhreverse/server.yaml`
  - `transport: tcp`
  - `resolve: server`
  - proxy: `10.66.0.1:18081`
  - listen: `0.0.0.0:39093`
- Pixel:
  - `transport: tcp`
  - `address_family: auto`
  - `connections: 1`
  - default route: `rmnet1`
  - Wi-Fi disabled by service script
- Pixel 控制面 WireGuard / `zhandroid-control` 尚未迁移,因此 `10.66.0.101:2022` 暂时不可作为 Pixel 控制面使用。

## 验证结果

- Pixel 可以主动连接 Hub reverse TCP:
  - Hub 日志出现 `reverse tcp client connected from 133.106.34.62:*`
- Hub 侧 proxy 小目标可用:
  - `https://ifconfig.me/ip` -> HTTP 200,出口 IP `133.106.34.62`
  - `https://www.google.com/generate_204` -> HTTP 204
- 2MB Cloudflare 下载 5 轮仅 1 轮成功:
  - 成功样本: `3.73 Mbps`
  - 失败样本多为 `OpenSSL SSL_connect: SSL_ERROR_SYSCALL`
- 10MB OVH 下载失败:
  - TLS 阶段 `SSL_ERROR_SYSCALL`
- 当前蜂窝状态:
  - Rakuten LTE Band 3,EARFCN `1500`,bandwidth `20MHz`
  - RSRP 约 `-90`,RSRQ 约 `-13`,RSSNR 约 `-1`
  - `isUsingCarrierAggregation=true`

## 结论

Pixel 7a 数据面可以接入 `zhreverse`,但在当前位置/当前 Rakuten 小区下,HTTPS 大下载仍明显不稳定,尚未看到相对 Motorola 的确定改善。`connections: 2` 在 Pixel 上第二条 session 多次 `i/o timeout`,当前保留 `connections: 1` 与历史最稳组合一致。

后续若继续推进 Pixel 替换,需要补齐 WireGuard 控制面和 `zhandroid-control` 自愈,再做换位置/换小区/换 SIM 的对比测速。
