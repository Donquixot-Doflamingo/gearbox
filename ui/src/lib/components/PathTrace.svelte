<script lang="ts">
  import type { PathStep } from '$lib/api/types.gen';
  import { fmtMicros } from '$lib/model/format';
  import JsonView from './JsonView.svelte';

  let { path, detail = false }: { path: PathStep[]; detail?: boolean } = $props();
  const COLOR: Record<string, string> = { rules: '--t-rules', table: '--t-table', transform: '--t-transform', switch: '--t-switch', ai: '--t-ai', output: '--t-output', lookup: '--t-lookup', gate: '--t-gate', experiment: '--t-experiment', subdecision: '--t-subdecision' };

  function outcomeText(s: PathStep): string {
    if (s.error) return s.error;
    switch (s.nodeType) {
      case 'rules':
        return s.outcome === 'default' ? 'no rule matched → default' : `matched ${s.outcome}`;
      case 'table':
        return s.outcome === 'default' ? 'no row matched → default' : `matched ${s.outcome}`;
      case 'switch':
        return `case ${s.outcome}`;
      case 'ai':
        return s.outcome === 'shadow' ? 'shadow: answer logged, not used' : s.outcome;
      default:
        return s.outcome;
    }
  }
</script>

<ol class="trace">
  {#each path as s, i (i)}
    <li>
      <span class="nd" style:--k="var({COLOR[s.nodeType] ?? '--muted'})" aria-hidden="true"></span>
      <span><span class="nm">{s.nodeId}</span> <span class="hint">{s.nodeType}</span><span class="oc" class:err={!!s.error || s.outcome === 'error'}>{outcomeText(s)}</span></span>
      <span class="us">{fmtMicros(s.micros)}</span>
      {#if detail && (s.input !== undefined || s.output !== undefined)}
        <details>
          <summary>Input and output</summary>
          <div class="grid-2" style="margin-top:6px">
            <div class="stack-sm"><span class="sub-h">Input</span><JsonView value={s.input} scroll /></div>
            <div class="stack-sm"><span class="sub-h">Output</span><JsonView value={s.output} scroll /></div>
          </div>
        </details>
      {/if}
    </li>
  {/each}
</ol>
