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
