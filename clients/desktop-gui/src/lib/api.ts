import { invoke } from "@tauri-apps/api/core";

// Mirrors zhvpn `status --json`.
export type Status = {
  running: boolean;
  proxy?: string;
  proxy_reachable: boolean;
  egress?: string;
  egress_ip?: string;
  egress_ipv4?: string;
  egress_ipv6?: string;
  error?: string;
};

// Mirrors zhvpn `login --json`.
export type LoginResult = {
  ok: boolean;
  egress?: string;
  proxy?: string;
  error?: string;
};

// connect/disconnect wrap `start`/`stop` (human output → {ok, message}).
// connect(globalProxy=true) enables Windows system proxy; fast=true passes
// `--fast` through to the sidecar and may trigger UAC.
export type ActionResult = {
  ok: boolean;
  message: string;
  warning?: string;
};

// Mirrors zhvpn `rotate-ip --json`.
export type RotateResult = {
  ok: boolean;
  status?: string;
  message?: string;
  before?: string;
  after?: string;
  egress?: string;
  error?: string;
};

export const api = {
  status: () => invoke<Status>("status"),
  statusIp: () => invoke<Status>("status_ip"),
  appVersion: () => invoke<string>("app_version"),
  login: (token: string) => invoke<LoginResult>("login", { token }),
  connect: (globalProxy: boolean, fast: boolean) =>
    invoke<ActionResult>("connect", { globalProxy, fast }),
  disconnect: () => invoke<ActionResult>("disconnect"),
  rotateIp: () => invoke<RotateResult>("rotate_ip"),
  logout: () => invoke<ActionResult>("logout"),
};
