<script lang="ts">
  let { label = 'Loading…' }: { label?: string } = $props();
</script>

<div class="loading" role="status" aria-live="polite"><span class="spinner" aria-hidden="true"></span>{label}</div>
