import { invoke } from "@tauri-apps/api/core";
import type { ResultDTO, StatusDTO } from "$lib/contracts.generated";

// zhvpn `status --json`, with the GUI's persistent proxy-recovery diagnostic.
export type Status = StatusDTO & {
  system_proxy_error?: string;
};

// Mirrors zhvpn `login --json`.
export type LoginResult = ResultDTO;

// connect/disconnect consume typed `start`/`stop --json` results.
// connect(globalProxy=true) requests a CLI-owned Windows proxy lease; fast=true passes
// `--fast` through to the sidecar and may trigger UAC.
export type ActionResult = ResultDTO & {
  warning?: string;
};

// Mirrors zhvpn `rotate-ip --json`.
export type RotateResult = ResultDTO;

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
