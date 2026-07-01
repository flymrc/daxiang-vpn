<script lang="ts">
  export let label: string;
  export let value: number | string;
  export let sub: string;
</script>

<div class="rounded-lg border border-hub-border bg-hub-panel p-4">
  <div class="mb-3 flex items-center justify-between">
    <span class="text-xs font-semibold text-hub-faint">{label}</span>
    <span class="h-2 w-2 rounded-full bg-hub-green"></span>
  </div>
  <div class="font-mono text-3xl font-bold">{value}</div>
  <div class="mt-2 text-xs text-hub-faint">{sub}</div>
</div>
