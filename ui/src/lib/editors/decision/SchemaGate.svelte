<script lang="ts">
  // Rules and tables need registered input and output schemas before anything else can be edited.
  import type { Snippet } from 'svelte';
  import type { SchemaRef } from '$lib/api/types.gen';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import { appHref } from '$lib/nav.svelte';
  import type { ProblemSource } from './types';

  let {
    app,
    input = $bindable(),
    output = $bindable(),
    pointer,
    problems,
    disabled = false,
    what = 'rules',
    children,
  }: { app: string; input: SchemaRef | undefined; output: SchemaRef | undefined; pointer: string; problems: ProblemSource; disabled?: boolean; what?: string; children: Snippet } = $props();

  let ready = $derived(!!input?.name && !!output?.name);
  let inErr = $derived(problems.problemsUnder(`${pointer}/inputSchema`).find((p) => p.severity === 'error')?.message);
  let outErr = $derived(problems.problemsUnder(`${pointer}/outputSchema`).find((p) => p.severity === 'error')?.message);
</script>

<section class="panel tight">
  <header><h2>Schemas</h2><span class="hint">required for {what}</span></header>
  <div class="grid-2">
    <SchemaRefPicker {app} bind:value={input} label="Input schema" id="{pointer.replace(/\//g, '-')}-in" required {disabled} error={inErr} hint="What this node receives as input; conditions are type-checked against it." />
    <SchemaRefPicker {app} bind:value={output} label="Output schema" id="{pointer.replace(/\//g, '-')}-out" required {disabled} error={outErr} hint="What every result must match." />
  </div>
</section>

{#if !ready}
  <div class="callout warn" role="status">
    <strong>Choose an input schema and an output schema first.</strong>
    <span>The {what} editor is disabled until both are set: conditions use the input schema's fields, and every result is checked against the output schema. A {what === 'rules' ? 'rules' : 'table'} node cannot be saved without them.</span>
    <span><a href={appHref('/schemas', { new: 1 })}>Register a schema</a> if the one you need is not in the list (then cut v1).</span>
  </div>
{/if}
<fieldset disabled={!ready || disabled} aria-describedby={ready ? undefined : 'schema-gate-msg'}>
  <div class="gated stack">{@render children()}</div>
</fieldset>
{#if !ready}<span id="schema-gate-msg" class="sr">Disabled until both schemas are chosen.</span>{/if}
