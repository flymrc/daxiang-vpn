# 2026-06-14 Android 出口 IPv6 路径澄清

## 背景

用户询问:Hub 服务商不支持 IPv6,为什么客户端经 Android 出口访问双栈目标仍能因 IPv6 变快。

## 结论

当前有两条不同链路:

- 隧道腿:Android `zhreverse client` 主动连 Hub `36.50.84.68:39093`,当前是 IPv4 TCP/yamux。
- 目标腿:Hub 侧 `10.66.0.1:18081` 收到 CONNECT 后,因 `resolve: client` 把域名交给 Android;Android 侧按 `address_family: ipv6` 解析/拨号目标网站,双栈目标会优先走手机 Rakuten IPv6。

因此 Hub 不支持 IPv6 只限制“隧道腿不能走 IPv6”,不妨碍“目标腿从手机到网站走 IPv6”。这能绕开 Rakuten IPv4/F5 侧对目标站的高故障路径,但不能解决 Android 到 Hub 的 IPv4/CGNAT 长连接抖动。

## 文档修正

- 修正 [hub-reverse-server.yaml.example](../../20-operations/configs/egress/hub-reverse-server.yaml.example) 中监听口注释:当前生产是 TCP/yamux,不是 UDP listener。
