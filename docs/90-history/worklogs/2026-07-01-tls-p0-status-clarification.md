# 2026-07-01 TLS P0 状态澄清

回看 2026-06-04 Hub 安全审查和安全 TODO 时，确认 TLS 相关状态需要区分两个入口：

- 已完成：Hub 管理控制台由 Caddy 提供公网 HTTPS 入口
  `https://jp-proxy.ruichao.dev/admin/`，反代到本机 `127.0.0.1:18100`。
- 初始回看时未完成：客户端授权 API 仍是 `http://36.50.84.68:18080`，
  `/api/client/bootstrap` 和 `/api/client/rotate-ip` 尚未通过 HTTPS 暴露。

只读验证：

- `http://36.50.84.68:18080/healthz` 返回 `{"status":"ok"}`。
- `https://jp-proxy.ruichao.dev/admin/` 返回 `200 OK`。
- `https://jp-proxy.ruichao.dev/healthz` 返回 `404 Not Found`，说明当前 HTTPS 域名没有承载客户端 API。
- `https://36.50.84.68:18080/healthz` TLS 握手失败，说明 18080 仍是明文 HTTP。

已更新：

- `docs/40-security/security-todo.md`
- `docs/40-security/security-audit-2026-06-04.md`

结论：admin HTTPS 已上线，但安全审查 P0-1/P0-2 仍未关闭。后续真正关闭 P0 需要把客户端
bootstrap/rotate API 迁到 HTTPS，并把客户端私钥改为本地生成、只上报公钥。

## 客户端 HTTPS 默认值准备

为 P0-1 的第一步迁移做本地代码准备：

- `clients/cli/internal/bootstrap/client.go` 默认 API base 从
  `http://36.50.84.68:18080` 改为 `https://jp-proxy.ruichao.dev`。
- 保留 `ZHVPN_API_BASE` 环境变量覆盖,方便本地测试和紧急回滚。
- `clients/cli/internal/bootstrap/client_test.go` 增加默认 HTTPS host 和 env 覆盖测试。
- `docs/20-operations/runbooks/hub-api-deploy.md` 增加 Caddy `/api/client/*` HTTPS 反代、
  验证命令和公网 `18080/tcp` 收口步骤。

## SSH key 整理

本机 Hub SSH key 文件从 `~/.ssh/daxiang_server` 重命名为 `~/.ssh/zongheng_server`,
`.pub` 同步重命名。指纹保持 `SHA256:j2TPLqcrM6MHy24eQBPOT9p8458mv9IpMSz8Zx7LJuo`。

## 生产 Caddy 路由上线

已在 Hub 更新 `/etc/caddy/Caddyfile`,把同一域名下的客户端授权 API 接入 HTTPS:

- `/api/client/*` -> `127.0.0.1:18080`
- `/healthz` -> `127.0.0.1:18080`
- `/admin*` -> `127.0.0.1:18100`
- 根路径 `/` 302 到 `/admin/`;未知路径返回 404

操作记录:

- 临时配置写入 `/tmp/Caddyfile.zongheng-api-tls`。
- 原配置备份为 `/etc/caddy/Caddyfile.bak-20260701084419-api-tls`。
- `caddy validate --config /etc/caddy/Caddyfile` 通过。
- `systemctl reload caddy` 成功,Caddy 保持 active。

公网验证:

- `https://jp-proxy.ruichao.dev/healthz` 返回 `{"status":"ok"}`。
- `https://jp-proxy.ruichao.dev/api/client/bootstrap` 用 HEAD 返回 `405 Method Not Allowed`,
  证明路由已到达 zhhub 客户端 API。
- `https://jp-proxy.ruichao.dev/admin/` 返回 `200 OK`。
- `http://36.50.84.68:18080/healthz` 仍返回 ok,作为老客户端迁移期兼容入口。

## Caddy 根路径白页修复

上线后发现 `https://jp-proxy.ruichao.dev/` 返回空白 `200 OK`。原因是 Caddyfile 中 `redir /admin/ 302` 在 block 里会把 `/admin/` 当 matcher,不是目标地址,导致根路径落成空响应。

修复:

- 备份 `/etc/caddy/Caddyfile` 到 `/etc/caddy/Caddyfile.bak-20260701130056-root-route-redir-fix`。
- 将根路径改为 `handle / { redir * /admin/ 302 }`。
- fallback 改为 `handle { respond "not found" 404 }`。
- `caddy validate --config /etc/caddy/Caddyfile` 通过。
- `systemctl reload caddy` 成功,Caddy 保持 active。

验证:

- `https://jp-proxy.ruichao.dev/` 返回 `302 Location: /admin/`。
- `https://jp-proxy.ruichao.dev/not-found-check` 返回 `404 Not Found`。
- `https://jp-proxy.ruichao.dev/admin/` 返回 `200 OK`。
- `https://jp-proxy.ruichao.dev/healthz` 返回 ok。

下一步:发布默认走 `https://jp-proxy.ruichao.dev` 的客户端,观察 bootstrap/rotate 正常后,
删除 ufw 的公网 `18080/tcp` 放行。

## Windows CLI 构建

已运行 `clients/cli/build.ps1`,生成默认走 HTTPS API base 的 Windows CLI 发布包:

- `dist/windows-amd64/zhvpn.exe`，约 17.2 MB。
- `dist/windows-arm64/zhvpn.exe`，约 15.6 MB。

验证:

- `go test ./clients/cli/internal/bootstrap ./clients/cli/internal/app ./hub ./hub/internal/auth ./hub/admin` 通过。
- `dist/windows-amd64/zhvpn.exe version --json` 返回 `{"ok":true,"version":"dev"}`。

`dist/` 为本地发布产物,当前未进入 git status。发布/分发后再观察新客户端 bootstrap 是否从
`https://jp-proxy.ruichao.dev` 进入;确认老客户端迁移完成后再删除 ufw 的 `18080/tcp`
公网放行。

## P0-2 本地 WireGuard 私钥准备

继续实现 P0-2 的代码和生产 Hub 迁移:

- CLI 在 `ZHVPN_HOME/wireguard/client.key` 生成/复用 WireGuard 私钥,文件权限 `0600`。
- bootstrap 请求新增 `wireguard_public_key`;客户端只上传公钥。
- Hub 验证公钥为 32 字节 base64,用 `wg set <iface> peer <public_key> allowed-ips <client_ip>/32`
  应用到 `wg0`。`ZHHUB_WG_INTERFACE` 可覆盖接口名,`ZHHUB_WG_BIN` 可覆盖 `wg` 路径。
- Hub 收到新协议请求后不再返回 `wireguard.private_key`;未上报公钥的老客户端仍暂时兼容 legacy
  private_key 响应。
- `hub/config/tokens.example.yaml` 去掉新 token 的 `private_key` 示例。

本地验证:

- `go test ./...` 通过。
- `go build ./hub` 和 `go build ./clients/cli` 通过。

生产部署:

- 部署前备份目录：`/root/zongheng-backups/20260701091751-p0-local-wg-key`。
- 备份内容包括旧 `zhhub`、`tokens.yaml`、`admin.db`、`wg0.conf`、`wg show wg0 allowed-ips`
  输出、`zhhub.service` unit 快照和 `SHA256SUMS`。
- 旧生产 `zhhub` SHA256：`8fe8fdb23a4636a2f80bfc6e99c380a038eab80ab61219514ee59071de7ff571`。
- 新生产 `zhhub` SHA256：`33e6b88b281b04cd3e0430d16ae3be7becbd0452f3a087fce4e0f5cd355f9e7d`。
- 新二进制已安装到 `/opt/zongheng/zhhub/zhhub` 并重启 `zhhub.service`。

生产验证:

- `zhhub.service` active。
- `https://jp-proxy.ruichao.dev/healthz` 返回 ok。
- `https://jp-proxy.ruichao.dev/api/client/bootstrap` 用 HEAD 返回 `405 Method Not Allowed`。
- `https://jp-proxy.ruichao.dev/admin/api/health` 返回 ok。
- 用生产已有 token 的 legacy 私钥在 Hub 本机派生同一公钥,按新协议提交
  `wireguard_public_key`;返回 `200 OK`,响应包含 `wireguard.public_key`,不包含
  `wireguard.private_key`。该验证使用同一来源 IP 做 `X-Forwarded-For`,避免触发 token
  单来源租约抢占;未打印完整 token、私钥或公钥。
- 验证后 `wg0` peer 数仍为 25,近 10 分钟无 `wireguard_peer_apply_failed` 或 peer 应用失败日志。

生产迁移剩余步骤:

1. 分发新客户端 CLI,并覆盖 Windows GUI sidecar / Python SDK bundled CLI。
2. 用新客户端 login/start 验证 `wg set` peer 生效。
3. 从 tokens 中移除客户端 legacy `wireguard.private_key`,只保留地址和其它路由元数据。
4. 新客户端稳定后,删除 ufw 的公网 `18080/tcp` 放行。

注意：截至本记录,P0-2 代码和 Hub 生产支持已上线,但 P0-2 不能标记关闭。原因是老客户端兼容路径仍会返回
legacy `wireguard.private_key`,生产 `tokens.yaml` 仍保留旧私钥字段,客户端还未分发。

## 客户端正式包重建与端口覆盖修复

本机用新 CLI 做 canary 时发现一个非默认路径问题:`zhvpn start --port 7897` 实际代理可用,但
`zhvpn status --json --no-ip-check` 仍显示缓存配置里的默认 `127.0.0.1:7890`。

修复:

- `start` 应用 `--port` / `ZHVPN_LOCAL_PORT` 覆盖后,把有效运行端口写回本地状态缓存。
- 状态缓存仍通过 `saveClientConfigCache` 写入,不会持久化 `wireguard.private_key`。
- 增加单元测试覆盖端口覆盖缓存行为。

验证:

- `go test ./clients/cli/internal/app ./clients/cli/internal/bootstrap` 通过。
- `go test ./...` 通过。
- 重新构建 Windows CLI:
  - `dist/windows-amd64/zhvpn.exe`
  - `dist/windows-arm64/zhvpn.exe`
- 用正式 `dist/windows-amd64/zhvpn.exe` + 临时 `ZHVPN_HOME` 验证:
  - `login ZH-JP-TEST-100 --json` 成功。
  - `start --port 7897 --json` 成功。
  - `status --json --no-ip-check` 返回 `proxy=127.0.0.1:7897` 且 `proxy_reachable=true`。
  - 测试后 stop,删除临时目录,并把生产自测 token 的 WireGuard peer 恢复回 legacy 公钥。
- 重新构建 Windows GUI x64 安装包:
  - `clients/desktop-gui/src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/纵横 VPN_0.4.10_x64-setup.exe`
- 重新构建 Python SDK bundled CLI 和 wheel:
  - `sdk/python/src/zongheng_vpn/bin/zhvpn.exe`
  - `sdk/python/dist/zongheng_vpn-0.1.1-py3-none-win_amd64.whl`
- `python -m unittest discover -s sdk/python/tests` 通过。
