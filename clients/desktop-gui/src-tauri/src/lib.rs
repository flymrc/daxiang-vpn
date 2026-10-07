mod proxy_journal;
#[cfg(windows)]
mod sysproxy;

use serde::Deserialize;
use serde_json::{json, Value};
use std::sync::OnceLock;
use std::time::Duration;
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, WindowEvent};
use tauri_plugin_shell::ShellExt;
use tauri_plugin_shell::process::CommandEvent;
use tokio::sync::Mutex;

static STATUS_LOCK: OnceLock<Mutex<()>> = OnceLock::new();
static ACTION_LOCK: OnceLock<Mutex<()>> = OnceLock::new();
#[cfg(windows)]
static INSTANCE_MUTEX: OnceLock<WindowsInstanceMutex> = OnceLock::new();
static SYSTEM_PROXY_ERROR: std::sync::Mutex<Option<String>> = std::sync::Mutex::new(None);
static GUI_PROXY_LEASE: std::sync::Mutex<Option<String>> = std::sync::Mutex::new(None);

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

#[cfg(windows)]
fn restore_legacy_proxy(app: &AppHandle) -> Result<(), String> {
    record_proxy_result(sysproxy::restore_legacy(app))
}

#[cfg(not(windows))]
fn restore_legacy_proxy(_app: &AppHandle) -> Result<(), String> {
    Ok(())
}

#[cfg(windows)]
fn backup_kind(app: &AppHandle) -> Result<Option<proxy_journal::BackupKind>, String> {
    sysproxy::backup_kind(app)
}

#[cfg(not(windows))]
fn backup_kind(_app: &AppHandle) -> Result<Option<proxy_journal::BackupKind>, String> {
    Ok(None)
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
        // A failed ownership check cannot authorize a second controller.
        show_startup_notice("无法确认客户端是否已经运行，请稍后重试。");
        return false;
    }
    let already_exists = unsafe { GetLastError() } == ERROR_ALREADY_EXISTS;
    if already_exists {
        // A window title is public and can belong to another application.
        // Do not activate an unverified HWND. Until a trusted activation
        // channel is available, leave the existing instance undisturbed.
        show_startup_notice("纵横 VPN 已经运行，请从系统托盘打开已有客户端。");
        unsafe {
            winapi::um::handleapi::CloseHandle(handle);
        }
        return false;
    }
    let _ = INSTANCE_MUTEX.set(WindowsInstanceMutex(handle));
    true
}

#[cfg(windows)]
fn show_startup_notice(message: &str) {
    use std::ffi::OsStr;
    use std::os::windows::ffi::OsStrExt;
    use winapi::um::winuser::{MessageBoxW, MB_ICONINFORMATION, MB_OK};

    let title: Vec<u16> = OsStr::new("纵横 VPN")
        .encode_wide()
        .chain(Some(0))
        .collect();
    let text: Vec<u16> = OsStr::new(message).encode_wide().chain(Some(0)).collect();
    unsafe {
        MessageBoxW(
            std::ptr::null_mut(),
            text.as_ptr(),
            title.as_ptr(),
            MB_OK | MB_ICONINFORMATION,
        );
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
    let _ = stderr;
    let line = stdout.lines().last().unwrap_or("").trim();
    serde_json::from_str::<Value>(line)
        // Damaged CLI output may contain credentials. Never echo raw output.
        .map_err(|_| "无法读取客户端响应，请重试；诊断输出未展示敏感内容。".to_string())
}

// Split "host:port" (host may be IPv4 / hostname, no brackets).
#[cfg(test)]
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
    status.get("running").and_then(Value::as_bool) == Some(true)
        && status.get("engine_state").and_then(Value::as_str) == Some("ready")
        && status
            .get("instance_id")
            .and_then(Value::as_str)
            .is_some_and(|s| valid_hex(s, 32))
        && status
            .get("config_generation")
            .and_then(Value::as_str)
            .is_some_and(|s| valid_hex(s, 64))
        && status
            .get("control_protocol_version")
            .and_then(Value::as_u64)
            == Some(1)
}

fn valid_hex(value: &str, len: usize) -> bool {
    value.len() == len
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

#[derive(Debug, Deserialize)]
struct SystemProxyReceipt {
    ok: bool,
    contract_version: u32,
    system_proxy_state: String,
    owned: bool,
    noop: bool,
    lease_id: Option<String>,
}

fn proxy_receipt(
    command_ok: bool,
    value: Value,
    expected: &str,
) -> Result<SystemProxyReceipt, String> {
    if !command_ok || value.get("ok").and_then(Value::as_bool) != Some(true) {
        return Err(value
            .get("error")
            .and_then(Value::as_str)
            .unwrap_or("系统代理操作未完成，请重试；客户端与恢复记录已保留。")
            .to_string());
    }
    let receipt: SystemProxyReceipt = serde_json::from_value(value)
        .map_err(|_| "无法确认系统代理操作结果，请保留恢复记录并重试。".to_string())?;
    let allowed = if expected == "inspect" {
        ["absent", "recorded", "foreign"].contains(&receipt.system_proxy_state.as_str())
    } else if expected == "recovered" {
        ["absent", "recovered"].contains(&receipt.system_proxy_state.as_str())
    } else {
        receipt.system_proxy_state == expected
    };
    if !receipt.ok
        || receipt.contract_version != 1
        || !allowed
        || receipt
            .lease_id
            .as_ref()
            .is_some_and(|id| !valid_hex(id, 32))
        || (expected == "acquired" && (!receipt.owned || receipt.lease_id.is_none()))
        || (["absent", "foreign"].contains(&receipt.system_proxy_state.as_str()) && receipt.owned)
        || (receipt.system_proxy_state == "absent" && receipt.lease_id.is_some())
        || (receipt.system_proxy_state == "absent" && !receipt.noop)
        || (["recorded", "foreign"].contains(&receipt.system_proxy_state.as_str())
            && receipt.lease_id.is_none())
        || (["released", "recovered"].contains(&receipt.system_proxy_state.as_str())
            && !receipt.noop
            && (!receipt.owned || receipt.lease_id.is_none()))
    {
        return Err("客户端返回的系统代理归属不完整，请保留恢复记录并重试。".to_string());
    }
    Ok(receipt)
}

async fn system_proxy_command(
    app: &AppHandle,
    action: &str,
    lease_id: Option<&str>,
) -> Result<SystemProxyReceipt, String> {
    let mut args = vec!["system-proxy", action, "--json"];
    if let Some(id) = lease_id {
        if !valid_hex(id, 32) {
            return Err("本次系统代理记录无效，请保留恢复记录。".to_string());
        }
        args.extend(["--lease-id", id]);
    }
    let (ok, stdout, stderr) = sidecar(app, &args).await?;
    let expected = match action {
        "acquire" => "acquired",
        "release" => "released",
        "recover" => "recovered",
        _ => "inspect",
    };
    let receipt = proxy_receipt(ok, parse_json(&stdout, &stderr)?, expected)?;
    if lease_id.is_some()
        && receipt.lease_id.as_deref().is_some()
        && receipt.lease_id.as_deref() != lease_id
    {
        return Err("系统代理归属已经变化，未确认释放，请保留恢复记录。".to_string());
    }
    Ok(receipt)
}

fn remember_gui_lease(receipt: &SystemProxyReceipt) {
    let mut lease = GUI_PROXY_LEASE
        .lock()
        .unwrap_or_else(|poison| poison.into_inner());
    // Reusing the same engine's pre-existing lease does not transfer ownership
    // from its CLI/SDK caller to this GUI session.
    if !receipt.noop || lease.as_deref() == receipt.lease_id.as_deref() {
        *lease = receipt.lease_id.clone();
    } else {
        // A previous GUI lease became obsolete; do not retain a stale release
        // handle, and do not adopt the replacement caller's lease.
        *lease = None;
    }
}

async fn release_gui_lease(app: &AppHandle) -> Result<(), String> {
    let lease = GUI_PROXY_LEASE
        .lock()
        .unwrap_or_else(|poison| poison.into_inner())
        .clone();
    if let Some(id) = lease {
        system_proxy_command(app, "release", Some(&id)).await?;
        *GUI_PROXY_LEASE
            .lock()
            .unwrap_or_else(|poison| poison.into_inner()) = None;
    }
    Ok(())
}

fn unresolved_unowned_proxy_error(has_cli_journal: bool) -> bool {
    has_cli_journal
        && GUI_PROXY_LEASE
            .lock()
            .unwrap_or_else(|poison| poison.into_inner())
            .is_none()
        && SYSTEM_PROXY_ERROR
            .lock()
            .unwrap_or_else(|poison| poison.into_inner())
            .is_some()
}

async fn quit_proxy_release(app: &AppHandle) -> Result<(), String> {
    if unresolved_unowned_proxy_error(
        backup_kind(app)? == Some(proxy_journal::BackupKind::CliLease),
    ) {
        return Err(
            "系统代理操作尚未确认完成，连接和恢复记录已保留；请先重试断开，再退出客户端。"
                .to_string(),
        );
    }
    restore_legacy_proxy(app)?;
    release_gui_lease(app).await
}

#[cfg(test)]
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

#[cfg(test)]
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

async fn acquire_system_proxy(app: &AppHandle) -> Result<(), String> {
    let result = system_proxy_command(app, "acquire", None)
        .await
        .map(|receipt| remember_gui_lease(&receipt));
    record_proxy_result(result)
}

async fn startup_proxy_recovery(app: &AppHandle) -> Result<(), String> {
    let kind = backup_kind(app)?;
    if kind.is_none() {
        return Ok(());
    }
    if kind == Some(proxy_journal::BackupKind::CliLease) {
        let receipt = system_proxy_command(app, "inspect", None).await?;
        if receipt.system_proxy_state == "foreign" || receipt.system_proxy_state == "absent" {
            return Ok(());
        }
    }
    let status = status_impl(app).await?;
    match status.get("engine_state").and_then(Value::as_str) {
        Some("stopped") => {
            if kind == Some(proxy_journal::BackupKind::LegacyGui) {
                restore_legacy_proxy(app)
            } else {
                system_proxy_command(app, "recover", None).await.map(|_| ())
            }
        }
        Some("ready" | "starting" | "stopping") => Ok(()),
        _ => Err(
            "无法确认原连接是否已经停止，代理恢复记录已保留；请检查连接状态后重试。".to_string(),
        ),
    }
}

// ---- shared action implementations (used by both #[command]s and the tray) ----

async fn status_impl(app: &AppHandle) -> Result<Value, String> {
    let _guard = STATUS_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    let result = match sidecar(app, &["status", "--json", "--no-ip-check"]).await {
        Ok((ok, stdout, stderr)) => status_payload(ok, &stdout, &stderr),
        Err(error) => Err(error),
    };
    proxy_status_result(result)
}

async fn status_ip_impl(app: &AppHandle) -> Result<Value, String> {
    let _guard = STATUS_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    let result = match sidecar(app, &["status", "--json"]).await {
        Ok((ok, stdout, stderr)) => status_payload(ok, &stdout, &stderr),
        Err(error) => Err(error),
    };
    proxy_status_result(result)
}

fn status_payload(ok: bool, stdout: &str, stderr: &str) -> Result<Value, String> {
    let value = parse_json(stdout, stderr)?;
    if !ok {
        // A missing configuration is a product state used to open the login
        // screen, never authority to acquire a lease or recover an engine.
        // Preserve only this complete, explicitly unready DTO on nonzero exit.
        if value.get("contract_version").and_then(Value::as_u64) == Some(1)
            && value.get("error_code").and_then(Value::as_str)
                == Some("client_config_unavailable")
            && value.get("engine_state").and_then(Value::as_str) == Some("degraded")
            && value.get("running").and_then(Value::as_bool) == Some(false)
            && value.get("proxy_reachable").and_then(Value::as_bool) == Some(false)
            && value.get("error").and_then(Value::as_str).is_some_and(|s| !s.is_empty())
        {
            return Ok(value);
        }
        return Err(value
            .get("error")
            .and_then(Value::as_str)
            .unwrap_or("无法确认当前连接状态，请重试。")
            .to_string());
    }
    Ok(value)
}

async fn login_impl(app: &AppHandle, token: &str) -> Result<Value, String> {
    let input = login_stdin(token)?;
    let command = app.shell().sidecar("zhvpn")
        .map_err(|_| "无法定位客户端登录程序。".to_string())?
        .args(LOGIN_ARGS).set_raw_out(true);
    let (mut events, mut child) = command.spawn()
        .map_err(|_| "无法启动客户端登录程序。".to_string())?;
    child.write(&input)
        .map_err(|_| "登录提交未确认完成，请先检查客户端状态；不会自动重复登录。".to_string())?;
    // Dropping the stdin owner closes the pipe, giving the bounded CLI reader
    // EOF. The shell plugin's separate waiter retains/reaps its owned process.
    drop(child);
    let observation = async {
        let mut stdout = Vec::new();
        while let Some(event) = events.recv().await {
            match event {
                CommandEvent::Stdout(chunk) => append_login_output(&mut stdout, &chunk)?,
                CommandEvent::Stderr(_) => {}, // never expose unparsed diagnostics
                CommandEvent::Terminated(exit) => {
                    return login_payload(exit.code == Some(0), &stdout, token);
                }
                CommandEvent::Error(_) => return Err("无法确认登录结果，请先检查客户端状态；诊断未展示敏感内容。".to_string()),
                _ => {},
            }
        }
        Err("登录结果未知，请先检查客户端状态；不会自动重复登录。".to_string())
    };
    tokio::time::timeout(Duration::from_secs(60), observation).await
        .map_err(|_| "等待登录结果超时，提交结果未知；请先检查客户端状态，不要自动重复登录。".to_string())?
}

const LOGIN_ARGS: [&str; 3] = ["login", "--token-stdin", "--json"];
const MAX_LOGIN_OUTPUT: usize = 16384;

fn login_stdin(token: &str) -> Result<Vec<u8>, String> {
    if token.trim().is_empty() || token.len() > 4096 || token.contains(['\r', '\n', '\0']) {
        return Err("授权码须为非空单行且长度不超过 4096 字节，请重新输入。".to_string());
    }
    Ok(token.as_bytes().to_vec())
}

fn append_login_output(stdout: &mut Vec<u8>, chunk: &[u8]) -> Result<(), String> {
    if stdout.len().saturating_add(chunk.len()) > MAX_LOGIN_OUTPUT {
        return Err("登录响应超出允许范围，结果未确认；诊断未展示敏感内容。".to_string());
    }
    stdout.extend_from_slice(chunk);
    Ok(())
}

fn redact_login_payload(value: &mut Value, token: &str) {
    match value {
        Value::String(text) => {
            if !token.is_empty() { *text = text.replace(token, "<redacted>"); }
            if !token.trim().is_empty() { *text = text.replace(token.trim(), "<redacted>"); }
        }
        Value::Array(items) => items.iter_mut().for_each(|item| redact_login_payload(item, token)),
        Value::Object(fields) => {
            for (key, item) in fields.iter_mut() {
                if ["token", "authorization_token", "private_key", "wireguard_private_key", "control_secret"].contains(&key.to_ascii_lowercase().as_str()) {
                    *item = json!("<redacted>");
                } else {
                    redact_login_payload(item, token);
                }
            }
        }
        _ => {},
    }
}

fn login_payload(command_ok: bool, stdout: &[u8], token: &str) -> Result<Value, String> {
    let mut value = parse_json(&String::from_utf8_lossy(stdout), "")?;
    if !value.is_object() || value.get("ok").and_then(Value::as_bool).is_none()
        || (!command_ok && value.get("ok").and_then(Value::as_bool) == Some(true)) {
        return Err("客户端未确认登录结果，请先检查状态。".to_string());
    }
    redact_login_payload(&mut value, token);
    Ok(value)
}

async fn connect_impl(app: &AppHandle, global_proxy: bool, fast: bool) -> Result<Value, String> {
    let _guard = ACTION_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    // v1 migration remains a checked restore, never an automatic v2 adoption.
    if backup_kind(app)? == Some(proxy_journal::BackupKind::LegacyGui) {
        restore_legacy_proxy(app)?;
    }
    let mut args = vec!["start", "--json"];
    if fast {
        args.push("--fast");
    }
    let (ok, stdout, stderr) = sidecar(app, &args).await?;
    let mut result = parse_json(&stdout, &stderr)?;
    if !ok && result.get("ok").and_then(Value::as_bool) == Some(true) {
        return Err("客户端未确认启动成功，请重试连接。".to_string());
    }
    if ok && result.get("ok").and_then(Value::as_bool) == Some(true) && global_proxy {
        // GUI 默认只启动本地代理。用户勾选“全局代理”时，才把 Windows
        // 系统代理指向本地代理；断开/退出时统一还原。
        if let Err(e) = acquire_system_proxy(app).await {
            result["warning"] = json!(format!("本地连接已启动，全局代理未启用：{e}"));
        }
    }
    Ok(result)
}

async fn disconnect_impl(app: &AppHandle) -> Result<Value, String> {
    let _guard = ACTION_LOCK.get_or_init(|| Mutex::new(())).lock().await;
    disconnect_inner(app).await
}

async fn disconnect_inner(app: &AppHandle) -> Result<Value, String> {
    // Keep the engine available while recovery is unresolved. A failed restore
    // must not leave Windows pointing at an engine that we then silently stop.
    restore_legacy_proxy(app)?;
    let (ok, stdout, stderr) = sidecar(app, &["stop", "--json"]).await?;
    let result = parse_json(&stdout, &stderr)?;
    if !ok || result.get("ok").and_then(Value::as_bool) != Some(true) {
        if result
            .get("error_code")
            .and_then(Value::as_str)
            .is_some_and(|code| code.starts_with("system_proxy_"))
        {
            record_proxy_result(Err(result
                .get("error")
                .and_then(Value::as_str)
                .unwrap_or("系统代理恢复未完成，连接已保留，请重试断开。")
                .to_string()))?;
        }
        return Ok(result);
    }
    *GUI_PROXY_LEASE
        .lock()
        .unwrap_or_else(|poison| poison.into_inner()) = None;
    record_proxy_result(Ok(()))?;
    Ok(result)
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
    let result = parse_json(&stdout, &stderr)?;
    if !ok && result.get("ok").and_then(Value::as_bool) == Some(true) {
        return Err("客户端未确认登出成功，请重试。".to_string());
    }
    Ok(result)
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
                            let recovery = quit_proxy_release(&a).await;
                            if record_proxy_result(recovery).is_ok() {
                                // Quit releases only this GUI session's new
                                // lease. Shared CLI/SDK engines stay running.
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
                    if record_proxy_result(startup_proxy_recovery(&handle).await).is_err() {
                        show_main(&handle);
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
    static GLOBAL_STATE_TEST_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());

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
    fn cli_proxy_receipt_requires_explicit_typed_ownership() {
        let valid = json!({"ok":true,"contract_version":1,"system_proxy_state":"acquired","owned":true,"noop":false,"lease_id":"0123456789abcdef0123456789abcdef"});
        assert!(proxy_receipt(true, valid.clone(), "acquired").is_ok());
        assert!(proxy_receipt(false, valid.clone(), "acquired").is_err());
        for (field, bad) in [
            ("owned", json!(false)),
            ("noop", json!(0)),
            ("contract_version", json!(2)),
            ("lease_id", json!("invalid")),
            ("system_proxy_state", json!("recorded")),
        ] {
            let mut value = valid.clone();
            value[field] = bad;
            assert!(proxy_receipt(true, value, "acquired").is_err(), "{field}");
        }
        let mut missing = valid.clone();
        missing.as_object_mut().unwrap().remove("noop");
        assert!(proxy_receipt(true, missing, "acquired").is_err());
        assert!(proxy_receipt(true, json!({"ok":true}), "acquired").is_err());
        assert!(proxy_receipt(true, json!({"ok":true,"contract_version":1,"system_proxy_state":"recorded","owned":false,"noop":true,"lease_id":"0123456789abcdef0123456789abcdef"}), "inspect").is_ok());
    }

    #[test]
    fn absent_recovery_is_an_explicit_unowned_noop() {
        let value = json!({"ok":true,"contract_version":1,"system_proxy_state":"absent","owned":false,"noop":true});
        assert!(proxy_receipt(true, value.clone(), "recovered").is_ok());
        assert!(proxy_receipt(true, value.clone(), "acquired").is_err());
        let mut malformed = value;
        malformed["noop"] = json!(false);
        assert!(proxy_receipt(true, malformed, "recovered").is_err());
    }

    #[test]
    fn failed_status_cannot_authorize_ready_identity() {
        let ready = ready_cli_status().to_string();
        assert!(status_payload(false, &ready, "").is_err());
        assert!(connected_in(&status_payload(true, &ready, "").unwrap()));
    }

    #[test]
    fn missing_configuration_reaches_login_without_granting_runtime_authority() {
        let _guard = GLOBAL_STATE_TEST_LOCK.lock().unwrap();
        let missing = json!({"contract_version":1,"error_code":"client_config_unavailable","error":"synthetic unconfigured home","engine_state":"degraded","running":false,"proxy_reachable":false});
        let status = status_payload(false, &missing.to_string(), "").unwrap();
        assert_eq!(status["error_code"], "client_config_unavailable");
        assert!(!connected_in(&status));
        record_proxy_result(Err("synthetic pending recovery".into())).unwrap_err();
        assert_eq!(proxy_status_result(Ok(status)).unwrap()["system_proxy_error"], "synthetic pending recovery");
        record_proxy_result(Ok(())).unwrap();
        for (field, bad) in [
            ("contract_version", json!(2)),
            ("error_code", json!("engine_control_unavailable")),
            ("engine_state", json!("ready")),
            ("running", json!(true)),
            ("proxy_reachable", json!(true)),
            ("error", json!("")),
        ] {
            let mut malformed = missing.clone();
            malformed[field] = bad;
            assert!(status_payload(false, &malformed.to_string(), "").is_err(), "{field}");
        }
    }

    #[test]
    fn login_uses_bounded_stdin_and_redacts_success_failure_and_invalid_output() {
        let token = "synthetic-authorization-value";
        assert_eq!(LOGIN_ARGS, ["login", "--token-stdin", "--json"]);
        assert!(LOGIN_ARGS.iter().all(|arg| !arg.contains(token)));
        assert_eq!(login_stdin(token).unwrap(), token.as_bytes());
        assert!(login_stdin(" \n\t ").is_err());
        assert!(login_stdin(&"a".repeat(4097)).is_err());
        assert!(login_stdin(&"中".repeat(1366)).is_err()); // UTF-8 byte limit
        assert!(login_stdin("synthetic\nsecond").is_err());
        assert!(login_stdin("synthetic\0secret").is_err());
        for ok in [true, false] {
            let dto = json!({"ok":ok,"message":format!("echo: {token}"),"nested":{"token":token,"values":[token]},"private_key":"synthetic-private"});
            let redacted = login_payload(ok, dto.to_string().as_bytes(), token).unwrap().to_string();
            assert!(!redacted.contains(token));
            assert!(!redacted.contains("synthetic-private"));
            assert!(redacted.contains("<redacted>"));
        }
        assert!(!login_payload(false, token.as_bytes(), token).unwrap_err().contains(token));
        assert!(login_payload(false, br#"{"ok":true}"#, token).is_err());
        let mut stdout = Vec::new();
        append_login_output(&mut stdout, &vec![b'x'; MAX_LOGIN_OUTPUT]).unwrap();
        assert!(append_login_output(&mut stdout, token.as_bytes()).is_err());
        assert_eq!(stdout.len(), MAX_LOGIN_OUTPUT);
    }

    #[test]
    fn login_pipe_child_probe() {
        if std::env::var("ZHVPN_SYNTHETIC_LOGIN_PIPE").ok().as_deref() != Some("yes") { return; }
        use std::io::Read;
        let args: Vec<_> = std::env::args().collect();
        assert!(LOGIN_ARGS.iter().all(|arg| args.iter().any(|observed| observed == arg)));
        assert!(args.iter().all(|arg| !arg.contains("synthetic-stdin-only-secret")));
        let mut input = String::new();
        std::io::stdin().take(4097).read_to_string(&mut input).unwrap();
        assert_eq!(input, "synthetic-stdin-only-secret");
        println!("{}", json!({"ok":true,"message":input}));
    }

    #[test]
    fn owned_login_child_receives_secret_only_through_closed_stdin_pipe() {
        use std::io::Write;
        use std::process::{Command, Stdio};
        let token = "synthetic-stdin-only-secret";
        let mut child = Command::new(std::env::current_exe().unwrap())
            .args(["--exact", "tests::login_pipe_child_probe", "--nocapture", "--"])
            .args(LOGIN_ARGS).env("ZHVPN_SYNTHETIC_LOGIN_PIPE", "yes")
            .stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::piped())
            .spawn().unwrap();
        let mut stdin = child.stdin.take().unwrap();
        stdin.write_all(&login_stdin(token).unwrap()).unwrap();
        drop(stdin);
        let output = child.wait_with_output().unwrap();
        assert!(output.status.success());
        let text = String::from_utf8(output.stdout).unwrap();
        let dto = text.lines().find(|line| line.starts_with("{\"message\"")).unwrap();
        let result = login_payload(true, dto.as_bytes(), token).unwrap();
        assert_eq!(result["message"], "<redacted>");
        assert!(!result.to_string().contains(token));
    }

    #[test]
    fn quit_ownership_does_not_adopt_sdk_lease_or_forget_unresolved_acquire() {
        let _guard = GLOBAL_STATE_TEST_LOCK.lock().unwrap();
        *GUI_PROXY_LEASE.lock().unwrap() = None;
        record_proxy_result(Ok(())).unwrap();
        let mut receipt = proxy_receipt(true, json!({"ok":true,"contract_version":1,"system_proxy_state":"acquired","owned":true,"noop":true,"lease_id":"0123456789abcdef0123456789abcdef"}), "acquired").unwrap();
        remember_gui_lease(&receipt);
        assert!(GUI_PROXY_LEASE.lock().unwrap().is_none());
        record_proxy_result(Err("synthetic unresolved acquisition".to_string())).unwrap_err();
        assert!(unresolved_unowned_proxy_error(true));
        assert!(!unresolved_unowned_proxy_error(false));
        receipt.noop = false;
        remember_gui_lease(&receipt);
        assert_eq!(
            GUI_PROXY_LEASE.lock().unwrap().as_deref(),
            receipt.lease_id.as_deref()
        );
        assert!(!unresolved_unowned_proxy_error(true));
        receipt.noop = true;
        receipt.lease_id = Some("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa".to_string());
        remember_gui_lease(&receipt);
        assert!(GUI_PROXY_LEASE.lock().unwrap().is_none());
        record_proxy_result(Ok(())).unwrap();
    }

    #[test]
    fn ordinary_listener_or_legacy_boolean_is_not_a_connected_gui_identity() {
        assert!(!connected_in(
            &json!({"running":false,"proxy_reachable":true})
        ));
        assert!(!connected_in(
            &json!({"running":true,"proxy_reachable":true})
        ));
        let mut status = ready_cli_status();
        assert!(connected_in(&status));
        status["proxy_reachable"] = json!(false);
        assert!(connected_in(&status)); // partial health remains manageable.
        status["instance_id"] = json!("unverified");
        assert!(!connected_in(&status));
        assert!(!parse_json(
            "synthetic-sensitive-private-value",
            "synthetic-sensitive-private-value"
        )
        .unwrap_err()
        .contains("synthetic-sensitive-private-value"));
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
        let _guard = GLOBAL_STATE_TEST_LOCK.lock().unwrap();
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
