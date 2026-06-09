<script lang="ts">
  import { onMount } from "svelte";
  import { api, type Status } from "$lib/api";

  let view = $state<"loading" | "login" | "main">("loading");
  let token = $state("");
  let fast = $state(false);
  let busy = $state(false);
  let errMsg = $state("");
  let info = $state("");
  let status = $state<Status | null>(null);
  let poll: ReturnType<typeof setInterval> | undefined;

  const connected = $derived(!!status && (status.running || status.proxy_reachable));

  async function refresh() {
    try {
      const s = await api.status();
      status = s;
      view = s.error && s.error.includes("未找到配置") ? "login" : "main";
    } catch (e) {
      errMsg = String(e);
    }
  }

  async function doLogin() {
    if (!token.trim()) return;
    busy = true;
    errMsg = "";
    try {
      const r = await api.login(token.trim());
      if (r.ok) {
        token = "";
        await refresh();
      } else {
        errMsg = r.error ?? "登录失败";
      }
    } catch (e) {
      errMsg = String(e);
    } finally {
      busy = false;
    }
  }

  async function toggle() {
    busy = true;
    errMsg = "";
    info = "";
    try {
      const r = connected ? await api.disconnect() : await api.connect(fast);
      if (!r.ok) errMsg = r.message || "操作失败";
      else if (r.warning) errMsg = r.warning;
      await refresh();
    } catch (e) {
      errMsg = String(e);
    } finally {
      busy = false;
    }
  }

  async function rotate() {
    busy = true;
    errMsg = "";
    info = "";
    try {
      const r = await api.rotateIp();
      if (r.ok) info = `已换 IP：${r.before ?? "?"} → ${r.after ?? "?"}`;
      else errMsg = r.error || "换 IP 失败";
      await refresh();
    } catch (e) {
      errMsg = String(e);
    } finally {
      busy = false;
    }
  }

  onMount(() => {
    refresh();
    poll = setInterval(() => {
      if (view === "main" && !busy) refresh();
    }, 5000);
    return () => clearInterval(poll);
  });
</script>

<main class="app">
  <header><h1>大象 VPN</h1></header>

  {#if view === "loading"}
    <p class="muted">加载中…</p>
  {:else if view === "login"}
    <section class="card">
      <p class="muted">输入授权码以登录</p>
      <input
        class="token"
        placeholder="授权码"
        bind:value={token}
        onkeydown={(e) => e.key === "Enter" && doLogin()}
        disabled={busy}
      />
      <button class="primary" onclick={doLogin} disabled={busy || !token.trim()}>
        {busy ? "登录中…" : "登录"}
      </button>
    </section>
  {:else}
    <section class="card">
      <div class="dot {connected ? 'on' : 'off'}"></div>
      <p class="state">{busy ? "处理中…" : connected ? "已连接" : "未连接"}</p>

      <button
        class="toggle {connected ? 'danger' : 'primary'}"
        onclick={toggle}
        disabled={busy}
      >
        {connected ? "断开" : "连接"}
      </button>

      <label class="fast">
        <input type="checkbox" bind:checked={fast} disabled={busy || connected} />
        全局模式（系统 TUN，启动时弹一次管理员授权）
      </label>

      <dl class="info">
        <dt>出口</dt>
        <dd>{status?.egress ?? "—"}</dd>
        <dt>出口 IP</dt>
        <dd>{status?.egress_ip ?? (connected ? "获取中…" : "—")}</dd>
        <dt>本地代理</dt>
        <dd>{status?.proxy ?? "—"}</dd>
      </dl>

      {#if connected}
        <button class="rotate" onclick={rotate} disabled={busy}>换 IP</button>
      {/if}
    </section>
  {/if}

  {#if info}
    <p class="info-msg">{info}</p>
  {/if}
  {#if errMsg}
    <p class="error">{errMsg}</p>
  {/if}
</main>

<style>
  :root {
    font-family: "Segoe UI", Inter, system-ui, sans-serif;
    color: #1a1a1a;
    background: #f5f6f8;
  }
  .app {
    max-width: 380px;
    margin: 0 auto;
    padding: 24px 20px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  header h1 {
    font-size: 20px;
    text-align: center;
    margin: 4px 0 0;
  }
  .card {
    background: #fff;
    border-radius: 14px;
    padding: 24px 20px;
    box-shadow: 0 1px 4px rgba(0, 0, 0, 0.08);
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 14px;
  }
  .muted {
    color: #6b7280;
    font-size: 14px;
    margin: 0;
  }
  .dot {
    width: 14px;
    height: 14px;
    border-radius: 50%;
  }
  .dot.on {
    background: #16a34a;
    box-shadow: 0 0 0 4px rgba(22, 163, 74, 0.18);
  }
  .dot.off {
    background: #9ca3af;
  }
  .state {
    font-size: 16px;
    font-weight: 600;
    margin: 0;
  }
  input.token {
    width: 100%;
    box-sizing: border-box;
    padding: 10px 12px;
    border: 1px solid #d1d5db;
    border-radius: 10px;
    font-size: 14px;
  }
  button {
    cursor: pointer;
    border: none;
    border-radius: 10px;
    font-size: 15px;
    font-weight: 600;
    padding: 10px 16px;
    transition: opacity 0.15s;
  }
  button:disabled {
    opacity: 0.55;
    cursor: default;
  }
  button.primary {
    background: #2563eb;
    color: #fff;
  }
  button.danger {
    background: #dc2626;
    color: #fff;
  }
  .toggle {
    width: 140px;
    height: 56px;
    border-radius: 28px;
    font-size: 17px;
  }
  .fast {
    font-size: 12px;
    color: #4b5563;
    display: flex;
    align-items: flex-start;
    gap: 6px;
    text-align: left;
    line-height: 1.4;
  }
  .info {
    width: 100%;
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 6px 12px;
    margin: 4px 0 0;
    font-size: 13px;
  }
  .info dt {
    color: #6b7280;
  }
  .info dd {
    margin: 0;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .rotate {
    background: #f3f4f6;
    color: #1a1a1a;
    border: 1px solid #d1d5db;
    font-size: 13px;
    padding: 8px 16px;
  }
  .info-msg {
    color: #166534;
    font-size: 13px;
    text-align: center;
    margin: 0;
  }
  .error {
    color: #b91c1c;
    font-size: 13px;
    text-align: center;
    margin: 0;
    word-break: break-all;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      color: #e5e7eb;
      background: #1f2229;
    }
    .card {
      background: #2a2e37;
      box-shadow: none;
    }
    input.token {
      background: #1f2229;
      color: #e5e7eb;
      border-color: #3a3f4b;
    }
    .info dt,
    .muted,
    .fast {
      color: #9ca3af;
    }
  }
</style>
