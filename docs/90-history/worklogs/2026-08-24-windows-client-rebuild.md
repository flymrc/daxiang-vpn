# 2026-08-24 Windows 客户端重建

## 背景

确认生产 Hub 和 Android `zhreverse` 出口可用后，重新构建当前 Windows x64 桌面客户端。

## 构建

执行：

```powershell
.\scripts\build-desktop-gui.ps1 -Target x64
```

构建成功，版本保持为 `0.4.11`。产物：

- `clients/desktop-gui/src-tauri/target/x86_64-pc-windows-msvc/release/bundle/nsis/纵横 VPN_0.4.11_x64-setup.exe`
- 大小：`7.51 MB`
- SHA256：`023B477D781A47FBBA62F9558E50C3EF16E63EB302961A2A01CAFEC3F31658A7`

## 验证

- Tauri 前端、Rust release 主程序和 NSIS installer 均构建通过。
- 随包 `zhvpn-x86_64-pc-windows-msvc.exe version --json` 返回 `0.4.11`。
- 随包 `zhvpn-x86_64-pc-windows-msvc.exe status --json` 可正常执行；本机构建时未连接，返回 `running=false`。
- `npm install` 审计提示 4 个依赖漏洞（1 low、1 moderate、2 high），本次仅重建，未执行依赖升级或自动修复。

本次未修改架构、生产配置或线上状态。
