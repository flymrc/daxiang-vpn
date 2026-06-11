mod sysproxy;

use serde_json::{json, Value};
use std::sync::OnceLock;
use std::time::Duration;
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, WindowEvent};
use tauri_plugin_shell::ShellExt;
use tokio::sync::Mutex;

static STATUS_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

// Run the bundled zhvpn sidecar and return (success, stdout, stderr).
// All the hard parts (UAC elevation for --fast, detached engine, PID files)
// live inside zhvpn.exe itself; this layer only shells out and reads results.
async fn sidecar(app: &AppHandle, args: &[&str]) -> Result<(bool, String, String), String> {
    let cmd = app
        .shell()
        .sidecar("zhvpn")
        .map_err(|e| format!("无法定位 zhvpn：{e}"))?;
    let output = cmd
        .args(args.to_vec())
        .output()
        .await
        .map_err(|e| format!("执行 zhvpn 失败：{e}"))?;
    let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();
    let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
    Ok((output.status.success(), stdout, stderr))
}

// Parse the single JSON line emitted by a `--json` command.
fn parse_json(stdout: &str, stderr: &str) -> Result<Value, String> {
    let line = stdout.lines().last().unwrap_or("").trim();
    serde_json::from_str::<Value>(line)
        .map_err(|e| format!("解析 zhvpn 输出失败：{e}（stdout={stdout} stderr={stderr}）"))
}

// Pick the most useful message from a non-JSON command (start/stop).
fn message(ok: bool, stdout: String, stderr: String) -> String {
    if ok {
        stdout
    } else if !stderr.is_empty() {
        stderr
    } else {
        stdout
    }
}

// Split "host:port" (host may be IPv4 / hostname, no brackets).
fn split_host_port(addr: &str) -> Option<(String, u16)> {
    let idx = addr.rfind(':')?;
    let host = &addr[..idx];
    let port = addr[idx + 1..].parse::<u16>().ok()?;
    if host.is_empty() {
        return None;
    }
    Some((host.to_string(), port))
}

fn connected_in(status: &Value) -> bool {
    status
        .get("running")
        .and_then(|x| x.as_bool())
        .unwrap_or(false)
        || status
            .get("proxy_reachable")
            .and_then(|x| x.as_bool())
            .unwrap_or(false)
}

async fn enable_system_proxy_from_status(app: &AppHandle) -> Result<(), String> {
    let (_ok, s_out, s_err) = sidecar(app, &["status", "--json"]).await?;
    let v = parse_json(&s_out, &s_err)?;
    let (host, port) = v
        .get("proxy")
        .and_then(|x| x.as_str())
        .and_then(split_host_port)
        .ok_or_else(|| "未从状态里读到本地代理地址".to_string())?;
    sysproxy::enable(app, &host, port).map_err(|e| e.to_string())
}

// ---- shared action implementations (used by both #[command]s and the tray) ----

async fn status_impl(app: &AppHandle) -> Result<Value, String> {
    let _guard = STATUS_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    let (_ok, stdout, stderr) = sidecar(app, &["status", "--json"]).await?;
    parse_json(&stdout, &stderr)
}

async fn login_impl(app: &AppHandle, token: &str) -> Result<Value, String> {
    let (_ok, stdout, stderr) = sidecar(app, &["login", token, "--json"]).await?;
    parse_json(&stdout, &stderr)
}

async fn connect_impl(app: &AppHandle, global_proxy: bool) -> Result<Value, String> {
    let args = vec!["start"];
    let (ok, stdout, stderr) = sidecar(app, &args).await?;
    let msg = message(ok, stdout, stderr);
    if ok && global_proxy {
        // GUI 默认只启动本地代理。用户勾选“全局代理”时，才把 Windows
        // 系统代理指向本地代理；断开/退出时统一还原。
        if let Err(e) = enable_system_proxy_from_status(app).await {
            return Ok(json!({
                "ok": true,
                "message": msg,
                "warning": format!("已连接，但自动设置系统代理失败：{e}")
            }));
        }
    }
    Ok(json!({ "ok": ok, "message": msg }))
}

async fn disconnect_impl(app: &AppHandle) -> Result<Value, String> {
    let _ = sysproxy::restore(app);
    let (ok, stdout, stderr) = sidecar(app, &["stop"]).await?;
    Ok(json!({ "ok": ok, "message": message(ok, stdout, stderr) }))
}

async fn rotate_impl(app: &AppHandle) -> Result<Value, String> {
    let (_ok, stdout, stderr) = sidecar(app, &["rotate-ip", "--json"]).await?;
    parse_json(&stdout, &stderr)
}

async fn logout_impl(app: &AppHandle) -> Result<Value, String> {
    // 先断开（还原系统代理 + 停引擎），再清掉配置，回到登录页。
    let _ = disconnect_impl(app).await;
    let (ok, stdout, stderr) = sidecar(app, &["logout", "--json"]).await?;
    parse_json(&stdout, &stderr)
        .or_else(|_| Ok(json!({ "ok": ok, "message": message(ok, stdout, stderr) })))
}

// ---- Tauri commands (frontend) just delegate to the impls ----

#[tauri::command]
async fn status(app: AppHandle) -> Result<Value, String> {
    status_impl(&app).await
}

#[tauri::command]
async fn login(app: AppHandle, token: String) -> Result<Value, String> {
    login_impl(&app, &token).await
}

#[tauri::command]
async fn connect(app: AppHandle, fast: bool) -> Result<Value, String> {
    // The frontend argument is still named `fast` for Tauri API compatibility,
    // but it now means "enable Windows system proxy".
    connect_impl(&app, fast).await
}

#[tauri::command]
async fn disconnect(app: AppHandle) -> Result<Value, String> {
    disconnect_impl(&app).await
}

#[tauri::command]
async fn rotate_ip(app: AppHandle) -> Result<Value, String> {
    rotate_impl(&app).await
}

#[tauri::command]
async fn logout(app: AppHandle) -> Result<Value, String> {
    logout_impl(&app).await
}

fn show_main(app: &AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // WebView 只加载打包进来的本地界面、所有功能走 Rust 命令，不发外部请求，
    // 因此不该走系统代理。连接后我们会把系统代理指向本地代理；若不强制 WebView
    // 绕过，它会把自己界面的请求也丢去走代理，代理处理不了内部域名 -> app 窗口里
    // 显示浏览器错误页。--no-proxy-server 让 WebView 永不使用任何代理，界面在任何
    // 网络/代理状态下都稳定加载（WinINET 的 *.localhost bypass 对 WebView2 不可靠）。
    std::env::set_var("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--no-proxy-server");

    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            status, login, connect, disconnect, rotate_ip, logout
        ])
        .setup(|app| {
            // 托盘菜单（仿 Tailscale）：状态行 + 连接/断开/换IP + 打开/退出。
            let status_item = MenuItem::with_id(app, "status", "○ 检查中…", false, None::<&str>)?;
            let connect_item = MenuItem::with_id(app, "connect", "连接", true, None::<&str>)?;
            let disconnect_item = MenuItem::with_id(app, "disconnect", "断开", true, None::<&str>)?;
            let rotate_item = MenuItem::with_id(app, "rotate", "换 IP", true, None::<&str>)?;
            let show_item = MenuItem::with_id(app, "show", "打开主界面", true, None::<&str>)?;
            let quit_item = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = Menu::with_items(
                app,
                &[
                    &status_item,
                    &PredefinedMenuItem::separator(app)?,
                    &connect_item,
                    &disconnect_item,
                    &rotate_item,
                    &PredefinedMenuItem::separator(app)?,
                    &show_item,
                    &quit_item,
                ],
            )?;
            let _tray = TrayIconBuilder::with_id("main")
                .icon(app.default_window_icon().unwrap().clone())
                .tooltip("纵横 VPN")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => show_main(app),
                    "quit" => {
                        let _ = sysproxy::restore(app);
                        app.exit(0);
                    }
                    "connect" => {
                        let a = app.clone();
                        tauri::async_runtime::spawn(async move {
                            let _ = connect_impl(&a, false).await;
                        });
                    }
                    "disconnect" => {
                        let a = app.clone();
                        tauri::async_runtime::spawn(async move {
                            let _ = disconnect_impl(&a).await;
                        });
                    }
                    "rotate" => {
                        let a = app.clone();
                        tauri::async_runtime::spawn(async move {
                            let _ = rotate_impl(&a).await;
                        });
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        show_main(tray.app_handle());
                    }
                })
                .build(app)?;

            // 轮询状态，实时刷新托盘状态行/可点项/提示。
            let handle = app.handle().clone();
            let status_i = status_item.clone();
            let connect_i = connect_item.clone();
            let disconnect_i = disconnect_item.clone();
            let rotate_i = rotate_item.clone();
            tauri::async_runtime::spawn(async move {
                // 启动对账：上次会话遗留代理备份且代理实际未运行（崩溃残留）则还原。
                if sysproxy::has_backup(&handle) {
                    if let Ok(v) = status_impl(&handle).await {
                        if !connected_in(&v) {
                            let _ = sysproxy::restore(&handle);
                        }
                    }
                }
                loop {
                    if let Ok(v) = status_impl(&handle).await {
                        let connected = connected_in(&v);
                        let ip = v.get("egress_ip").and_then(|x| x.as_str()).unwrap_or("");
                        let egress = v.get("egress").and_then(|x| x.as_str()).unwrap_or("");
                        let header = if connected {
                            let tail = if !ip.is_empty() { ip } else { egress };
                            format!("● 已连接 · {tail}")
                        } else {
                            "○ 未连接".to_string()
                        };
                        let _ = status_i.set_text(&header);
                        let _ = connect_i.set_enabled(!connected);
                        let _ = disconnect_i.set_enabled(connected);
                        let _ = rotate_i.set_enabled(connected);
                        if let Some(tray) = handle.tray_by_id("main") {
                            let tip = if connected {
                                "纵横 VPN · 已连接"
                            } else {
                                "纵横 VPN · 未连接"
                            };
                            let _ = tray.set_tooltip(Some(tip));
                        }
                    }
                    tokio::time::sleep(Duration::from_secs(5)).await;
                }
            });
            Ok(())
        })
        // 关窗 = 收进托盘（保持连接），真正退出走托盘「退出」。
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
