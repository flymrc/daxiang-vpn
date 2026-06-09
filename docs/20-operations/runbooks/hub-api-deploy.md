# Hub 授权 API 部署

## 构建

在本机仓库执行：

```powershell
cd c:\Users\xuotq\daxiang-vpn
$env:GOOS="linux"
$env:GOARCH="amd64"
go build -o dist/linux-amd64/dxhub ./hub
Remove-Item Env:\GOOS, Env:\GOARCH
```

## Hub 服务器目录

建议部署到：

```text
/opt/daxiang-vpn/dxhub
```

文件：

```text
/opt/daxiang-vpn/dxhub/dxhub
/opt/daxiang-vpn/dxhub/tokens.yaml
```

## 环境变量

```text
DXHUB_TOKENS=/opt/daxiang-vpn/dxhub/tokens.yaml
DXHUB_LISTEN=0.0.0.0:18080
DXHUB_ANDROID_CONTROL_KEY=/root/.ssh/dxandroid_control_hub
DXHUB_TOKEN_LEASE_SECONDS=30
```

## systemd 服务

```ini
[Unit]
Description=Daxiang VPN Hub API
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/daxiang-vpn/dxhub
Environment=DXHUB_TOKENS=/opt/daxiang-vpn/dxhub/tokens.yaml
Environment=DXHUB_LISTEN=0.0.0.0:18080
Environment=DXHUB_ANDROID_CONTROL_KEY=/root/.ssh/dxandroid_control_hub
Environment=DXHUB_TOKEN_LEASE_SECONDS=30
ExecStart=/opt/daxiang-vpn/dxhub/dxhub
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

保存为：

```text
/etc/systemd/system/dxhub.service
```

启动：

```bash
systemctl daemon-reload
systemctl enable --now dxhub
systemctl status dxhub
```

## 防火墙

Hub 需要开放：

```text
TCP 18080
```

当前 MVP 先用 HTTP。正式生产建议加 HTTPS 或反向代理。

## 验证

```bash
curl http://127.0.0.1:18080/healthz
```

预期：

```json
{"status":"ok"}
```

## 客户端 token 单来源租约

`/api/client/bootstrap` 会按 token 记录最近一次成功 bootstrap 的来源 IP。默认 `DXHUB_TOKEN_LEASE_SECONDS=30`：

- 同一个 token 在同一公网来源继续 bootstrap，会刷新租约。
- 同一个 token 在不同公网来源 30 秒内再次 bootstrap，会返回 `409 {"error":"token_in_use"}`。
- 设置 `DXHUB_TOKEN_LEASE_SECONDS=0` 可关闭该保护。

来源识别只信任本机或内网反向代理传入的 `X-Forwarded-For`；公网客户端直连时按 TCP 源地址计算，避免客户端伪造来源绕过 token 租约。

## Android 出口一键换 IP

Hub API 提供客户端无感入口:

```text
POST /api/client/rotate-ip
```

客户端只提交自己的授权 token 和断网秒数,Hub 校验 token 后通过 Android 控制面 SSH 触发 `/data/adb/dxandroid/rotate-ip.sh`。客户机不需要 Android SSH 私钥,也不需要知道跳板。

Hub 侧需要一把无 passphrase 的服务专用 key:

```bash
ssh-keygen -t ed25519 -N "" \
  -C "dxhub-android-control@36.50.84.68" \
  -f /root/.ssh/dxandroid_control_hub
chmod 600 /root/.ssh/dxandroid_control_hub
```

把公钥追加到 Android:

```bash
cat /root/.ssh/dxandroid_control_hub.pub
# 将输出追加到 Android /data/adb/dxandroid/.ssh/authorized_keys
```

当前 Hub 公钥:

```text
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMFnqYTgTqmQkJ314sFHCuaHd5q4NvrjsWZwNsR8E5H7 dxhub-android-control@36.50.84.68
```

> 若 API 返回 `control_failed`,优先检查 `DXHUB_ANDROID_CONTROL_KEY` 指向的私钥是否无 passphrase,以及对应公钥是否已在 Android `authorized_keys` 中。
