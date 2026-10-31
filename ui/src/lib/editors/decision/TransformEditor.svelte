<script lang="ts">
  // Transform and output nodes: ordered field mappings (target ← expression) or one expression,
  // defaults, drops, and a preview that runs the saved draft through evaluate.
  import { api } from '$lib/api';
  import type { DecisionSpec, Node, OutputNode, PathStep, TransformNode } from '$lib/api/types.gen';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import ExprInput from '$lib/components/ExprInput.svelte';
  import JsonEditor from '$lib/components/JsonEditor.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import type { DraftEditor } from '$lib/draft.svelte';
  import { exampleFor } from '$lib/model/schemaTree';
  import { currentEnv } from '$lib/nav.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import type { ProblemSource } from './types';

  let { app, node = $bindable(), index, problems, ed, disabled = false }: { app: string; node: Node; index: number; problems: ProblemSource; ed: DraftEditor<DecisionSpec>; disabled?: boolean } = $props();

  let n = $derived(node as TransformNode | OutputNode);
  let tr = $derived(node.type === 'transform' ? (node as TransformNode) : null);
  let base = $derived(`/nodes/${index}`);
  let useExpression = $derived(typeof n.expression === 'string');

  $effect(() => {
    if (!n.fields && !useExpression) n.fields = [];
  });

  function setUseExpression(on: boolean): void {
    if (on) {
      n.expression = n.expression ?? '{}';
    } else {
      delete n.expression;
      if (!n.fields) n.fields = [];
    }
  }
  function addField(): void {
    n.fields = [...(n.fields ?? []), { target: '', expr: '' }];
    queueMicrotask(() => document.getElementById(`${base}-t-${(n.fields?.length ?? 1) - 1}`)?.focus());
  }
  function move(i: number, d: number): void {
    const f = n.fields!;
    const j = i + d;
    if (j < 0 || j >= f.length) return;
    const [x] = f.splice(i, 1);
    f.splice(j, 0, x);
  }

  // preview via evaluate (runs the saved draft)
  const tests = $derived(ed.spec.tests ?? []);
  let previewSource = $state('');
  let previewInput: unknown = $state(undefined);
  let previewStep: PathStep | null = $state(null);
  let previewError: unknown = $state(null);
  let previewNote = $state('');
  let previewing = $state(false);

  $effect(() => {
    if (previewInput !== undefined) return;
    const t0 = tests[0];
    if (t0) {
      previewSource = '0';
      previewInput = $state.snapshot(t0.input);
      return;
    }
    const ref = ed.spec.inputSchema;
    const sch = ref ? registry.schema(app, ref.name, ref.version) : null;
    if (sch) previewInput = exampleFor(sch);
  });

  function pickSource(v: string): void {
    previewSource = v;
    if (v !== '' && tests[Number(v)]) previewInput = $state.snapshot(tests[Number(v)].input);
  }

  async function preview(saveFirst: boolean): Promise<void> {
    previewing = true;
    previewError = null;
    previewStep = null;
    previewNote = '';
    try {
      if (saveFirst && ed.dirty && !(await ed.save())) return;
      const t = previewSource !== '' ? tests[Number(previewSource)] : undefined;
      const r = await api.evaluateDecision({ app, name: ed.name, body: { source: 'draft', environment: currentEnv(), input: $state.snapshot(previewInput), ctx: t?.ctx ?? { now: new Date().toISOString() }, skipAI: true } });
      previewStep = r.path.find((s) => s.nodeId === node.id) ?? null;
      if (!previewStep) previewNote = `${node.id} did not run for this input (path: ${r.path.map((s) => s.nodeId).join(' → ') || 'none'}).`;
      if (r.error) previewError = r.error;
    } catch (e) {
      previewError = e;
    } finally {
      previewing = false;
    }
  }

  function valueAt(obj: unknown, target: string): string {
    let cur = obj;
    for (const seg of target.replace(/\[(\d+)\]/g, '.$1').split('.')) {
      if (cur === null || cur === undefined || typeof cur !== 'object') return '—';
      cur = (cur as Record<string, unknown>)[seg];
    }
    return cur === undefined ? '(omitted)' : JSON.stringify(cur);
  }

  let dropText = $derived((tr?.drop ?? []).join(', '));
</script>

<div class="grid-3">
  <section class="panel">
    <header><h2>Input</h2>
      <select aria-label="Preview input" value={previewSource} onchange={(e) => pickSource(e.currentTarget.value)} style="width:auto;max-width:60%">
        <option value="">custom</option>
        {#each tests as t, i (i)}<option value={String(i)}>test: {t.name}</option>{/each}
      </select>
    </header>
    <JsonEditor bind:value={previewInput} rows={14} onchange={() => (previewSource = '')} />
    <div class="actions">
      <button type="button" class="btn btn-sm btn-primary" onclick={() => preview(true)} disabled={previewing || disabled}>{ed.dirty ? 'Save and preview' : 'Preview'}</button>
      {#if ed.dirty}<button type="button" class="btn btn-sm" onclick={() => preview(false)} disabled={previewing}>Preview saved draft</button>{/if}
    </div>
    <p class="hint">Preview runs the whole decision (saved draft) with this request input and shows this node's result. AI nodes are skipped.</p>
  </section>

  <section class="panel">
    <header>
      <h2>{node.id}</h2>
      <div class="row-sm">
        {#if tr}
          <label for="{base}-mode" class="hint">Mode</label>
          <select id="{base}-mode" bind:value={tr.mode} style="width:auto">
            <option value="build">build (start empty)</option>
            <option value="patch">patch (start from input)</option>
          </select>
        {/if}
      </div>
    </header>
    <div class="seg" role="radiogroup" aria-label="Mapping style" style="align-self:flex-start">
      <button type="button" role="radio" aria-checked={!useExpression} onclick={() => setUseExpression(false)}>fields</button>
      <button type="button" role="radio" aria-checked={useExpression} onclick={() => setUseExpression(true)}>one expression</button>
    </div>
    {#if useExpression}
      <ExprInput value={n.expression ?? ''} onchange={(v) => (n.expression = v)} multiline label="Expression that builds the whole output" error={problems.problemsUnder(`${base}/expression`)[0]?.message} />
    {:else}
      <div>
        {#each n.fields ?? [] as f, i (i)}
          <div class="map-row">
            <input id="{base}-t-{i}" type="text" class="tgt" bind:value={f.target} placeholder="target.path" aria-label="Target path {i + 1}" aria-invalid={problems.problemsUnder(`${base}/fields/${i}/target`).length ? 'true' : undefined} />
            <div class="ex"><ExprInput bind:value={f.expr} placeholder="input.lead.city" error={problems.problemsUnder(`${base}/fields/${i}/expr`)[0]?.message} /></div>
            <div class="row-sm" style="flex-wrap:nowrap">
              <button type="button" class="btn btn-xs btn-ghost" onclick={() => move(i, -1)} disabled={i === 0} aria-label="Move up">↑</button>
              <button type="button" class="btn btn-xs btn-ghost" onclick={() => move(i, 1)} disabled={i === (n.fields?.length ?? 0) - 1} aria-label="Move down">↓</button>
              <button type="button" class="btn btn-xs btn-ghost" onclick={() => n.fields!.splice(i, 1)} aria-label="Remove field">✕</button>
            </div>
            {#if previewStep}<span class="pv" class:err={!!previewStep.error}>{previewStep.error ? previewStep.error : valueAt(previewStep.output, f.target)}</span>{/if}
          </div>
        {/each}
      </div>
      <div class="actions"><button type="button" class="btn btn-sm" onclick={addField}>Add field</button><span class="hint">Fields run in order; later fields can read <code class="inl">out</code>.</span></div>
    {/if}

    {#if tr}
      <details>
        <summary class="hint">Defaults, drops, nil handling and output schema</summary>
        <div class="stack" style="margin-top:8px">
          <div class="stack-sm">
            <h3 class="sub-h">Defaults (data)</h3>
            {#each tr.defaults ?? [] as d, i (i)}
              <div class="row-sm">
                <input type="text" class="code" bind:value={d.target} placeholder="target.path" aria-label="Default target" style="flex:1" />
                <input type="text" class="code" value={JSON.stringify(d.value)} onchange={(e) => { try { tr!.defaults![i].value = JSON.parse(e.currentTarget.value); } catch { tr!.defaults![i].value = e.currentTarget.value; } }} aria-label="Default value (JSON)" style="flex:1" />
                <button type="button" class="btn btn-xs btn-ghost" onclick={() => tr!.defaults!.splice(i, 1)} aria-label="Remove default">✕</button>
              </div>
            {/each}
            <div><button type="button" class="btn btn-xs" onclick={() => (tr!.defaults = [...(tr!.defaults ?? []), { target: '', value: '' }])}>Add default</button></div>
          </div>
          <div class="field"><label for="{base}-drop">Drop paths (comma-separated)</label><input id="{base}-drop" type="text" class="code" value={dropText} onchange={(e) => (tr!.drop = e.currentTarget.value.split(',').map((s) => s.trim()).filter(Boolean))} /></div>
          <label class="check"><input type="checkbox" checked={tr.omitNil !== false} onchange={(e) => (tr!.omitNil = e.currentTarget.checked ? undefined : false)} /> Omit fields that come out nil</label>
          <SchemaRefPicker {app} bind:value={tr.outputSchema} label="Output schema (optional)" hint="When set, the output is checked against it; the next rules node's input schema must accept it." />
        </div>
      </details>
    {/if}
    <Problems problems={problems.problemsUnder(base).filter((p) => !/\/fields\/\d+\/expr$/.test(p.path))} />
  </section>

  <section class="panel" aria-live="polite">
    <header><h2>Output</h2>{#if previewStep}<span class="hint">{previewStep.micros ?? 0} µs</span>{/if}</header>
    {#if previewError}<ErrorNote error={previewError} />{/if}
    {#if previewStep}
      {#if previewStep.error}<p class="result-line bad">{previewStep.error}</p>{/if}
      <JsonView value={previewStep.output} scroll />
      <details><summary class="hint">This node's input</summary><JsonView value={previewStep.input} scroll /></details>
    {:else if previewNote}
      <p class="hint">{previewNote}</p>
    {:else}
      <p class="hint">Press Preview to run the draft and see what {node.id} builds.</p>
    {/if}
  </section>
</div>
