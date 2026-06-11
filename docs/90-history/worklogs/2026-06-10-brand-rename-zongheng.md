# 2026-06-10 品牌与内部标识切换为纵横 / Zongheng

## 背景

项目品牌名切换为“纵横”，并要求旧品牌的英文/内部标识也同步替换。

## 变更

- 中文品牌：`大象` → `纵横`。
- 英文品牌与模块：`Daxiang` / `daxiang` → `Zongheng` / `zongheng`。
- 客户端命令与包名：`dxvpn` / `DXVPN` → `zhvpn` / `ZHVPN`。
- Hub 环境变量：`DXHUB_*` → `ZHHUB_*`。
- Android reverse：`dxreverse` / `DXREV*` → `zhreverse` / `ZHREV*`。
- Android control/status：`dxandroid` / `dxadb` / `DXCTL` 等 → `zhandroid` / `zhadb` / `ZHCTL` 等。
- Go module：`daxiang-vpn` → `zongheng-vpn`。
- Android status applicationId / namespace：`dev.zongheng.zhandroidstatus`。
- Tauri package / binary / sidecar：`zhvpn-desktop` + `zhvpn`。
- macOS launchd plist 与辅助脚本文件名同步改为 `com.zongheng.zhvpn.*` / `zhvpn-*`。
- 文档、runbook、worklog、示例 token 前缀同步从 `DX-*` 改为 `ZH-*`。

## 未改动

- GitHub remote 未改，仍是 `git@github.com:flymrc/daxiang-vpn.git`。
- 生产主机、远端 systemd/service.d、真实 token、真实已部署路径未执行变更；这些需要单独授权部署/迁移。

## 目录变更

- 本地工作区目录已从 `workshops/products/daxiang-vpn` 改为 `workshops/products/zongheng-vpn`。
- 父仓库 `.gitmodules` 的 submodule path 已同步为 `workshops/products/zongheng-vpn`，url 保持旧 GitHub 地址。

## 验证

- `git grep` 检查旧中文/英文品牌与旧内部标识：除标准 `SPDX-License-Identifier` 外无旧品牌匹配。
- 文件名搜索旧 `daxiang` / `dx*` 相关旧标识：工作区源文件路径无匹配。
- `go test ./...` 通过，module 显示为 `zongheng-vpn/...`。
- Windows CLI sidecar 重建通过：`GOOS=windows GOARCH=amd64 GOAMD64=v1 go build -o clients/desktop-gui/src-tauri/binaries/zhvpn-x86_64-pc-windows-msvc.exe ./clients/cli`，产物为 PE32+ x86-64。
- Android 出口 Go 组件交叉编译通过：`zhreverse`、`zhandroid-control` 为 linux/arm64 静态 ELF。
- `npm ci` 通过。
- `npm run check`：0 errors，1 warning（缺 `@types/node` type definition，既有问题）。
- `npm run build` 通过，Svelte static build 写入 `clients/desktop-gui/build`。
- `cargo check --manifest-path clients/desktop-gui/src-tauri/Cargo.toml --target x86_64-pc-windows-msvc` 通过（本机补装 rust stable minimal 和 `x86_64-pc-windows-msvc` target 后）。
- `npm run tauri -- build --target x86_64-pc-windows-msvc` 未完成：macOS 环境缺 MSVC `link.exe` / cargo-xwin，无法在当前机器直接链接 Windows Tauri 安装包。
- Android status `sh ./gradlew assembleDebug` 未通过：本机缺 Java Runtime。
