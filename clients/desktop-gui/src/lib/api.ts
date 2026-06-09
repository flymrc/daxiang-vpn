import { invoke } from "@tauri-apps/api/core";

// Mirrors dxvpn `status --json`.
export type Status = {
  running: boolean;
  proxy?: string;
  proxy_reachable: boolean;
  egress?: string;
  egress_ip?: string;
  error?: string;
};

// Mirrors dxvpn `login --json`.
export type LoginResult = {
  ok: boolean;
  egress?: string;
  proxy?: string;
  error?: string;
};

// connect/disconnect wrap `start`/`stop` (human output → {ok, message}).
export type ActionResult = {
  ok: boolean;
  message: string;
};

export const api = {
  status: () => invoke<Status>("status"),
  login: (token: string) => invoke<LoginResult>("login", { token }),
  connect: (fast: boolean) => invoke<ActionResult>("connect", { fast }),
  disconnect: () => invoke<ActionResult>("disconnect"),
};
