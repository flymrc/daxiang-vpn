mod proxy_journal;
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
static ACTION_LOCK: OnceLock<Mutex<()>> = OnceLock::new();
static INSTANCE_MUTEX: OnceLock<WindowsInstanceMutex> = OnceLock::new();
static SYSTEM_PROXY_ERROR: std::sync::Mutex<Option<String>> = std::sync::Mutex::new(None);

fn record_proxy_result(result: Result<(), String>) -> Result<(), String> {
    let mut error = SYSTEM_PROXY_ERROR
        .lock()
        .unwrap_or_else(|poison| poison.into_inner());
    *error = result.as_ref().err().cloned();
    if let Err(message) = &result {
        eprintln!("{message}");
    }
    result
}

fn with_proxy_error(mut status: Value) -> Value {
    let error = SYSTEM_PROXY_ERROR
        .lock()
        .unwrap_or_else(|poison| poison.into_inner());
    if let (Some(message), Some(object)) = (error.as_ref(), status.as_object_mut()) {
        object.insert("system_proxy_error".to_string(), json!(message));
    }
    status
}

fn proxy_status_result(result: Result<Value, String>) -> Result<Value, String> {
    match result {
        Ok(status) => Ok(with_proxy_error(status)),
        Err(message) => {
            let error = SYSTEM_PROXY_ERROR
                .lock()
                .unwrap_or_else(|poison| poison.into_inner());
            match error.as_ref() {
                Some(recovery) => Err(format!("{recovery}\n状态读取失败：{message}")),
                None => Err(message),
            }
        }
    }
}

fn restore_system_proxy(app: &AppHandle) -> Result<(), String> {
    record_proxy_result(sysproxy::restore(app))
}

#[cfg(windows)]
struct WindowsInstanceMutex(winapi::shared::ntdef::HANDLE);

#[cfg(windows)]
unsafe impl Send for WindowsInstanceMutex {}
#[cfg(windows)]
unsafe impl Sync for WindowsInstanceMutex {}

#[cfg(windows)]
impl Drop for WindowsInstanceMutex {
    fn drop(&mut self) {
        unsafe {
            winapi::um::handleapi::CloseHandle(self.0);
        }
    }
}

#[cfg(windows)]
fn acquire_single_instance() -> bool {
    use std::ffi::OsStr;
    use std::os::windows::ffi::OsStrExt;
    use winapi::shared::winerror::ERROR_ALREADY_EXISTS;
    use winapi::um::errhandlingapi::GetLastError;
    use winapi::um::synchapi::CreateMutexW;

    let name: Vec<u16> = OsStr::new("Local\\ZonghengVPNDesktop")
        .encode_wide()
        .chain(Some(0))
        .collect();
    let handle = unsafe { CreateMutexW(std::ptr::null_mut(), 1, name.as_ptr()) };
    if handle.is_null() {
        return true;
    }
    let already_exists = unsafe { GetLastError() } == ERROR_ALREADY_EXISTS;
    if already_exists {
        show_existing_instance();
        unsafe {
            winapi::um::handleapi::CloseHandle(handle);
        }
        return false;
    }
    let _ = INSTANCE_MUTEX.set(WindowsInstanceMutex(handle));
    true
}

#[cfg(windows)]
fn show_existing_instance() {
    use std::ffi::OsStr;
    use std::os::windows::ffi::OsStrExt;
    use winapi::um::winuser::{FindWindowW, SetForegroundWindow, ShowWindow, SW_RESTORE, SW_SHOW};

    let title: Vec<u16> = OsStr::new("纵横 VPN")
        .encode_wide()
        .chain(Some(0))
        .collect();
    let hwnd = unsafe { FindWindowW(std::ptr::null(), title.as_ptr()) };
    if hwnd.is_null() {
        return;
    }
    unsafe {
        ShowWindow(hwnd, SW_SHOW);
        ShowWindow(hwnd, SW_RESTORE);
        SetForegroundWindow(hwnd);
    }
}

#[cfg(not(windows))]
fn acquire_single_instance() -> bool {
    true
}

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

fn ready_proxy_address(command_ok: bool, status: &Value) -> Result<(String, u16), String> {
    let refuse = |reason: &str| {
        format!("system_proxy_enable_refused: 未启用全局代理（{reason}）；请确认客户端引擎就绪后重试连接")
    };
    if !command_ok {
        return Err(refuse("zhvpn 状态命令失败"));
    }
    // These are the CLI statusResult fields, not the configured proxy address
    // alone. Missing/incorrectly typed or contradictory observations fail closed.
    if status.get("running").and_then(Value::as_bool) != Some(true) {
        return Err(refuse("引擎未运行或运行状态缺失"));
    }
    if status.get("engine_state").and_then(Value::as_str) != Some("ready") {
        return Err(refuse("引擎未就绪或引擎状态缺失"));
    }
    if status.get("proxy_reachable").and_then(Value::as_bool) != Some(true) {
        return Err(refuse("本地代理不可达或可达状态缺失"));
    }
    // The CLI omits these fields on success. Even a present empty/malformed
    // error is not accepted as evidence that it is safe to change Windows.
    if status.get("error").is_some() || status.get("error_code").is_some() {
        return Err(refuse("状态包含错误"));
    }
    status
        .get("proxy")
        .and_then(Value::as_str)
        .and_then(split_host_port)
        .filter(|(_, port)| *port != 0)
        .ok_or_else(|| refuse("本地代理地址缺失或无效"))
}

fn enable_proxy_from_observation(
    observation: Result<(bool, Value), String>,
    enable: impl FnOnce(&str, u16) -> Result<(), String>,
) -> Result<(), String> {
    let (command_ok, status) = observation.map_err(|error| {
        format!(
            "system_proxy_enable_refused: 无法确认引擎状态，未启用全局代理；请重试连接：{error}"
        )
    })?;
    let (host, port) = ready_proxy_address(command_ok, &status)?;
    // Validation is completed before the adapter can access a journal/registry.
    // The engine can still fail after observation: this is not an atomic lease
    // shared with the CLI controller (that migration remains pending).
    enable(&host, port)
}

async fn enable_system_proxy_from_status(app: &AppHandle) -> Result<(), String> {
    let observation = match sidecar(app, &["status", "--json", "--no-ip-check"]).await {
        Ok((ok, stdout, stderr)) => parse_json(&stdout, &stderr).map(|status| (ok, status)),
        Err(error) => Err(error),
    };
    record_proxy_result(enable_proxy_from_observation(observation, |host, port| {
        sysproxy::enable(app, host, port)
    }))
}

// ---- shared action implementations (used by both #[command]s and the tray) ----

async fn status_impl(app: &AppHandle) -> Result<Value, String> {
    let _guard = STATUS_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    let result = match sidecar(app, &["status", "--json", "--no-ip-check"]).await {
        Ok((_ok, stdout, stderr)) => parse_json(&stdout, &stderr),
        Err(error) => Err(error),
    };
    proxy_status_result(result)
}

async fn status_ip_impl(app: &AppHandle) -> Result<Value, String> {
    let _guard = STATUS_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    let result = match sidecar(app, &["status", "--json"]).await {
        Ok((_ok, stdout, stderr)) => parse_json(&stdout, &stderr),
        Err(error) => Err(error),
    };
    proxy_status_result(result)
}

async fn login_impl(app: &AppHandle, token: &str) -> Result<Value, String> {
    let (_ok, stdout, stderr) = sidecar(app, &["login", token, "--json"]).await?;
    parse_json(&stdout, &stderr)
}

async fn connect_impl(app: &AppHandle, global_proxy: bool, fast: bool) -> Result<Value, String> {
    let _guard = ACTION_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    let mut args = vec!["start"];
    if fast {
        args.push("--fast");
    }
    let (ok, stdout, stderr) = sidecar(app, &args).await?;
    let msg = message(ok, stdout, stderr);
    if ok && global_proxy {
        // GUI 默认只启动本地代理。用户勾选“全局代理”时，才把 Windows
        // 系统代理指向本地代理；断开/退出时统一还原。
        if let Err(e) = enable_system_proxy_from_status(app).await {
            return Ok(json!({
                "ok": true,
                "message": msg,
                "warning": format!("未能安全启用全局代理：{e}")
            }));
        }
    }
    Ok(json!({ "ok": ok, "message": msg }))
}

async fn disconnect_impl(app: &AppHandle) -> Result<Value, String> {
    let _guard = ACTION_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    disconnect_inner(app).await
}

async fn disconnect_inner(app: &AppHandle) -> Result<Value, String> {
    // Keep the engine available while recovery is unresolved. A failed restore
    // must not leave Windows pointing at an engine that we then silently stop.
    restore_system_proxy(app)?;
    let (ok, stdout, stderr) = sidecar(app, &["stop"]).await?;
    Ok(json!({ "ok": ok, "message": message(ok, stdout, stderr) }))
}

async fn rotate_impl(app: &AppHandle) -> Result<Value, String> {
    let (_ok, stdout, stderr) = sidecar(app, &["rotate-ip", "--json"]).await?;
    parse_json(&stdout, &stderr)
}

async fn logout_impl(app: &AppHandle) -> Result<Value, String> {
    let _guard = ACTION_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    // 先断开（还原系统代理 + 停引擎），再清掉配置，回到登录页。
    let disconnected = disconnect_inner(app).await?;
    if disconnected.get("ok").and_then(Value::as_bool) != Some(true) {
        return Ok(disconnected);
    }
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
async fn status_ip(app: AppHandle) -> Result<Value, String> {
    status_ip_impl(&app).await
}

#[tauri::command]
fn app_version() -> &'static str {
    env!("CARGO_PKG_VERSION")
}

#[tauri::command]
async fn login(app: AppHandle, token: String) -> Result<Value, String> {
    login_impl(&app, &token).await
}

#[tauri::command]
async fn connect(app: AppHandle, global_proxy: bool, fast: bool) -> Result<Value, String> {
    connect_impl(&app, global_proxy, fast).await
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
    if !acquire_single_instance() {
        return;
    }

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
            status,
            status_ip,
            app_version,
            login,
            connect,
            disconnect,
            rotate_ip,
            logout
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
                        let a = app.clone();
                        tauri::async_runtime::spawn(async move {
                            let _guard = ACTION_LOCK.get_or_init(|| Mutex::new(())).lock().await;
                            if restore_system_proxy(&a).is_ok() {
                                a.exit(0);
                            } else {
                                // Persistent status diagnostic + recovery file,
                                // not a transient notification lost on exit.
                                show_main(&a);
                            }
                        });
                    }
                    "connect" => {
                        let a = app.clone();
                        tauri::async_runtime::spawn(async move {
                            let _ = connect_impl(&a, false, false).await;
                        });
                    }
                    "disconnect" => {
                        let a = app.clone();
                        tauri::async_runtime::spawn(async move {
                            if disconnect_impl(&a).await.is_err() {
                                show_main(&a);
                            }
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
                {
                    let _guard = ACTION_LOCK.get_or_init(|| Mutex::new(())).lock().await;
                    match sysproxy::has_backup(&handle) {
                        Ok(true) => match status_impl(&handle).await {
                            Ok(v) if !connected_in(&v) => {
                                if restore_system_proxy(&handle).is_err() { show_main(&handle); }
                            }
                            Ok(_) => {}
                            Err(e) => {
                                let _ = record_proxy_result(Err(format!("无法确认代理是否仍在运行，恢复记录已保留；请重试断开或退出：{e}")));
                                show_main(&handle);
                            }
                        },
                        Ok(false) => {}
                        Err(e) => {
                            let _ = record_proxy_result(Err(e));
                            show_main(&handle);
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

#[cfg(test)]
mod tests {
    use super::*;

    // Synthetic JSON matching clients/cli/internal/app.statusResult. No sidecar
    // or user's Internet Settings is used by these readiness-gate tests.
    fn ready_cli_status() -> Value {
        json!({
            "running": true,
            "proxy": "127.0.0.1:7890",
            "proxy_reachable": true,
            "egress": "synthetic CLI DTO",
            "engine_state": "ready",
            "instance_id": "0123456789abcdef0123456789abcdef",
            "config_generation": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
            "control_protocol_version": 1
        })
    }

    #[test]
    fn ready_cli_observation_authorizes_exactly_one_proxy_enable() {
        let mut calls = 0;
        enable_proxy_from_observation(Ok((true, ready_cli_status())), |host, port| {
            calls += 1;
            assert_eq!((host, port), ("127.0.0.1", 7890));
            Ok(())
        })
        .unwrap();
        assert_eq!(calls, 1);
    }

    #[test]
    fn failed_command_or_invalid_output_never_enters_the_proxy_adapter() {
        for observation in [
            Ok((false, ready_cli_status())),
            Err("status execution failed".to_string()),
            parse_json("invalid synthetic status", "").map(|status| (true, status)),
        ] {
            let mut calls = 0;
            let error = enable_proxy_from_observation(observation, |_, _| {
                calls += 1;
                Ok(())
            })
            .unwrap_err();
            assert!(error.contains("system_proxy_enable_refused"));
            assert_eq!(calls, 0);
        }
    }

    #[test]
    fn stopped_degraded_or_incomplete_cli_status_never_enters_the_proxy_adapter() {
        let mut observations = Vec::new();
        for state in ["stopped", "degraded", "starting", "stopping"] {
            let mut status = ready_cli_status();
            status["engine_state"] = json!(state);
            observations.push(status);
        }
        for field in ["running", "engine_state", "proxy_reachable", "proxy"] {
            let mut missing = ready_cli_status();
            missing.as_object_mut().unwrap().remove(field);
            observations.push(missing);
            let mut wrong_type = ready_cli_status();
            wrong_type[field] = json!(42);
            observations.push(wrong_type);
        }
        for field in ["running", "proxy_reachable"] {
            let mut status = ready_cli_status();
            status[field] = json!(false);
            observations.push(status);
        }
        let mut stopped = ready_cli_status();
        stopped["running"] = json!(false);
        stopped["engine_state"] = json!("stopped");
        stopped["proxy_reachable"] = json!(false);
        stopped["port_occupied"] = json!(true);
        observations.push(stopped);
        for status in observations {
            let mut calls = 0;
            let error = enable_proxy_from_observation(Ok((true, status)), |_, _| {
                calls += 1;
                Ok(())
            })
            .unwrap_err();
            assert!(error.contains("system_proxy_enable_refused"));
            assert_eq!(calls, 0);
        }
    }

    #[test]
    fn cli_error_or_invalid_proxy_address_never_enters_the_proxy_adapter() {
        let mut observations = Vec::new();
        for field in ["error", "error_code"] {
            for value in [
                json!("engine_control_unavailable"),
                json!(""),
                Value::Null,
                json!(false),
            ] {
                let mut status = ready_cli_status();
                status[field] = value;
                observations.push(status);
            }
        }
        for address in ["", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:not-a-port"] {
            let mut status = ready_cli_status();
            status["proxy"] = json!(address);
            observations.push(status);
        }
        for status in observations {
            let mut calls = 0;
            assert!(enable_proxy_from_observation(Ok((true, status)), |_, _| {
                calls += 1;
                Ok(())
            })
            .is_err());
            assert_eq!(calls, 0);
        }
    }

    #[test]
    fn proxy_adapter_failure_after_readiness_validation_is_propagated() {
        let mut calls = 0;
        let error = enable_proxy_from_observation(Ok((true, ready_cli_status())), |_, _| {
            calls += 1;
            Err("system_proxy_write_failed: synthetic denial".to_string())
        })
        .unwrap_err();
        assert_eq!(calls, 1);
        assert!(error.contains("system_proxy_write_failed"));
    }

    #[test]
    fn recovery_error_remains_in_status_until_a_successful_retry() {
        let message = "system_proxy_restore_failed: 请保留恢复记录并重试退出";
        assert!(record_proxy_result(Err(message.to_string())).is_err());
        let status = with_proxy_error(json!({ "running": true, "proxy_reachable": true }));
        assert_eq!(
            status.get("system_proxy_error").and_then(Value::as_str),
            Some(message)
        );
        assert_eq!(status.get("running").and_then(Value::as_bool), Some(true));
        assert!(proxy_status_result(Err("sidecar unavailable".to_string()))
            .unwrap_err()
            .contains(message));
        assert!(record_proxy_result(Ok(())).is_ok());
        assert!(with_proxy_error(json!({ "running": false }))
            .get("system_proxy_error")
            .is_none());
        let refused = ready_proxy_address(false, &ready_cli_status()).unwrap_err();
        assert!(record_proxy_result(Err(refused.clone())).is_err());
        assert_eq!(
            with_proxy_error(ready_cli_status())
                .get("system_proxy_error")
                .and_then(Value::as_str),
            Some(refused.as_str())
        );
        record_proxy_result(Ok(())).unwrap();
    }
}
