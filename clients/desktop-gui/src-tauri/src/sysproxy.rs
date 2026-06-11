// Windows 系统代理（WinINET）的设置 / 还原。
//
// 默认用户态模式下，zhvpn 只起一个本地代理（127.0.0.1:7890），不会自动让系统流量
// 走它。这里在「连接」时把系统代理指过去、在「断开/退出」时还原，让浏览器等开箱即用。
//
// 还原依赖一个备份文件：首次打开代理时把原状态写盘，断开/退出还原后删除。即使 GUI
// 崩溃，下次启动也能据此把用户的系统代理恢复回去（见 lib.rs 的启动对账）。

use serde::{Deserialize, Serialize};
use std::fs;
use std::path::PathBuf;
use tauri::Manager;
use winapi::shared::ntdef::NULL;
use winapi::um::wininet::{
    InternetSetOptionA, INTERNET_OPTION_REFRESH, INTERNET_OPTION_SETTINGS_CHANGED,
};
use winreg::{enums, RegKey};

// 不走代理的地址：本机回环 + 内网段（含 WireGuard 隧道 10.66.0.0/16）。
// `*.localhost` 必须有：Tauri 在 Windows 用 http://tauri.localhost 提供自身界面，
// 不排除的话连接后 WebView 会把自己的资源请求丢去走代理，导致 app 窗口里显示错误页。
const BYPASS: &str =
    "localhost;*.localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;192.168.*;<local>";
const INTERNET_SETTINGS: &str = "SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Internet Settings";

#[derive(Serialize, Deserialize)]
struct ProxyState {
    enable: bool,
    #[serde(default)]
    server: String,
    #[serde(default)]
    host: String,
    #[serde(default)]
    port: u16,
    #[serde(default)]
    bypass: String,
    #[serde(default)]
    auto_config_url: String,
}

fn backup_path(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let dir = app.path().app_config_dir().map_err(|e| e.to_string())?;
    Ok(dir.join("proxy-backup.json"))
}

fn internet_settings_read() -> Result<RegKey, String> {
    RegKey::predef(enums::HKEY_CURRENT_USER)
        .open_subkey_with_flags(INTERNET_SETTINGS, enums::KEY_READ)
        .map_err(|e| format!("读取系统代理失败：{e}"))
}

fn internet_settings_write() -> Result<RegKey, String> {
    RegKey::predef(enums::HKEY_CURRENT_USER)
        .open_subkey_with_flags(INTERNET_SETTINGS, enums::KEY_SET_VALUE)
        .map_err(|e| format!("打开系统代理设置失败：{e}"))
}

fn flush_settings() {
    unsafe {
        InternetSetOptionA(NULL, INTERNET_OPTION_SETTINGS_CHANGED, NULL, 0);
        InternetSetOptionA(NULL, INTERNET_OPTION_REFRESH, NULL, 0);
    }
}

fn current() -> ProxyState {
    let Ok(key) = internet_settings_read() else {
        return ProxyState {
            enable: false,
            server: String::new(),
            host: String::new(),
            port: 0,
            bypass: String::new(),
            auto_config_url: String::new(),
        };
    };
    ProxyState {
        enable: key.get_value::<u32, _>("ProxyEnable").unwrap_or(0) == 1,
        server: key.get_value("ProxyServer").unwrap_or_default(),
        host: String::new(),
        port: 0,
        bypass: key.get_value("ProxyOverride").unwrap_or_default(),
        auto_config_url: key.get_value("AutoConfigURL").unwrap_or_default(),
    }
}

/// 打开系统代理指向本地代理；首次打开时把原状态备份到磁盘。
pub fn enable(app: &tauri::AppHandle, host: &str, port: u16) -> Result<(), String> {
    let path = backup_path(app)?;
    if !path.exists() {
        let st = current();
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).map_err(|e| e.to_string())?;
        }
        fs::write(
            &path,
            serde_json::to_string(&st).map_err(|e| e.to_string())?,
        )
        .map_err(|e| e.to_string())?;
    }
    let key = internet_settings_write()?;
    let server = format!("http={host}:{port};https={host}:{port};socks={host}:{port}");
    key.set_value("ProxyEnable", &1u32)
        .map_err(|e| format!("设置系统代理失败：{e}"))?;
    key.set_value("ProxyServer", &server)
        .map_err(|e| format!("设置系统代理失败：{e}"))?;
    key.set_value("ProxyOverride", &BYPASS)
        .map_err(|e| format!("设置系统代理失败：{e}"))?;
    let _: Result<(), _> = key.delete_value("AutoConfigURL");
    flush_settings();
    Ok(())
}

/// 还原到备份的系统代理状态（无备份则直接关闭），并删除备份文件。
pub fn restore(app: &tauri::AppHandle) -> Result<(), String> {
    let path = backup_path(app)?;
    match fs::read_to_string(&path) {
        Ok(data) => {
            if let Ok(st) = serde_json::from_str::<ProxyState>(&data) {
                if let Ok(key) = internet_settings_write() {
                    let server = if !st.server.is_empty() {
                        st.server
                    } else if !st.host.is_empty() && st.port != 0 {
                        format!("{}:{}", st.host, st.port)
                    } else {
                        String::new()
                    };
                    let _ = key.set_value("ProxyEnable", &(if st.enable { 1u32 } else { 0u32 }));
                    let _ = key.set_value("ProxyServer", &server);
                    let _ = key.set_value("ProxyOverride", &st.bypass);
                    if st.auto_config_url.is_empty() {
                        let _: Result<(), _> = key.delete_value("AutoConfigURL");
                    } else {
                        let _ = key.set_value("AutoConfigURL", &st.auto_config_url);
                    }
                    flush_settings();
                }
            }
            let _ = fs::remove_file(&path);
            Ok(())
        }
        Err(_) => disable(),
    }
}

fn disable() -> Result<(), String> {
    let key = internet_settings_write()?;
    key.set_value("ProxyEnable", &0u32)
        .map_err(|e| format!("关闭系统代理失败：{e}"))?;
    flush_settings();
    Ok(())
}

/// 是否存在未还原的备份（上次会话设过代理但没干净还原，可能是崩溃残留）。
pub fn has_backup(app: &tauri::AppHandle) -> bool {
    backup_path(app).map(|p| p.exists()).unwrap_or(false)
}
