<script lang="ts">
  import type { BucketRange } from '$lib/model/buckets';

  let { ranges, label = 'Buckets 0 to 9999' }: { ranges: BucketRange[]; label?: string } = $props();
  let used = $derived(ranges.reduce((s, r) => s + Math.max(0, r.to - r.from + 1), 0));
  let aria = $derived(`${label}: ` + ranges.map((r) => `${r.name} ${(r.share * 100).toFixed(2).replace(/\.00$/, '')}%`).join(', '));
  let vi = $derived.by(() => {
    let i = 0;
    return ranges.map((r) => (r.kind === 'holdout' ? -1 : i++));
  });
</script>

<div>
  <div class="bucket" role="img" aria-label={aria}>
    {#each ranges as r, i (r.name + i)}
      {#if r.to >= r.from}
        <div class={r.kind === 'holdout' ? 'b-hold' : `b-${vi[i] % 6}`} style:flex={r.to - r.from + 1} title="{r.name}: {r.from}–{r.to}">{r.share >= 0.06 ? r.name : ''}</div>
      {/if}
    {/each}
    {#if used < 10000}<div class="b-free" style:flex={10000 - used}>{10000 - used >= 600 ? 'unassigned' : ''}</div>{/if}
  </div>
  <div class="scale"><span>bucket 0</span><span>9 999</span></div>
</div>
