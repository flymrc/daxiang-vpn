use serde_json::{json, Value};
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
    Ok(json!({ "ok": ok, "message": message(ok, stdout, stderr) }))
}

#[tauri::command]
async fn disconnect(app: tauri::AppHandle) -> Result<Value, String> {
    let (ok, stdout, stderr) = sidecar(&app, &["stop"]).await?;
    Ok(json!({ "ok": ok, "message": message(ok, stdout, stderr) }))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![status, login, connect, disconnect])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
