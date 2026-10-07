# Hub HTTP 与管理台边界（2026-10-07）

状态：隔离开发分支已实现。本文件描述源码和本地验证，不代表当前生产二进制或 Caddy 已采用这些参数。

## 兼容入口与来源

`hub/internal/httpboundary` 是 legacy、trusted-client、Admin 三个实际 `http.Server` 的共同构造边界。默认 header/JSON 请求体上限各 16 KiB；ReadHeaderTimeout 5s、ReadTimeout 10s、WriteTimeout/IdleTimeout 各 30s。四个 JSON 写入口（bootstrap、客户端 rotate、Admin login/rotate）在读取和副作用之前安装 `MaxBytesReader`，校验完整正文及 EOF；超限 413，尾随对象或垃圾 400。兼容已有的未知字段，不在此切片改变旧客户端 DTO。

兼容 listener 无条件忽略 XFF，仅使用 TCP remote IP；WireGuard 私网、loopback 都不隐式变成代理。trusted-client 与 Admin listener 只信任 TCP remote 为精确 `127.0.0.1` / `::1` 的代理，规范化 IPv4-mapped 地址。XFF 最大 512 字节、8 跳，完整解析后从右向左剥离精确可信代理跳，取第一条非代理 IP。格式错误拒绝且不更新租约。listener 身份固定，不能由请求 header 选择可信模式。

第三波把 bootstrap/client rotate、Admin login/rotate/exit-IP 接到同一昂贵请求准入对象；快照/health 读取保留独立可用路径。候选默认全局/来源并发8/2、全局20/s burst40、来源1/s burst4，来源表最多1024项、闲置10分钟回收；超额429、容量不足503，均不排队。来源只承担准入与审计，不成为设备授权身份。HTTP 写超时不证明 wg/SSH 子进程终止；WG/SSH 另有 request context 与有限 native 执行预算，见[执行合同](legacy-control-process-budget.md)。设备/会话身份租约、所有产品入口及生产容量未完成，P4.5 保持未勾选。Caddy 转发规则与生产入口必须在受控上线前独立复核，不能凭这些本地规则推断线上来源可信。

## 管理台真实状态

`hub/admin/web/src/lib/validation.ts` 校验实际运行响应；生成的 TypeScript 类型不代替运行校验。第二波为五项 snapshot，第六波加入 migration readiness contract2 后为完整六项，全部通过才提交新状态；0、空列表和未知分别显示，不用 demo 补真实空数据、默认 RTT 或统计值。读取或 shape 失败清除旧数据、秘密和 modal，显示状态未知并禁用依赖当前数据的操作。页面的“管理 API 已响应”只表示该管理快照已读取，不表示所有代理数据面或业务请求健康。尚未升级的生产 contract1 不能给新版完整读取制造健康。

每次刷新推进数据 generation，独立 owner 只释放自己的刷新占用；较早的成功 snapshot 不能覆盖后来发生的 401/403 权限失效。secret/exit-IP 结果绑定请求目标与 generation，刷新后迟到结果不能重新展示，清理也不能误清后来的同类请求。401/403 会清除旧授权状态及秘密，401 返回登录。

mutation 有独立 pending 锁，手动健康刷新不能允许第二次提交。网络失败或无法校验 rotate 回执时报告结果未确认，页内锁定再次 rotate；普通刷新不能解除该锁。第三波增加可选 `rotate_state=idle|cooldown|unknown` 服务端投影：启动后的执行失败保留 unknown，不随普通冷却到期重派，管理台刷新/重载仍禁用该节点。字段缺失显示未报告；idle只表示本地无锁。服务端标记当前仅存于 Hub 进程内，没有跨重启 durable operation ID/receipt 或安全 clear 协议，故完整 P1.3 未完成。换 IP“已触发”也不表示出口已恢复或 IP 已改变。

节点名称、运营商与接口来自实际返回；没有对应字段就显示未提供运行证据，去掉原来的固定运营商/接口示例。页面不把这些示例冒充当前手机状态。

demo 只在 Vite DEV 且显式 `?preview=1` 时启用，标注演示数据且所有操作禁用，不请求 API。生产构建忽略该 query。开发态错误和生产空数据不会自动进入 demo。

## 合同与依赖门禁

`scripts/check-admin-contract.ps1` 使用 canonical OpenAPI、oapi-codegen YAML、sqlc YAML/schema/queries，生成到独立临时目录，核对 Go/TypeScript 与数据库代码的摘要和 inventory。只重定位 oapi 的根 output；sqlc 执行前由 `hub/admin/cmd/contract-config-guard` 拒绝未支持的 generator/plugin、逃逸路径、未知配置字段、重复 YAML 或多文档。新配置能力需要显式扩展门禁，不能以手拼旧选项掩盖配置变化。oapi-codegen 对 OpenAPI 3.1 的已知支持限制仍保留，确定性生成不等于完整 3.1 语义验证。

安全 gate 同时扫描 desktop 和 Admin npm tree，显式包含 dev/optional/peer，不受继承的 `NPM_CONFIG_OMIT=dev` 影响。真实 registry 负例锁定：省略 dev 会隐藏已知开发依赖漏洞，统一策略仍拒绝。安装依赖使用锁文件；不得以 audit exit=0 或仅检查生产依赖宣称全树安全。

Admin 从 Tailwind 3 更新到 4.3.3，PostCSS 使用 `@tailwindcss/postcss`，保留现有 theme/CSS。此版本以现代浏览器为基线（Safari 16.4、Chrome 111、Firefox 128）；未新增对更旧管理浏览器的支持承诺。[官方升级说明](https://tailwindcss.com/docs/upgrade-guide)。Node 24 用于当前类型检查、构建和直接执行 TypeScript consumer 负例；工具环境需满足此验证基线。

验证包括 runtime consumer 负例、真实 loopback 慢 header/body/write/idle 和超大 header、来源伪造且无租约副作用，以及真实 Chrome 对编译页面的合成 API 验收。第三波补充共享准入满载/读路径保留、服务端 unknown 到期/重载不解锁和 native 执行故障反例。Chrome 合成数据不是生产登录、生产出口或打包客户端证据。具体命令和证据见[第二波 worklog](../90-history/worklogs/2026-10-07-zhvpn-security-boundaries.md)与[执行预算 worklog](../90-history/worklogs/2026-10-07-legacy-control-process-budget.md)。
