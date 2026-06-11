# 2026-06-10 Windows GUI 全局模式无效调查

## 现象

客户反馈 Windows GUI 打开「全局模式 / 全局代理」后没有作用。

## 调查结论

当前 GUI 的「全局模式」并不真正实现全局互联网流量接管。

证据：

- GUI 复选框文案：`clients/desktop-gui/src/routes/+page.svelte` 标为“全局模式（系统 TUN，启动时弹一次管理员授权）”。
- GUI 调用：`clients/desktop-gui/src-tauri/src/lib.rs` 中 `connect_impl(fast=true)` 只调用 sidecar `zhvpn start --fast`。
- 同一函数明确在 `fast` 模式下跳过 Windows 系统代理设置：`if ok && !fast { sysproxy::enable(...) }`。
- CLI `start --fast` 只把 `systemTUN=true` 传给 `shared/proxy.WriteSingBoxConfig` / `proxy.Start`。
- `shared/proxy/singbox.go` 生成的 WireGuard endpoint `allowed_ips` 实际是 `10.66.0.0/24`，只覆盖 Hub/内网段，不是 `0.0.0.0/0` / `::/0`。
- sing-box 配置仍只有本地 `mixed` inbound + HTTP outbound，经 WireGuard detour 到出口代理；没有全局 TUN inbound / auto_route / DNS hijack 类配置。

因此：

- 默认模式：启动本地代理，并由 GUI 设置 Windows 系统代理，浏览器等遵守系统代理的应用会生效。
- 当前 fast/“全局模式”：启动提权引擎，让 WireGuard endpoint 使用系统栈并路由 `10.66.0.0/24`，但没有设置系统代理，也没有默认路由接管，所以普通应用直连公网仍不会走 VPN。

## 根因

产品文案/文档把 `--fast` 描述成“全局 TUN”，但实现上它只是“系统 WireGuard endpoint + 内网段可达 + 本地代理仍存在”。GUI 在 fast 模式下又刻意不设置系统代理，导致用户感知为“开了全局但没效果”。

## 后续选项

1. 快速止血：隐藏/禁用 GUI 的「全局模式」开关，改文案为“高性能/实验模式”，默认继续用系统代理模式交付。
2. 短期修复：fast 模式下也设置 Windows 系统代理，让浏览器先可用；但这仍不是全局 TUN。
3. 真正实现全局：重做 sing-box 配置，增加 tun inbound、auto_route、strict_route、DNS 处理，并验证 Windows UAC、路由恢复、断开清理、IPv6/DNS 泄漏。

## 验证命令

- `git grep` / `read_file` 检查 GUI 与 CLI 调用链。
- `go test ./...` 通过。
- `npm run check` 通过：0 errors，1 warning（既有 `@types/node` 缺失）。
