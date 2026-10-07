import type { components } from "./openapi";
import * as validate from "./validation";

export type AuthMe = components["schemas"]["AuthMeResponse"];
export type Overview = components["schemas"]["OverviewResponse"];
export type TokenSummary = components["schemas"]["TokenSummary"];
export type LeaseSummary = components["schemas"]["LeaseSummary"];
export type EgressSummary = components["schemas"]["EgressSummary"];
export type AuditEvent = components["schemas"]["AuditEvent"];
export type RotateIPResponse = components["schemas"]["RotateIPResponse"];
export type TokenSecretResponse = components["schemas"]["TokenSecretResponse"];
export type EgressExitIPResponse = components["schemas"]["EgressExitIPResponse"];

export class ApiError extends Error {
  status: number;
  code: string;

  constructor(status: number, code: string) {
    super(code);
    this.status = status;
    this.code = code;
  }
}

export class AdminApi {
  csrfToken = "";

  async request<T>(path: string, init: RequestInit = {}, allowBusy = false): Promise<T> {
    const method = init.method || "GET";
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }
    if (method !== "GET" && path !== "/auth/login" && this.csrfToken) {
      headers.set("X-CSRF-Token", this.csrfToken);
    }
    const res = await fetch(`/admin/api${path}`, {
      ...init,
      method,
      headers,
      credentials: "same-origin",
    });
    if (res.status === 204) {
      return undefined as T;
    }
    const text = await res.text();
    let data: unknown;
    try { data = text ? JSON.parse(text) : {}; }
    catch { throw new ApiError(res.ok ? 502 : res.status, "response_invalid"); }
    const result = data !== null && typeof data === "object" ? data as Record<string, unknown> : {};
    if (!res.ok && !(allowBusy && res.status === 409 && result.status === "busy")) {
      throw new ApiError(res.status, typeof result.error === "string" ? result.error : "request_failed");
    }
    return data as T;
  }

  async login(username: string, password: string): Promise<AuthMe> {
    const me = validate.session(await this.request<AuthMe>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }));
    this.csrfToken = me.csrf_token;
    return me;
  }

  async logout(): Promise<void> {
    await this.request<void>("/auth/logout", { method: "POST" });
    this.csrfToken = "";
  }

  async me(): Promise<AuthMe> {
    const me = validate.session(await this.request<AuthMe>("/auth/me"));
    this.csrfToken = me.csrf_token;
    return me;
  }

  overview() {
    return this.request<Overview>("/overview");
  }

  tokens() {
    return this.request<{ tokens: TokenSummary[] }>("/tokens");
  }

  revealToken(tokenId: string) {
    return this.request<TokenSecretResponse>(`/tokens/${encodeURIComponent(tokenId)}/secret`);
  }

  leases() {
    return this.request<{ leases: LeaseSummary[] }>("/leases");
  }

  egress() {
    return this.request<{ egress: EgressSummary[] }>("/egress");
  }

  revealEgressExitIP(egressId: string) {
    return this.request<EgressExitIPResponse>(`/egress/${encodeURIComponent(egressId)}/exit-ip`);
  }

  events(limit = 80) {
    return this.request<{ events: AuditEvent[] }>(`/events?limit=${limit}`);
  }

  rotateIP(egressId: string, downSeconds = 8) {
    return this.request<RotateIPResponse>(`/egress/${encodeURIComponent(egressId)}/rotate-ip`, {
      method: "POST",
      body: JSON.stringify({ down_seconds: downSeconds }),
    }, true);
  }
}
