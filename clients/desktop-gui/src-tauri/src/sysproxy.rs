// Windows WinINET adapter for the recoverable proxy journal. A missing journal
// never grants permission to disable a proxy configured by another application.

use crate::proxy_journal::{self, JournalStore, RegistryAdapter, RegistryValue};
use std::fs::{self, File, OpenOptions};
use std::io::{ErrorKind, Write};
use std::os::windows::fs::OpenOptionsExt;
use std::os::windows::io::AsRawHandle;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use tauri::Manager;
use winapi::shared::ntdef::NULL;
use winapi::um::fileapi::{LockFileEx, UnlockFileEx};
use winapi::um::minwinbase::{LOCKFILE_EXCLUSIVE_LOCK, LOCKFILE_FAIL_IMMEDIATELY, OVERLAPPED};
use winapi::um::wininet::{
    InternetSetOptionA, INTERNET_OPTION_REFRESH, INTERNET_OPTION_SETTINGS_CHANGED,
};
use winapi::um::winnt::{FILE_SHARE_READ, FILE_SHARE_WRITE, HANDLE};
use winreg::{enums, RegKey, RegValue};

const BYPASS: &str = "localhost;*.localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;192.168.*;<local>";
const INTERNET_SETTINGS: &str = "SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Internet Settings";
// Serializes this GUI's registry/journal operations, including startup recovery
// and tray actions. It does not coordinate third-party proxy applications.
static OPERATION_LOCK: Mutex<()> = Mutex::new(());

fn backup_path(app: &tauri::AppHandle) -> Result<PathBuf, String> {
    let dir = app.path().app_config_dir().map_err(|e| e.to_string())?;
    Ok(dir.join("proxy-backup.json"))
}

struct JournalOperationLock {
    file: File,
    overlapped: OVERLAPPED,
}

impl JournalOperationLock {
    fn acquire(path: &Path) -> Result<Self, String> {
        // Keep the named file persistent. Excluding FILE_SHARE_DELETE also
        // prevents unlink/rename from replacing the file while a guard is held.
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .share_mode(FILE_SHARE_READ | FILE_SHARE_WRITE)
            .open(path)
            .map_err(|e| {
                format!("system_proxy_lock_failed: 无法打开跨会话代理操作锁，未修改系统代理：{e}")
            })?;
        // This is synchronous I/O, offset zero, and a non-inheritable std File
        // handle. FAIL_IMMEDIATELY ensures contention never blocks the UI.
        let mut overlapped: OVERLAPPED = unsafe { std::mem::zeroed() };
        if unsafe {
            LockFileEx(
                file.as_raw_handle() as HANDLE,
                LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY,
                0,
                1,
                0,
                &mut overlapped,
            )
        } == 0
        {
            return Err(format!("system_proxy_lock_failed: 无法获得跨会话代理操作锁，未修改系统代理；另一会话可能正在处理，请稍后重试：{}", std::io::Error::last_os_error()));
        }
        Ok(Self { file, overlapped })
    }
}

impl Drop for JournalOperationLock {
    fn drop(&mut self) {
        if unsafe {
            UnlockFileEx(
                self.file.as_raw_handle() as HANDLE,
                0,
                1,
                0,
                &mut self.overlapped,
            )
        } == 0
        {
            // Closing the File afterwards also releases its byte-range lock.
            eprintln!(
                "system_proxy_unlock_failed: {}",
                std::io::Error::last_os_error()
            );
        }
        // Never remove the named lock file, including on errors or unwinding.
    }
}

fn with_journal_lock(
    journal_path: &Path,
    action: impl FnOnce(&mut FileJournalStore) -> Result<(), String>,
) -> Result<(), String> {
    let parent = journal_path
        .parent()
        .ok_or_else(|| "system_proxy_lock_failed: 恢复记录目录无效".to_string())?;
    fs::create_dir_all(parent)
        .map_err(|e| format!("system_proxy_lock_failed: 无法创建恢复记录目录：{e}"))?;
    let lock_path = journal_path.with_file_name("proxy-operation.lock");
    let _guard = JournalOperationLock::acquire(&lock_path)?;
    let mut store = FileJournalStore {
        path: journal_path.to_path_buf(),
    };
    // Guard covers load, snapshot, journal persistence, registry mutation,
    // notification, and recovery-file removal as one application transaction.
    action(&mut store)
}

struct FileJournalStore {
    path: PathBuf,
}

impl JournalStore for FileJournalStore {
    fn load(&mut self) -> Result<Option<Vec<u8>>, String> {
        match fs::read(&self.path) {
            Ok(data) => Ok(Some(data)),
            Err(e) if e.kind() == ErrorKind::NotFound => Ok(None),
            Err(e) => Err(format!("无法读取 {}: {e}", self.path.display())),
        }
    }

    fn create(&mut self, data: &[u8]) -> Result<(), String> {
        if let Some(parent) = self.path.parent() {
            fs::create_dir_all(parent).map_err(|e| format!("无法创建恢复记录目录: {e}"))?;
        }
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&self.path)
            .map_err(|e| format!("无法创建 {}: {e}", self.path.display()))?;
        file.write_all(data)
            .and_then(|_| file.sync_all())
            .map_err(|e| format!("无法持久化 {}: {e}; 文件已保留", self.path.display()))
    }

    fn remove(&mut self) -> Result<(), String> {
        fs::remove_file(&self.path).map_err(|e| format!("无法删除 {}: {e}", self.path.display()))
    }
}

struct WindowsRegistry {
    key_path: String,
    notify_system: bool,
}

impl WindowsRegistry {
    fn system() -> Self {
        Self {
            key_path: INTERNET_SETTINGS.to_string(),
            notify_system: true,
        }
    }
}

fn registry_type(kind: u32) -> Result<enums::RegType, String> {
    use enums::*;
    match kind {
        0 => Ok(REG_NONE),
        1 => Ok(REG_SZ),
        2 => Ok(REG_EXPAND_SZ),
        3 => Ok(REG_BINARY),
        4 => Ok(REG_DWORD),
        5 => Ok(REG_DWORD_BIG_ENDIAN),
        6 => Ok(REG_LINK),
        7 => Ok(REG_MULTI_SZ),
        8 => Ok(REG_RESOURCE_LIST),
        9 => Ok(REG_FULL_RESOURCE_DESCRIPTOR),
        10 => Ok(REG_RESOURCE_REQUIREMENTS_LIST),
        11 => Ok(REG_QWORD),
        _ => Err("不支持的注册表值类型".to_string()),
    }
}

impl RegistryAdapter for WindowsRegistry {
    fn read(&mut self, name: &str) -> Result<Option<RegistryValue>, String> {
        let key = RegKey::predef(enums::HKEY_CURRENT_USER)
            .open_subkey_with_flags(&self.key_path, enums::KEY_QUERY_VALUE)
            .map_err(|e| format!("打开代理设置读取失败: {e}"))?;
        match key.get_raw_value(name) {
            Ok(value) => Ok(Some(RegistryValue {
                kind: value.vtype as u32,
                bytes: value.bytes,
            })),
            Err(e) if e.kind() == ErrorKind::NotFound => Ok(None),
            Err(e) => Err(e.to_string()),
        }
    }

    fn write(&mut self, name: &str, value: Option<&RegistryValue>) -> Result<(), String> {
        let key = RegKey::predef(enums::HKEY_CURRENT_USER)
            .open_subkey_with_flags(&self.key_path, enums::KEY_SET_VALUE)
            .map_err(|e| format!("打开代理设置写入失败: {e}"))?;
        match value {
            Some(value) => key
                .set_raw_value(
                    name,
                    &RegValue {
                        bytes: value.bytes.clone(),
                        vtype: registry_type(value.kind)?,
                    },
                )
                .map_err(|e| e.to_string()),
            None => match key.delete_value(name) {
                Ok(()) => Ok(()),
                Err(e) if e.kind() == ErrorKind::NotFound => Ok(()),
                Err(e) => Err(e.to_string()),
            },
        }
    }

    fn notify(&mut self) -> Result<(), String> {
        // Synthetic-key tests never broadcast a real user's WinINET change.
        if !self.notify_system {
            return Ok(());
        }
        for option in [INTERNET_OPTION_SETTINGS_CHANGED, INTERNET_OPTION_REFRESH] {
            if unsafe { InternetSetOptionA(NULL, option, NULL, 0) } == 0 {
                return Err(format!(
                    "WinINET 通知失败 ({option}): {}",
                    std::io::Error::last_os_error()
                ));
            }
        }
        Ok(())
    }
}

fn desired(host: &str, port: u16) -> [Option<RegistryValue>; 4] {
    [
        Some(RegistryValue::dword(1)),
        Some(RegistryValue::string(&format!(
            "http={host}:{port};https={host}:{port};socks={host}:{port}"
        ))),
        Some(RegistryValue::string(BYPASS)),
        None,
    ]
}

pub fn enable(app: &tauri::AppHandle, host: &str, port: u16) -> Result<(), String> {
    let _guard = OPERATION_LOCK
        .lock()
        .map_err(|_| "系统代理操作锁已失效，请重新打开客户端".to_string())?;
    let path = backup_path(app)?;
    with_journal_lock(&path, |store| {
        proxy_journal::enable(&mut WindowsRegistry::system(), store, desired(host, port))
    })
    .map_err(|e| format!("{e}\n恢复记录：{}", path.display()))
}

pub fn restore(app: &tauri::AppHandle) -> Result<(), String> {
    let _guard = OPERATION_LOCK
        .lock()
        .map_err(|_| "系统代理操作锁已失效，请重新打开客户端".to_string())?;
    let path = backup_path(app)?;
    with_journal_lock(&path, |store| {
        proxy_journal::restore(&mut WindowsRegistry::system(), store)
    })
        .map_err(|e| format!("{e}\n请保留恢复记录：{}；在 Windows 设置 > 网络和 Internet > 代理中核对设置，再重试断开或退出", path.display()))
}

pub fn has_backup(app: &tauri::AppHandle) -> Result<bool, String> {
    match fs::metadata(backup_path(app)?) {
        Ok(_) => Ok(true),
        Err(e) if e.kind() == ErrorKind::NotFound => Ok(false),
        Err(e) => Err(format!("检查系统代理恢复记录失败: {e}")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::BTreeMap;
    use std::process::Command;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn test_directory(label: &str) -> PathBuf {
        std::env::temp_dir().join(format!(
            "zhvpn-{label}-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ))
    }

    #[test]
    fn operation_lock_is_exclusive_persistent_and_protects_its_filename() {
        let directory = test_directory("proxy-lock");
        fs::create_dir(&directory).unwrap();
        let path = directory.join("proxy-operation.lock");
        fs::write(&path, b"persistent lock file").unwrap();
        let guard = JournalOperationLock::acquire(&path).unwrap();
        assert!(JournalOperationLock::acquire(&path).is_err());
        assert!(fs::remove_file(&path).is_err());
        assert!(fs::rename(&path, directory.join("replacement.lock")).is_err());
        let mut flags = 0;
        assert_ne!(
            unsafe {
                winapi::um::handleapi::GetHandleInformation(
                    guard.file.as_raw_handle() as HANDLE,
                    &mut flags,
                )
            },
            0
        );
        assert_eq!(flags & winapi::um::winbase::HANDLE_FLAG_INHERIT, 0);
        drop(guard);
        assert_eq!(fs::read(&path).unwrap(), b"persistent lock file");
        drop(JournalOperationLock::acquire(&path).unwrap());
        assert!(path.exists());
        // Cleanup only this uniquely named test directory after handles close.
        fs::remove_file(path).unwrap();
        fs::remove_dir(directory).unwrap();
    }

    // Invoked in a real child process by the next test. Running it normally is
    // a no-op; environment parameters are scoped to the child Command only.
    #[test]
    fn operation_lock_child_probe() {
        let Some(path) = std::env::var_os("ZHVPN_TEST_PROXY_JOURNAL") else {
            return;
        };
        let journal_path = PathBuf::from(path);
        let locked = std::env::var("ZHVPN_TEST_PROXY_LOCKED").unwrap() == "yes";
        let mut entered = false;
        let result = with_journal_lock(&journal_path, |store| {
            entered = true;
            if locked {
                store.remove()?;
            }
            Ok(())
        });
        if locked {
            assert!(result.unwrap_err().contains("system_proxy_lock_failed"));
            assert!(!entered);
            assert!(journal_path.exists());
        } else {
            result.unwrap();
            assert!(entered);
        }
    }

    #[test]
    fn child_process_cannot_enter_transaction_until_guard_is_released() {
        let directory = test_directory("proxy-lock-child");
        fs::create_dir(&directory).unwrap();
        let journal = directory.join("proxy-backup.json");
        fs::write(&journal, b"recovery evidence").unwrap();
        let guard = JournalOperationLock::acquire(&directory.join("proxy-operation.lock")).unwrap();
        let run_child = |locked: bool| {
            let output = Command::new(std::env::current_exe().unwrap())
                .args([
                    "--exact",
                    "sysproxy::tests::operation_lock_child_probe",
                    "--nocapture",
                ])
                .env("ZHVPN_TEST_PROXY_JOURNAL", &journal)
                .env("ZHVPN_TEST_PROXY_LOCKED", if locked { "yes" } else { "no" })
                .output()
                .unwrap();
            assert!(
                output.status.success(),
                "child failed: {} {}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
        };
        run_child(true);
        assert_eq!(fs::read(&journal).unwrap(), b"recovery evidence");
        drop(guard);
        run_child(false);
        assert!(directory.join("proxy-operation.lock").exists());
        fs::remove_file(journal).unwrap();
        fs::remove_file(directory.join("proxy-operation.lock")).unwrap();
        fs::remove_dir(directory).unwrap();
    }

    #[derive(Default)]
    struct TestRegistry {
        values: BTreeMap<String, RegistryValue>,
        reads: usize,
    }
    impl RegistryAdapter for TestRegistry {
        fn read(&mut self, name: &str) -> Result<Option<RegistryValue>, String> {
            self.reads += 1;
            Ok(self.values.get(name).cloned())
        }
        fn write(&mut self, name: &str, value: Option<&RegistryValue>) -> Result<(), String> {
            match value {
                Some(value) => {
                    self.values.insert(name.to_string(), value.clone());
                }
                None => {
                    self.values.remove(name);
                }
            }
            Ok(())
        }
        fn notify(&mut self) -> Result<(), String> {
            Ok(())
        }
    }

    struct ContendingStore<'a> {
        inner: &'a mut FileJournalStore,
        contender: &'a mut TestRegistry,
        refused: bool,
    }
    impl JournalStore for ContendingStore<'_> {
        fn load(&mut self) -> Result<Option<Vec<u8>>, String> {
            self.inner.load()
        }
        fn create(&mut self, data: &[u8]) -> Result<(), String> {
            self.inner.create(data)?;
            // Exact vulnerable interleaving: A saved the journal but has not
            // written any settings; B must not restore/delete A's evidence.
            let result = with_journal_lock(&self.inner.path, |store| {
                proxy_journal::restore(self.contender, store)
            });
            self.refused = result.is_err();
            assert!(result.unwrap_err().contains("system_proxy_lock_failed"));
            assert_eq!(self.contender.reads, 0);
            assert!(self.inner.path.exists());
            Ok(())
        }
        fn remove(&mut self) -> Result<(), String> {
            self.inner.remove()
        }
    }

    #[test]
    fn competing_restore_cannot_delete_a_persisted_inflight_enable_journal() {
        let directory = test_directory("proxy-transaction");
        let path = directory.join("proxy-backup.json");
        let mut registry = TestRegistry::default();
        let mut contender = TestRegistry::default();
        with_journal_lock(&path, |inner| {
            let mut store = ContendingStore {
                inner,
                contender: &mut contender,
                refused: false,
            };
            proxy_journal::enable(&mut registry, &mut store, desired("127.0.0.1", 7890))?;
            assert!(store.refused);
            Ok(())
        })
        .unwrap();
        assert!(path.exists());
        with_journal_lock(&path, |store| proxy_journal::restore(&mut registry, store)).unwrap();
        assert!(registry.values.is_empty());
        assert!(!path.exists());
        assert!(with_journal_lock(&path, |_| Err("synthetic failure".to_string())).is_err());
        with_journal_lock(&path, |_| Ok(())).unwrap();
        assert!(directory.join("proxy-operation.lock").exists());
        fs::remove_file(directory.join("proxy-operation.lock")).unwrap();
        fs::remove_dir(directory).unwrap();
    }

    #[test]
    fn windows_adapter_roundtrip_uses_only_an_isolated_synthetic_key() {
        let id = format!(
            "{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        );
        let prefix = "Software\\ZonghengVPNTests\\ProxyJournal-";
        let key_path = format!("{prefix}{id}");
        assert!(key_path.starts_with(prefix) && !key_path.contains("Internet Settings"));
        let hkcu = RegKey::predef(enums::HKEY_CURRENT_USER);
        let (key, _) = hkcu.create_subkey(&key_path).unwrap();
        key.set_raw_value(
            "ProxyServer",
            &RegValue {
                bytes: vec![1, 0, 255],
                vtype: enums::REG_BINARY,
            },
        )
        .unwrap();
        key.set_value("AutoConfigURL", &"").unwrap();
        drop(key);
        let mut registry = WindowsRegistry {
            key_path: key_path.clone(),
            notify_system: false,
        };
        let original: Vec<_> = proxy_journal::FIELDS
            .iter()
            .map(|name| registry.read(name).unwrap())
            .collect();
        let directory = std::env::temp_dir().join(format!("zhvpn-proxy-journal-{id}"));
        let path = directory.join("proxy-backup.json");
        let mut store = FileJournalStore { path: path.clone() };
        proxy_journal::enable(&mut registry, &mut store, desired("127.0.0.1", 7890)).unwrap();
        assert!(path.exists());
        proxy_journal::restore(&mut registry, &mut store).unwrap();
        let restored: Vec<_> = proxy_journal::FIELDS
            .iter()
            .map(|name| registry.read(name).unwrap())
            .collect();
        assert_eq!(restored, original);
        assert!(!path.exists());
        proxy_journal::restore(&mut registry, &mut store).unwrap();
        // Only this test's verified synthetic subtree is removed.
        assert!(key_path.starts_with(prefix));
        hkcu.delete_subkey_all(&key_path).unwrap();
        fs::remove_dir(directory).unwrap();
    }

    #[test]
    fn file_store_never_replaces_an_existing_recovery_record() {
        let id = format!(
            "{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        );
        let directory = std::env::temp_dir().join(format!("zhvpn-proxy-journal-store-{id}"));
        let path = directory.join("proxy-backup.json");
        let mut store = FileJournalStore { path: path.clone() };
        assert!(store.load().unwrap().is_none());
        store.create(b"original recovery record").unwrap();
        assert!(store.create(b"replacement").is_err());
        assert_eq!(
            store.load().unwrap().as_deref(),
            Some(b"original recovery record".as_slice())
        );
        store.remove().unwrap();
        fs::remove_dir(directory).unwrap();
    }
}
