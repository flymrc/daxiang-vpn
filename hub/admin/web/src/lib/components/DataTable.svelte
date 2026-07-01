<script lang="ts">
  export let headers: string[];
</script>

<div class="overflow-hidden rounded-lg border border-hub-border bg-hub-panel">
  <table class="w-full border-collapse text-left text-sm">
    <thead>
      <tr class="border-b border-hub-border text-[11px] uppercase tracking-wide text-hub-faint">
        {#each headers as header}
          <th class="px-4 py-3 font-bold">{header}</th>
        {/each}
      </tr>
    </thead>
    <tbody>
      <slot />
    </tbody>
  </table>
</div>
