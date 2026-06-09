mod sysproxy;

use serde_json::{json, Value};
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Manager, WindowEvent};
use tauri_plugin_shell::ShellExt;

// Run the bundled dxvpn sidecar and return (success, stdout, stderr).
// All the hard parts (UAC elevation for --fast, detached engine, PID files)
// live inside dxvpn.exe itself; this layer only shells out and reads results.
async fn sidecar(app: &tauri::AppHandle, args: &[&str]) -> Result<(bool, String, String), String> {
    let cmd = app
        .shell()
        .sidecar("dxvpn")
        .map_err(|e| format!("无法定位 dxvpn：{e}"))?;
    let output = cmd
        .args(args.to_vec())
        .output()
        .await
        .map_err(|e| format!("执行 dxvpn 失败：{e}"))?;
    let stdout = String::from_utf8_lossy(&output.stdout).trim().to_string();
    let stderr = String::from_utf8_lossy(&output.stderr).trim().to_string();
    Ok((output.status.success(), stdout, stderr))
}

// Parse the single JSON line emitted by a `--json` command.
fn parse_json(stdout: &str, stderr: &str) -> Result<Value, String> {
    let line = stdout.lines().last().unwrap_or("").trim();
    serde_json::from_str::<Value>(line)
        .map_err(|e| format!("解析 dxvpn 输出失败：{e}（stdout={stdout} stderr={stderr}）"))
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

#[tauri::command]
async fn status(app: tauri::AppHandle) -> Result<Value, String> {
    let (_ok, stdout, stderr) = sidecar(&app, &["status", "--json"]).await?;
    parse_json(&stdout, &stderr)
}

#[tauri::command]
async fn login(app: tauri::AppHandle, token: String) -> Result<Value, String> {
    let (_ok, stdout, stderr) = sidecar(&app, &["login", token.as_str(), "--json"]).await?;
    parse_json(&stdout, &stderr)
}

#[tauri::command]
async fn connect(app: tauri::AppHandle, fast: bool) -> Result<Value, String> {
    let mut args = vec!["start"];
    if fast {
        args.push("--fast");
    }
    let (ok, stdout, stderr) = sidecar(&app, &args).await?;
    if ok && !fast {
        // 默认用户态模式：自动把系统代理指向本地代理，浏览器等无需手动设置。
        // --fast（系统 TUN）是全局路由，不需要也不应设系统代理。
        if let Ok((_, s_out, s_err)) = sidecar(&app, &["status", "--json"]).await {
            if let Ok(v) = parse_json(&s_out, &s_err) {
                if let Some((h, port)) = v
                    .get("proxy")
                    .and_then(|x| x.as_str())
                    .and_then(split_host_port)
                {
                    if let Err(e) = sysproxy::enable(&app, &h, port) {
                        return Ok(json!({
                            "ok": true,
                            "message": stdout,
                            "warning": format!("已连接，但自动设置系统代理失败：{e}")
                        }));
                    }
                }
            }
        }
    }
    Ok(json!({ "ok": ok, "message": message(ok, stdout, stderr) }))
}

#[tauri::command]
async fn disconnect(app: tauri::AppHandle) -> Result<Value, String> {
    let _ = sysproxy::restore(&app);
    let (ok, stdout, stderr) = sidecar(&app, &["stop"]).await?;
    Ok(json!({ "ok": ok, "message": message(ok, stdout, stderr) }))
}

#[tauri::command]
async fn rotate_ip(app: tauri::AppHandle) -> Result<Value, String> {
    let (_ok, stdout, stderr) = sidecar(&app, &["rotate-ip", "--json"]).await?;
    parse_json(&stdout, &stderr)
}

fn show_main(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.unminimize();
        let _ = w.set_focus();
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![
            status, login, connect, disconnect, rotate_ip
        ])
        .setup(|app| {
            // 托盘：左键打开主界面，右键菜单 打开/退出。退出时还原系统代理。
            let show = MenuItem::with_id(app, "show", "打开主界面", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &quit])?;
            let _tray = TrayIconBuilder::new()
                .icon(app.default_window_icon().unwrap().clone())
                .tooltip("大象 VPN")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => show_main(app),
                    "quit" => {
                        let _ = sysproxy::restore(app);
                        app.exit(0);
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

            // 启动对账：若存在上次会话遗留的代理备份，且代理实际未在运行，则还原
            // （覆盖 GUI 上次崩溃 / 强退导致系统代理悬挂指向死掉的本地端口的情况）。
            let handle = app.handle().clone();
            tauri::async_runtime::spawn(async move {
                if sysproxy::has_backup(&handle) {
                    if let Ok((_, out, err)) = sidecar(&handle, &["status", "--json"]).await {
                        if let Ok(v) = parse_json(&out, &err) {
                            let running =
                                v.get("running").and_then(|x| x.as_bool()).unwrap_or(false);
                            let reachable = v
                                .get("proxy_reachable")
                                .and_then(|x| x.as_bool())
                                .unwrap_or(false);
                            if !running && !reachable {
                                let _ = sysproxy::restore(&handle);
                            }
                        }
                    }
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
