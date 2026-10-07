# 2026-10-07 客户端引擎日志与派发边界

第八波在隔离 `codex/zhvpn-steelman-runtime` 上基于第七波收据 `82f69c0` 继续实施。原工作区基线、无关RDP文件与生产保持保护，未推送/部署/改peer或入口，未跑Jetstar。

## 本波范围

[客户端日志清单](../../30-implementation/client-engine-log-plan.md)先补Windows后台错误丢失与Darwin无限原始APPEND。只写有限typed事件，不持久化依赖原始message或秘密；私有自有槽位、单writer和sticky日志health独立于进程ready与代理恢复。现有signed control v1不改，新增只读认证日志health与可选CLI状态合同。

并行处理第七波发现的 `ctx.Err → SetDeadline → command write` 和clear-deadline窗口：目标是在owned gated upstream上明确派发与关闭顺序，不持全局Gate锁做网络IO、不整体关闭含retained流的session。真正可验证的线性化语义与旧实现同步反例待实现后登记，不预先承诺零kernel在途bytes或yamux内部对象全部回收。

P6.4、持续撤销SLA、G01–G05和生产迁移均未因本片完成而整体通过。实现、实际验收和提交/构建分别登记。

## 实现与独立复核

新event v1维护源为typed Go struct/event-code-count规则，确定性logschemagen投影；默认check/test只读，不使用继承env静默改schema。CLI v1新增可选日志state和9个固定health错误码枚举，五份生成投影、共同fixture、Go/app/Python/Rust消费者一并更新。源码和存储health集合双向核对；非固定engine_log_synthetic_secret拒绝，不通过字符串形状复制caller文本。

自有4槽实际每槽256KiB-64，owner marker最多256bytes，总namespace<=1MiB；真实5600次事件轮转与reopen延续序号，Windows样本4槽+marker为853052bytes。Unix祖先NOFOLLOW/owner/mode重查、Windows句柄/ACL/reparse/单link、未知namespace/部分header/完整坏行/旧日志保留与写入中晚hardlink/权限扩大拒绝均验收。不自动修ACL、认领未知文件或无限重试轮转。

PlatformLogWriter会隐式开Cache/Clash并导致最小registry初始化失败，故不接。专用child强制warn级无色无时间stderr；匿名pipe reader只计WARN[/ERROR[或suppressed，原文不落盘，stdout永久null。Windows独立审查复现缓存旧os.File/标准log/自建logger可绕过SetStdHandle，修为退役旧独立HANDLE与重绑默认logger，旧失败新CombinedOutput0。不同File共用有效HANDLE固定拒绝启动，避免双Close和复用风险；一般RunEngine不改输出/logger。

日志事件64项队列+唯一worker，health atomic；unknown文件IO在途仍允许排队，不能丢ready/stopped。同步实际Store write hook证明child实际ready、独立RPC可返回unknown、Stop响应/真实启动lease到期与外层3秒自退出；这是受控IO hook，不是操作系统磁盘故障或硬SLA。BeforeStop拒绝后超过3.2s仍ready/控制可用，不启动强退预算；正常Stop先logger回收再释放lifetime，立即重新Start通过。queue overflow/fixed write/sync/namespace失败sticky。SIGKILL/panic/断电末条flush与任意startup文件IO退出预算不承诺。

受管上游AttachFenced同时转交资源与fence。Gate决策锁内仅许可/计数，不做网络IO；每conn write/普通deadline/past revoker各1许可，额外立即ErrCapacity。future/clear取消后拒绝或迟到修复past；Close等待raw Close及此前三类permit完成，公开Release/Untrack不能提前删除预算。past外部许可在本conn physical Close开始后拒绝，Close自己raw past撤销保留。相同实际HTTP/yamux/TCP target/已有retained fixture overlay第七波HEAD82f69c0，两窗确实失败（future后cancel command写1、clear恢复zero），新源修复past/closed write0、retained echo保持。pre-close已许可IO与kernelbytes不可撤回，内部stream/5min timer/contextual Open容量仍未解决。

## 失败记录与时钟观察边界

Linux首轮门禁错误覆盖整个shared/proxy，调用尚未支持的Linux产品后台launch，三项旧生命周期测试失败；改为显式本轮EngineLog/Dedicated/GeneralRunEngine slice和实际Hub/reverse链路，未放松产品断言或增加Linux launch支持。`wave8-linux-final.log`保留。

下一轮v2的blocked logger expiry在4.95s未观察到stopping而失败；私有仅诊断test overlay连续3次在约5s看到真实signed stopping并实际77退出，原失败未复现、根因仍未定，不能归因已证实WSL跳时。确切可修的时基差异是fixture用无monotonic的Unix截止，而产品AfterFunc把remaining转为单调duration。仅将观察预算改为time.Now锚定remaining+1s，秒数不增加、真实stopping/lifetime/77断言不变，并加入公共relative时间/最后phase诊断。v12与Linuxv2为中间收据，最终全部重跑v13/Linuxv3，不覆盖失败日志。

## 冻结验收

`check-steelman.ps1 -EvidenceDirectory <私有新目录>/security-v13` exit0：产品tags Go/test/vet、Windows race、actual CLI-Hub TLS/WG/protected peer/revoke及新增独立日志status/正常ready-stopped留存、offline update、SDK35/Rust41/Admin43、两棵Svelte零错误警告、builder9/16、NSIS13+完整模板与npm省略dev拒绝通过。真实CLI status全程同5s只读预算，unknown可待healthy、degraded/错实例/秘密立即失败；authority请求计数不增，不隐式bootstrap。

6个Go OS/arch扫描symbol/package0、module1（GO-2026-5932未导入OpenPGP，有效例外截止2026-11-06），两棵含dev/optional/peer完整npm零漏洞，四Rust target零漏洞/未审阅警告。Go1.26.7，govuln数据库last_modified为2026-10-01；不称整个module零风险。证据 `check-steelman-v13.log` / `security-v13/`。

`sh scripts/check-proxy-barrier-linux.sh`最终v3 exit0：本轮shared/proxy slice21.859s、proxygate1.848s、reverse2.674s、deviceapi8.217s、deviceauth0.190s及schema/vet通过。实际native父/namespace中的八项原WG/产品屏障均PASS，namespace6.07s；顶层helper-only SKIP是由父测试重新exec进自有namespace后的正常结构，不是跳过所需能力。Linux为CGO=0普通测试，未运行race或Linux产品client launch；Darwin arm64测试包编译单独通过，Mac行为未验。`wave8-linux-final-v3.log`为最终证据。

冻结v13开始至全部门禁结束661项完整源码清单SHA漂移0。原工作区HEAD仍e69645a1bee6bb28927e18caa3e43b2021296129，保护基线79项逐文件hash漂移0；current status89行包括另10项RDP独立工作，未动。私有收据/日志/产物在Git树外 `.local/zongheng-vpn/steelman/2026-10-07/`，不入Git。

## 本地源码与构建收据

源码提交 `3b5a2c576aa344f907031b2c35c31c69499424b2`，61文件、5181新增/73删除；提交前最终gate源码没有漂移，仅补本波文档。提交后worktree clean，再运行 `build-steelman-dev.ps1 -OutputDirectory <私有新目录>/development-wave8-final` exit0；661项全源码清单构建中漂移0，十个产物SHA逐一匹配manifest。Node/Admin与Device生成gate有旧OpenAPI3.1工具warning，但当前投影确定性/消费者门禁通过，本波未改HTTP契约。

实际Windows AMD64 CLI version child返回完整源码SHA、source_state=clean、product=cli/version=dev、Go1.26.7、contract_version=1/protocol_version=2。构建signed=false、acceptance=compile_only、release_ready=false；未签名、未发行、未推送、未部署或生产验收。

开发manifest SHA256 `38069728adbc66468d164095be09ee79aed560d4d8b453d1301be6bc308213dd`；source-manifest SHA256 `2bbbf3c709eaed5155b0adc3948b91ac6ee8553c4165fefe4fb91209e683903f`。完整内容为私有 `development-wave8-final/manifest.json` / `acceptance-receipt.json`；随后文档收据提交仅修改本清单/日志和总计划，不改变已验收产品源码。
