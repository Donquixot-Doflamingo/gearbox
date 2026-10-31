<script lang="ts">
  import type { DecisionSpec, Node, SwitchNode } from '$lib/api/types.gen';
  import ExprInput from '$lib/components/ExprInput.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import type { ProblemSource } from './types';

  let { node = $bindable(), spec, index, problems }: { node: Node; spec: DecisionSpec; index: number; problems: ProblemSource } = $props();
  let sw = $derived(node as SwitchNode);
  let base = $derived(`/nodes/${index}`);
  let targets = $derived(spec.nodes.filter((x) => x.id !== node.id).map((x) => x.id));

  $effect(() => {
    if (!sw.cases) sw.cases = [];
  });

  function move(i: number, d: number): void {
    const j = i + d;
    if (j < 0 || j >= sw.cases.length) return;
    const [x] = sw.cases.splice(i, 1);
    sw.cases.splice(j, 0, x);
  }
</script>

<section class="panel">
  <header><h2>Cases</h2><span class="hint">the first true case wins</span></header>
  {#each sw.cases ?? [] as c, i (i)}
    <div class="map-row" style="grid-template-columns:minmax(0,.6fr) minmax(0,1.6fr) minmax(0,.8fr) auto">
      <input type="text" class="code" bind:value={sw.cases[i].id} placeholder="case id" aria-label="Case {i + 1} id" />
      <ExprInput bind:value={sw.cases[i].when} placeholder="nodes.eligibility.eligible" error={problems.problemsUnder(`${base}/cases/${i}/when`)[0]?.message} />
      <select bind:value={sw.cases[i].next} aria-label="Case {c.id || i + 1} goes to">
        <option value="">— choose —</option>
        {#each targets as t (t)}<option value={t}>{t}</option>{/each}
      </select>
      <div class="row-sm" style="flex-wrap:nowrap">
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => move(i, -1)} disabled={i === 0} aria-label="Move up">↑</button>
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => move(i, 1)} disabled={i === sw.cases.length - 1} aria-label="Move down">↓</button>
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => sw.cases.splice(i, 1)} aria-label="Remove case">✕</button>
      </div>
    </div>
  {/each}
  <div><button type="button" class="btn btn-sm" onclick={() => (sw.cases = [...(sw.cases ?? []), { id: `case_${(sw.cases?.length ?? 0) + 1}`, when: 'true', next: '' }])}>Add case</button></div>
  <div class="field" style="max-width:320px">
    <label for="{base}-default">Default (no case matched)</label>
    <select id="{base}-default" bind:value={sw.default}>
      <option value="">— choose —</option>
      {#each targets as t (t)}<option value={t}>{t}</option>{/each}
    </select>
  </div>
  <p class="hint">Tip: drag from the node's right port on the canvas to add a branch. The output of a switch is <code class="inl">{'{"case": "<id>"}'}</code>; the next node usually reads an earlier node with <code class="inl">from</code>.</p>
  <Problems problems={problems.problemsUnder(base).filter((p) => !p.path.includes('/cases/'))} />
</section>
