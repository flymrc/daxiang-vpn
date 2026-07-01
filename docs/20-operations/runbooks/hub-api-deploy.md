# Hub 授权 API 部署

## 构建

在本机仓库执行：

```powershell
cd c:\Users\xuotq\zongheng-vpn
pushd hub/admin/web
npm ci
npm run build:embed
popd
$env:GOOS="linux"
$env:GOARCH="amd64"
go build -o dist/linux-amd64/zhhub ./hub
Remove-Item Env:\GOOS, Env:\GOARCH
```

## Hub 服务器目录

建议部署到：

```text
/opt/zongheng/zhhub
```

文件：

```text
/opt/zongheng/zhhub/zhhub
/opt/zongheng/zhhub/tokens.yaml
/opt/zongheng/zhhub/admin.db
```

> 2026-06-11 已完成 `dxhub` → `zhhub` 迁移:生产 unit 为 `zhhub.service`,二进制与 tokens 在 `/opt/zongheng/zhhub/`,环境变量全部 `ZHHUB_*`,控制面 key `/root/.ssh/zhandroid_control_hub`,监听 `0.0.0.0:18080`。旧 dx 服务 / 目录 / key 已归档到 Hub `/root/dx-attic-20260611/`(可回滚)。详见 [2026-06-11-dxhub-to-zhhub-cutover.md](../../90-history/worklogs/2026-06-11-dxhub-to-zhhub-cutover.md)。

## 环境变量

```text
ZHHUB_TOKENS=/opt/zongheng/zhhub/tokens.yaml
ZHHUB_LISTEN=0.0.0.0:18080
ZHHUB_ANDROID_CONTROL_KEY=/root/.ssh/zhandroid_control_hub
ZHHUB_ANDROID_CONTROL_KNOWN_HOSTS=/root/.ssh/zhandroid_control_known_hosts
ZHHUB_ANDROID_CONTROL_HOST_KEY_POLICY=accept-new
ZHHUB_ANDROID_CARRIER_CACHE_SECONDS=300
ZHHUB_TOKEN_LEASE_SECONDS=30
ZHHUB_ADMIN_ENABLED=1
ZHHUB_ADMIN_LISTEN=127.0.0.1:18100
ZHHUB_ADMIN_DB=/opt/zongheng/zhhub/admin.db
ZHHUB_ADMIN_PUBLIC_HOST=jp-proxy.ruichao.dev
ZHHUB_ADMIN_USER=admin
ZHHUB_ADMIN_PASSWORD_HASH=<argon2id-phc-hash>
ZHHUB_ADMIN_REVERSE_HEALTH_URL=http://10.66.0.1:18081/debug/session-health
ZHHUB_ADMIN_EXIT_IP_CHECK_URL=https://api64.ipify.org
ZHHUB_ADMIN_EXIT_IP_CHECK_TIMEOUT_SECONDS=8
```

`ZHHUB_ANDROID_CARRIER_CACHE_SECONDS` 控制 bootstrap 响应里 Android 运营商名的控制面 SSH 探测缓存,默认 300 秒;设为 `0` 可禁用动态探测并使用 token 配置里的显示名。Hub 控制面 SSH 默认用独立 known_hosts 文件和 `accept-new` 策略,首次连接自动记录主机 key,后续若主机 key 变化会阻止连接;紧急回滚可临时把 `ZHHUB_ANDROID_CONTROL_HOST_KEY_POLICY` 设为 `no`。

生成管理员密码 hash:

```powershell
go run ./hub/cmd/zhhub-admin-hash "change-this-password"
```

把输出写入 systemd 的 `ZHHUB_ADMIN_PASSWORD_HASH`,不要把明文或 hash 提交到公开仓库。

## systemd 服务

```ini
[Unit]
Description=Zongheng VPN Hub API
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/zongheng/zhhub
Environment=ZHHUB_TOKENS=/opt/zongheng/zhhub/tokens.yaml
Environment=ZHHUB_LISTEN=0.0.0.0:18080
Environment=ZHHUB_ANDROID_CONTROL_KEY=/root/.ssh/zhandroid_control_hub
Environment=ZHHUB_ANDROID_CONTROL_KNOWN_HOSTS=/root/.ssh/zhandroid_control_known_hosts
Environment=ZHHUB_ANDROID_CONTROL_HOST_KEY_POLICY=accept-new
Environment=ZHHUB_ANDROID_CARRIER_CACHE_SECONDS=300
Environment=ZHHUB_TOKEN_LEASE_SECONDS=30
Environment=ZHHUB_ADMIN_ENABLED=1
Environment=ZHHUB_ADMIN_LISTEN=127.0.0.1:18100
Environment=ZHHUB_ADMIN_DB=/opt/zongheng/zhhub/admin.db
Environment=ZHHUB_ADMIN_PUBLIC_HOST=jp-proxy.ruichao.dev
Environment=ZHHUB_ADMIN_USER=admin
Environment=ZHHUB_ADMIN_PASSWORD_HASH=<argon2id-phc-hash>
Environment=ZHHUB_ADMIN_REVERSE_HEALTH_URL=http://10.66.0.1:18081/debug/session-health
Environment=ZHHUB_ADMIN_EXIT_IP_CHECK_URL=https://api64.ipify.org
Environment=ZHHUB_ADMIN_EXIT_IP_CHECK_TIMEOUT_SECONDS=8
ExecStart=/opt/zongheng/zhhub/zhhub
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

保存为：

```text
/etc/systemd/system/zhhub.service
```

启动：

```bash
systemctl daemon-reload
systemctl enable --now zhhub
systemctl status zhhub
```

## 防火墙

目标状态下 Hub 对公网只需要开放：

```text
TCP 80
TCP 443
```

`18100/tcp` 不开放给公网,只监听 `127.0.0.1`。`18080/tcp` 是 zhhub 客户端 API 的明文本地后端,只应由 Caddy 在本机反代访问;老客户端还在直连 `http://36.50.84.68:18080` 的迁移期可临时保留公网放行,待 HTTPS 客户端发布并确认升级后必须收掉公网 `18080/tcp`。

`80/443` 由 Caddy 使用,用于 `jp-proxy.ruichao.dev` 自动 HTTPS 和反向代理。

## Admin 敏感值 reveal

管理控制台默认仍以脱敏方式展示授权码和出口 IP:

- `/admin/api/tokens` 只返回 `masked_token` 和稳定 hash `id`,不返回完整 token。
- 点授权码眼睛时调用 `GET /admin/api/tokens/{token_id}/secret`,只 reveal 单个 token,并写入 `admin.reveal_token` 审计事件。
- 点出口 IP 眼睛时调用 `GET /admin/api/egress/{egress_id}/exit-ip`,Hub 通过该出口的 `proxy_addr` 请求 `ZHHUB_ADMIN_EXIT_IP_CHECK_URL`。默认 URL 是 `https://api64.ipify.org`,超时默认 8 秒。

不要把 reveal 响应、完整 token 或管理员密码写入日志/文档/对话。

## DNS

控制台直接使用 `jp-proxy.ruichao.dev`,替代原测速页入口。

推荐部署形态是把 Cloudflare DNS 记录切为 DNS-only:

```text
jp-proxy.ruichao.dev A 36.50.84.68
```

这样 Caddy 可以直接完成公网证书签发和续期。如果继续使用 Cloudflare 代理,浏览器侧证书由 Cloudflare 提供,Caddy 只负责源站侧 TLS/反代;此时需要单独确认 Cloudflare SSL 模式和源站证书策略。

## Caddy 与 Librespeed

控制台和客户端授权 API 的公网 HTTPS 入口都由 Caddy 负责,不使用 Dokku。Caddy 自动签发/续期证书:

- `/api/client/*` 和 `/healthz` 反代到 zhhub 客户端 API 后端 `127.0.0.1:18080`。
- `/admin/*` 反代到 zhhub admin listener `127.0.0.1:18100`。

示例 Caddyfile:

```caddyfile
jp-proxy.ruichao.dev {
  encode gzip zstd

  handle /api/client/* {
    reverse_proxy 127.0.0.1:18080
  }

  handle /healthz {
    reverse_proxy 127.0.0.1:18080
  }

  handle /admin* {
    reverse_proxy 127.0.0.1:18100
  }

  handle {
    respond "not found" 404
  }
}
```

现有 `linuxserver/librespeed` 不能继续占用公网 `80/tcp`;控制台上线将直接替代原测速页。部署前先记录现有容器参数,再取消自动重启并停止容器,方便必要时回滚:

```bash
docker inspect librespeed > /root/librespeed.inspect.before-admin-panel.json
docker update --restart=no librespeed
docker stop librespeed
```

更新 Caddy 配置前必须验证:

```bash
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy
```

## 验证

```bash
curl http://127.0.0.1:18080/healthz
curl http://127.0.0.1:18100/admin/api/health
curl https://jp-proxy.ruichao.dev/healthz
curl -I https://jp-proxy.ruichao.dev/
curl -I https://jp-proxy.ruichao.dev/api/client/bootstrap
curl -I https://jp-proxy.ruichao.dev/admin/
curl -I https://jp-proxy.ruichao.dev/not-found-check
curl -I https://jp-proxy.ruichao.dev/admin/api/tokens/not-real/secret
curl -I https://jp-proxy.ruichao.dev/admin/api/egress/jp-android-01/exit-ip
```

预期：

```json
{"status":"ok"}
```

`/api/client/bootstrap` 只接受 POST,所以 `curl -I` 预期是 `405 Method Not Allowed`;这能证明 HTTPS 入口已经路由到 zhhub 客户端 API,而不是 Caddy/admin 的 404。

根路径 `/` 和未知路径都预期 `404 Not Found`,避免返回空白 200 或暴露控制台入口提示。

未登录访问 admin reveal API 预期 `401 Unauthorized`。登录后的 reveal 验证应只在本机/生产会话内进行,不得把完整 token 输出到共享日志。

## 客户端 HTTPS 迁移

2026-07-01 起,新客户端默认授权 API 为:

```text
https://jp-proxy.ruichao.dev
```

客户端仍支持 `ZHVPN_API_BASE` 覆盖,用于本地测试或紧急回滚。迁移顺序:

1. 先部署 Caddy `/api/client/*` HTTPS 反代并验证。
2. 再发布默认走 `https://jp-proxy.ruichao.dev` 的客户端。
3. 观察新客户端 bootstrap/rotate 正常。
4. 最后删除 ufw 的公网 `18080/tcp` 放行,只保留 Caddy `80/443`。

## 客户端本地 WireGuard 密钥迁移

2026-07-01 起,新 `zhhub` 支持客户端本地生成 WireGuard 私钥、bootstrap 只上报公钥:

```json
{
  "token": "ZH-JP-TEST-001",
  "wireguard_public_key": "CLIENT_WIREGUARD_PUBLIC_KEY"
}
```

Hub 会校验公钥为 32 字节 base64,并执行:

```bash
wg set wg0 peer <client_public_key> allowed-ips <client_ip>/32
```

`ZHHUB_WG_INTERFACE` 可覆盖 WireGuard 接口名,默认 `wg0`;`ZHHUB_WG_BIN` 可覆盖 `wg`
命令路径。新协议响应不包含 `wireguard.private_key`,只返回运行所需配置和
`wireguard.public_key`。迁移期内,未上报 `wireguard_public_key` 的老客户端仍兼容
legacy `wireguard.private_key` 响应。

生产部署记录:

- 备份目录：`/root/zongheng-backups/20260701091751-p0-local-wg-key`。
- 新 `zhhub` SHA256：`33e6b88b281b04cd3e0430d16ae3be7becbd0452f3a087fce4e0f5cd355f9e7d`。
- 验证：新协议 bootstrap 返回 `200 OK`,响应省略 `wireguard.private_key`;`wg0` peer
  数保持 25,近 10 分钟无 peer 应用失败日志。

admin SQLite 容量防护生产部署记录:

- 备份目录：`/root/zongheng-backups/20260701125316-admin-sqlite-retention`。
- 新 `zhhub` SHA256：`aff3855a6ae5bfd53fc62dc2342ba397d84c7444dde47d0da550735d92b6baf2`。
- 验证：`zhhub` 和 `caddy` 均 active;`127.0.0.1:18080/healthz`、`127.0.0.1:18100/admin/api/health`、
  `https://jp-proxy.ruichao.dev/healthz` 正常;`https://jp-proxy.ruichao.dev/admin/` 返回 200;
  `https://jp-proxy.ruichao.dev/api/client/bootstrap` 的 HEAD 返回预期 405。
- 启动维护验证：`/opt/zongheng/zhhub/admin.db-wal` 从部署前约 4.0M 收缩到约 20K;
  `journalctl -u zhhub.service --since "5 min ago"` 无 `admin db maintenance failed`、panic 或 fatal。

剩余收尾:

1. 分发新 CLI,并同步更新 Windows GUI sidecar 与 Python SDK bundled CLI。
2. 用真实新客户端 login/start 验证本地私钥路径和 peer 应用。
3. 清理生产 `tokens.yaml` 中客户端 legacy `wireguard.private_key`。
4. 完成 HTTPS 客户端迁移后关闭公网 `18080/tcp`。

## 客户端 token 单来源租约

`/api/client/bootstrap` 会按 token 记录最近一次成功 bootstrap 的来源 IP。默认 `ZHHUB_TOKEN_LEASE_SECONDS=30`：

- 同一个 token 在同一公网来源继续 bootstrap，会刷新租约。
- 同一个 token 在不同公网来源 30 秒内再次 bootstrap，会返回 `409 {"error":"token_in_use"}`。
- 设置 `ZHHUB_TOKEN_LEASE_SECONDS=0` 可关闭该保护。

来源识别只信任本机或内网反向代理传入的 `X-Forwarded-For`；公网客户端直连时按 TCP 源地址计算，避免客户端伪造来源绕过 token 租约。

## Android 出口一键换 IP

Hub API 提供客户端无感入口:

```text
POST /api/client/rotate-ip
```

客户端只提交自己的授权 token 和断网秒数,Hub 校验 token 后通过 Android 控制面 SSH 触发 `/data/adb/zhandroid/rotate-ip.sh`。客户机不需要 Android SSH 私钥,也不需要知道跳板。

Hub 会按出口加一把非阻塞换 IP 锁:第一次请求触发 Android 控制面,保护窗口内的后续请求不会再次执行换 IP,而是返回 `409 {"status":"busy","message":"换 IP 正在进行中，请稍后再试"}`。保护窗口默认是 `down_seconds + 45s`,可通过 `ZHHUB_ROTATE_LOCK_EXTRA_SECONDS` 调整额外秒数。

Hub 侧需要一把无 passphrase 的服务专用 key:

```bash
ssh-keygen -t ed25519 -N "" \
  -C "zhhub-android-control@36.50.84.68" \
  -f /root/.ssh/zhandroid_control_hub
chmod 600 /root/.ssh/zhandroid_control_hub
```

把公钥追加到 Android:

```bash
cat /root/.ssh/zhandroid_control_hub.pub
# 将输出追加到 Android /data/adb/zhandroid/.ssh/authorized_keys
```

当前 Hub 公钥:

```text
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMFnqYTgTqmQkJ314sFHCuaHd5q4NvrjsWZwNsR8E5H7 zhhub-android-control@36.50.84.68
```

> 若 API 返回 `control_failed`,优先检查 `ZHHUB_ANDROID_CONTROL_KEY` 指向的私钥是否无 passphrase,以及对应公钥是否已在 Android `authorized_keys` 中。
