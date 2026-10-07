# 2026-10-07 设备凭据到真实 WG 代理链

第五波基于第四波 `a2ecd68` 与文档 `0283143`，仅在既有隔离工作树实施。没有推送、部署、WG0/系统代理修改或 Jetstar 流程。专项合同见[设备启动](../../30-implementation/v2-proxy-bootstrap.md)，原清单见[第五波 checkbox](../../30-implementation/v2-proxy-integration-plan.md)。

## 实现

canonical OpenAPI 新增 `proxy.bootstrap` purpose/request/response，确定性生成 DTO。默认 OFF 的可信 profile 绑定 authority epoch、managed-by、独立 customer interface、WG endpoint/public key、proxy `/32`、revision/digest；Linux受保护来源读取不修改权限。Store 在同authority事务内重验credential、最新公钥/地址、generation/convergence/outbox/负面历史与nonce，返回最长30s正常TLS投影，不输出私钥或token。

客户端显式bind先持久化独立clamped X25519私钥再提交apply；未知结果沿原intent/receipt恢复，缺key或不匹配不能自动换key。PrepareStartLocked只返回经过严格TLS/identity/profile/epoch/revision/期限验证的Config；根在同home锁内生成exact-route配置并启动真实engine，禁止已有engine接管与legacy降级。生成配置与整投影一起绑定engine generation；public StartContext还比对canonical完整bytes、在ready答复后重验取消并撤回本次exact-child。

新增独立start contract2 DTO/decoder/schema和正式漂移检查。旧generic receipt保持原条件，新增start仅early setup失败；generic schema检查覆盖字段sets/version/command/code枚举，未声称全部旧条件自动生成。

profile-enabled Service.Run在TLS监听前同步完成≤30s initial Tick，失败固定脱敏错误；profileOFF原顺序保留。实际代码没有建立整个外部WG数据面的启动封锁，见下文反例。

## 分层验证

| 证据 | 结果 | 范围 |
| --- | --- | --- |
| Hub/shared contract新模型 | Windows四包race全部PASS；WSL同四包native全部PASS；generator/vet/diff-check exit0 | 新purpose、profile严格JSON/摘要/公钥、14authority事实子例；Linuxprotected reader/FIFO/symlink/hardlink/权限/坏来源不初始化DB |
| deviceclient全包 | Windows race 6.219s；WSL native 2.011s；最终两schema test复验1.940s/0.012s | 29/28顶层，真实TLS/私有文件、lost-bind两种同key恢复、3写失败时序、17投影负例、strictflags与CA输入；未冒称这些mock响应已有WG |
| actual WG/CLI正式三组 | Windows race runner PASS12.397s；WSL actual WG/TLS两组PASS8.231s，Linux产品CLI明确skip | normal CGO0 Windows CLI/TLS/SQLite/sing-box/realUDP WG/proxy/target marker；handshake/rx/tx对应key；revoke新请求失效、protected仍通、迟到apply及Store/runtime冷启动不复活 |
| profile-enabled service正式三组 | Windows race PASS2.183s；WSL native PASS0.861s | 实际TCP不监听barrier、失败executor、真实旧WG source initial cleanup完成后firstHTTPS、OFF顺序正对照 |
| root config/proxy/app正式 | Windows race全部PASS | 路由/TTL31/identity/key/私有cache、canonicalbytes mismatch、过期发布/激活与generation绑定、严格早期单JSON |
| 独立native启动overlay | Windows race PASS6.205s；WSL native PASS3.284s | 实际owned __engine 子进程拒过期/metadata篡改/迟到activate；只是loopback control边界，不计作WG流量 |
| 独立Service部分失败overlay | Windows race PASS1.455s；WSL native PASS0.177s | 预取消、snapshot等待取消、一个旧peer已清除/第二snapshot失败→TLS始终不监听；第二旧peer仍能访问目标的真实负面证据 |
| 独立root cache/setup overlay | Windows race PASS1.174s | 3顶层+8投影负例；无私钥cache、legacy无canary网络/旧key、生成拒绝、目录冲突、actual app.Run早HOME脱敏；完整TLS/cache失败仅源码顺序复核 |
| 独立cancel/错误bytes修复后overlay | Windows race PASS2.582s | 实际native child已经HMAC ready，relay取消后StartContext拒绝并清ownedchild；direct-only bytes不能获得v2stamp；没有旧baseline执行证据 |

真实WG fixture只监听127.0.0.1 UDP，内部采用真实wireguard-go crypto/handshake/routing与gVisor stack；并非memory WG结果，也不是production reverse/mTLS/allowed_proxy_cidrs验收。网络、home、target、HTTP proxy及peer均本次owned。CLI executable正常CGO0构建，`go test -race`只证明相应测试runner/其依赖的instrumentation，不能说产品exe全部race执行。Linux无C编译器，没有Linux race证据。

## 未完成事实

独立反例证明TLS initial gate不是数据面fail-closed屏障：对账部分失败，旧的外部WG peer仍可能可达。需要落实实际部署WG/proxy的启动顺序/客户权限屏障、持续对账与会话撤销SLA，不能靠关闭API解决旧数据面。fixture成功执行Tick再发流量证明受控顺序，不能替代失败窗口的完整部署生命周期。

campaign固定分母/installation lineage与完整阻断投影、受控导入/实际恢复、系统代理Mac adapter/实机、签名安装维护/外部更新防回滚、日志容量与性能仍有源码或验收缺口。旧login/import可能改变liveengine的status cache标签是原P2余项，本波device start本身拒绝已有engine，并未把跨入口状态整体完成。

早setup的`device start`失败使用generic contract2 `Receipt/local_error`，不泄露HOME；有可用home的启动结果使用`StartReceipt/rejected|engine_state_unknown|engine_ready`。消费者应先识别该setup失败；engine_ready只说明本地engine，目标流量另验。启动TTL不等于持续lease；同owner整state回放未被本地存储阻断。

冻结 v6 `scripts/check-steelman.ps1` exit0。新 start schema/生成漂移、Go product tags 全包/vet/race、真实 WG/CLI/Service 六组（14.047s）、离线更新 CLI、SDK33/Rust41、builder9/16、NSIS13+完整模板、GUI/Admin零错误警告、Admin20消费者均通过。6 个 Go OS/arch 的 symbol/package 均0；module级未导入 OpenPGP 例外仍1且未扩大。两棵 npm（含dev）漏洞0，4个Rust目标漏洞0/未审例外0；现有例外到2026-11-06。

独立取消负例已提升正式测试，Windows race 2.432s/vet/diff exit0。端口预检查代码已接线，但没有独立占用/抢占场景的运行证据，留在第五波计划余项。

冻结源提交 `17e7689c82559d81303151b1e0116a6dfac7ee37` 后，`build-steelman-dev.ps1` 九目标 clean 开发构建 exit0；9 个产物 SHA256 全部与 manifest 相同。真实 Windows amd64 CLI `version --json` 返回该完整 SHA、clean、Go1.26.7、sidecar protocol2/CLI contract1。start contract2 独立于旧 CLI contract1。产物均 unsigned/compile_only，`release_ready=false`。原工作区79个保护文件与清册比较无新增hash漂移。

局部 PASS 不替代整体 Steelman、Mac/phone 实机、签名 release 或任何生产安全迁移门禁。原始日志 `unified-final-frozen-v6.log`、`security-final-frozen-v6/`、`build-wave5-final.log`、`development-wave5-final/` 和 fixture 在原 workspace 父目录 `.local/zongheng-vpn/steelman/2026-10-07/` 与专门私有review目录，未入Git。
