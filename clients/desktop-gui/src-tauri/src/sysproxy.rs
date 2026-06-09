// Windows 系统代理（WinINET）的设置 / 还原。
//
// 默认用户态模式下，dxvpn 只起一个本地代理（127.0.0.1:7890），不会自动让系统流量
// 走它。这里在「连接」时把系统代理指过去、在「断开/退出」时还原，让浏览器等开箱即用。
//
// 还原依赖一个备份文件：首次打开代理时把原状态写盘，断开/退出还原后删除。即使 GUI
// 崩溃，下次启动也能据此把用户的系统代理恢复回去（见 lib.rs 的启动对账）。

use serde::{Deserialize, Serialize};
use std::fs;
use std::path::PathBuf;
use sysproxy::Sysproxy;
use tauri::Manager;

// 不走代理的地址：本机回环 + 内网段（含 WireGuard 隧道 10.66.0.0/16）。
// `*.localhost` 必须有：Tauri 在 Windows 用 http://tauri.localhost 提供自身界面，
// 不排除的话连接后 WebView 会把自己的资源请求丢去走代理，导致 app 窗口里显示错误页。
const BYPASS: &str =
    "localhost;*.localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;192.168.*;<local>";

#[derive(Serialize, Deserialize)]
struct ProxyState {
    enable: bool,
    host: String,
    port: u16,
    bypass: String,
}

fn backup_path(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let dir = app.path().app_config_dir().map_err(|e| e.to_string())?;
    Ok(dir.join("proxy-backup.json"))
}

fn current() -> Result<Sysproxy, String> {
    Sysproxy::get_system_proxy().map_err(|e| format!("读取系统代理失败：{e}"))
}

/// 打开系统代理指向本地代理；首次打开时把原状态备份到磁盘。
pub fn enable(app: &tauri::AppHandle, host: &str, port: u16) -> Result<(), String> {
    let path = backup_path(app)?;
    if !path.exists() {
        // 读当前系统代理做备份；读失败（本来没设代理，或注册表里是 ":0" 这类空值，
        // sysproxy 解析会报 "failed to parse string"）就备份一个"关闭"状态，不要因此中断设置。
        let st = match current() {
            Ok(cur) => ProxyState {
                enable: cur.enable,
                host: cur.host,
                port: cur.port,
                bypass: cur.bypass,
            },
            Err(_) => ProxyState {
                enable: false,
                host: String::new(),
                port: 0,
                bypass: String::new(),
            },
        };
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).map_err(|e| e.to_string())?;
        }
        fs::write(
            &path,
            serde_json::to_string(&st).map_err(|e| e.to_string())?,
        )
        .map_err(|e| e.to_string())?;
    }
    let sp = Sysproxy {
        enable: true,
        host: host.to_string(),
        port,
        bypass: BYPASS.to_string(),
    };
    sp.set_system_proxy()
        .map_err(|e| format!("设置系统代理失败：{e}"))
}

/// 还原到备份的系统代理状态（无备份则直接关闭），并删除备份文件。
pub fn restore(app: &tauri::AppHandle) -> Result<(), String> {
    let path = backup_path(app)?;
    match fs::read_to_string(&path) {
        Ok(data) => {
            if let Ok(st) = serde_json::from_str::<ProxyState>(&data) {
                let sp = Sysproxy {
                    enable: st.enable,
                    host: st.host,
                    port: st.port,
                    bypass: st.bypass,
                };
                let _ = sp.set_system_proxy();
            }
            let _ = fs::remove_file(&path);
            Ok(())
        }
        Err(_) => disable(),
    }
}

fn disable() -> Result<(), String> {
    let mut sp = current().unwrap_or(Sysproxy {
        enable: false,
        host: String::new(),
        port: 0,
        bypass: String::new(),
    });
    sp.enable = false;
    sp.set_system_proxy()
        .map_err(|e| format!("关闭系统代理失败：{e}"))
}

/// 是否存在未还原的备份（上次会话设过代理但没干净还原，可能是崩溃残留）。
pub fn has_backup(app: &tauri::AppHandle) -> bool {
    backup_path(app).map(|p| p.exists()).unwrap_or(false)
}
