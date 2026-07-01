<script lang="ts">
  export let title: string;
  export let sub: string;
  export let active = false;
</script>

<div class={`rounded-lg border p-3 ${active ? "border-hub-blue bg-blue-500/10" : "border-hub-border bg-[#0d1117]"}`}>
  <div class={`font-semibold ${active ? "text-blue-300" : ""}`}>{title}</div>
  <div class="mt-1 truncate font-mono text-[11px] text-hub-faint">{sub}</div>
</div>
