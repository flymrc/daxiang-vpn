import { invoke } from "@tauri-apps/api/core";

// Mirrors zhvpn `status --json`.
export type Status = {
  running: boolean;
  proxy?: string;
  proxy_reachable: boolean;
  egress?: string;
  egress_ip?: string;
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
// connect may also carry a `warning` (e.g. system proxy auto-set failed).
export type ActionResult = {
  ok: boolean;
  message: string;
  warning?: string;
};

// Mirrors zhvpn `rotate-ip --json`.
export type RotateResult = {
  ok: boolean;
  before?: string;
  after?: string;
  egress?: string;
  error?: string;
};

export const api = {
  status: () => invoke<Status>("status"),
  login: (token: string) => invoke<LoginResult>("login", { token }),
  connect: (fast: boolean) => invoke<ActionResult>("connect", { fast }),
  disconnect: () => invoke<ActionResult>("disconnect"),
  rotateIp: () => invoke<RotateResult>("rotate_ip"),
  logout: () => invoke<ActionResult>("logout"),
};
