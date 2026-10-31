<script lang="ts">
  import type { FleetSummary } from '$lib/api/types.gen';

  let { summary, generation }: { summary: FleetSummary; generation: number } = $props();
  let parts = $derived(
    Object.entries(summary.generations ?? { [String(generation)]: summary.onLatest })
      .map(([g, n]) => ({ g: Number(g), n }))
      .filter((p) => p.n > 0)
      .sort((a, b) => b.g - a.g),
  );
  let total = $derived(Math.max(1, parts.reduce((s, p) => s + p.n, 0)));
  let label = $derived(parts.map((p) => `${p.n} on generation ${p.g}`).join(', '));
</script>

<div class="stack-sm">
  <div class="spread" role="img" aria-label={label || 'No sidecars reporting'}>
    {#each parts as p (p.g)}<i class={p.g === generation ? 's-ok' : 's-warn'} style:width="{(p.n / total) * 100}%"></i>{/each}
  </div>
  <div class="row">
    {#each parts as p (p.g)}<span class="row-sm"><span class="dot {p.g === generation ? 'ok' : 'warn'}"></span>{p.n.toLocaleString()} on generation {p.g}</span>{/each}
    {#if !parts.length}<span class="hint">No sidecars have reported in the last 10 minutes.</span>{/if}
  </div>
</div>
