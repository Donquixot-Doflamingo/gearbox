<script lang="ts">
  let { percent, steps = [5, 25, 50, 100] }: { percent: number; steps?: number[] } = $props();
</script>

<ol class="stepper" aria-label="Rollout steps">
  {#each steps as s (s)}
    {@const state = s < percent ? 'done' : s === percent ? 'current' : 'next'}
    <li class={state === 'next' ? '' : state} aria-current={state === 'current' ? 'step' : undefined}>
      <span class="pct">{s}%</span>
      <span class="st">{state === 'done' ? 'done' : state === 'current' ? 'current' : 'next'}</span>
    </li>
  {/each}
</ol>
{#if !steps.includes(percent)}<p class="hint">Now at {percent}% (between steps).</p>{/if}
