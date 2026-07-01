<script lang="ts">
  export let label: string;
  export let value: string;
</script>

<div class="p-4">
  <div class="text-[11px] font-semibold text-hub-faint">{label}</div>
  <div class="mt-2 truncate font-mono text-sm font-semibold">{value}</div>
</div>
