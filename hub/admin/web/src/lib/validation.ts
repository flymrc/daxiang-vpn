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
