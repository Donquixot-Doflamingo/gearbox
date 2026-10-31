<script lang="ts">
  let { next, loading = false, onmore, count, total }: { next?: string; loading?: boolean; onmore: () => void; count?: number; total?: number } = $props();
</script>

{#if next || count !== undefined}
  <div class="row">
    {#if count !== undefined}<span class="hint">Showing {count.toLocaleString()}{total !== undefined ? ` of ${total.toLocaleString()}` : ''}</span>{/if}
    {#if next}<button type="button" class="btn btn-sm" onclick={onmore} disabled={loading}>{loading ? 'Loading…' : 'Load more'}</button>{/if}
  </div>
{/if}
