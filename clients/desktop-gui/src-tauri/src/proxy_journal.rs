//! Recoverable system-proxy operations. No platform APIs live in this module.
//! The journal is deliberately independent of Tauri so CLI ownership can be
//! introduced later without changing the persisted recovery contract.

use serde::{Deserialize, Serialize};

pub const FIELDS: [&str; 4] = [
    "ProxyEnable",
    "ProxyServer",
    "ProxyOverride",
    "AutoConfigURL",
];
const SCHEMA_VERSION: u32 = 1;
const MANUAL_RECOVERY: &str =
    "记录已保留。请先手动核对并恢复 Windows 代理设置，再将记录重命名归档，最后重试断开或退出";

fn invalid_journal(code: &str, reason: &str) -> String {
    format!("{code}: {reason}；{MANUAL_RECOVERY}")
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RegistryValue {
    pub kind: u32,
    pub bytes: Vec<u8>,
}

impl RegistryValue {
    pub fn dword(value: u32) -> Self {
        Self {
            kind: 4,
            bytes: value.to_le_bytes().to_vec(),
        }
    }

    pub fn string(value: &str) -> Self {
        Self {
            kind: 1,
            bytes: value
                .encode_utf16()
                .chain(Some(0))
                .flat_map(u16::to_le_bytes)
                .collect(),
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct FieldChange {
    name: String,
    // None means the value was absent, including its type, not an empty string.
    original: Option<RegistryValue>,
    written: Option<RegistryValue>,
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Journal {
    schema_version: u32,
    owner: String,
    scope: String,
    fields: Vec<FieldChange>,
}

pub trait RegistryAdapter {
    fn read(&mut self, name: &str) -> Result<Option<RegistryValue>, String>;
    fn write(&mut self, name: &str, value: Option<&RegistryValue>) -> Result<(), String>;
    fn notify(&mut self) -> Result<(), String>;
}

pub trait JournalStore {
    fn load(&mut self) -> Result<Option<Vec<u8>>, String>;
    /// Must refuse replacement and durably persist before any registry writes.
    fn create(&mut self, data: &[u8]) -> Result<(), String>;
    fn remove(&mut self) -> Result<(), String>;
}

fn decode(data: &[u8]) -> Result<Journal, String> {
    let value: serde_json::Value = serde_json::from_slice(data)
        .map_err(|_| invalid_journal("system_proxy_journal_invalid", "代理恢复记录损坏"))?;
    if value.get("schema_version").is_none() {
        return Err(invalid_journal(
            "system_proxy_journal_legacy",
            "旧代理备份没有写入归属，无法安全自动恢复",
        ));
    }
    let journal: Journal = serde_json::from_value(value)
        .map_err(|_| invalid_journal("system_proxy_journal_invalid", "代理恢复记录格式无效"))?;
    if journal.schema_version != SCHEMA_VERSION
        || journal.owner != "desktop-gui"
        || journal.scope != "wininet-hkcu"
    {
        return Err(invalid_journal(
            "system_proxy_journal_incompatible",
            "代理恢复记录版本或归属不匹配",
        ));
    }
    if journal.fields.len() != FIELDS.len()
        || journal.fields.iter().zip(FIELDS).any(|(field, name)| {
            field.name != name
                || [&field.original, &field.written]
                    .iter()
                    .any(|value| value.as_ref().is_some_and(|raw| raw.kind > 11))
        })
    {
        return Err(invalid_journal(
            "system_proxy_journal_invalid",
            "代理恢复记录字段无效",
        ));
    }
    Ok(journal)
}

fn read_field(
    registry: &mut impl RegistryAdapter,
    name: &str,
) -> Result<Option<RegistryValue>, String> {
    registry
        .read(name)
        .map_err(|e| format!("system_proxy_read_failed: {name}: {e}"))
}

fn conflict(name: &str) -> String {
    format!("system_proxy_ownership_conflict: {name} 已被其他程序修改；未覆盖新设置，恢复记录已保留。请先核对 Windows 代理设置；若确认保留当前新设置，可将记录重命名归档后重试退出")
}

pub fn enable(
    registry: &mut impl RegistryAdapter,
    store: &mut impl JournalStore,
    desired: [Option<RegistryValue>; 4],
) -> Result<(), String> {
    if let Some(data) = store
        .load()
        .map_err(|e| format!("system_proxy_journal_read_failed: {e}"))?
    {
        // Diagnose incompatible/legacy data without treating it as our lease.
        decode(&data)?;
        return Err("system_proxy_recovery_pending: 已有未完成的代理恢复记录；请先重试断开或退出，不能覆盖原备份".to_string());
    }
    let mut fields = Vec::with_capacity(FIELDS.len());
    for (name, written) in FIELDS.into_iter().zip(desired) {
        fields.push(FieldChange {
            name: name.to_string(),
            original: read_field(registry, name)?,
            written,
        });
    }
    if fields.iter().all(|field| field.original == field.written) {
        return Ok(());
    }
    let journal = Journal {
        schema_version: SCHEMA_VERSION,
        owner: "desktop-gui".to_string(),
        scope: "wininet-hkcu".to_string(),
        fields,
    };
    let bytes = serde_json::to_vec(&journal)
        .map_err(|e| format!("system_proxy_journal_encode_failed: {e}"))?;
    store
        .create(&bytes)
        .map_err(|e| format!("system_proxy_journal_write_failed: {e}"))?;
    for field in &journal.fields {
        if field.original == field.written {
            continue;
        }
        // Other applications do not participate in our lock. This fresh read
        // narrows the race window; it is not a cross-process compare-and-swap.
        if read_field(registry, &field.name)? != field.original {
            return Err(conflict(&field.name));
        }
        registry
            .write(&field.name, field.written.as_ref())
            .map_err(|e| {
                format!(
                    "system_proxy_write_failed: {}: {e}; 恢复记录已保留，请重试断开",
                    field.name
                )
            })?;
        if read_field(registry, &field.name)? != field.written {
            return Err(conflict(&field.name));
        }
    }
    for field in &journal.fields {
        if read_field(registry, &field.name)? != field.written {
            return Err(conflict(&field.name));
        }
    }
    registry
        .notify()
        .map_err(|e| format!("system_proxy_notify_failed: {e}; 恢复记录已保留，请重试断开"))
}

pub fn restore(
    registry: &mut impl RegistryAdapter,
    store: &mut impl JournalStore,
) -> Result<(), String> {
    let Some(data) = store
        .load()
        .map_err(|e| format!("system_proxy_journal_read_failed: {e}"))?
    else {
        // No lease: do not even query or open the user's registry settings.
        return Ok(());
    };
    let journal = decode(&data)?;
    // Fail the whole group before writes if we observe foreign settings. A
    // restored ProxyEnable can otherwise break a newly changed ProxyServer.
    for field in &journal.fields {
        let current = read_field(registry, &field.name)?;
        if current != field.original && current != field.written {
            return Err(conflict(&field.name));
        }
        if field.original == field.written && current != field.original {
            return Err(conflict(&field.name));
        }
    }
    for field in &journal.fields {
        if field.original == field.written {
            continue;
        }
        let current = read_field(registry, &field.name)?;
        if current == field.original {
            continue;
        } // Previously restored / never written.
        if current != field.written {
            return Err(conflict(&field.name));
        }
        registry
            .write(&field.name, field.original.as_ref())
            .map_err(|e| {
                format!(
                    "system_proxy_restore_failed: {}: {e}; 恢复记录已保留，请重试断开或退出",
                    field.name
                )
            })?;
        if read_field(registry, &field.name)? != field.original {
            return Err(conflict(&field.name));
        }
    }
    for field in &journal.fields {
        if read_field(registry, &field.name)? != field.original {
            return Err(conflict(&field.name));
        }
    }
    registry.notify().map_err(|e| {
        format!("system_proxy_notify_failed: {e}; 恢复记录已保留，请重试断开或退出")
    })?;
    store.remove().map_err(|e| {
        format!(
            "system_proxy_journal_remove_failed: {e}; 设置已恢复，恢复记录已保留，请重试断开或退出"
        )
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::BTreeMap;

    #[derive(Default)]
    struct FakeRegistry {
        values: BTreeMap<String, RegistryValue>,
        reads: usize,
        writes: usize,
        notifications: usize,
        fail_read: Option<usize>,
        fail_write: Option<usize>,
        fail_notify: bool,
        mutate_on_read: Option<(usize, String, RegistryValue)>,
    }

    impl RegistryAdapter for FakeRegistry {
        fn read(&mut self, name: &str) -> Result<Option<RegistryValue>, String> {
            self.reads += 1;
            if let Some((at, field, value)) = &self.mutate_on_read {
                if *at == self.reads {
                    self.values.insert(field.clone(), value.clone());
                }
            }
            if self.fail_read == Some(self.reads) {
                return Err("read denied".to_string());
            }
            Ok(self.values.get(name).cloned())
        }
        fn write(&mut self, name: &str, value: Option<&RegistryValue>) -> Result<(), String> {
            self.writes += 1;
            if self.fail_write == Some(self.writes) {
                return Err("write denied".to_string());
            }
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
            self.notifications += 1;
            if self.fail_notify {
                Err("notification failed".to_string())
            } else {
                Ok(())
            }
        }
    }

    #[derive(Default)]
    struct FakeStore {
        data: Option<Vec<u8>>,
        fail_load: bool,
        fail_create: bool,
        fail_remove: bool,
    }
    impl JournalStore for FakeStore {
        fn load(&mut self) -> Result<Option<Vec<u8>>, String> {
            if self.fail_load {
                Err("access denied".to_string())
            } else {
                Ok(self.data.clone())
            }
        }
        fn create(&mut self, data: &[u8]) -> Result<(), String> {
            if self.fail_create || self.data.is_some() {
                return Err("persist failed".to_string());
            }
            self.data = Some(data.to_vec());
            Ok(())
        }
        fn remove(&mut self) -> Result<(), String> {
            if self.fail_remove {
                return Err("delete denied".to_string());
            }
            self.data = None;
            Ok(())
        }
    }

    fn desired() -> [Option<RegistryValue>; 4] {
        [
            Some(RegistryValue::dword(1)),
            Some(RegistryValue::string("http=127.0.0.1:7890")),
            Some(RegistryValue::string("localhost")),
            None,
        ]
    }

    fn existing() -> FakeRegistry {
        let mut registry = FakeRegistry::default();
        registry
            .values
            .insert("ProxyEnable".to_string(), RegistryValue::dword(1));
        registry.values.insert(
            "ProxyServer".to_string(),
            RegistryValue::string("old-proxy:8080"),
        );
        // Preserve an unusual type and an empty string instead of normalizing.
        registry.values.insert(
            "ProxyOverride".to_string(),
            RegistryValue {
                kind: 2,
                bytes: vec![0, 0],
            },
        );
        registry.values.insert(
            "AutoConfigURL".to_string(),
            RegistryValue::string("https://example.invalid/proxy.pac"),
        );
        registry
    }

    #[test]
    fn no_journal_is_a_complete_noop_even_when_registry_is_inaccessible() {
        let mut registry = existing();
        let original = registry.values.clone();
        registry.fail_read = Some(1);
        restore(&mut registry, &mut FakeStore::default()).unwrap();
        assert_eq!(registry.values, original);
        assert_eq!(
            (registry.reads, registry.writes, registry.notifications),
            (0, 0, 0)
        );
    }

    #[test]
    fn roundtrip_preserves_raw_types_bytes_and_absent_values() {
        for mut registry in [existing(), FakeRegistry::default()] {
            let original = registry.values.clone();
            let mut store = FakeStore::default();
            enable(&mut registry, &mut store, desired()).unwrap();
            let journal = decode(store.data.as_ref().unwrap()).unwrap();
            assert_eq!(journal.schema_version, 1);
            restore(&mut registry, &mut store).unwrap();
            assert_eq!(registry.values, original);
            assert!(store.data.is_none());
            let writes = registry.writes;
            restore(&mut registry, &mut store).unwrap();
            assert_eq!(registry.writes, writes);
        }
    }

    #[test]
    fn journal_read_errors_do_not_touch_registry() {
        let mut registry = existing();
        let mut store = FakeStore {
            fail_load: true,
            ..Default::default()
        };
        assert!(restore(&mut registry, &mut store)
            .unwrap_err()
            .contains("journal_read_failed"));
        assert!(enable(&mut registry, &mut store, desired()).is_err());
        assert_eq!(
            (registry.reads, registry.writes, registry.notifications),
            (0, 0, 0)
        );
    }

    #[test]
    fn snapshot_read_failure_does_not_persist_or_write() {
        let mut registry = existing();
        registry.fail_read = Some(3);
        let mut store = FakeStore::default();
        assert!(enable(&mut registry, &mut store, desired()).is_err());
        assert_eq!(registry.writes, 0);
        assert!(store.data.is_none());
    }

    #[test]
    fn persistence_failure_precedes_any_registry_write() {
        let mut registry = existing();
        let original = registry.values.clone();
        let mut store = FakeStore {
            fail_create: true,
            ..Default::default()
        };
        assert!(enable(&mut registry, &mut store, desired())
            .unwrap_err()
            .contains("journal_write_failed"));
        assert_eq!(registry.values, original);
        assert_eq!(registry.writes, 0);
    }

    #[test]
    fn enable_partial_write_failure_is_recoverable_at_each_field() {
        for failure in 1..=4 {
            let mut registry = FakeRegistry::default();
            registry.values.insert(
                "AutoConfigURL".to_string(),
                RegistryValue::string("old-pac"),
            );
            let original = registry.values.clone();
            let mut store = FakeStore::default();
            registry.fail_write = Some(failure);
            assert!(enable(&mut registry, &mut store, desired()).is_err());
            assert!(store.data.is_some());
            registry.fail_write = None;
            restore(&mut registry, &mut store).unwrap();
            assert_eq!(registry.values, original);
            assert!(store.data.is_none());
        }
    }

    #[test]
    fn enable_notification_failure_retains_recovery_record() {
        let mut registry = existing();
        let original = registry.values.clone();
        let mut store = FakeStore::default();
        registry.fail_notify = true;
        assert!(enable(&mut registry, &mut store, desired())
            .unwrap_err()
            .contains("notify_failed"));
        assert!(store.data.is_some());
        registry.fail_notify = false;
        restore(&mut registry, &mut store).unwrap();
        assert_eq!(registry.values, original);
    }

    #[test]
    fn restore_partial_write_failure_retries_idempotently() {
        for failure in 1..=4 {
            let mut registry = FakeRegistry::default();
            registry.values.insert(
                "AutoConfigURL".to_string(),
                RegistryValue::string("old-pac"),
            );
            let original = registry.values.clone();
            let mut store = FakeStore::default();
            enable(&mut registry, &mut store, desired()).unwrap();
            registry.fail_write = Some(registry.writes + failure);
            assert!(restore(&mut registry, &mut store).is_err());
            assert!(store.data.is_some());
            registry.fail_write = None;
            restore(&mut registry, &mut store).unwrap();
            assert_eq!(registry.values, original);
            assert!(store.data.is_none());
        }
    }

    #[test]
    fn restore_notification_failure_does_not_delete_journal() {
        let mut registry = existing();
        let original = registry.values.clone();
        let mut store = FakeStore::default();
        enable(&mut registry, &mut store, desired()).unwrap();
        registry.fail_notify = true;
        assert!(restore(&mut registry, &mut store)
            .unwrap_err()
            .contains("notify_failed"));
        assert_eq!(registry.values, original);
        assert!(store.data.is_some());
        let writes = registry.writes;
        registry.fail_notify = false;
        restore(&mut registry, &mut store).unwrap();
        assert_eq!(registry.writes, writes);
        assert!(store.data.is_none());
    }

    #[test]
    fn journal_delete_failure_is_reported_and_retryable() {
        let mut registry = existing();
        let original = registry.values.clone();
        let mut store = FakeStore::default();
        enable(&mut registry, &mut store, desired()).unwrap();
        store.fail_remove = true;
        assert!(restore(&mut registry, &mut store)
            .unwrap_err()
            .contains("journal_remove_failed"));
        assert_eq!(registry.values, original);
        assert!(store.data.is_some());
        let writes = registry.writes;
        store.fail_remove = false;
        restore(&mut registry, &mut store).unwrap();
        assert_eq!(registry.writes, writes);
    }

    #[test]
    fn foreign_change_blocks_group_restore_without_overwriting_any_field() {
        let mut registry = existing();
        let mut store = FakeStore::default();
        enable(&mut registry, &mut store, desired()).unwrap();
        registry.values.insert(
            "ProxyServer".to_string(),
            RegistryValue::string("new-foreign-proxy:9999"),
        );
        let foreign = registry.values.clone();
        let writes = registry.writes;
        assert!(restore(&mut registry, &mut store)
            .unwrap_err()
            .contains("ownership_conflict"));
        assert_eq!(registry.values, foreign);
        assert_eq!(registry.writes, writes);
        assert!(store.data.is_some());
    }

    #[test]
    fn restore_rechecks_ownership_immediately_before_each_write() {
        let mut registry = existing();
        let mut store = FakeStore::default();
        enable(&mut registry, &mut store, desired()).unwrap();
        let writes = registry.writes;
        // Initial four reads pass. The next read is immediately before restoring
        // ProxyServer (ProxyEnable did not change and is not owned).
        let foreign = RegistryValue::string("new-foreign-proxy:9999");
        registry.mutate_on_read = Some((
            registry.reads + 5,
            "ProxyServer".to_string(),
            foreign.clone(),
        ));
        assert!(restore(&mut registry, &mut store)
            .unwrap_err()
            .contains("ownership_conflict"));
        assert_eq!(registry.values.get("ProxyServer"), Some(&foreign));
        assert_eq!(registry.writes, writes);
        assert!(store.data.is_some());
    }

    #[test]
    fn enable_rechecks_original_values_before_writing() {
        let mut registry = existing();
        let mut store = FakeStore::default();
        let foreign = RegistryValue::string("foreign:8888");
        registry.mutate_on_read = Some((5, "ProxyServer".to_string(), foreign.clone()));
        assert!(enable(&mut registry, &mut store, desired())
            .unwrap_err()
            .contains("ownership_conflict"));
        assert_eq!(registry.values.get("ProxyServer"), Some(&foreign));
        assert_eq!(registry.writes, 0);
        assert!(store.data.is_some());
    }

    #[test]
    fn legacy_corrupt_or_unknown_schema_are_preserved_without_registry_access() {
        for data in [
            br#"{"enable":true,"server":"old:8080"}"#.as_slice(),
            b"not-json",
            br#"{"schema_version":99}"#,
        ] {
            let mut registry = existing();
            let mut store = FakeStore {
                data: Some(data.to_vec()),
                ..Default::default()
            };
            assert!(restore(&mut registry, &mut store).is_err());
            assert!(enable(&mut registry, &mut store, desired()).is_err());
            assert_eq!(store.data.as_deref(), Some(data));
            assert_eq!((registry.reads, registry.writes), (0, 0));
        }
    }

    #[test]
    fn every_incompatible_record_has_a_manual_archive_recovery_path() {
        let mut registry = existing();
        let mut valid = FakeStore::default();
        enable(&mut registry, &mut valid, desired()).unwrap();
        let template: serde_json::Value =
            serde_json::from_slice(valid.data.as_ref().unwrap()).unwrap();
        let mut cases = vec![
            b"not-json".to_vec(),
            br#"{"enable":true}"#.to_vec(),
            br#"{"schema_version":1}"#.to_vec(),
        ];
        for (key, value) in [
            ("schema_version", serde_json::json!(99)),
            ("owner", serde_json::json!("another-owner")),
            ("scope", serde_json::json!("another-scope")),
            ("unknown", serde_json::json!(true)),
            ("fields", serde_json::json!([])),
        ] {
            let mut data = template.clone();
            data[key] = value;
            cases.push(serde_json::to_vec(&data).unwrap());
        }
        for (key, value) in [
            ("name", serde_json::json!("ProxyServer")),
            ("original", serde_json::json!({"kind":12,"bytes":[]})),
            ("written", serde_json::json!({"kind":12,"bytes":[]})),
        ] {
            let mut data = template.clone();
            data["fields"][0][key] = value;
            cases.push(serde_json::to_vec(&data).unwrap());
        }
        for data in cases {
            let mut registry = existing();
            let mut store = FakeStore {
                data: Some(data.clone()),
                ..Default::default()
            };
            for result in [
                restore(&mut registry, &mut store),
                enable(&mut registry, &mut store, desired()),
            ] {
                let error = result.unwrap_err();
                assert!(error.contains("手动核对并恢复 Windows 代理"), "{error}");
                assert!(error.contains("将记录重命名归档"), "{error}");
                assert!(error.contains("重试断开或退出"), "{error}");
            }
            assert_eq!(store.data, Some(data));
            assert_eq!(
                (registry.reads, registry.writes, registry.notifications),
                (0, 0, 0)
            );
        }
    }

    #[test]
    fn existing_journal_is_not_overwritten_by_reconnect() {
        let mut registry = existing();
        let mut store = FakeStore::default();
        enable(&mut registry, &mut store, desired()).unwrap();
        let data = store.data.clone();
        let writes = registry.writes;
        assert!(enable(&mut registry, &mut store, desired())
            .unwrap_err()
            .contains("recovery_pending"));
        assert_eq!(store.data, data);
        assert_eq!(registry.writes, writes);
    }

    #[test]
    fn restore_read_failures_preserve_the_journal_for_retry() {
        for offset in [1, 5, 6] {
            let mut registry = existing();
            let original = registry.values.clone();
            let mut store = FakeStore::default();
            enable(&mut registry, &mut store, desired()).unwrap();
            registry.fail_read = Some(registry.reads + offset);
            assert!(restore(&mut registry, &mut store)
                .unwrap_err()
                .contains("read_failed"));
            assert!(store.data.is_some());
            registry.fail_read = None;
            restore(&mut registry, &mut store).unwrap();
            assert_eq!(registry.values, original);
        }
    }

    #[test]
    fn ownership_is_rechecked_for_every_restored_field() {
        for (index, name) in FIELDS.into_iter().enumerate() {
            let mut registry = FakeRegistry::default();
            registry.values.insert(
                "AutoConfigURL".to_string(),
                RegistryValue::string("old-pac"),
            );
            let mut store = FakeStore::default();
            enable(&mut registry, &mut store, desired()).unwrap();
            let foreign = RegistryValue::string("foreign-setting");
            registry.mutate_on_read = Some((
                registry.reads + 5 + index * 2,
                name.to_string(),
                foreign.clone(),
            ));
            assert!(restore(&mut registry, &mut store)
                .unwrap_err()
                .contains("ownership_conflict"));
            assert_eq!(registry.values.get(name), Some(&foreign));
            assert!(store.data.is_some());
        }
    }
}
