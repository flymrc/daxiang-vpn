# 2026-06-11 zhreverse v4-only 目标 Hub 直拨兜底

## 背景

- 用户报告:全局代理已连,手机直接测速不慢,但经代理上网很慢,`https://www.skymark.co.jp/ja/` 直接打不开,Ookla 测速间歇报 socket error。
- 本机经 `127.0.0.1:7890` 复现:
  - `api64.ipify.org`(双栈)0.9s 返回 200,出口手机 IPv6 正常。
  - `api.ipify.org`(v4-only)3/3 失败,每次精确 ~15.3s TLS 握手死亡。
  - `www.skymark.co.jp`(Akamai,无 AAAA)非确定性,2 次里 1 次 15.5s 死、1 次 0.6s 成功。
  - `speed.cloudflare.com` 20MB(双栈)8.1 Mbps 跑完,带宽路径本身通。
- 「15s 整、TLS ClientHello 后等不到 ServerHello」是 [2026-06-10 速度审计](2026-06-10-pixel-7a-speed-audit.md) 抓包确认的乐天 F5 BIG-IP `Policy action` RST 签名。
- 6-10 起 `resolve: client` + 手机 `address_family: ipv6` 让双栈目标走健康的 v6 侧;但 **v4-only 目标没有 v6 可走,只能过 F5 的 v4 高故障侧**——当天验证时 v4-only 还 3/3 成功,今天 0/3,说明 F5 v4 侧故障率时变,今天特别糟。

## 症状对应

- skymark 打不开:v4-only 站,主文档请求就死在 F5,浏览器白屏。
- 测速报错:Ookla `*.ooklaserver.net` 节点多数 v4-only(ZeroOmega 里失败 43 次);上传是持续 v4 长流最易被掐。
- 整体慢:页面里大量 v4-only 广告/统计域名各挂 15s 才死,既拖渲染又占 Hub 单客户端 48 并发槽。

## 改动:`egress/reverse` Hub server 加 `v4_only_direct`

- 新增配置 `v4_only_direct` 和 `--v4-only-direct` 参数(默认关)。
- `handleProxy` 进入后先判 `shouldDialDirect`:
  - IPv4 字面量目标 → 直拨。
  - 主机名 → Hub 侧查 AAAA;无 AAAA 视为 v4-only → 直拨;有 AAAA → 照旧转发手机。
  - AAAA 查询结果带 10min TTL 缓存(`v4DirectCache`,上限 4096 条满则清空);解析失败不缓存,按双栈交手机侧解析兜底。
- 直拨路径 `handleProxyDirect`:Hub 本机 `net.Dialer` 拨 `tcp4`,复用 `pipeBoth` + `proxy_idle_timeout` 做双向转发和空闲回收。
- 代价:v4-only 目标出口 IP 变成 Hub VPS `36.50.84.68`(机房 IP),不再是手机住宅 IP;双栈目标不受影响仍走手机 IPv6。
- 示例配置 [hub-reverse-server.yaml.example](../../20-operations/configs/egress/hub-reverse-server.yaml.example) 加 `v4_only_direct: true`。

## 测试

- `go vet ./egress/reverse`、`go test ./egress/reverse ./hub/... ./clients/cli/... ./shared/config/...`:通过。
- 新增 `TestShouldDialDirect`(IPv4 字面量直拨、IPv6 字面量走手机、缓存命中 v4-only/双栈、畸形 authority、开关关闭),并在 example 测试断言 `V4OnlyDirect` 已开。

## 生产部署

- 交叉编译 `dist/reverse/zhreverse-linux-amd64`(GOOS=linux GOARCH=amd64 CGO_ENABLED=0)。
- Hub 二进制备份 `/opt/zongheng/zhreverse/zhreverse.bak.20260611-v4only-direct`,新二进制部署到 `/opt/zongheng/zhreverse/zhreverse`。
- 配置备份 `/etc/zongheng/zhreverse/server.yaml.bak-20260611-v4only`,追加 `v4_only_direct: true`。
- `systemctl restart zhreverse-hub.service`,启动日志确认 `v4_only_direct=true`,隧道自动重连(手机 `133.106.34.62`)。

## 部署后验证

Hub 经 `10.66.0.1:18081`:

| 测试 | 结果 |
| --- | --- |
| `api.ipify.org`(v4-only) | 200,出口 `36.50.84.68`(Hub VPS 直拨) |
| `api64.ipify.org`(双栈) | 200,出口 `240b:c010:421:d18c:0:42:e654:1701`(手机 v6,未变) |
| `www.skymark.co.jp/ja/` x3 | 全 200,0.10-0.13s,36779 字节 |

本机经 `127.0.0.1:7890`:

| 测试 | 结果 |
| --- | --- |
| skymark x3 | 全 200,~0.15s |
| `api.ipify.org` x3 | 全 200,出口 `36.50.84.68` |
| `api64.ipify.org` | 200,出口手机 v6 |

skymark 从「15s 后白屏」变成「0.15s 打开」。

## 回滚

- 二进制 / 配置均有 `.bak*-20260611-v4only*` 备份。
- 配置删 `v4_only_direct: true`(或设 false)即恢复全部走手机出口。

## 后续

- v4-only 出口 IP 是机房 IP,部分风控站点可能识别;主流大流量站点基本都有 v6 不受影响。
- 终态仍是 worklog 6-10 建议的 QUIC over v6 隧道 + Hub VPS 启用 IPv6,让隧道腿也上 v6。
