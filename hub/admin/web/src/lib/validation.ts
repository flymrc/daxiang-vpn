// Runtime checks consume the canonical generated DTOs. HTTP 200 and a type cast
// do not make a usable snapshot; extensions remain compatible, required facts do not.
import type { components } from "./openapi";
type DTO = components["schemas"];
const object = (x: unknown): x is Record<string, unknown> => x !== null && typeof x === "object" && !Array.isArray(x);
const text = (x: unknown, nonempty = false): x is string => typeof x === "string" && x.length <= 4096 && (!nonempty || x.length > 0);
const count = (x: unknown): x is number => Number.isSafeInteger(x) && typeof x === "number" && x >= 0;
const date = (x: unknown): x is string => text(x, true) && /^\d{4}-\d{2}-\d{2}T/.test(x) && Number.isFinite(Date.parse(x));
const nullableDate = (x: unknown) => x === null || date(x);
const strings = (x: Record<string, unknown>, keys: string[]) => keys.every(k => text(x[k]));
const counts = (x: Record<string, unknown>, keys: string[]) => keys.every(k => count(x[k]));
const rows = (x: unknown, check: (row: Record<string, unknown>) => boolean) => Array.isArray(x) && x.length <= 50000 && x.every(row => object(row) && check(row));
function requireValid(valid: boolean): void { if (!valid) throw new Error("admin_response_invalid"); }

export function session(x: unknown): DTO["AuthMeResponse"] {
  requireValid(object(x) && text(x.username, true) && text(x.csrf_token, true) && date(x.expires_at) && Date.parse(x.expires_at) > Date.now());
  return x as DTO["AuthMeResponse"];
}
export function snapshot(o: unknown, t: unknown, l: unknown, e: unknown, a: unknown): void {
  requireValid(object(o) && object(o.hub) && strings(o.hub, ["public_ip", "wg_ip", "version"]) && count(o.hub.uptime_seconds) &&
    object(o.stats) && counts(o.stats, ["token_count", "enabled_token_count", "active_lease_count", "egress_online_count", "rotate_today_count"]) && date(o.updated_at));
  requireValid(object(t) && rows(t.tokens, x => text(x.id, true) && strings(x, ["masked_token", "client_name", "egress_id", "egress_name", "wg_address"]) &&
    typeof x.enabled === "boolean" && ["enabled", "disabled", "expired", "expiring"].includes(x.status as string) &&
    (x.expires_at === null || text(x.expires_at)) && nullableDate(x.last_active_at)));
  requireValid(object(l) && rows(l.leases, x => text(x.token_id, true) && strings(x, ["masked_token", "client_name", "source_ip", "egress_id"]) && date(x.seen_at) && nullableDate(x.expires_at)));
  requireValid(object(e) && rows(e.egress, x => text(x.id, true) && strings(x, ["display_name", "region", "type", "management_addr", "proxy_addr"]) &&
    ["online", "offline", "degraded", "deprecated"].includes(x.status as string) && (x.rotate_lock_until === undefined || nullableDate(x.rotate_lock_until)) &&
    (x.rotate_state === undefined || ["idle", "cooldown", "unknown"].includes(x.rotate_state as string)) &&
    (x.raw_health === undefined || x.raw_health === null || object(x.raw_health)) && (x.session_count === undefined || count(x.session_count)) && (x.active_connections === undefined || count(x.active_connections))));
  requireValid(object(a) && rows(a.events, x => count(x.id) && strings(x, ["actor", "source_ip", "event_type", "target", "result"]) && date(x.occurred_at) &&
    (x.error_code === undefined || x.error_code === null || text(x.error_code)) && (x.detail === undefined || x.detail === null || object(x.detail))));
}
export function tokenSecret(x: unknown, id: string): DTO["TokenSecretResponse"] {
  requireValid(object(x) && x.id === id && text(x.token, true));
  return x as DTO["TokenSecretResponse"];
}
export function exitIP(x: unknown, id: string): DTO["EgressExitIPResponse"] {
  requireValid(object(x) && x.egress_id === id && text(x.exit_ip, true) && date(x.checked_at) &&
    (x.ipv4 === undefined || text(x.ipv4)) && (x.ipv6 === undefined || text(x.ipv6)));
  return x as DTO["EgressExitIPResponse"];
}
export function rotate(x: unknown, id: string, seconds: number): DTO["RotateIPResponse"] {
  requireValid(object(x) && x.egress_id === id && x.down_seconds === seconds && ["triggered", "busy"].includes(x.status as string) &&
    (x.retry_after_seconds === undefined || count(x.retry_after_seconds)) && (x.message === undefined || text(x.message)));
  return x as DTO["RotateIPResponse"];
}

// Exhaustive presentation maps consume generated canonical enums. Unknown or
// missing migration facts cannot become a partial, apparently healthy report.
export const migrationBlockerLabels: Record<DTO["MigrationBlocker"], string> = {
  campaign_not_configured: "迁移 campaign 尚未配置，T0 未设置",
  inventory_not_registered: "尚未登记批准的固定清册",
  e2e_evidence_unavailable: "缺少规定的端到端验收证据",
  approved_release_unavailable: "缺少批准的发行版本证据",
  revocation_evidence_unavailable: "缺少数据面撤销证据",
  session_cleanup_unavailable: "缺少持续会话清理证据",
  recovery_evidence_unavailable: "缺少恢复验收证据",
  quiet_window_unavailable: "连续观察窗口尚未完成",
  observer_continuity_unavailable: "尚不能证明 observer 连续性",
  dataplane_startup_unverified: "数据面启动及失败屏障尚未完成核验",
  observation_write_failed: "观察写入健康未得到确认",
  observer_gap: "持久观察历史存在缺口",
  unobserved_tokens: "仍有未观测成员",
  non_secure_bootstrap_tokens: "仍有 legacy 或未知的最近 bootstrap 观测",
  extra_unregistered: "存在清册外的未登记成员",
  source_missing: "源配置已缺失，固定清册成员仍保留",
  source_disabled: "源配置已停用，尚不能据此证明撤销完成",
  source_expired: "源配置已到期，尚不能据此证明撤销完成",
  source_invalid: "源配置无效，需人工核验",
  disposition_required: "成员处置尚待核验",
  lineage_unknown: "安装保管链未知",
  lineage_unverified: "安装引用仅为声明，保管链未经核验",
  shared_lineage_unverified: "共享安装引用未经核验",
  historical_legacy: "历史保留 legacy 事实",
  historical_unknown: "历史保留未知客户端事实",
  historical_compat: "历史保留兼容入口事实",
  historical_denied: "历史保留拒绝请求事实",
  historical_error: "历史保留错误请求事实",
};
export const migrationSourceLabels: Record<DTO["MigrationClientStatus"]["source_state"], string> = {
  enabled: "当前启用", disabled: "已停用", expired: "已到期", missing: "源已缺失", invalid: "源无效",
};
export const migrationLineageLabels: Record<DTO["MigrationClientStatus"]["lineage_state"], string> = {
  unknown: "保管链未知", declared_unverified: "单一声明，未核验", shared_unverified: "共享声明，未核验",
};
export const migrationMembershipLabels: Record<DTO["MigrationClientStatus"]["membership"], string> = {
  baseline: "固定清册", extra: "额外未登记", unregistered: "未登记清册",
};
export const migrationClassLabels: Record<DTO["MigrationClientStatus"]["migration_class"], string> = {
  secure_bootstrap: "安全 bootstrap 观测", legacy: "legacy 观测", unknown: "未知观测",
};
export const migrationRunLabels: Record<DTO["MigrationObserverRun"]["state"], string> = {
  open: "观测运行未关闭", closed: "正常关闭", failed: "失败 / 缺口",
};
export const migrationReasonLabels: Record<DTO["MigrationObserverRun"]["reason"], string> = {
  none: "无已记录失败", write_failed: "写入失败", unclean_shutdown: "未正常关闭", sink_replaced: "观察 sink 换代", observer_closed: "观察运行已关闭",
};
const migrationRequiredBlockers: DTO["MigrationBlocker"][] = [
  "campaign_not_configured", "e2e_evidence_unavailable", "approved_release_unavailable", "revocation_evidence_unavailable",
  "session_cleanup_unavailable", "recovery_evidence_unavailable", "quiet_window_unavailable", "observer_continuity_unavailable", "dataplane_startup_unverified",
];
const hex = (x: unknown, length: number): x is string => typeof x === "string" && x.length === length && /^[a-f0-9]+$/.test(x);
const finiteLabel = (x: unknown): x is string => text(x) && x.length <= 128 && !/[\u0000-\u001f\u007f]/.test(x);
const enumValue = (x: unknown, labels: Record<string, string>) => typeof x === "string" && Object.hasOwn(labels, x);
const boundedCount = (x: unknown) => count(x) && x <= 4096;
const distinct = (xs: unknown[]) => new Set(xs).size === xs.length;
const migrationBlocks = (x: unknown): x is DTO["MigrationBlocker"][] => Array.isArray(x) && x.length <= Object.keys(migrationBlockerLabels).length && distinct(x) && x.every(v => enumValue(v, migrationBlockerLabels));
const historyCountFields = ["secure_bootstrap_count", "legacy_count", "unknown_count", "compat_ingress_count", "denied_count", "error_count"];
function migrationHistory(x: unknown): boolean {
  return object(x) && counts(x, historyCountFields) && nullableDate(x.first_seen_at) && nullableDate(x.last_seen_at) &&
    ((x.first_seen_at === null && x.last_seen_at === null) || (date(x.first_seen_at) && date(x.last_seen_at) && Date.parse(x.first_seen_at) <= Date.parse(x.last_seen_at)));
}
function migrationClient(x: unknown): boolean {
  return object(x) && hex(x.token_id, 12) && enumValue(x.membership, migrationMembershipLabels) && enumValue(x.source_state, migrationSourceLabels) &&
    (x.owner_ref === "" || hex(x.owner_ref, 32)) && typeof x.shared === "boolean" && enumValue(x.lineage_state, migrationLineageLabels) &&
    Array.isArray(x.installation_refs) && x.installation_refs.length <= 16 && distinct(x.installation_refs) && x.installation_refs.every(ref => hex(ref, 32)) &&
    x.lineage_state === (x.shared || x.installation_refs.length > 1 ? "shared_unverified" : x.owner_ref !== "" && x.installation_refs.length === 1 ? "declared_unverified" : "unknown") &&
    typeof x.observed === "boolean" && typeof x.private_key_returned === "boolean" && ["client_product", "client_version", "ingress", "key_mode"].every(key => finiteLabel(x[key])) &&
    count(x.protocol_version) && enumValue(x.migration_class, migrationClassLabels) && nullableDate(x.last_seen_at) && migrationHistory(x.history) && migrationBlocks(x.blockers);
}
function migrationObserver(x: unknown): boolean {
  if (!object(x) || !hex(x.current_run_id, 32) || typeof x.healthy !== "boolean" || !boundedCount(x.gap_count) || !nullableDate(x.last_success_at) ||
      !Array.isArray(x.runs) || x.runs.length > 4096) return false;
  if (!x.runs.every(run => object(run) && hex(run.run_id, 32) && date(run.started_at) && nullableDate(run.ended_at) && nullableDate(run.last_success_at) &&
      enumValue(run.state, migrationRunLabels) && enumValue(run.reason, migrationReasonLabels))) return false;
  const runs = x.runs as DTO["MigrationObserverRun"][];
  if (!distinct(runs.map(run => run.run_id))) return false;
  if (!runs.every(run => (run.ended_at === null || Date.parse(run.ended_at) >= Date.parse(run.started_at)) &&
    (run.last_success_at === null || Date.parse(run.last_success_at) >= Date.parse(run.started_at)) &&
    (run.state === "open" ? run.ended_at === null && run.reason === "none" : run.state === "closed" ? run.ended_at !== null && run.reason === "none" : run.reason !== "none"))) return false;
  const current = runs.find(run => run.run_id === x.current_run_id);
  const gaps = runs.filter(run => run.state === "failed" || (run.state === "open" && run.run_id !== x.current_run_id)).length;
  return !!current && x.gap_count === gaps && (!x.healthy || (x.gap_count === 0 && current.state === "open" && current.reason === "none"));
}
function requiredMemberBlocks(row: DTO["MigrationClientStatus"]): DTO["MigrationBlocker"][] {
  const needed: DTO["MigrationBlocker"][] = ["approved_release_unavailable", "e2e_evidence_unavailable", "revocation_evidence_unavailable", "session_cleanup_unavailable", "recovery_evidence_unavailable"];
  if (row.membership === "extra") needed.push("extra_unregistered");
  if (row.source_state !== "enabled") needed.push(`source_${row.source_state}`, "disposition_required");
  if (row.lineage_state === "unknown") needed.push("lineage_unknown");
  if (row.lineage_state === "declared_unverified") needed.push("lineage_unverified");
  if (row.lineage_state === "shared_unverified" || row.shared || row.installation_refs.length > 1) needed.push("shared_lineage_unverified");
  if (!row.observed) needed.push("unobserved_tokens");
  if (row.migration_class !== "secure_bootstrap") needed.push("non_secure_bootstrap_tokens");
  const historyBlocks: [keyof DTO["MigrationHistory"], DTO["MigrationBlocker"]][] = [
    ["legacy_count", "historical_legacy"], ["unknown_count", "historical_unknown"], ["compat_ingress_count", "historical_compat"], ["denied_count", "historical_denied"], ["error_count", "historical_error"],
  ];
  for (const [key, code] of historyBlocks) if (typeof row.history[key] === "number" && row.history[key] > 0) needed.push(code);
  return needed;
}
export function migration(x: unknown): DTO["MigrationReadinessResponse"] {
  if (!object(x)) throw new Error("admin_response_invalid");
  requireValid(x.contract_version === 2 && x.ready === false && x.mode === "observation_only" && x.campaign_configured === false && x.t0 === null &&
    typeof x.inventory_registered === "boolean" && text(x.registry_id) && text(x.approved_inventory_sha256) &&
    ["baseline_member_count", "member_count", "extra_member_count", "valid_token_count", "observed_token_count", "unobserved_token_count", "secure_bootstrap_token_count", "legacy_token_count", "unknown_token_count", "compat_ingress_token_count"].every(key => boundedCount(x[key])) &&
    date(x.generated_at) && date(x.observer_started_at) && nullableDate(x.last_observation_at) && typeof x.observation_write_healthy === "boolean" &&
    migrationBlocks(x.blockers) && migrationObserver(x.observer) && Array.isArray(x.clients) && x.clients.length <= 4096 && x.clients.every(migrationClient));
  const dto = x as DTO["MigrationReadinessResponse"];
  const members = dto.clients;
  const blocks = new Set(dto.blockers);
  requireValid(distinct(members.map(row => row.token_id)) && dto.member_count === members.length &&
    dto.baseline_member_count === members.filter(row => row.membership === "baseline").length &&
    dto.extra_member_count === members.filter(row => row.membership === "extra").length &&
    dto.valid_token_count === members.filter(row => row.source_state === "enabled").length &&
    dto.observed_token_count === members.filter(row => row.observed).length && dto.unobserved_token_count === members.filter(row => !row.observed).length &&
    dto.secure_bootstrap_token_count === members.filter(row => row.migration_class === "secure_bootstrap").length &&
    dto.legacy_token_count === members.filter(row => row.migration_class === "legacy").length && dto.unknown_token_count === members.filter(row => row.migration_class === "unknown").length &&
    dto.compat_ingress_token_count === members.filter(row => row.history.compat_ingress_count > 0).length &&
    dto.observation_write_healthy === dto.observer.healthy && migrationRequiredBlockers.every(code => blocks.has(code)) &&
    members.every(row => row.blockers.every(code => blocks.has(code)) && requiredMemberBlocks(row).every(code => row.blockers.includes(code))));
  if (dto.inventory_registered) {
    requireValid(hex(dto.registry_id, 32) && hex(dto.approved_inventory_sha256, 64) && members.every(row => row.membership !== "unregistered"));
  } else {
    requireValid(dto.registry_id === "" && dto.approved_inventory_sha256 === "" && dto.baseline_member_count === 0 && dto.extra_member_count === 0 &&
      members.every(row => row.membership === "unregistered") && blocks.has("inventory_not_registered"));
  }
  requireValid((dto.observer.gap_count === 0 || (!dto.observer.healthy && blocks.has("observer_gap"))) &&
    (dto.observer.healthy || blocks.has("observation_write_failed")));
  return dto;
}
