# 2026-06-11 zhreverse 手机侧 TLS 首飞看门狗 + 重连重放

## 背景

- 用户报告 `https://www.ip138.com/` 等站点经常打不开,怀疑 IPv4 问题。诊断确认:
  - `www.ip138.com` 无 AAAA(CNAME → 仅 A `14.128.1.31`),只能走手机 IPv4 出口。
  - 手机 v4 出口必经乐天 F5 BIG-IP,其 v4 侧按单条连接随机黑洞:TCP 握手成功、ClientHello 被 ACK,但零响应字节,约 15s 后 `Policy action` RST(机制见 [2026-06-10 速度审计](2026-06-10-pixel-7a-speed-audit.md))。
  - 故障率时变:当天上午 `api.ipify.org` 0/3 全挂,下午本机实测 ip138 5/5、api.ipify.org 3/3 成功(出口 `210.157.194.130`,手机乐天 v4)。「经常会断」即时变故障率的体现。
- Hub 直拨兜底当天已按产品边界废弃([Hub 不再作为出口兜底](2026-06-11-no-hub-egress-fallback-and-gui-p0.md)),修复必须留在手机侧、保住宅出口 IP。
- 关键观察:F5 v4 故障是**按连接**随机的(同时段 1/2、2/3 成功),换条新连接重发同样的首包通常就能成功 → 重试收益大。

## 改动:`egress/reverse` 手机侧 CONNECT 中继加「首字节看门狗 + 重连重放」

`handleConnectStream` 的 `pipeBoth(stream, targetConn, 0)` 替换为 `relayWithHandshakeRetry`:

- **布防条件**:首个客户端载荷是 TLS ClientHello(`0x16 0x03 ... 0x01`)。此时重放在协议上等价于客户端自己断开重连,安全。明文 HTTP 等非 TLS 首包不布防(重放可能重复执行副作用),行为与旧实现一致。
- **看门狗**:发出 ClientHello 后,目标在 `handshakeFirstByteTimeout`(3s)内零响应字节(超时或直接被 RST)→ 重拨目标(复用 `dialTarget`,含 DNS 与地址族偏好)并整体重放已缓冲的客户端字节。
- **额度**:总计最多 `handshakeMaxDials`(3)次拨号(初拨 + 2 次重拨)。额度用完**不掐连接**,清死线退回普通阻塞中继(最后一条连接重新获得无限等待,行为不差于旧实现)。
- **重放窗口永久关闭**的时机:目标回过第一个字节(`settle`),或客户端首飞超过 `handshakeReplayLimit`(16KB)。之后绝不重放。
- 并发安全:客户端→目标方向的写、目标连接换新+重放在同一把锁下串行,保证重放字节先于后续新字节到达;会话拆除(`closed`)后不再重拨。
- 重试路径打日志(`no server bytes ... redialing 2/3`、`target answered on dial 2/3`),便于在手机 logcat 观察 F5 v4 故障窗口。

预期效果:单次成功率 50-66% 的坏窗口下,3 次拨号把成功率推到 ~87-96%,且把「挂 15s 后白屏」变成「~3s 内重试成功」。中途断流(F5 流表丢失)不在本修复范围。

## 测试

- `gofmt`、`go vet ./egress/reverse`:通过。
- `go test ./egress/reverse ./hub/... ./clients/cli/... ./shared/...`:全部通过。
- 新增单测:
  - `TestLooksLikeTLSClientHello`:ClientHello 识别与负样本(明文 HTTP、ServerHello、短包、坏版本)。
  - `TestRelayHandshakeRetryReplaysClientHello`:首连黑洞 → 重拨 1 次,重放字节逐字节等于原 ClientHello,响应正常回传。
  - `TestRelayHandshakeNoRetryForNonTLS`:明文请求后目标断开,0 次重拨。
  - `TestRelayHandshakeNoRetryAfterServerBytes`:目标回 1 字节后断开,0 次重拨。
  - `TestRelayHandshakeFallsBackAfterMaxDials`:连续黑洞耗尽额度后退回阻塞中继,晚到的响应仍能送达;重拨次数恰为 `handshakeMaxDials-1`。
  - `TestRelayHandshakeReplayLimitDisablesRetry`:首飞超过 16KB 后放弃重试。

## 构建产物

- `dist/reverse/zhreverse-linux-arm64`(GOOS=linux GOARCH=arm64 CGO_ENABLED=0,手机端)
  - SHA256 `c831574156bb14e48166021fc646c0b6d0e650e8487e2c897c6f56cc664d31f5`
- 无配置变更:看门狗默认启用,无新增 yaml 字段;Hub 端协议不变,无需同步部署 Hub 二进制。

## 生产部署(同日完成)

- 路径:本机 scp → Hub `/tmp/zhreverse-tls-retry` → Hub 经控制面 SSH(`/root/.ssh/zhandroid_control_hub` → `10.66.0.101:2022`)以 `cat >` 流式推到手机 `/data/local/tmp/zhreverse.new`。两跳 SHA256 均核对一致。
- 手机安装:备份运行中二进制为 `/data/adb/zhreverse/bin/zhreverse.bak-20260611-tls-retry`,经 `.staged` + `mv` 替换(避免 text busy),`pkill` 后由 `99-zhreverse-egress.sh` 监督循环 5s 自动重启,隧道立即重连(新 PID 9393)。
- 踩坑复现:`pkill -f bin/zhreverse` 把执行命令的远程 shell 自己也匹配杀掉(模式在自身 cmdline 里),导致 SSH 会话挂死、后续 `echo` 不执行——但安装与 kill 本身已生效。下次用 `pkill zhreverse`(不带 `-f`)或先写脚本再执行。
- 部署时发现手机上有 `zhreverse.bak*-operator-info` / `zhreverse.failed-operator-info-*`:为当日 Hub 侧「动态运营商展示名」工作的零散残留,该方案 zhreverse 改动已失败回滚、产品决定数据面不为展示文案改协议(见 [win-gui-local-global-modes](2026-06-11-win-gui-local-global-modes.md)),被替换的运行中二进制即等价于 main 的回滚版,无功能丢失。

## 部署后验证

本机经 `127.0.0.1:7890`:

| 测试 | 结果 |
| --- | --- |
| `www.ip138.com` x5 | 全 302(正常重定向),0.68-1.18s |
| `api.ipify.org`(v4-only)x3 | 全 200,出口 `210.157.194.130`(手机乐天 v4,住宅 IP) |
| `api64.ipify.org`(双栈) | 200,出口 `240b:c010:652:cd2e:...`(手机 v6,未变) |

手机日志 `grep -c redialing` = 0:当前 F5 v4 侧处于健康窗口,看门狗未触发,符合预期。坏窗口出现时日志会有 `no server bytes ... redialing` / `target answered on dial` 配对,可作为 F5 故障窗口的观测信号。

## 回滚(看门狗二进制)

- 手机:`cp /data/adb/zhreverse/bin/zhreverse.bak-20260611-tls-retry /data/adb/zhreverse/bin/zhreverse.staged && chmod 700 ... && mv ... zhreverse && pkill zhreverse`(监督循环自动重拉)。
- 无配置依赖,Hub 端无需任何操作。

## 追加:隧道腿 `connections: 1 -> 2`(同日,看门狗部署后用户仍报 v4 不稳)

### 复诊:两条 IPv4 腿要分开看

看门狗部署后用户仍报「ipv4 还是不稳」。本机复测此刻 v4-only 反而全好(`api.ipify.org` 6/6、`ip138` 3/3,出口手机 v4 `210.157.193.73`),看门狗 `redialing` 日志 0 条——F5 v4 目标侧正处健康窗口,看门狗未触发。但日志暴露真正在抖的是**另一条 IPv4 腿**:

| IPv4 腿 | 路径 | 故障 | 看门狗 |
| --- | --- | --- | --- |
| 目标腿 | 手机→网站:443(过 F5) | 黑洞 15s RST | ✅ 已覆盖 |
| **隧道腿** | 手机→Hub:39093(裸 CGNAT,Hub 无 v6) | 整条 yamux 冻死/RST | ❌ 覆盖不到 |

Hub `journalctl` 证据:当天隧道 57 次掉线,反复成簇出现 `read reverse command response ... failed: i/o deadline reached`,紧跟 `yamux: keepalive failed: i/o deadline reached` → 整条隧道死亡重连(13:18、13:27 两波典型)。`connections: 1` 时这条单 yamux 会话一冻,**当时所有正在跑的代理连接同时全死**——即用户体感的「好好的一起断」。看门狗靠「在隧道上重拨目标」重试,隧道本身死了就无从重试,故对此故障形态无能为力。这正是 [2026-06-10 审计](2026-06-10-pixel-7a-speed-audit.md) P1 点名、一直没落地的韧性项。

### 改动

- 手机 `client.yaml`:`connections: 1 -> 2`(备份 `client.yaml.bak-20260611-conn2`),`pkill zhreverse` 由监督循环重拉。两条独立隧道会话,Hub 轮询;一条冻死另一条兜底,单次 RST 不再连坐全部连接。
- 示例配置 [android-reverse-client.yaml.example](../../20-operations/configs/egress/android-reverse-client.yaml.example):`connections: 2` 并加注释说明隧道腿故障。
- `main_test.go`:`TestLoadReverseConfigExamples` 断言 `connections == 2`。`go test ./egress/reverse` 通过。

### 部署后验证

- 手机日志两行 `connected to reverse tcp server 36.50.84.68:39093`;Hub 侧 `client connected from 210.157.193.73:17733` 与 `:6650` 两条会话。
- 本机经 `127.0.0.1:7890`:`ip138` 8/8 成功(0.30-0.75s)。

### 回滚(连接数)

- 手机:`cp /data/adb/zhreverse/client.yaml.bak-20260611-conn2 /data/adb/zhreverse/client.yaml && pkill zhreverse`。

### 后续

- 隧道腿的治本仍是审计 P0:给 Hub VPS 开 IPv6、隧道腿走 v6(或 QUIC over v6),绕开 CGNAT TCP 流表对长连接的威胁。`connections: 2` 只缩小单会话死亡的爆炸半径,若射频/CGNAT 同时冻死两条会话则收益有限。

## 追加 2:手机端 DNS 缓存 + 健康检查对齐 v6(同日,打磨计划 1/2 项)

### DNS 缓存(打磨计划第 1 项)

- `egress/reverse/main.go`:`dialTarget` 解析前查进程内缓存(`dnsCacheGet/dnsCachePut`,TTL 60s,上限 1024 条满则整体清空,解析失败不缓存)。缓存存原始 `ips`,`orderIPs` 仍在拨号时排序;FETCH 路径与看门狗重拨共用 `dialTarget` 自动受益(重拨免 DNS,更快)。
- 单测:`TestDNSCacheHitAndExpiry`(命中/过期/未知主机)、`TestDNSCacheCapacityReset`(超限整体清空)。注入 `now` 参数避免 sleep。
- `gofmt`/`go vet`/`go test ./egress/reverse`:通过。
- 部署:二进制 SHA256 `e622f386cd16beed759e9ae7b8e671d4a451bf50930713467fce2ea6b9aefb33`,两跳哈希核对一致;手机备份 `zhreverse.bak-20260611-dnscache`,经 `.staged`+`mv` 替换,`pkill zhreverse`(不带 `-f`,坑备忘生效)后 supervisor 重拉,新 PID 24963,两条隧道会话即刻重连。

### 健康检查对齐 v6(打磨计划第 2 项)

- `scripts/check-android-reverse-egress.sh` 重写:v6 主路径(`api6.ipify.org`)FAIL 级、`240b:` 标注 Rakuten;v4(`api.ipify.org`)失败仅 WARN,**但回 Hub IP `36.50.84.68` 判 hub-fallback 回归 FAIL**;保留 cloudflare 1MB 下载探针。v4 前缀不断言(实测 `133.106.x`/`210.157.x` 多段)。
- `scripts/check-android-egress-health.ps1` 同步:参数改 `EgressIPv6Urls`(api6 + api64 只认 v6 形态)+ `EgressIPv4Url` + `HubPublicIP`,探测块按同语义分级。
- `server-access.md`:`connections: 2`、双会话、v6/v4 探测口径与常用检查命令同步。

### 部署后验证

- Hub 执行新版 sh 脚本:`PASS v6 egress_ip=240b:c010:630:ea89:... (Rakuten)`、`PASS v4 egress_ip=210.157.193.73`、下载探针 200。
- 本机经 `127.0.0.1:7890`:ip138 6/6 成功。

### 回滚(DNS 缓存二进制)

- 手机:`zhreverse.bak-20260611-dnscache` 换回即可,流程同上。

### 明日待办(2026-06-12)

1. **Hub VPS 启用 IPv6**(打磨计划第 6 项,运维先查 Hostsymbol 面板拿分配,详细步骤已写在计划文档)。
2. **QUIC over UDP A/B**(打磨计划第 3 项,与 IPv6 并行,不依赖机房)。
