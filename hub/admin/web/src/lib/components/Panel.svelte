<script lang="ts">
  export let title: string;
</script>

<div class="rounded-lg border border-hub-border bg-hub-panel p-4">
  <h3 class="mb-3 font-semibold">{title}</h3>
  <slot />
</div>
