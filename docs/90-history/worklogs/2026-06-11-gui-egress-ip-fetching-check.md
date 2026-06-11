# 2026-06-11 GUI 出口 IP「获取中」排查

## 背景

用户反馈 IPv6 支持后速度明显变快,但 Windows GUI 的「出口 IP」经常显示「获取中...」,需要确认是 UI 展示问题,还是代理链路间歇性短路。

## 排查

- 代码链路:
  - `clients/desktop-gui/src/routes/+page.svelte` 在 `status.egress_ip` 为空且已连接时显示「获取中...」。
  - GUI 每 5 秒调用 Tauri `status`,后端再执行 sidecar `zhvpn status --json`。
  - `zhvpn status --json` 仅在本地代理端口可达时,通过 `netcheck.PublicIPViaHTTPProxy` 依次请求:
    - `https://api64.ipify.org`
    - `https://ifconfig.me/ip`
    - `https://api.ipify.org`
- 已安装包检查:
  - `zhvpn-desktop.exe` 为 0.4.3。
  - 已安装 `zhvpn.exe` SHA256 与仓库 `clients/desktop-gui/src-tauri/binaries/zhvpn-x86_64-pc-windows-msvc.exe` 一致。
- 本机启动本地代理后验证:
  - 20 次 `zhvpn status --json` 全部返回 `egress_ip`。
  - 60 次长采样约 3 分 40 秒,`missing_ip=0`,`slow_gt4s=0`,`avg=1.66s`,`max=3.15s`。
  - 本地 `127.0.0.1:7890` TCP 探测持续可达。
  - 拆分 endpoint 测试中:
    - `api64.ipify.org` 返回手机 IPv6 `240b:c010:662:d7b7:0:44:f8bf:7901`。
    - `ifconfig.me/ip` 返回同一手机 IPv6。
    - `api.ipify.org` 返回 Hub VPS `36.50.84.68`,符合 `v4_only_direct` 预期。
- 生产 Hub SSH 快检未执行成功:当前本机对 `root@36.50.84.68` 无可用 SSH 凭据。

## 结论

本次本机复测未复现代理链路间歇性短路,也未复现 `egress_ip` 长时间缺失。当前证据更倾向于 GUI 状态显示/探测时序问题:

- 连接刚启动、换 IP、或本地代理短暂不可达时,`status.egress_ip` 为空会被前端显示为「获取中...」。
- 若之前没有缓存到旧出口 IP,界面会直接显示「获取中...」,但这不等价于数据面断路。
- v4-only 探测返回 Hub VPS 是 `v4_only_direct` 的设计结果,不是 IPv6 手机出口失效。

## 后续建议

- GUI 可保留上一轮有效 `egress_ip`,仅在探测失败时显示旧 IP + 刷新状态,避免把短暂探测空窗误读为断路。
- 若用户再次看到长时间「获取中...」,同时运行:
  - `zhvpn.exe status --json`
  - `curl.exe -sS --max-time 8 -x http://127.0.0.1:7890 https://api64.ipify.org`
  - `Test-NetConnection 127.0.0.1 -Port 7890`
  用三者区分 UI、出口 IP endpoint、和本地代理端口。

## 后续更正

后续产品边界明确:Hub 不应作为出口兜底。本文中把 `api.ipify.org -> 36.50.84.68` 视为当时 `v4_only_direct` 预期,但该策略已废弃。新版 `zhreverse` 忽略 `v4_only_direct`,IPv4-only 目标仍应走手机出口;若 Rakuten IPv4 路径故障,GUI 应显示 IPv4 不可用。
