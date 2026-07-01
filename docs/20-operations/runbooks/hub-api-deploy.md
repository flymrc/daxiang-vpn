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
ZHHUB_ADMIN_PUBLIC_HOST=panel.jp-proxy.ruichao.dev
ZHHUB_ADMIN_USER=admin
ZHHUB_ADMIN_PASSWORD_HASH=<argon2id-phc-hash>
ZHHUB_ADMIN_REVERSE_HEALTH_URL=http://10.66.0.1:18081/debug/session-health
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
Environment=ZHHUB_ADMIN_PUBLIC_HOST=panel.jp-proxy.ruichao.dev
Environment=ZHHUB_ADMIN_USER=admin
Environment=ZHHUB_ADMIN_PASSWORD_HASH=<argon2id-phc-hash>
Environment=ZHHUB_ADMIN_REVERSE_HEALTH_URL=http://10.66.0.1:18081/debug/session-health
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

Hub 需要开放：

```text
TCP 18080
TCP 80
TCP 443
```

`18100/tcp` 不开放给公网,只监听 `127.0.0.1`。`80/443` 由 Caddy 使用,用于 `panel.jp-proxy.ruichao.dev` 自动 HTTPS 和反向代理。

## Caddy 与 Librespeed

控制台公网入口由 Caddy 负责,不使用 Dokku。Caddy 自动签发/续期证书,并通过 `basic_auth` 提供第一层门禁。示例 Caddyfile:

```caddyfile
panel.jp-proxy.ruichao.dev {
  basic_auth {
    admin <caddy-bcrypt-hash>
  }
  reverse_proxy 127.0.0.1:18100
}

:80 {
  reverse_proxy 127.0.0.1:18000
}
```

生成 Caddy Basic Auth hash:

```bash
caddy hash-password --plaintext 'change-this-basic-auth-password'
```

现有 `linuxserver/librespeed` 不能继续占用公网 `80/tcp`;部署控制台前应先记录现有容器参数,再迁移到本机端口:

```bash
docker inspect librespeed > /root/librespeed.inspect.before-admin-panel.json
docker stop librespeed
docker rm librespeed
# 按 inspect 中的 volume/env 重新启动,端口改为:
# -p 127.0.0.1:18000:80
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
curl -I https://panel.jp-proxy.ruichao.dev/admin/
```

预期：

```json
{"status":"ok"}
```

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
