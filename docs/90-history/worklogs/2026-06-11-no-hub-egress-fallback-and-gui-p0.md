# 2026-06-11 Hub 不再作为出口兜底 + GUI P0 修正

## 背景

用户明确产品边界:Hub 就是 Hub,不能承担兜底代理/兜底出口功能。若 Rakuten/手机 IPv4 路径不好,客户端应如实显示 IPv4 不可用或异常;不能把 Hub VPS `36.50.84.68` 展示成“出口 IPv4”。

## 改动

- `egress/reverse`:废弃并忽略 `v4_only_direct`;移除 Hub 侧 v4-only 直拨路径。即使旧配置仍写 `v4_only_direct: true`,新版服务端也不会把 v4-only 目标改由 Hub 直拨。
- `clients/cli`:公网 IP 探测结果若等于 Hub endpoint IP,不再作为 `egress_ipv4` 返回。
- `clients/desktop-gui`:版本提升到 `0.4.5`;IPv4/IPv6 分开展示真实可用性,IPv4 探测完成但无手机 IPv4 时显示 `不可用`。
- `clients/desktop-gui`:新增“复制诊断”基础版,包含 GUI 版本、连接状态、当前 status JSON、最近一次 IPv4/IPv6 探测结果与时间。
- 示例配置:Hub reverse server example 中 `v4_only_direct: false`,并标记为 deprecated/ignored。
- 文档:更新 README、架构、server-access、diagnostics、desktop-gui 与 GUI TODO,明确 Hub 不作为出口兜底。

## 验证

- `gofmt`:已执行。
- `go test ./egress/reverse ./clients/cli/... ./shared/config/...`:通过。
- `npm run check`:0 errors,1 warning(`tsconfig.json` 缺少 `node` 类型定义,既有 warning)。
- `cargo fmt --manifest-path clients\desktop-gui\src-tauri\Cargo.toml --check`:通过。
- `cargo check --manifest-path clients\desktop-gui\src-tauri\Cargo.toml`:通过,识别 `zhvpn-desktop v0.4.5`。
- `.\clients\desktop-gui\build.ps1 -Target amd64`:通过,产物:
  - `clients/desktop-gui/src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/纵横 VPN_0.4.5_x64-setup.exe`

构建时 `npm install` 报告 3 个 low severity vulnerabilities,未在本次修正中处理。

## 生产部署

- 使用本地 `~/.ssh/daxiang_server` 连接 Hub `root@36.50.84.68`。
- 构建 Linux amd64:
  - `GOOS=linux GOARCH=amd64 go build -o build/zhreverse-linux-amd64-no-hub-fallback ./egress/reverse`
  - SHA256 `697dc9bad01851c87819a0e209ba7d9178f134709ecb12a64331a1ba754fa335`
- 上传到 `/tmp/zhreverse-no-hub-fallback`。
- 备份旧二进制:
  - `/opt/zongheng/zhreverse/zhreverse.bak-20260611091353-no-hub-fallback`
- 安装到 `/opt/zongheng/zhreverse/zhreverse`,重启 `zhreverse-hub.service`。

## 生产验证

- `zhreverse-hub.service`:active/running,新 PID `156876`。
- 新启动日志:
  - `v4_only_direct is deprecated and ignored: Hub must not act as an egress fallback`
  - `reverse server listening transport=tcp resolve=client tunnel=0.0.0.0:39093 proxy=10.66.0.1:18081 max_proxy_connections=96 max_proxy_connections_per_client=48 proxy_idle_timeout=2m0s`
  - `reverse tcp client connected from 210.157.193.234:62923`
- 经代理 IPv6:
  - `curl --proxy http://10.66.0.1:18081 https://api64.ipify.org` -> `240b:c010:662:d7b7:0:44:f8bf:7901`
- 经代理 v4-only:
  - `curl --proxy http://10.66.0.1:18081 https://api.ipify.org` -> exit `28` 超时,没有返回 Hub IP。
- Hub 直连公网:
  - `curl https://api.ipify.org` -> `36.50.84.68`

结论:线上 Hub 已切到 no-Hub-fallback 语义。IPv4-only 路径若手机侧失败,表现为失败/超时,不再由 Hub VPS 出口。
