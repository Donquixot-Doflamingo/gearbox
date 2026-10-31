<script lang="ts">
  // lookup, gate, experiment and subdecision nodes: a reference plus a few fields.
  import type { ExperimentNode, GateNode, LookupNode, Node, SubdecisionNode } from '$lib/api/types.gen';
  import ExprInput from '$lib/components/ExprInput.svelte';
  import JsonEditor from '$lib/components/JsonEditor.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import ResourcePicker from '$lib/components/ResourcePicker.svelte';
  import type { ProblemSource } from './types';

  let { app, node = $bindable(), index, problems, decision }: { app: string; node: Node; index: number; problems: ProblemSource; decision: string } = $props();
  let base = $derived(`/nodes/${index}`);
  let lk = $derived(node.type === 'lookup' ? (node as LookupNode) : null);
  let gt = $derived(node.type === 'gate' ? (node as GateNode) : null);
  let ex = $derived(node.type === 'experiment' ? (node as ExperimentNode) : null);
  let sd = $derived(node.type === 'subdecision' ? (node as SubdecisionNode) : null);
  let err = (p: string) => problems.problemsUnder(`${base}/${p}`)[0]?.message;
</script>

<section class="panel">
  {#if lk}
    <header><h2>Lookup</h2><span class="hint">output: the value, or the default when the key is missing</span></header>
    <div class="grid-2">
      <ResourcePicker {app} kind="lookups" bind:value={lk.lookup} label="Lookup table" error={err('lookup')} />
      <ExprInput bind:value={lk.key} label="Key" placeholder="input.lead.city" error={err('key')} />
    </div>
    <JsonEditor bind:value={lk.default} label="Default when the key is missing (data, optional)" rows={3} allowUndefined />
  {:else if gt}
    <header><h2>Gate</h2><span class="hint">output: {'{"value": bool, "rule": id}'}</span></header>
    <ResourcePicker {app} kind="gates" bind:value={gt.gate} label="Gate" error={err('gate')} hint="Evaluated for ctx.unit with this node's input as attributes." />
  {:else if ex}
    <header><h2>Experiment</h2><span class="hint">output: variant, parameters, in_experiment</span></header>
    <ResourcePicker {app} kind="experiments" bind:value={ex.experiment} label="Experiment" error={err('experiment')} hint="Every evaluation writes an exposure record (never sampled)." />
  {:else if sd}
    <header><h2>Subdecision</h2><span class="hint">output: the other decision's output</span></header>
    <div class="grid-2">
      <ResourcePicker {app} kind="decisions" bind:value={sd.decision} label="Decision" error={err('decision')} filter={(s) => s.name !== decision} />
      <ExprInput value={sd.input ?? ''} label="Input (optional)" placeholder="input (default)" onchange={(v) => (sd!.input = v || undefined)} error={err('input')} />
    </div>
    <p class="hint">Runs the version of {sd.decision || 'that decision'} live in the same environment. Cycles are rejected.</p>
  {/if}
  <Problems problems={problems.problemsUnder(base)} />
</section>
