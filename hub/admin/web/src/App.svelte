<script lang="ts">
  import { onMount } from "svelte";
  import { AdminApi, ApiError } from "$lib/api";
  import * as validate from "$lib/validation";
  import type { AuditEvent, AuthMe, EgressExitIPResponse, EgressSummary, LeaseSummary, MigrationReadiness, Overview, TokenSummary } from "$lib/api";

  type View = "overview" | "tokens" | "egress" | "clients" | "migration" | "logs";
  type ThemeName = "深空蓝" | "石墨灰" | "午夜紫" | "浅色";
  type ExitIPRow = { label?: string; value: string; muted?: boolean };
  type TokenSortKey = "masked_token" | "client_name" | "status" | "egress_id" | "wg_address" | "expires_at" | "last_active";
  type SortDirection = "asc" | "desc";

  const api = new AdminApi();

  const themes: Record<ThemeName, Record<string, string>> = {
    深空蓝: {
      bg: "#0b0e13",
      bg0: "#0a0d12",
      bg1: "#0d1117",
      bg2: "#0f141b",
      bg3: "#11161d",
      sel: "#121a24",
      hov: "#10161e",
      rowh: "#0c1117",
      bd: "#1b222c",
      bd2: "#232c37",
      tx: "#e6edf3",
      tx2: "#9aa7b4",
      tx3: "#677483",
      cardsh: "none",
    },
    石墨灰: {
      bg: "#0e0f11",
      bg0: "#0b0c0e",
      bg1: "#121316",
      bg2: "#16181b",
      bg3: "#1a1d21",
      sel: "#1e2228",
      hov: "#181a1e",
      rowh: "#141518",
      bd: "#23262b",
      bd2: "#2e3238",
      tx: "#eceef0",
      tx2: "#a3a8af",
      tx3: "#6c7178",
      cardsh: "none",
    },
    午夜紫: {
      bg: "#0d0b14",
      bg0: "#0b0913",
      bg1: "#100d1a",
      bg2: "#14111f",
      bg3: "#181426",
      sel: "#1c1730",
      hov: "#15111f",
      rowh: "#100d1a",
      bd: "#241f33",
      bd2: "#2f2942",
      tx: "#ece8f5",
      tx2: "#a79fbd",
      tx3: "#6f6786",
      cardsh: "none",
    },
    浅色: {
      bg: "#e9ebf0",
      bg0: "#ffffff",
      bg1: "#ffffff",
      bg2: "#ffffff",
      bg3: "#eef1f5",
      sel: "#e8f0fd",
      hov: "#f0f3f8",
      rowh: "#f5f7fb",
      bd: "#e3e7ec",
      bd2: "#d2d8e0",
      tx: "#16202e",
      tx2: "#566273",
      tx3: "#8893a1",
      cardsh: "0 1px 2px rgba(16,24,40,.05), 0 4px 12px rgba(16,24,40,.07)",
    },
  };

  const themeSwatches: Record<ThemeName, string> = {
    深空蓝: "#4c8dff",
    石墨灰: "#8d96a3",
    午夜紫: "#a876ff",
    浅色: "#f6f8fb",
  };

  const themeStorageKey = "zhhub-admin-theme";
  const tokenPageSize = 10;
  const migrationPageSize = 20;
  const themeOrder: ThemeName[] = ["深空蓝", "浅色", "石墨灰", "午夜紫"];
  const viewRoutes: Record<View, string> = {
    overview: "overview",
    tokens: "tokens",
    egress: "egress",
    clients: "clients",
    migration: "migration",
    logs: "logs",
  };
  const routeViews: Record<string, View> = {
    overview: "overview",
    tokens: "tokens",
    egress: "egress",
    clients: "clients",
    leases: "clients",
    migration: "migration",
    logs: "logs",
    events: "logs",
  };
  const tokenSortLabels: Record<TokenSortKey, string> = {
    masked_token: "授权码",
    client_name: "客户端",
    status: "状态",
    egress_id: "出口",
    wg_address: "WG 地址",
    expires_at: "到期",
    last_active: "最近活跃",
  };
  const defaultTokenSortDirections: Record<TokenSortKey, SortDirection> = {
    masked_token: "asc",
    client_name: "asc",
    status: "asc",
    egress_id: "asc",
    wg_address: "asc",
    expires_at: "asc",
    last_active: "desc",
  };
  const tokenStatusOrder: Record<TokenSummary["status"], number> = {
    enabled: 1,
    expiring: 2,
    disabled: 3,
    expired: 4,
  };
  const tokenSortCollator = new Intl.Collator("zh-Hans-CN", { numeric: true, sensitivity: "base" });

  const demoOverview: Overview = {
    hub: { public_ip: "36.50.84.68", wg_ip: "10.66.0.1", version: "zhhub v0.4.2", uptime_seconds: 18 * 86400 + 7 * 3600 },
    stats: { token_count: 21, enabled_token_count: 19, active_lease_count: 2, egress_online_count: 1, rotate_today_count: 8 },
    updated_at: new Date().toISOString(),
  };

  const demoTokens: TokenSummary[] = buildDemoTokens();

  const demoLeases: LeaseSummary[] = [
    { token_id: "tok-1", masked_token: "ZH-***01", client_name: "cn-client-01", source_ip: "219.76.18.x", egress_id: "jp-android-01", seen_at: new Date().toISOString(), expires_at: new Date(Date.now() + 30000).toISOString() },
    { token_id: "tok-4", masked_token: "ZH-***04", client_name: "admin-innernet", source_ip: "10.66.0.5", egress_id: "管理内网", seen_at: new Date(Date.now() - 2000).toISOString(), expires_at: new Date(Date.now() + 30000).toISOString() },
  ];

  const demoEgress: EgressSummary[] = [
    {
      id: "jp-android-01",
      display_name: "日本手机出口",
      region: "JP",
      type: "android-reverse",
      management_addr: "10.66.0.101:2022",
      proxy_addr: "10.66.0.1:18081",
      status: "online",
      session_count: 1,
      active_connections: 3,
      rotate_lock_until: null,
      raw_health: { exit_ip: "153.246.142.77", latency_ms: 42 },
    },
    {
      id: "mac-mini",
      display_name: "Mac mini 出口",
      region: "JP",
      type: "socks",
      management_addr: "",
      proxy_addr: "10.66.0.100:1080",
      status: "deprecated",
      session_count: 0,
      active_connections: 0,
      rotate_lock_until: null,
      raw_health: null,
    },
  ];

  const demoEvents: AuditEvent[] = [
    { id: 1, occurred_at: todayAt("14:38:02"), actor: "cn-client-01", source_ip: "219.76.18.x", event_type: "rotate-ip", target: "jp-android-01", detail: { down_seconds: 8 }, result: "triggered", error_code: null },
    { id: 2, occurred_at: todayAt("14:37:51"), actor: "cn-client-03", source_ip: "180.12.x", event_type: "rotate-ip", target: "jp-android-01", detail: { retry_after: "12s" }, result: "busy", error_code: "rotate_busy" },
    { id: 3, occurred_at: todayAt("14:22:10"), actor: "cn-client-01", source_ip: "219.76.18.x", event_type: "bootstrap", target: "jp-android-01", detail: { token: "ZH-JP-***01" }, result: "ok", error_code: null },
    { id: 4, occurred_at: todayAt("14:09:33"), actor: "ZH-JP-***04", source_ip: "180.12.x", event_type: "bootstrap", target: "jp-android-01", detail: { reason: "token_in_use" }, result: "denied", error_code: "token_in_use" },
    { id: 5, occurred_at: todayAt("13:55:02"), actor: "ZH-***000", source_ip: "45.83.x", event_type: "bootstrap", target: "-", detail: { reason: "invalid_token" }, result: "denied", error_code: "invalid_token" },
    { id: 6, occurred_at: todayAt("13:40:18"), actor: "cn-client-01", source_ip: "219.76.18.x", event_type: "rotate-ip", target: "jp-android-01", detail: { down_seconds: 8 }, result: "triggered", error_code: null },
  ];

  let ready = false;
  // Demo data is an explicit local preview, never a fallback for live empty/error results.
  const previewEnabled = import.meta.env.DEV && new URLSearchParams(window.location.search).get("preview") === "1";
  let dataReady = false;
  let dataError = "";
  let refreshing = false;
  let activeRefresh: object | null = null;
  let dataGeneration = 0;
  let mutationPending = false;
  let mutationUnknown = false;
  const unknownOverview: Overview = {
    hub: { public_ip: "未知", wg_ip: "未知", version: "状态未知", uptime_seconds: 0 },
    stats: { token_count: 0, enabled_token_count: 0, active_lease_count: 0, egress_online_count: 0, rotate_today_count: 0 },
    updated_at: "",
  };
  let authed = false;
  let view: View = "overview";
  let me: AuthMe | null = null;
  let username = "admin";
  let password = "";
  let loginError = "";
  let loading = false;
  let toast = "";
  let theme: ThemeName = readSavedTheme();
  let themeMenu = false;
  let modal: "rotate" | null = null;
  let rotateTarget = "jp-android-01";
  let updatedAt = "";
  let overview: Overview | null = null;
  let tokens: TokenSummary[] = [];
  let leases: LeaseSummary[] = [];
  let egress: EgressSummary[] = [];
  let events: AuditEvent[] = [];
  let migrationReadiness: MigrationReadiness | null = null;
  let migrationPage = 1;
  let observerPage = 1;
  let tokenSecrets: Record<string, string> = {};
  let exitIPSecrets: Record<string, EgressExitIPResponse> = {};
  let revealingTokenID = "";
  let revealingExitIPID = "";
  let tokenPage = 1;
  let tokenSortKey: TokenSortKey = "last_active";
  let tokenSortDirection: SortDirection = "desc";

  onMount(() => {
    view = readHashView();
    const onHashChange = () => {
      view = readHashView();
    };
    window.addEventListener("hashchange", onHashChange);

    void (async () => {
      if (previewEnabled) {
        me = { username: "preview", csrf_token: "", expires_at: new Date(Date.now() + 86400000).toISOString() };
        authed = true;
        loadDemoData();
        ready = true;
        return;
      }
      try {
        me = await api.me();
        authed = true;
        await refreshAll();
      } catch {
        authed = false;
      } finally {
        ready = true;
      }
    })();

    return () => window.removeEventListener("hashchange", onHashChange);
  });

  $: themeVars = [
    ...Object.entries(themes[theme]).map(([key, value]) => `--${key}:${value}`),
    "--accent:#4c8dff",
  ].join(";");
  $: themeOptions = themeOrder.map((name) => ({
    name,
    swatch: themeSwatches[name],
    active: name === theme,
  }));
  $: displayOverview = dataReady ? (overview ?? unknownOverview) : unknownOverview;
  $: displayTokens = dataReady ? tokens : [];
  $: displayLeases = dataReady ? leases : [];
  $: displayEgress = dataReady ? egress : [];
  $: displayEvents = dataReady ? events : [];
  $: displayMigration = dataReady && !previewEnabled ? migrationReadiness : null;
  $: migrationMembers = displayMigration?.clients ?? [];
  $: migrationPageCount = Math.max(1, Math.ceil(migrationMembers.length / migrationPageSize));
  $: if (migrationPage > migrationPageCount) migrationPage = migrationPageCount;
  $: pagedMigrationMembers = migrationMembers.slice((migrationPage - 1) * migrationPageSize, migrationPage * migrationPageSize);
  $: observerRuns = [...(displayMigration?.observer.runs ?? [])].sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at));
  $: observerPageCount = Math.max(1, Math.ceil(observerRuns.length / migrationPageSize));
  $: if (observerPage > observerPageCount) observerPage = observerPageCount;
  $: pagedObserverRuns = observerRuns.slice((observerPage - 1) * migrationPageSize, observerPage * migrationPageSize);
  $: canOperate = dataReady && !previewEnabled && !loading && !mutationPending;
  $: canRotate = canOperate && !mutationUnknown;
  $: enabledTokens = displayTokens.filter((token) => token.enabled).length;
  $: tokenLastActiveByID = buildTokenLastActiveByID(displayLeases);
  $: sortedTokens = sortTokens(displayTokens, tokenLastActiveByID, tokenSortKey, tokenSortDirection);
  $: tokenPageCount = Math.max(1, Math.ceil(sortedTokens.length / tokenPageSize));
  $: if (tokenPage > tokenPageCount) tokenPage = tokenPageCount;
  $: tokenRangeStart = sortedTokens.length === 0 ? 0 : (tokenPage - 1) * tokenPageSize + 1;
  $: tokenRangeEnd = Math.min(sortedTokens.length, tokenPage * tokenPageSize);
  $: pagedTokens = sortedTokens.slice(Math.max(0, tokenRangeStart - 1), tokenRangeEnd);
  $: tokenPageItems = pageItems(tokenPage, tokenPageCount);
  $: stats = [
    { label: "在线客户端", value: dataReady ? String(displayOverview.stats.active_lease_count) : "—", sub: dataReady ? `共 ${enabledTokens} 个启用授权码` : "状态未验证", dot: dataReady ? "ok" : "idle" },
    { label: "启用授权码", value: dataReady ? String(displayOverview.stats.enabled_token_count) : "—", sub: dataReady ? `共 ${displayTokens.length} 个 token` : "状态未验证", dot: dataReady ? "ok" : "idle" },
    { label: "出口在线", value: dataReady ? String(displayOverview.stats.egress_online_count) : "—", sub: dataReady ? `${displayEgress.filter((node) => node.status === "online").length} 在线 · ${displayEgress.filter((node) => node.status === "deprecated").length} 已弃用` : "状态未验证", dot: dataReady ? "ok" : "idle" },
    { label: "今日换 IP", value: dataReady ? String(displayOverview.stats.rotate_today_count) : "—", sub: dataReady ? "客户端 · 管理员" : "状态未验证", dot: dataReady && displayOverview.stats.rotate_today_count === 0 ? "ok" : "idle" },
  ];

  async function login() {
    loginError = "";
    loading = true;
    try {
      me = await api.login(username, password);
      password = "";
      authed = true;
      await refreshAll();
    } catch (err) {
      loginError = err instanceof ApiError && err.code === "rate_limited" ? "登录失败次数过多，请稍后再试" : "用户名或密码不正确";
    } finally {
      loading = false;
    }
  }

  async function refreshAll() {
    if (refreshing) return;
    if (previewEnabled) { loadDemoData(); return; }
    refreshing = true;
    const refreshOwner = {};
    activeRefresh = refreshOwner;
    const generation = ++dataGeneration;
    loading = true;
    dataReady = false;
    migrationReadiness = null;
    dataError = "";
    modal = null;
    tokenSecrets = {};
    exitIPSecrets = {};
    revealingTokenID = "";
    revealingExitIPID = "";
    try {
      const [nextOverview, nextTokens, nextLeases, nextEgress, nextEvents, nextMigration] = await Promise.all([
        api.overview(),
        api.tokens(),
        api.leases(),
        api.egress(),
        api.events(),
        api.migrationReadiness(),
      ]);
      validate.snapshot(nextOverview, nextTokens, nextLeases, nextEgress, nextEvents);
      if (generation !== dataGeneration || activeRefresh !== refreshOwner) return;
      overview = nextOverview;
      tokens = nextTokens.tokens;
      leases = nextLeases.leases;
      egress = nextEgress.egress;
      events = nextEvents.events;
      migrationReadiness = nextMigration;
      migrationPage = 1;
      observerPage = 1;
      updatedAt = new Date().toLocaleTimeString();
      dataReady = true;
    } catch (err) {
      if (err instanceof ApiError && [401, 403].includes(err.status)) {
        authorityFailure(err);
        return;
      }
      if (generation !== dataGeneration || activeRefresh !== refreshOwner) return;
      overview = null;
      tokens = []; leases = []; egress = []; events = [];
      migrationReadiness = null;
      dataError = "数据读取失败，当前状态未知；请重新刷新";
    } finally {
      if (activeRefresh === refreshOwner) {
        activeRefresh = null;
        refreshing = false;
        loading = false;
      }
    }
  }

  function loadDemoData() {
    overview = demoOverview;
    tokens = demoTokens;
    leases = demoLeases;
    egress = demoEgress;
    events = demoEvents;
    migrationReadiness = null;
    updatedAt = new Date().toLocaleTimeString();
    dataReady = true;
    dataError = "";
  }

  function pickTheme(name: ThemeName) {
    theme = name;
    saveTheme(name);
    themeMenu = false;
  }

  function go(next: View) {
    view = next;
    const nextHash = `#/${viewRoutes[next]}`;
    if (window.location.hash !== nextHash) {
      window.location.hash = nextHash;
    }
  }

  function readHashView(): View {
    const route = window.location.hash.replace(/^#\/?/, "").split(/[/?]/, 1)[0];
    return routeViews[route] || "overview";
  }

  function readSavedTheme(): ThemeName {
    try {
      const saved = localStorage.getItem(themeStorageKey) as ThemeName | null;
      return saved && themeOrder.includes(saved) ? saved : "深空蓝";
    } catch {
      return "深空蓝";
    }
  }

  function saveTheme(name: ThemeName) {
    try {
      localStorage.setItem(themeStorageKey, name);
    } catch {
      // Theme persistence is nice-to-have; the UI should still work if storage is blocked.
    }
  }

  function showToast(message: string) {
    toast = message;
    window.setTimeout(() => {
      toast = "";
    }, 2800);
  }

  function authorityFailure(err: unknown) {
    if (!(err instanceof ApiError) || ![401, 403].includes(err.status)) return;
    dataGeneration++;
    dataReady = false;
    overview = null;
    tokens = []; leases = []; egress = []; events = [];
    migrationReadiness = null;
    tokenSecrets = {}; exitIPSecrets = {};
    revealingTokenID = ""; revealingExitIPID = "";
    modal = null;
    dataError = "访问权限已失效，请重新登录";
    if (err.status === 401) authed = false;
  }

  function openRotate(node: EgressSummary) {
    if (!canRotate) return;
    if (node.rotate_state === "unknown") {
      showToast("换 IP 结果未确认，请先核实出口和操作日志");
      return;
    }
    if (node.rotate_state === "cooldown") {
      showToast("换 IP 调度或冷却尚未结束，请刷新状态");
      return;
    }
    const lockedUntil = activeRotateLockUntil(node);
    if (lockedUntil) {
      showToast(`换 IP 正在进行中，${remainingRetryText(lockedUntil)}`);
      void refreshAll();
      return;
    }
    rotateTarget = node.id;
    modal = "rotate";
  }

  async function toggleTokenSecret(tokenID: string) {
    if (!canOperate || revealingTokenID) return;
    if (tokenSecrets[tokenID]) {
      const next = { ...tokenSecrets };
      delete next[tokenID];
      tokenSecrets = next;
      return;
    }
    revealingTokenID = tokenID;
    const generation = dataGeneration;
    try {
      const result = validate.tokenSecret(await api.revealToken(tokenID), tokenID);
      if (generation !== dataGeneration || !dataReady || refreshing) return;
      tokenSecrets = { ...tokenSecrets, [tokenID]: result.token };
    } catch (err) {
      authorityFailure(err);
      showToast("授权码读取失败");
    } finally {
      if (generation === dataGeneration && revealingTokenID === tokenID) revealingTokenID = "";
    }
  }

  async function toggleExitIP(node: EgressSummary) {
    if (!canOperate || revealingExitIPID) return;
    if (exitIPSecrets[node.id]) {
      const next = { ...exitIPSecrets };
      delete next[node.id];
      exitIPSecrets = next;
      return;
    }
    revealingExitIPID = node.id;
    const generation = dataGeneration;
    try {
      const result = validate.exitIP(await api.revealEgressExitIP(node.id), node.id);
      if (generation !== dataGeneration || !dataReady || refreshing) return;
      exitIPSecrets = { ...exitIPSecrets, [node.id]: result };
    } catch (err) {
      authorityFailure(err);
      showToast("出口 IP 探测失败，请稍后再试");
    } finally {
      if (generation === dataGeneration && revealingExitIPID === node.id) revealingExitIPID = "";
    }
  }

  async function confirmRotate() {
    if (!canRotate || mutationPending) { modal = null; return; }
    const node = displayEgress.find(item => item.id === rotateTarget);
    if (!node || !nodeCanRotate(node)) { modal = null; return; }
    mutationPending = true;
    const target = rotateTarget;
    modal = null;
    loading = true;
    try {
      const result = validate.rotate(await api.rotateIP(target, 8), target, 8);
      showToast(result.status === "busy" ? `换 IP 冷却中 ${result.retry_after_seconds || 1}s` : "换 IP 已触发 · down_seconds=8");
      await refreshAll();
    } catch (err) {
      authorityFailure(err);
      mutationUnknown = true;
      showToast("换 IP 结果未确认，请查询状态后再处理");
    } finally {
      mutationPending = false;
      loading = false;
    }
  }

  function navClass(name: View, current: View) {
    return current === name ? "on" : "";
  }

  function sortTokenTable(key: TokenSortKey) {
    if (tokenSortKey === key) {
      tokenSortDirection = tokenSortDirection === "asc" ? "desc" : "asc";
    } else {
      tokenSortKey = key;
      tokenSortDirection = defaultTokenSortDirections[key];
    }
    tokenPage = 1;
  }

  function tokenSortAria(key: TokenSortKey) {
    if (tokenSortKey !== key) return undefined;
    return tokenSortDirection === "asc" ? "ascending" : "descending";
  }

  function tokenSortIcon(key: TokenSortKey) {
    if (tokenSortKey !== key) return "↕";
    return tokenSortDirection === "asc" ? "↑" : "↓";
  }

  function tokenSortTitle(key: TokenSortKey) {
    const next = tokenSortKey === key ? (tokenSortDirection === "asc" ? "desc" : "asc") : defaultTokenSortDirections[key];
    return `按${tokenSortLabels[key]}${next === "asc" ? "升序" : "降序"}排序`;
  }

  function buildTokenLastActiveByID(rows: LeaseSummary[]) {
    const result: Record<string, string> = {};
    for (const row of rows) {
      if (row.seen_at) result[row.token_id] = row.seen_at;
    }
    return result;
  }

  function sortTokens(rows: TokenSummary[], lastActiveByID: Record<string, string>, key: TokenSortKey, direction: SortDirection) {
    const factor = direction === "asc" ? 1 : -1;
    return [...rows].sort((left, right) => {
      const selected = compareTokenRows(left, right, key, lastActiveByID);
      if (selected !== 0) return selected * factor;
      return tokenSortCollator.compare(left.masked_token, right.masked_token) || tokenSortCollator.compare(left.client_name, right.client_name);
    });
  }

  function compareTokenRows(left: TokenSummary, right: TokenSummary, key: TokenSortKey, lastActiveByID: Record<string, string>) {
    switch (key) {
      case "masked_token":
        return tokenSortCollator.compare(left.masked_token, right.masked_token);
      case "client_name":
        return tokenSortCollator.compare(left.client_name, right.client_name);
      case "status":
        return compareNumber(tokenStatusOrder[left.status] ?? 99, tokenStatusOrder[right.status] ?? 99);
      case "egress_id":
        return tokenSortCollator.compare(left.egress_id, right.egress_id);
      case "wg_address":
        return tokenSortCollator.compare(left.wg_address, right.wg_address);
      case "expires_at":
        return compareNumber(tokenExpirySortValue(left.expires_at), tokenExpirySortValue(right.expires_at));
      case "last_active":
        return compareNumber(tokenLastActiveSortValue(left, lastActiveByID), tokenLastActiveSortValue(right, lastActiveByID));
    }
  }

  function compareNumber(left: number, right: number) {
    if (left === right) return 0;
    return left < right ? -1 : 1;
  }

  function tokenExpirySortValue(value?: string | null) {
    if (!value) return Number.POSITIVE_INFINITY;
    const parsed = new Date(value).getTime();
    return Number.isNaN(parsed) ? Number.POSITIVE_INFINITY : parsed;
  }

  function tokenLastActiveSortValue(token: TokenSummary, lastActiveByID: Record<string, string>) {
    const parsed = new Date(tokenLastActiveAt(token, lastActiveByID) || "").getTime();
    return Number.isNaN(parsed) ? 0 : parsed;
  }

  function tokenLastActiveAt(token: TokenSummary, lastActiveByID: Record<string, string>) {
    return token.last_active_at || lastActiveByID[token.id] || null;
  }

  function statusPill(status: string) {
    if (["online", "enabled", "ok", "triggered"].includes(status)) return "ok";
    if (["busy", "expiring", "degraded"].includes(status)) return "warn";
    if (["offline", "expired", "error", "denied"].includes(status)) return "err";
    if (status === "deprecated") return "muted";
    return "muted";
  }

  function statusLabel(status: string) {
    const labels: Record<string, string> = {
      online: "在线",
      degraded: "降级",
      offline: "离线",
      deprecated: "已弃用",
      enabled: "启用",
      disabled: "已停用",
      expiring: "即将到期",
      expired: "已过期",
      triggered: "triggered",
      busy: "busy",
      ok: "通过",
      denied: "拒绝",
    };
    return labels[status] || status;
  }

  function dotClass(status: string) {
    if (["online", "enabled", "ok", "triggered"].includes(status)) return "ok";
    if (["busy", "expiring", "degraded"].includes(status)) return "warn";
    if (["offline", "expired", "error", "denied"].includes(status)) return "err";
    if (status === "deprecated") return "dep";
    return "idle";
  }

  function tokenLast(token: TokenSummary, lastActiveByID: Record<string, string>) {
    const seenAt = tokenLastActiveAt(token, lastActiveByID);
    if (seenAt) return { label: `在线 · ${secondsAgo(seenAt)}`, cls: "ok" };
    if (token.status === "expired") return { label: "已过期", cls: "err" };
    return { label: "未接入", cls: "idle" };
  }

  function shortDate(value?: string | null) {
    if (!value) return "永久";
    if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return value;
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return value;
    return parsed.toLocaleString();
  }

  function activeRotateLockUntil(node: EgressSummary) {
    const value = node.rotate_lock_until;
    if (!value) return null;
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return value;
    return parsed.getTime() > Date.now() ? value : null;
  }

  function nodeCanRotate(node: EgressSummary) {
    return node.rotate_state !== "unknown" && node.rotate_state !== "cooldown" && !activeRotateLockUntil(node);
  }

  function rotateStateLabel(node: EgressSummary) {
    if (node.rotate_state === "unknown") return "结果未确认 · 需核验";
    if (node.rotate_state === "cooldown" || activeRotateLockUntil(node)) return node.rotate_lock_until ? `调度 / 冷却至 ${shortDate(node.rotate_lock_until)}` : "调度 / 冷却中";
    return node.rotate_state === "idle" ? "本地无锁" : "未报告";
  }

  function remainingRetryText(value: string) {
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return "请稍后再试";
    const seconds = Math.max(1, Math.ceil((parsed.getTime() - Date.now()) / 1000));
    if (seconds < 60) return `约 ${seconds} 秒后可再试`;
    return `约 ${Math.ceil(seconds / 60)} 分钟后可再试`;
  }

  function timeOnly(value?: string | null) {
    if (!value) return "--:--:--";
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return value;
    return parsed.toLocaleTimeString();
  }

  function secondsAgo(value?: string | null) {
    if (!value) return "--";
    const parsed = new Date(value).getTime();
    if (Number.isNaN(parsed)) return "--";
    const seconds = Math.max(0, Math.round((Date.now() - parsed) / 1000));
    return `${seconds}s`;
  }

  function uptimeLabel(seconds: number) {
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    return `${days}d ${String(hours).padStart(2, "0")}h`;
  }

  function eventDetail(event: AuditEvent) {
    if (!event.detail) return event.error_code || "";
    return Object.entries(event.detail)
      .map(([key, value]) => `${key}=${String(value)}`)
      .join(" · ");
  }

  function tokenValue(tokenID: string, maskedToken: string, revealedToken: string | undefined, revealingID: string) {
    if (revealingID === tokenID) return "读取中...";
    return revealedToken || maskedToken;
  }

  function pageItems(current: number, total: number): Array<number | string> {
    if (total <= 7) return Array.from({ length: total }, (_, index) => index + 1);
    const pages = new Set([1, total, current - 1, current, current + 1].filter((value) => value >= 1 && value <= total));
    const result: Array<number | string> = [];
    let previous = 0;
    for (const page of Array.from(pages).sort((a, b) => a - b)) {
      if (previous && page - previous > 1) result.push(`gap-${previous}-${page}`);
      result.push(page);
      previous = page;
    }
    return result;
  }

  function exitIPValue(node: EgressSummary) {
    const raw = node.raw_health || {};
    const value = raw.exit_ip || raw.public_ip || raw.ip;
    return typeof value === "string" ? value : null;
  }

  function exitIPRows(node: EgressSummary, revealed: EgressExitIPResponse | undefined, revealingID: string): ExitIPRow[] {
    if (revealingID === node.id) return [{ value: "探测中...", muted: true }];
    if (revealed) {
      const rows: ExitIPRow[] = [];
      rows.push(revealed.ipv6 ? { label: "IPv6", value: revealed.ipv6 } : { label: "IPv6", value: "暂不可用", muted: true });
      rows.push(revealed.ipv4 ? { label: "IPv4", value: revealed.ipv4 } : { label: "IPv4", value: "暂不可用", muted: true });
      return rows;
    }
    const value = exitIPValue(node);
    return [{ value: value ? maskIPAddress(value) : "点击眼睛探测", muted: !value }];
  }

  function maskIPAddress(value: string) {
    if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) {
      const parts = value.split(".");
      return `${parts[0]}.${parts[1]}.***.***`;
    }
    if (value.includes(":")) {
      const parts = value.split(":").filter(Boolean);
      if (parts.length >= 2) return `${parts[0]}:${parts[1]}:****`;
    }
    return "***";
  }

  function latency(node: EgressSummary) {
    const raw = node.raw_health || {};
    const value = raw.latency_ms ?? raw.latency;
    return typeof value === "number" && Number.isFinite(value) && value >= 0 ? `${value}ms` : "—";
  }

  function todayAt(hms: string) {
    const day = new Date().toISOString().slice(0, 10);
    return `${day}T${hms}+09:00`;
  }

  function buildDemoTokens(): TokenSummary[] {
    return Array.from({ length: 21 }, (_, index) => {
      const n = index + 1;
      let status: TokenSummary["status"] = "enabled";
      let enabled = true;
      let egressID = "jp-android-01";
      let egressName = "jp-android-01";
      let expiresAt: string | null = "2026-12-31";
      let clientName = `cn-client-${String(n).padStart(2, "0")}`;

      if (n === 1) {
        status = "expiring";
        expiresAt = "2026-07-01";
      } else if (n === 4) {
        clientName = "admin-innernet";
        egressID = "管理内网";
        egressName = "管理内网";
        expiresAt = null;
      } else if (n === 20) {
        status = "disabled";
        enabled = false;
        clientName = "trial-20";
        expiresAt = "2026-09-30";
      } else if (n === 21) {
        status = "expired";
        enabled = false;
        clientName = "legacy-21";
        egressID = "mac-mini";
        egressName = "mac-mini";
        expiresAt = "2026-06-10";
      } else if (n % 7 === 0) {
        egressID = "管理内网";
        egressName = "管理内网";
        expiresAt = null;
      } else if (n <= 10) {
        expiresAt = "2026-09-30";
      }

      return {
        id: `tok-${n}`,
        masked_token: `ZH-***${String(n).padStart(2, "0")}`,
        client_name: clientName,
        enabled,
        status,
        egress_id: egressID,
        egress_name: egressName,
        wg_address: `10.66.0.${19 + n}/24`,
        expires_at: expiresAt,
        last_active_at: null,
      };
    });
  }
</script>

{#if !ready}
  <main class="app loading" style={themeVars}>
    <div class="mono muted">loading admin console</div>
  </main>
{:else if !authed}
  <main class="app loginwrap" style={themeVars}>
    <form class="login card col gap16" on:submit|preventDefault={login}>
      <div class="fx ac gap10">
        <div class="mark"></div>
        <div>
          <div class="brand">纵横 Hub<span class="dim"> 控制台</span></div>
          <div class="note mono">jp-proxy.ruichao.dev</div>
        </div>
      </div>
      <label class="field">
        <span class="lbl">管理员</span>
        <input class="inp" bind:value={username} autocomplete="username" />
      </label>
      <label class="field">
        <span class="lbl">密码</span>
        <input class="inp" bind:value={password} type="password" autocomplete="current-password" />
      </label>
      {#if loginError}
        <div class="warnbox errbox">{loginError}</div>
      {/if}
      <button class="btn primary loginbtn" disabled={loading}>登录</button>
    </form>
  </main>
{:else}
  <div class="app col" style={themeVars}>
    <header class="topbar fx ac jb">
      <div class="fx ac gap14">
        <div class="fx ac gap10">
          <div class="mark"></div>
          <div class="brand">纵横 Hub<span class="dim"> 控制台</span></div>
        </div>
        <span class="vchip mono">{displayOverview.hub.version || "版本未知"}</span>
      </div>
      <div class="fx ac gap14">
        <div class="hubpill fx ac gap8">
          {#if dataReady}<span class="livedot"></span>{/if}
          <span class="dim">{previewEnabled ? "本地预览" : dataReady ? "管理 API 已响应" : "Hub 状态未知"}</span>
          <span class="mono hubip">{displayOverview.hub.public_ip}</span>
          <span class="muted">·</span>
          <span class="mono dim">{displayOverview.hub.wg_ip}</span>
          <span class="muted">·</span>
          <span class="muted">uptime {dataReady ? uptimeLabel(displayOverview.hub.uptime_seconds) : "—"}</span>
        </div>
      </div>
      <div class="fx ac gap10">
        <div class="fx ac gap6 muted topupdate">
          {#if dataReady}<span class="livedot tiny"></span>{/if}{loading ? "同步中" : dataReady ? `更新于 ${updatedAt}` : "尚未验证"}
        </div>
        <button class="iconbtn fx ac jc" on:click={refreshAll} title="刷新" disabled={loading}>
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" />
            <path d="M13.5 2v3.2h-3.2" />
          </svg>
        </button>
        <div class="posrel">
          <button class="themebtn fx ac gap8" on:click={() => (themeMenu = !themeMenu)}>
            <span class="sw" style={`background:${themeSwatches[theme]}`}></span>
            <span>{theme}</span>
            <svg width="11" height="11" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.4">
              <path d="M3 4.5 6 7.5 9 4.5" />
            </svg>
          </button>
          {#if themeMenu}
            <div class="tmenu">
              <div class="tmlbl">主题</div>
              {#each themeOptions as item}
                <button class={`tmi fx ac gap10 ${item.active ? "on" : ""}`} on:click={() => pickTheme(item.name)}>
                  <span class="sw" style={`background:${item.swatch}`}></span>
                  <span class="f1">{item.name}</span>
                  {#if item.active}<span class="acc">✓</span>{/if}
                </button>
              {/each}
            </div>
          {/if}
        </div>
        <div class="who fx ac gap8">
          <span class="mono dim">{me?.username || "root"}@hub</span>
          <span class="av fx ac jc">{(me?.username || "R").slice(0, 1).toUpperCase()}</span>
        </div>
      </div>
    </header>

    <div class="body fx f1">
      <aside class="side col">
        <div class="navlbl">运营</div>
        <button aria-label="总览" aria-current={view === "overview" ? "page" : undefined} title="总览" class={`navitem fx ac gap10 ${navClass("overview", view)}`} on:click={() => go("overview")}>
          <svg class="nico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
            <rect x="1.6" y="1.6" width="5" height="5" rx="1.2" /><rect x="9.4" y="1.6" width="5" height="5" rx="1.2" /><rect x="1.6" y="9.4" width="5" height="5" rx="1.2" /><rect x="9.4" y="9.4" width="5" height="5" rx="1.2" />
          </svg>
          <span class="f1">总览</span>
        </button>
        <button aria-label="授权码" aria-current={view === "tokens" ? "page" : undefined} title="授权码" class={`navitem fx ac gap10 ${navClass("tokens", view)}`} on:click={() => go("tokens")}>
          <svg class="nico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
            <circle cx="5" cy="5" r="3.2" /><path d="M7.3 7.3 13 13" /><path d="M11 11l1.6-1.6" />
          </svg>
          <span class="f1">授权码</span><span class="badge mono fx ac jc">{displayTokens.length}</span>
        </button>
        <button aria-label="出口节点" aria-current={view === "egress" ? "page" : undefined} title="出口节点" class={`navitem fx ac gap10 ${navClass("egress", view)}`} on:click={() => go("egress")}>
          <svg class="nico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
            <rect x="2" y="2.2" width="12" height="5" rx="1.4" /><rect x="2" y="8.8" width="12" height="5" rx="1.4" /><circle cx="4.6" cy="4.7" r=".8" fill="currentColor" stroke="none" /><circle cx="4.6" cy="11.3" r=".8" fill="currentColor" stroke="none" />
          </svg>
          <span class="f1">出口节点</span><span class="badge mono fx ac jc">{displayEgress.length}</span>
        </button>
        <button aria-label="在线客户端" aria-current={view === "clients" ? "page" : undefined} title="在线客户端" class={`navitem fx ac gap10 ${navClass("clients", view)}`} on:click={() => go("clients")}>
          <svg class="nico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4">
            <circle cx="6" cy="5.4" r="2.4" /><path d="M2 13.2c0-2.3 1.8-3.6 4-3.6s4 1.3 4 3.6" /><circle cx="12" cy="4.8" r="1.8" /><path d="M11.4 9.8c1.7.1 2.9 1.2 2.9 3" />
          </svg>
          <span class="f1">在线客户端</span><span class="badge mono fx ac jc">{displayLeases.length}</span>
        </button>
        <button aria-label="迁移观测" aria-current={view === "migration" ? "page" : undefined} title="迁移观测" class={`navitem fx ac gap10 ${navClass("migration", view)}`} on:click={() => go("migration")}>
          <svg class="nico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4"><path d="m8 1.5 5.5 2v4c0 3-2.3 5.3-5.5 7-3.2-1.7-5.5-4-5.5-7v-4Z"/><path d="M5 8h6M8 5v6"/></svg>
          <span class="f1">迁移观测</span><span class="badge mono fx ac jc">{displayMigration?.member_count ?? "—"}</span>
        </button>
        <button aria-label="操作日志" aria-current={view === "logs" ? "page" : undefined} title="操作日志" class={`navitem fx ac gap10 ${navClass("logs", view)}`} on:click={() => go("logs")}>
          <svg class="nico" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round">
            <path d="M3 4h10M3 8h10M3 12h7" />
          </svg>
          <span class="f1">操作日志</span>
        </button>
        <div class="f1"></div>
        <div class="sidefoot col gap6">
          <div class="navlbl footlbl">HUB 概况</div>
          <div class="sfrow fx ac jb"><span class="muted">授权 API</span><span class="mono dim">:18080</span></div>
          <div class="sfrow fx ac jb"><span class="muted">WG 入口</span><span class="mono dim">:51820</span></div>
          <div class="sfrow fx ac jb"><span class="muted">reverse 入口</span><span class="mono dim">:18081</span></div>
          <div class="sfrow fx ac jb"><span class="muted">reverse TCP</span><span class="mono dim">:39093</span></div>
        </div>
      </aside>

      <main class="content f1 col gap16">
        {#if dataError}<div class="warnbox" role="status">{dataError}</div>{/if}
        {#if mutationPending}<div class="warnbox" role="status">换 IP 请求正在确认；暂不能再次提交</div>{/if}
        {#if mutationUnknown}<div class="warnbox" role="status">换 IP 结果未确认，暂不能再次提交；请核实操作日志和出口状态</div>{/if}
        {#if previewEnabled}<div class="warnbox" role="status">本地预览 · 演示数据 · 操作已禁用</div>{/if}
        {#if view === "overview"}
          <div class="fx ac jb">
            <div>
              <div class="h1">总览</div>
              <div class="sub">纵横 VPN · Hub 实时状态</div>
            </div>
            <div class="fx ac gap10">
              <span class="tag">{previewEnabled ? "演示数据" : "管理视图"}</span>
              <button class="btn ghost btnxs" on:click={refreshAll}>刷新数据</button>
            </div>
          </div>
          <div class="stats">
            {#each stats as item}
              <div class="card col gap12">
                <div class="fx ac jb"><span class="statlbl">{item.label}</span><span class={`dot ${item.dot}`}></span></div>
                <div class="statbig mono">{item.value}</div>
                <div class="statsub">{item.sub}</div>
              </div>
            {/each}
          </div>
          <div class="two">
            <div class="card col gap14">
              <div class="fx ac jb"><span class="sechd">出口节点健康</span><button class="linkbtn note acc" on:click={() => go("egress")}>查看全部 →</button></div>
              <div class="col gap10">
                {#each displayEgress as node}
                  <div class={`healthrow fx ac jb ${node.status === "deprecated" ? "depcard" : ""}`}>
                    <div class="fx ac gap10 min0">
                      <span class={`dot ${dotClass(node.status)}`}></span>
                      <div class="min0">
                        <div class="strong truncate">{node.id}</div>
                        <div class="note mono truncate">{node.display_name} · {node.proxy_addr}</div>
                      </div>
                    </div>
                    <div class="fx ac gap16 healthmetrics">
                      <div class="tc"><div class="note">会话</div><div class="mono strong">{node.session_count ?? "—"}</div></div>
                      <div class="tc"><div class="note">连接</div><div class="mono strong">{node.active_connections ?? "—"}</div></div>
                      {#if node.raw_health}
                        <div class="tc"><div class="note">延迟</div><div class="mono strong">{latency(node)}</div></div>
                      {/if}
                      <span class={`pill ${statusPill(node.status)}`}>{statusLabel(node.status)}</span>
                    </div>
                  </div>
                {/each}
              </div>
            </div>
            <div class="card col gap14">
              <div class="fx ac jb"><span class="sechd">最近操作</span><button class="linkbtn note acc" on:click={() => go("logs")}>完整日志 →</button></div>
              <div class="col">
                {#each displayEvents.slice(0, 6) as event}
                  <div class="logline fx ac jb">
                    <div class="fx ac gap10 min0">
                      <span class="mono muted logtime">{timeOnly(event.occurred_at)}</span>
                      <span class={`pill ${event.event_type === "rotate-ip" ? "acc" : "info"}`}>{event.event_type}</span>
                      <span class="dim truncate">{event.actor}</span>
                    </div>
                    <span class={`pill ${statusPill(event.result)}`}>{statusLabel(event.result)}</span>
                  </div>
                {/each}
              </div>
            </div>
          </div>
        {:else if view === "tokens"}
          <div class="fx ac jb">
            <div><div class="h1">授权码</div><div class="sub">{displayTokens.length} 个 · {enabledTokens} 启用 · token 即客户端登录凭证</div></div>
            <button class="btn primary" disabled><span class="plus">+</span>新建授权码</button>
          </div>
          <div class="fx ac gap8">
            <button class="chip on" disabled>全部 {displayTokens.length}</button><button class="chip" disabled>启用 {enabledTokens}</button><button class="chip" disabled>已停用</button><button class="chip" disabled>已过期</button>
          </div>
          <div class="card flush">
            <table class="tbl">
              <thead>
                <tr>
                  <th aria-sort={tokenSortAria("masked_token")}>
                    <button class="sorthead" type="button" title={tokenSortTitle("masked_token")} on:click={() => sortTokenTable("masked_token")}>
                      <span>授权码</span><span class={`sortmark ${tokenSortKey === "masked_token" ? "on" : ""}`} aria-hidden="true">{tokenSortIcon("masked_token")}</span>
                    </button>
                  </th>
                  <th aria-sort={tokenSortAria("client_name")}>
                    <button class="sorthead" type="button" title={tokenSortTitle("client_name")} on:click={() => sortTokenTable("client_name")}>
                      <span>客户端</span><span class={`sortmark ${tokenSortKey === "client_name" ? "on" : ""}`} aria-hidden="true">{tokenSortIcon("client_name")}</span>
                    </button>
                  </th>
                  <th aria-sort={tokenSortAria("status")}>
                    <button class="sorthead" type="button" title={tokenSortTitle("status")} on:click={() => sortTokenTable("status")}>
                      <span>状态</span><span class={`sortmark ${tokenSortKey === "status" ? "on" : ""}`} aria-hidden="true">{tokenSortIcon("status")}</span>
                    </button>
                  </th>
                  <th aria-sort={tokenSortAria("egress_id")}>
                    <button class="sorthead" type="button" title={tokenSortTitle("egress_id")} on:click={() => sortTokenTable("egress_id")}>
                      <span>出口</span><span class={`sortmark ${tokenSortKey === "egress_id" ? "on" : ""}`} aria-hidden="true">{tokenSortIcon("egress_id")}</span>
                    </button>
                  </th>
                  <th aria-sort={tokenSortAria("wg_address")}>
                    <button class="sorthead" type="button" title={tokenSortTitle("wg_address")} on:click={() => sortTokenTable("wg_address")}>
                      <span>WG 地址</span><span class={`sortmark ${tokenSortKey === "wg_address" ? "on" : ""}`} aria-hidden="true">{tokenSortIcon("wg_address")}</span>
                    </button>
                  </th>
                  <th aria-sort={tokenSortAria("expires_at")}>
                    <button class="sorthead" type="button" title={tokenSortTitle("expires_at")} on:click={() => sortTokenTable("expires_at")}>
                      <span>到期</span><span class={`sortmark ${tokenSortKey === "expires_at" ? "on" : ""}`} aria-hidden="true">{tokenSortIcon("expires_at")}</span>
                    </button>
                  </th>
                  <th aria-sort={tokenSortAria("last_active")}>
                    <button class="sorthead" type="button" title={tokenSortTitle("last_active")} on:click={() => sortTokenTable("last_active")}>
                      <span>最近活跃</span><span class={`sortmark ${tokenSortKey === "last_active" ? "on" : ""}`} aria-hidden="true">{tokenSortIcon("last_active")}</span>
                    </button>
                  </th>
                  <th class="right">操作</th>
                </tr>
              </thead>
              <tbody>
                {#each pagedTokens as row}
                  {@const last = tokenLast(row, tokenLastActiveByID)}
                  <tr>
                    <td>
                      <div class="secretline tokenline mono strong">
                        <span class="secrettext">{tokenValue(row.id, row.masked_token, tokenSecrets[row.id], revealingTokenID)}</span>
                        <button
                          class="eyebtn"
                          type="button"
                          aria-label={tokenSecrets[row.id] ? "隐藏授权码" : "显示授权码"}
                          title={tokenSecrets[row.id] ? "隐藏授权码" : "显示授权码"}
                          aria-pressed={Boolean(tokenSecrets[row.id])}
                          disabled={!canOperate || revealingTokenID === row.id}
                          on:click|stopPropagation={() => toggleTokenSecret(row.id)}
                        >
                          {#if tokenSecrets[row.id]}
                            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m3 3 18 18" /><path d="M10.6 10.6a2 2 0 0 0 2.8 2.8" /><path d="M9.5 5.2A9.7 9.7 0 0 1 12 5c5 0 8.5 4.1 9.6 6.2a1.7 1.7 0 0 1 0 1.6 15 15 0 0 1-2.1 2.9" /><path d="M6.4 6.4A15 15 0 0 0 2.4 11.2a1.7 1.7 0 0 0 0 1.6C3.5 14.9 7 19 12 19a9.7 9.7 0 0 0 4.2-.9" /></svg>
                          {:else}
                            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2.4 11.2C3.5 9.1 7 5 12 5s8.5 4.1 9.6 6.2a1.7 1.7 0 0 1 0 1.6C20.5 14.9 17 19 12 19s-8.5-4.1-9.6-6.2a1.7 1.7 0 0 1 0-1.6Z" /><circle cx="12" cy="12" r="3" /></svg>
                          {/if}
                        </button>
                      </div>
                    </td>
                    <td class="dim">{row.client_name}</td>
                    <td><span class={`pill ${statusPill(row.status)}`}>{statusLabel(row.status)}</span></td>
                    <td class="mono dim">{row.egress_id}</td>
                    <td class="mono muted">{row.wg_address}</td>
                    <td class="mono dim">{shortDate(row.expires_at)}</td>
                    <td><span class="fx ac gap8"><span class={`dot ${last.cls}`}></span><span class="dim">{last.label}</span></span></td>
                    <td><div class="fx ac gap6 jend"><button class="btn btnxs" disabled>{row.enabled ? "停用" : "启用"}</button><button class="btn btnxs ghost" disabled>编辑</button><button class="btn btnxs ghost danger" disabled>删除</button></div></td>
                  </tr>
                {/each}
              </tbody>
            </table>
            {#if tokenPageCount > 1}
              <div class="pager fx ac jb">
                <div class="note mono">显示 {tokenRangeStart}-{tokenRangeEnd} / {sortedTokens.length}</div>
                <div class="fx ac gap6">
                  <button class="btn btnxs ghost" disabled={tokenPage === 1} on:click={() => (tokenPage = Math.max(1, tokenPage - 1))}>上一页</button>
                  {#each tokenPageItems as item}
                    {#if typeof item === "number"}
                      <button
                        class={`pagebtn mono ${item === tokenPage ? "on" : ""}`}
                        type="button"
                        aria-current={item === tokenPage ? "page" : undefined}
                        on:click={() => (tokenPage = item)}
                      >
                        {item}
                      </button>
                    {:else}
                      <span class="pagegap mono">...</span>
                    {/if}
                  {/each}
                  <button class="btn btnxs ghost" disabled={tokenPage === tokenPageCount} on:click={() => (tokenPage = Math.min(tokenPageCount, tokenPage + 1))}>下一页</button>
                </div>
              </div>
            {/if}
          </div>
        {:else if view === "egress"}
          <div class="fx ac jb">
            <div><div class="h1">出口节点</div><div class="sub">基础设施侧 · Android 反向出口为当前生产路径</div></div>
            <button class="btn ghost btnxs" on:click={refreshAll}>健康检查</button>
          </div>
          {#each displayEgress as node}
            {#if node.id === "jp-android-01"}
              <div class="card flush">
                <div class="nodehd fx ac jb">
                  <div class="fx ac gap12">
                    <span class={`dot ${dotClass(node.status)} bigdot`}></span>
                    <div>
                      <div class="fx ac gap10"><span class="node-title">{node.id}</span><span class={`pill ${statusPill(node.status)}`}>{statusLabel(node.status)}</span><span class="tag">{previewEnabled ? "演示" : "已登记"}</span></div>
                      <div class="note mono node-sub">{node.display_name} · {node.type}</div>
                    </div>
                  </div>
                  <div class="fx ac gap8"><button class="btn primary" disabled={!canRotate || !nodeCanRotate(node)} on:click={() => openRotate(node)}>换 IP</button><button class="btn" disabled>重连隧道</button><button class="btn ghost" disabled>控制台 SSH</button></div>
                </div>
                <div class="kv flat">
                  <div class="kvc ipcard">
                    <div class="kvl">当前出口 IP</div>
                    <div class="kvv secretline ipsecret mono">
                      <div class="ipstack">
                        {#each exitIPRows(node, exitIPSecrets[node.id], revealingExitIPID) as item}
                          <div class={`iprow ${item.label ? "" : "single"}`}>
                            {#if item.label}
                              <span class="iplabel">{item.label}</span>
                            {/if}
                            <span class={`ipvalue ${item.muted ? "muted" : ""}`}>{item.value}</span>
                          </div>
                        {/each}
                      </div>
                      <button
                        class="eyebtn"
                        type="button"
                        aria-label={exitIPSecrets[node.id] ? "隐藏出口 IP" : "显示出口 IP"}
                        title={exitIPSecrets[node.id] ? "隐藏出口 IP" : "显示出口 IP"}
                        aria-pressed={Boolean(exitIPSecrets[node.id])}
                        disabled={!canOperate || revealingExitIPID === node.id}
                        on:click|stopPropagation={() => toggleExitIP(node)}
                      >
                        {#if exitIPSecrets[node.id]}
                          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m3 3 18 18" /><path d="M10.6 10.6a2 2 0 0 0 2.8 2.8" /><path d="M9.5 5.2A9.7 9.7 0 0 1 12 5c5 0 8.5 4.1 9.6 6.2a1.7 1.7 0 0 1 0 1.6 15 15 0 0 1-2.1 2.9" /><path d="M6.4 6.4A15 15 0 0 0 2.4 11.2a1.7 1.7 0 0 0 0 1.6C3.5 14.9 7 19 12 19a9.7 9.7 0 0 0 4.2-.9" /></svg>
                        {:else}
                          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2.4 11.2C3.5 9.1 7 5 12 5s8.5 4.1 9.6 6.2a1.7 1.7 0 0 1 0 1.6C20.5 14.9 17 19 12 19s-8.5-4.1-9.6-6.2a1.7 1.7 0 0 1 0-1.6Z" /><circle cx="12" cy="12" r="3" /></svg>
                        {/if}
                      </button>
                    </div>
                  </div>
                  <div class="kvc"><div class="kvl">回程延迟</div><div class="kvv mono">{latency(node)}</div></div>
                  <div class="kvc"><div class="kvl">运行时隧道接口</div><div class="kvv muted">未提供运行证据</div></div>
                  <div class="kvc"><div class="kvl">换 IP 锁</div><div class="kvv fx ac gap6"><span class={`dot ${node.rotate_state === "unknown" || node.rotate_state === "cooldown" || activeRotateLockUntil(node) ? "warn" : "idle"}`}></span>{rotateStateLabel(node)}</div></div>
                  <div class="kvc"><div class="kvl">运行时运营商 / 制式</div><div class="kvv muted">未提供运行证据</div></div>
                  <div class="kvc"><div class="kvl">今日换 IP</div><div class="kvv mono">{displayOverview.stats.rotate_today_count} 次</div></div>
                  <div class="kvc"><div class="kvl">proxy_addr</div><div class="kvv mono">{node.proxy_addr}</div></div>
                  <div class="kvc"><div class="kvl">management_addr</div><div class="kvv mono">{node.management_addr}</div></div>
                </div>
              </div>
            {:else}
              <div class="card depcard fx ac jb">
                <div class="fx ac gap12"><span class="dot dep bigdot"></span><div><div class="fx ac gap10"><span class="legacy-title">{node.id}</span><span class="pill muted">已弃用</span></div><div class="note mono node-sub">{node.display_name} · {node.proxy_addr}</div></div></div>
                <div class="note legacy-note">2026-06-15 弃用 · 仅作历史诊断 / 管理内网对象,不再承载新流量</div>
              </div>
            {/if}
          {/each}
        {:else if view === "clients"}
          <div class="fx ac jb">
            <div><div class="h1">在线客户端</div><div class="sub">实时租约 · lease TTL 30s · 一个 token 同时只允许一个源 IP</div></div>
            <div class="fx ac gap6 muted client-count">{#if dataReady}<span class="livedot tiny"></span>{displayLeases.length} 个会话{:else}会话状态未知{/if}</div>
          </div>
          <div class="card flush">
            <table class="tbl">
              <thead><tr><th>授权码</th><th>客户端</th><th>源 IP</th><th>出口</th><th>接入时间</th><th>心跳</th><th class="right">操作</th></tr></thead>
              <tbody>
                {#each displayLeases as row}
                  <tr>
                    <td>
                      <div class="secretline tokenline mono">
                        <span class="secrettext">{tokenValue(row.token_id, row.masked_token, tokenSecrets[row.token_id], revealingTokenID)}</span>
                        <button
                          class="eyebtn"
                          type="button"
                          aria-label={tokenSecrets[row.token_id] ? "隐藏授权码" : "显示授权码"}
                          title={tokenSecrets[row.token_id] ? "隐藏授权码" : "显示授权码"}
                          aria-pressed={Boolean(tokenSecrets[row.token_id])}
                          disabled={!canOperate || revealingTokenID === row.token_id}
                          on:click|stopPropagation={() => toggleTokenSecret(row.token_id)}
                        >
                          {#if tokenSecrets[row.token_id]}
                            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m3 3 18 18" /><path d="M10.6 10.6a2 2 0 0 0 2.8 2.8" /><path d="M9.5 5.2A9.7 9.7 0 0 1 12 5c5 0 8.5 4.1 9.6 6.2a1.7 1.7 0 0 1 0 1.6 15 15 0 0 1-2.1 2.9" /><path d="M6.4 6.4A15 15 0 0 0 2.4 11.2a1.7 1.7 0 0 0 0 1.6C3.5 14.9 7 19 12 19a9.7 9.7 0 0 0 4.2-.9" /></svg>
                          {:else}
                            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2.4 11.2C3.5 9.1 7 5 12 5s8.5 4.1 9.6 6.2a1.7 1.7 0 0 1 0 1.6C20.5 14.9 17 19 12 19s-8.5-4.1-9.6-6.2a1.7 1.7 0 0 1 0-1.6Z" /><circle cx="12" cy="12" r="3" /></svg>
                          {/if}
                        </button>
                      </div>
                    </td><td class="dim">{row.client_name}</td><td class="mono dim">{row.source_ip}</td><td class="mono dim">{row.egress_id}</td><td class="mono muted">{timeOnly(row.seen_at)}</td><td><span class="fx ac gap8"><span class="dot ok"></span><span class="dim mono">{secondsAgo(row.seen_at)}</span></span></td>
                    <td><div class="fx jend"><button class="btn btnxs danger" disabled>断开</button></div></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else if view === "migration"}
          <div class="fx ac jb migration-heading">
            <div><div class="h1">迁移观测</div><div class="sub">observation-only · provisional 固定清册 · 只读报告</div></div>
            <span class="pill err">NO-GO</span>
          </div>
          <div class="warnbox" role="status">T0 未设置 · campaign 尚未启动。清册登记与安装引用都是声明；安全 bootstrap 观测不等于合规、批准发行或真实安装保管链。</div>
          {#if !displayMigration}
            <div class="card col gap10" role="status"><span class="sechd">迁移观测状态未知</span><span class="dim">{previewEnabled ? "本地预览未连接迁移观测 API，未使用演示数据填补报告。" : loading ? "正在读取并校验完整报告；旧快照已隐藏。" : "未取得可验证的 v2 完整报告。请刷新；旧响应、读取失败和权限失效均不能表示迁移已就绪。"}</span></div>
          {:else}
            <div class="stats migration-stats">
              <div class="card col gap10"><span class="statlbl">批准清册固定分母</span><span class="statbig mono">{displayMigration.inventory_registered ? displayMigration.baseline_member_count : "未登记"}</span><span class="statsub">停用、到期及源缺失不会移出清册</span></div>
              <div class="card col gap10"><span class="statlbl">全部持久成员</span><span class="statbig mono">{displayMigration.member_count}</span><span class="statsub">当前启用来源 {displayMigration.valid_token_count} · 额外未登记 {displayMigration.extra_member_count}</span></div>
              <div class="card col gap10"><span class="statlbl">有观察 / 未观察</span><span class="statbig mono">{displayMigration.observed_token_count} / {displayMigration.unobserved_token_count}</span><span class="statsub">观测覆盖不能替代迁移放行证据</span></div>
              <div class="card col gap10"><span class="statlbl">持久 observer 缺口</span><span class="statbig mono">{displayMigration.observer.gap_count}</span><span class="statsub">{displayMigration.observer.healthy ? "本次写入无已知缺口，连续观察仍未核验" : "写入健康未确认，历史缺口仍保留"}</span></div>
            </div>
            <div class="card col gap10 migration-identifiers">
              <div class="fx ac jb"><span class="sechd">清册登记</span><span class="pill warn">{displayMigration.inventory_registered ? "已登记 provisional 清册" : "清册未登记"}</span></div>
              <span class="note">固定分母不是生产 campaign；单一声明和共享声明都未经保管链核验。</span>
              {#if displayMigration.inventory_registered}
                <div class="note">registry <span class="mono dim">{displayMigration.registry_id}</span></div>
                <div class="note">批准原始清册 SHA256 <span class="mono dim">{displayMigration.approved_inventory_sha256}</span></div>
              {/if}
              <div class="note">报告生成于 {new Date(displayMigration.generated_at).toLocaleString()} · 最近观察 {displayMigration.last_observation_at ? new Date(displayMigration.last_observation_at).toLocaleString() : "尚无"}</div>
            </div>
            <div class="card col gap10">
              <span class="sechd">全局阻断 · {displayMigration.blockers.length} 项</span>
              <ul class="migration-blocks">
                {#each displayMigration.blockers as code}<li><span>{validate.migrationBlockerLabels[code]}</span><span class="mono note">{code}</span></li>{/each}
              </ul>
            </div>
            <div class="card col gap10"><span class="note">历史字段可能重叠，旧记录导入也会保留负事实；不能相加作为独立请求总量或合规比例。</span></div>
            <div class="card flush">
              <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard focus enables arrow-key scrolling of this bounded region.) -->
              <div class="migration-table-scroll" tabindex="0" role="region" aria-label="持久迁移成员，横向滚动表格">
                <table class="tbl migration-table">
                  <thead><tr><th>持久成员 / 清册来源</th><th>当前来源 / 最近成功 bootstrap</th><th>安装声明 · 未核验</th><th>单调观察历史</th><th>逐成员阻断</th></tr></thead>
                  <tbody>
                    {#each pagedMigrationMembers as row (row.token_id)}
                      <tr>
                        <td><div class="mono strong">{row.token_id}</div><div class="note">{validate.migrationMembershipLabels[row.membership]}</div></td>
                        <td><div class="pill warn">{validate.migrationSourceLabels[row.source_state]}</div><div class="note">{validate.migrationClassLabels[row.migration_class]}</div><div class="note">客户端自报 {row.client_product || "未知"} {row.client_version || "未知"}</div><div class="note">协议 {row.protocol_version === 0 ? "未知" : row.protocol_version} · {row.ingress || "未知入口"} · {row.key_mode || "未知密钥模式"}</div><div class="note">{row.private_key_returned ? "最近成功响应返回过私钥" : "最近成功响应未标记返回私钥"}</div></td>
                        <td><div class="pill warn">{validate.migrationLineageLabels[row.lineage_state]}</div><div class="note">owner 声明 <span class="mono">{row.owner_ref || "未知"}</span></div><div class="note">{row.shared ? "共享声明" : "未声明共享；不等于独占已核验"}</div>{#each row.installation_refs as ref}<div class="mono note">{ref}</div>{:else}<div class="note">无安装引用声明</div>{/each}</td>
                        <td><div class="note">安全 bootstrap {row.history.secure_bootstrap_count} · legacy {row.history.legacy_count} · 未知 {row.history.unknown_count}</div><div class="note">兼容入口 {row.history.compat_ingress_count} · 拒绝 {row.history.denied_count} · 错误 {row.history.error_count}</div><div class="note">首次观察 {row.history.first_seen_at ? new Date(row.history.first_seen_at).toLocaleString() : "尚无"}</div><div class="note">最近观察 {row.history.last_seen_at ? new Date(row.history.last_seen_at).toLocaleString() : "尚无"}</div><div class="note">后来成功不会删除早期负事实</div></td>
                        <td><details><summary>查看全部 {row.blockers.length} 项阻断</summary><ul class="migration-member-blocks">{#each row.blockers as code}<li><span>{validate.migrationBlockerLabels[code]}</span><span class="mono note">{code}</span></li>{/each}</ul></details></td>
                      </tr>
                    {:else}<tr><td colspan="5" class="muted">尚无持久成员；零分母不表示可以启动迁移。</td></tr>{/each}
                  </tbody>
                </table>
              </div>
              <div class="pager fx ac jb"><span class="note">{migrationMembers.length === 0 ? 0 : (migrationPage - 1) * migrationPageSize + 1}–{Math.min(migrationPage * migrationPageSize, migrationMembers.length)} / {migrationMembers.length} 个成员</span><div class="fx ac gap8"><button class="pagebtn" aria-label="成员上一页" disabled={migrationPage <= 1} on:click={() => migrationPage--}>上一页</button><span class="note">{migrationPage}/{migrationPageCount}</span><button class="pagebtn" aria-label="成员下一页" disabled={migrationPage >= migrationPageCount} on:click={() => migrationPage++}>下一页</button></div></div>
            </div>
            <div class="card col gap10 migration-identifiers"><span class="sechd">持久 observer 运行历史</span><span class="note">本次 run <span class="mono dim">{displayMigration.observer.current_run_id}</span> · 缺口 {displayMigration.observer.gap_count}</span><span class="note">正常关闭或当前写入健康不能证明已完成连续观察窗口；历史缺口不会因重启洗掉。</span></div>
            <div class="card flush">
              <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard focus enables arrow-key scrolling of this bounded region.) -->
              <div class="migration-table-scroll" tabindex="0" role="region" aria-label="持久 observer 历史，横向滚动表格">
                <table class="tbl migration-observer-table"><thead><tr><th>run ID</th><th>状态 / 原因</th><th>开始</th><th>结束</th><th>最近成功写入</th></tr></thead><tbody>
                  {#each pagedObserverRuns as run (run.run_id)}<tr><td class="mono">{run.run_id}</td><td><div class="pill warn">{validate.migrationRunLabels[run.state]}</div><div class="note">{validate.migrationReasonLabels[run.reason]}</div></td><td class="note">{new Date(run.started_at).toLocaleString()}</td><td class="note">{run.ended_at ? new Date(run.ended_at).toLocaleString() : "未关闭"}</td><td class="note">{run.last_success_at ? new Date(run.last_success_at).toLocaleString() : "尚无"}</td></tr>{/each}
                </tbody></table>
              </div>
              <div class="pager fx ac jb"><span class="note">{observerRuns.length} 个持久 run · 分页不会删除历史缺口</span><div class="fx ac gap8"><button class="pagebtn" aria-label="observer 上一页" disabled={observerPage <= 1} on:click={() => observerPage--}>上一页</button><span class="note">{observerPage}/{observerPageCount}</span><button class="pagebtn" aria-label="observer 下一页" disabled={observerPage >= observerPageCount} on:click={() => observerPage++}>下一页</button></div></div>
            </div>
          {/if}
        {:else}
          <div class="fx ac jb">
            <div><div class="h1">操作日志</div><div class="sub">bootstrap 与 rotate-ip 事件 · token 已脱敏</div></div>
            <div class="fx ac gap8"><button class="chip on" disabled>全部</button><button class="chip" disabled>bootstrap</button><button class="chip" disabled>rotate-ip</button></div>
          </div>
          <div class="card flush">
            <table class="tbl">
              <thead><tr><th>时间</th><th>事件</th><th>客户端 / token</th><th>出口</th><th>详情</th><th class="right">结果</th></tr></thead>
              <tbody>
                {#each displayEvents as row}
                  <tr>
                    <td class="mono muted">{timeOnly(row.occurred_at)}</td>
                    <td><span class={`pill ${row.event_type === "rotate-ip" ? "acc" : "info"}`}>{row.event_type}</span></td>
                    <td class="mono dim">{row.actor}</td>
                    <td class="mono muted">{row.target}</td>
                    <td class="mono muted detail">{eventDetail(row)}</td>
                    <td><div class="fx jend"><span class={`pill ${statusPill(row.result)}`}>{statusLabel(row.result)}</span></div></td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </main>
    </div>

    {#if modal === "rotate"}
      <div class="ovl fx ac jc">
        <button class="backdropbtn" aria-label="关闭弹窗" on:click={() => (modal = null)}></button>
        <div class="modal" role="dialog" aria-modal="true" aria-label={`为 ${rotateTarget} 换 IP`} tabindex="-1">
          <div class="mhd fx ac gap10">
            <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="#e0b341" stroke-width="1.5">
              <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" /><path d="M13.5 2v3.2h-3.2" />
            </svg>
            <span class="modal-title">为 {rotateTarget} 换 IP</span>
          </div>
          <div class="mbd col gap14">
            <div class="note">通过 zhandroid-control 触发手机出口换公网 IP。换 IP 期间出口会短暂断流。</div>
            <div class="col gap8">
              <div class="kvl">断流时长 down_seconds</div>
              <div class="fx ac gap8"><button class="seg on mono" disabled>8s</button><button class="seg mono" disabled>15s</button><button class="seg mono" disabled>30s</button><span class="note">默认 8s · 上限 60s</span></div>
            </div>
            <div class="warnbox fx ac gap8"><span class="dot warn"></span>触发后 45s 内将加锁,期间重复请求会返回 busy。</div>
          </div>
          <div class="mft fx ac jend gap10"><button class="btn ghost" on:click={() => (modal = null)}>取消</button><button class="btn primary" disabled={!canRotate} on:click={confirmRotate}>确认换 IP</button></div>
        </div>
      </div>
    {/if}

    {#if toast}
      <div class="toasts"><div class="toast"><span class="tdot info"></span><span class="tmsg f1">{toast}</span></div></div>
    {/if}
  </div>
{/if}

<style>
  .app {
    height: 100vh;
    background: var(--bg);
    color: var(--tx);
    font-family: "IBM Plex Sans", "Segoe UI", system-ui, sans-serif;
    font-size: 14px;
    min-width: 1080px;
  }

  .loading,
  .loginwrap {
    display: grid;
    place-items: center;
    padding: 24px;
  }

  .login {
    width: 360px;
    padding: 22px;
  }

  .loginbtn {
    width: 100%;
    height: 36px;
  }

  .fx { display: flex; }
  .col { display: flex; flex-direction: column; }
  .ac { align-items: center; }
  .jb { justify-content: space-between; }
  .jc { justify-content: center; }
  .jend { justify-content: flex-end; }
  .f1 { flex: 1; min-width: 0; }
  .min0 { min-width: 0; }
  .posrel { position: relative; }
  .gap6 { gap: 6px; }
  .gap8 { gap: 8px; }
  .gap10 { gap: 10px; }
  .gap12 { gap: 12px; }
  .gap14 { gap: 14px; }
  .gap16 { gap: 16px; }
  .gap18 { gap: 18px; }
  .mono { font-family: "IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-feature-settings: "tnum"; }
  .dim { color: var(--tx2); }
  .muted { color: var(--tx3); }
  .acc { color: var(--accent); }
  .strong { font-weight: 600; }
  .small { font-weight: 400; font-size: 12px; }
  .right { text-align: right; }
  .truncate { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .topbar {
    height: 54px;
    padding: 0 18px;
    border-bottom: 1px solid var(--bd);
    background: var(--bg1);
    flex: none;
  }

  .mark {
    width: 22px;
    height: 22px;
    border-radius: 6px;
    background: var(--accent);
    transform: rotate(45deg);
    flex: none;
  }

  .brand {
    font-weight: 700;
    font-size: 15px;
    letter-spacing: .01em;
  }

  .vchip {
    font-size: 11px;
    color: var(--tx3);
    border: 1px solid var(--bd2);
    border-radius: 5px;
    padding: 1px 7px;
  }

  .hubpill {
    height: 30px;
    padding: 0 12px;
    border: 1px solid var(--bd2);
    background: rgba(63,185,80,.07);
    border-radius: 8px;
    font-size: 12.5px;
  }

  .hubip { color: var(--tx); }
  .topupdate { font-size: 12px; }

  .livedot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #3fb950;
    box-shadow: 0 0 0 0 rgba(63,185,80,.55);
    animation: pp 2.2s infinite;
    flex: none;
  }

  .livedot.tiny {
    width: 6px;
    height: 6px;
  }

  @keyframes pp {
    0% { box-shadow: 0 0 0 0 rgba(63,185,80,.5); }
    70% { box-shadow: 0 0 0 6px rgba(63,185,80,0); }
    100% { box-shadow: 0 0 0 0 rgba(63,185,80,0); }
  }

  .iconbtn {
    width: 30px;
    height: 30px;
    border-radius: 7px;
    border: 1px solid var(--bd2);
    background: var(--bg3);
    color: var(--tx2);
    cursor: pointer;
  }

  .iconbtn:hover {
    color: var(--tx);
    border-color: var(--tx3);
  }

  .who {
    height: 30px;
    padding: 0 4px 0 10px;
    border-radius: 8px;
    border: 1px solid var(--bd);
    background: var(--bg3);
    font-size: 12.5px;
  }

  .av {
    width: 22px;
    height: 22px;
    border-radius: 5px;
    background: var(--bd2);
    color: var(--tx2);
    font-size: 11px;
    font-weight: 700;
  }

  .themebtn {
    height: 30px;
    padding: 0 6px 0 11px;
    border-radius: 8px;
    border: 1px solid var(--bd2);
    background: var(--bg3);
    color: var(--tx2);
    font: inherit;
    font-size: 12.5px;
    font-weight: 500;
    cursor: pointer;
    white-space: nowrap;
  }

  .themebtn:hover {
    color: var(--tx);
    border-color: var(--tx3);
  }

  .sw {
    width: 13px;
    height: 13px;
    border-radius: 4px;
    border: 1px solid rgba(127,127,127,.35);
    flex: none;
  }

  .tmenu {
    position: absolute;
    top: 38px;
    right: 0;
    width: 188px;
    background: var(--bg2);
    border: 1px solid var(--bd2);
    border-radius: 10px;
    box-shadow: 0 16px 40px rgba(0,0,0,.4);
    padding: 6px;
    z-index: 60;
  }

  .tmi {
    width: 100%;
    height: 34px;
    padding: 0 9px;
    border-radius: 7px;
    border: none;
    background: transparent;
    color: var(--tx2);
    font: inherit;
    font-size: 13px;
    cursor: pointer;
    text-align: left;
  }

  .tmi:hover,
  .tmi.on {
    background: var(--sel);
    color: var(--tx);
  }

  .tmlbl {
    font-size: 10px;
    letter-spacing: .08em;
    color: var(--tx3);
    font-weight: 600;
    padding: 5px 9px 3px;
  }

  .body {
    min-height: 0;
  }

  .side {
    width: 212px;
    flex: none;
    border-right: 1px solid var(--bd);
    background: var(--bg0);
    padding: 14px 12px;
  }

  .navlbl {
    font-size: 10.5px;
    letter-spacing: .09em;
    color: var(--tx3);
    font-weight: 600;
    padding: 0 8px;
    margin: 10px 0 6px;
  }

  .navitem {
    width: 100%;
    height: 36px;
    padding: 0 11px;
    border-radius: 8px;
    border: none;
    background: transparent;
    color: var(--tx2);
    font: inherit;
    font-weight: 500;
    cursor: pointer;
    text-align: left;
  }

  .navitem:hover {
    background: var(--hov);
    color: var(--tx);
  }

  .navitem.on {
    background: var(--sel);
    color: var(--tx);
    box-shadow: inset 2px 0 0 var(--accent);
  }

  .navitem.on .nico {
    color: var(--accent);
  }

  .nico {
    color: var(--tx3);
    flex: none;
  }

  .badge {
    min-width: 20px;
    height: 18px;
    padding: 0 6px;
    border-radius: 9px;
    background: var(--bd);
    color: var(--tx2);
    font-size: 11px;
    font-weight: 600;
  }

  .navitem.on .badge {
    background: var(--bd2);
    color: var(--tx);
  }

  .sidefoot {
    border-top: 1px solid var(--bd);
    margin-top: 12px;
    padding-top: 12px;
  }

  .footlbl {
    margin: 0 0 4px;
  }

  .sfrow {
    font-size: 11.5px;
    padding: 3px 8px;
  }

  .content {
    padding: 22px 26px;
    overflow: auto;
    min-height: 0;
  }

  .h1 {
    font-size: 20px;
    font-weight: 700;
    letter-spacing: .01em;
  }

  .sub {
    color: var(--tx3);
    font-size: 13px;
    margin-top: 3px;
  }

  .stats {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 14px;
  }

  .card {
    background: var(--bg2);
    border: 1px solid var(--bd);
    border-radius: 11px;
    padding: 16px;
    box-shadow: var(--cardsh, none);
  }

  .card.flush {
    padding: 0;
    overflow: hidden;
  }

  .statlbl {
    font-size: 12px;
    color: var(--tx3);
    font-weight: 500;
  }

  .statbig {
    font-size: 32px;
    font-weight: 700;
    line-height: 1;
    letter-spacing: -.02em;
  }

  .statsub {
    font-size: 11.5px;
    color: var(--tx3);
  }

  .sechd {
    font-size: 13px;
    font-weight: 600;
    color: var(--tx);
  }

  .two {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 14px;
  }

  .healthrow {
    padding: 11px 13px;
    border: 1px solid var(--bd2);
    border-radius: 9px;
    background: var(--bg1);
    min-height: 62px;
  }

  .healthmetrics {
    flex: none;
    min-width: 242px;
    justify-content: flex-end;
  }

  .depcard {
    opacity: .62;
  }

  .tc {
    text-align: center;
  }

  .note {
    font-size: 12px;
    color: var(--tx3);
  }

  .logline {
    padding: 8px 2px;
    border-bottom: 1px solid #141a22;
  }

  .logline:last-child {
    border-bottom: none;
  }

  .logtime {
    font-size: 11.5px;
  }

  .tbl {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }

  .tbl th {
    text-align: left;
    font-weight: 600;
    color: var(--tx3);
    font-size: 10.5px;
    letter-spacing: .05em;
    padding: 8px 15px;
    border-bottom: 1px solid var(--bd);
    white-space: nowrap;
    text-transform: uppercase;
  }

  .tbl th.right {
    text-align: right;
  }

  .sorthead {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    min-height: 20px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: inherit;
    font: inherit;
    font-weight: inherit;
    letter-spacing: inherit;
    text-transform: inherit;
    cursor: pointer;
  }

  .sorthead:hover {
    color: var(--tx);
  }

  .sorthead:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .sortmark {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 13px;
    height: 13px;
    color: var(--tx3);
    font-size: 10px;
    line-height: 1;
  }

  .sortmark.on {
    color: var(--accent);
  }

  .tbl td {
    padding: 8px 15px;
    border-bottom: 1px solid var(--bd);
    vertical-align: middle;
    white-space: nowrap;
  }

  .tbl tr:last-child td {
    border-bottom: none;
  }

  .tbl tbody tr:hover {
    background: var(--rowh);
  }

  .detail {
    font-size: 12px;
  }

  .secretline {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    max-width: 100%;
  }

  .secrettext {
    display: inline-block;
    min-width: 96px;
    max-width: 210px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .tokenline {
    display: inline-grid;
    grid-template-columns: minmax(0, 178px) 24px;
    align-items: center;
    gap: 8px;
    width: 210px;
    max-width: 210px;
  }

  .tokenline .secrettext {
    min-width: 0;
    max-width: 178px;
    width: 178px;
  }

  .kvv.secretline {
    display: flex;
    margin-top: 5px;
  }

  .kvv .secrettext {
    min-width: 112px;
    max-width: 160px;
  }

  .ipcard {
    min-width: 0;
  }

  .ipsecret {
    align-items: flex-start;
    gap: 10px;
  }

  .ipstack {
    display: grid;
    gap: 4px;
    min-width: 0;
    flex: 1;
  }

  .iprow {
    display: grid;
    grid-template-columns: 34px minmax(0, 1fr);
    align-items: baseline;
    column-gap: 8px;
    line-height: 1.35;
  }

  .iprow.single {
    grid-template-columns: minmax(0, 1fr);
  }

  .iplabel {
    color: var(--tx3);
    font-size: 11px;
    font-weight: 700;
  }

  .ipvalue {
    white-space: normal;
    overflow: visible;
    text-overflow: clip;
    overflow-wrap: anywhere;
    word-break: break-all;
  }

  .ipsecret .eyebtn {
    margin-top: -2px;
  }

  .eyebtn {
    width: 24px;
    height: 24px;
    border-radius: 6px;
    border: 1px solid var(--bd2);
    background: transparent;
    color: var(--tx3);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    flex: none;
    padding: 0;
  }

  .eyebtn:hover {
    color: var(--tx);
    border-color: var(--tx3);
    background: var(--bg3);
  }

  .eyebtn:disabled {
    cursor: wait;
    opacity: .55;
  }

  .eyebtn svg {
    width: 15px;
    height: 15px;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .pager {
    min-height: 44px;
    padding: 8px 14px;
    border-top: 1px solid var(--bd);
    background: var(--bg1);
  }

  .pagebtn {
    min-width: 26px;
    height: 26px;
    padding: 0 8px;
    border-radius: 7px;
    border: 1px solid var(--bd2);
    background: transparent;
    color: var(--tx2);
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }

  .pagebtn:hover {
    color: var(--tx);
    border-color: var(--tx3);
    background: var(--bg3);
  }

  .pagebtn.on {
    color: #06121f;
    background: var(--accent);
    border-color: transparent;
  }

  .pagegap {
    color: var(--tx3);
    padding: 0 3px;
    font-size: 12px;
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex: none;
  }

  .bigdot {
    width: 10px;
    height: 10px;
  }

  .ok { background: #3fb950; }
  .warn { background: #d6a531; }
  .err { background: #f85149; }
  .idle { background: #5b6675; }
  .dep { background: #414b57; }

  .pill {
    padding: 2px 9px;
    border-radius: 999px;
    font-size: 11px;
    font-weight: 600;
    white-space: nowrap;
    display: inline-block;
  }

  .pill.ok { color: #54c264; background: rgba(63,185,80,.13); }
  .pill.warn { color: #e0b341; background: rgba(214,165,49,.14); }
  .pill.err { color: #ff6a60; background: rgba(248,81,73,.14); }
  .pill.muted { color: var(--tx2); background: rgba(138,151,166,.12); }
  .pill.info { color: #6fb0ff; background: rgba(76,141,255,.13); }
  .pill.acc { color: #c08bff; background: rgba(168,118,255,.14); }

  .btn {
    height: 30px;
    padding: 0 13px;
    border-radius: 7px;
    border: 1px solid var(--bd2);
    background: var(--bg3);
    color: var(--tx);
    font: inherit;
    font-weight: 600;
    font-size: 13px;
    cursor: pointer;
    white-space: nowrap;
  }

  .btn:hover {
    border-color: var(--tx3);
    background: var(--sel);
  }

  .btn:disabled,
  .chip:disabled,
  .seg:disabled {
    cursor: not-allowed;
    opacity: .48;
    filter: grayscale(.25);
  }

  .btn:disabled:hover {
    border-color: var(--bd2);
    background: var(--bg3);
  }

  .btn.primary {
    background: var(--accent);
    border-color: transparent;
    color: #06121f;
  }

  .btn.primary:hover {
    filter: brightness(1.08);
  }

  .btn.primary:disabled,
  .btn.primary:disabled:hover {
    background: var(--accent);
    border-color: transparent;
    color: #06121f;
    filter: grayscale(.25);
  }

  .btn.ghost {
    background: transparent;
    border-color: transparent;
    color: var(--tx2);
  }

  .btn.ghost:hover {
    color: var(--tx);
    background: var(--bg3);
  }

  .btn.ghost:disabled,
  .btn.ghost:disabled:hover {
    background: transparent;
    border-color: transparent;
    color: var(--tx2);
  }

  .btn.danger:hover {
    border-color: #5a2b2b;
    color: #ff7a72;
  }

  .btnxs {
    height: 26px;
    padding: 0 9px;
    font-size: 12px;
  }

  .plus {
    font-weight: 700;
    margin-right: 5px;
  }

  .chip {
    height: 28px;
    padding: 0 12px;
    border-radius: 7px;
    border: 1px solid var(--bd2);
    background: var(--bg2);
    color: var(--tx2);
    font-size: 12.5px;
    font-weight: 500;
    cursor: pointer;
  }

  .chip.on {
    background: var(--sel);
    color: var(--tx);
    border-color: var(--bd2);
  }

  .chip:disabled {
    pointer-events: none;
  }

  .linkbtn {
    border: none;
    background: transparent;
    padding: 0;
    cursor: pointer;
    font: inherit;
  }

  .tag {
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: .04em;
    color: var(--tx3);
    border: 1px solid var(--bd2);
    border-radius: 5px;
    padding: 1px 7px;
  }

  .kv {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 1px;
    background: var(--bd);
    border: 1px solid var(--bd);
    border-radius: 9px;
    overflow: hidden;
  }

  .kv.flat {
    border: none;
    border-radius: 0;
  }

  .kvc {
    background: var(--bg1);
    padding: 13px 15px;
  }

  .kvl {
    font-size: 11px;
    color: var(--tx3);
  }

  .kvv {
    font-size: 14px;
    font-weight: 600;
    margin-top: 5px;
  }

  .nodehd {
    padding: 16px;
    border-bottom: 1px solid var(--bd);
  }

  .node-title {
    font-weight: 700;
    font-size: 16px;
  }

  .legacy-title {
    font-weight: 700;
    font-size: 15px;
  }

  .node-sub {
    margin-top: 3px;
  }

  .legacy-note {
    max-width: 320px;
    text-align: right;
  }

  .client-count {
    font-size: 12px;
  }

  .ovl {
    position: fixed;
    inset: 0;
    background: rgba(5,8,12,.66);
    backdrop-filter: blur(2px);
    z-index: 50;
  }

  .backdropbtn {
    position: absolute;
    inset: 0;
    border: 0;
    background: transparent;
    cursor: default;
  }

  .modal {
    position: relative;
    z-index: 1;
    width: 440px;
    background: var(--bg2);
    border: 1px solid var(--bd2);
    border-radius: 13px;
    box-shadow: 0 24px 60px rgba(0,0,0,.5);
  }

  .modal-title {
    font-weight: 700;
    font-size: 15px;
  }

  .mhd {
    padding: 17px 19px;
    border-bottom: 1px solid var(--bd);
  }

  .mbd {
    padding: 19px;
  }

  .mft {
    padding: 15px 19px;
    border-top: 1px solid var(--bd);
  }

  .warnbox {
    background: rgba(214,165,49,.09);
    border: 1px solid rgba(214,165,49,.25);
    border-radius: 8px;
    padding: 11px 13px;
    font-size: 12.5px;
    color: #d8bd84;
  }

  .errbox {
    color: #ff9a94;
    background: rgba(248,81,73,.12);
    border-color: rgba(248,81,73,.32);
  }

  .seg {
    height: 30px;
    padding: 0 12px;
    border: 1px solid var(--bd2);
    background: var(--bg1);
    color: var(--tx2);
    border-radius: 7px;
    font-size: 13px;
  }

  .seg.on {
    border-color: var(--accent);
    color: var(--tx);
    background: rgba(76,141,255,.12);
  }

  .seg:disabled {
    pointer-events: none;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 7px;
  }

  .lbl {
    font-size: 11.5px;
    color: var(--tx3);
    font-weight: 600;
    letter-spacing: .02em;
  }

  .inp {
    height: 36px;
    padding: 0 12px;
    border: 1px solid var(--bd2);
    background: var(--bg1);
    border-radius: 8px;
    color: var(--tx);
    font: inherit;
    font-size: 13px;
    width: 100%;
  }

  .inp:focus {
    outline: none;
    border-color: var(--accent);
  }

  .toasts {
    position: fixed;
    right: 20px;
    bottom: 20px;
    z-index: 80;
    display: flex;
    flex-direction: column;
    gap: 10px;
    align-items: flex-end;
  }

  .toast {
    min-width: 230px;
    max-width: 360px;
    background: var(--bg2);
    border: 1px solid var(--bd2);
    border-radius: 10px;
    box-shadow: 0 12px 34px rgba(0,0,0,.42);
    padding: 11px 14px;
    display: flex;
    align-items: center;
    gap: 11px;
    animation: tin .22s ease;
  }

  @keyframes tin {
    from { opacity: 0; transform: translateY(8px); }
    to { opacity: 1; transform: none; }
  }

  .tdot {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    flex: none;
  }

  .tdot.info {
    background: #6fb0ff;
  }

  .tmsg {
    font-size: 13px;
    color: var(--tx);
    font-weight: 500;
  }

  @media (max-width: 1180px) {
    .hubpill .muted:last-child,
    .topupdate {
      display: none;
    }

    .healthmetrics {
      min-width: 178px;
      gap: 10px;
    }
  }

  .migration-identifiers { overflow-wrap: anywhere; }
  .migration-blocks { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px 24px; margin: 0; padding-left: 18px; }
  .migration-blocks li > span, .migration-member-blocks li > span { display: block; overflow-wrap: anywhere; }
  .migration-table-scroll { max-width: 100%; overflow-x: auto; }
  .migration-table { min-width: 1060px; table-layout: fixed; }
  .migration-table td { vertical-align: top; white-space: normal; overflow-wrap: anywhere; }
  .migration-table th:first-child { width: 150px; }
  .migration-table th:nth-child(2) { width: 225px; }
  .migration-table th:nth-child(3) { width: 240px; }
  .migration-table th:nth-child(4) { width: 225px; }
  .migration-table td .note { margin-top: 5px; }
  .migration-member-blocks { padding-left: 18px; margin: 10px 0 0; }
  .migration-member-blocks li { margin-top: 8px; }
  .migration-observer-table { min-width: 870px; }
  .migration-table-scroll:focus-visible, summary:focus-visible, .navitem:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

  @media (max-width: 1080px) {
    :global(body) { min-width: 0; }
    .app { min-width: 0; }
    .hubpill { display: none; }
  }
  @media (max-width: 760px) {
    .topbar { padding: 0 10px; gap: 8px; }
    .brand { font-size: 13px; white-space: nowrap; }
    .vchip, .who { display: none; }
    .body { flex-direction: column; }
    .side { width: auto; flex-direction: row; gap: 4px; padding: 8px; border-right: 0; border-bottom: 1px solid var(--bd); }
    .side > .navlbl, .side > .sidefoot, .side > .f1 { display: none; }
    .navitem { width: auto; flex: 1; justify-content: center; padding: 0 6px; }
    .navitem .f1, .navitem .badge { display: none; }
    .content { padding: 14px; }
    .content > .fx { flex-wrap: wrap; gap: 10px; }
    .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
    .two { grid-template-columns: minmax(0, 1fr); }
    .card.flush { flex-shrink: 0; min-width: 0; max-width: 100%; overflow-x: auto; }
    .tbl { min-width: 760px; }
    .migration-table { min-width: 1060px; }
    .migration-observer-table { min-width: 870px; }
    .migration-blocks { grid-template-columns: minmax(0, 1fr); }
    .healthrow, .nodehd { flex-wrap: wrap; gap: 12px; }
    .healthmetrics { min-width: 0; justify-content: flex-start; }
    .kv { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .kvv { overflow-wrap: anywhere; }
    .pager { flex-wrap: wrap; gap: 8px; }
    .login { width: min(360px, 100%); }
    .modal { width: min(440px, calc(100vw - 24px)); }
  }
</style>
