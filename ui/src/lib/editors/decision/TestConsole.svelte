<script lang="ts">
  // Run the decision with an input and context; show output, path and every node's input and output.
  import { api } from '$lib/api';
  import type { DecisionSpec, EvaluateResult } from '$lib/api/types.gen';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import JsonEditor from '$lib/components/JsonEditor.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import PathTrace from '$lib/components/PathTrace.svelte';
  import type { DraftEditor } from '$lib/draft.svelte';
  import { fmtMicros } from '$lib/model/format';
  import { exampleFor } from '$lib/model/schemaTree';
  import { currentEnv } from '$lib/nav.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import { shell } from '$lib/stores/shell.svelte';
  import { toast } from '$lib/stores/toast.svelte';

  let { app, ed, onpath }: { app: string; ed: DraftEditor<DecisionSpec>; onpath: (ids: string[]) => void } = $props();

  let input: unknown = $state(undefined);
  let now = $state('2026-10-30T10:00:00Z');
  let unit = $state('L-1');
  let env = $state(currentEnv() === 'prod' ? 'dev' : currentEnv());
  let source = $state('draft');
  let version = $state(0);
  let skipAI = $state(true);
  let result: EvaluateResult | null = $state(null);
  let error: unknown = $state(null);
  let busy = $state(false);

  $effect(() => {
    if (input !== undefined) return;
    const t = ed.spec.tests?.[0];
    if (t) {
      input = $state.snapshot(t.input);
      if (t.ctx?.now) now = t.ctx.now;
      if (t.ctx?.unit) unit = t.ctx.unit;
      return;
    }
    const ref = ed.spec.inputSchema ?? (ed.spec.nodes.find((n) => n.id === ed.spec.entry) as { inputSchema?: { name: string; version: number } } | undefined)?.inputSchema;
    const sch = ref ? registry.schema(app, ref.name, ref.version) : null;
    if (sch) input = exampleFor(sch);
    else if (sch === null) input = {};
  });

  async function run(): Promise<void> {
    busy = true;
    error = null;
    try {
      result = await api.evaluateDecision({ app, name: ed.name, body: { source: source === 'draft' ? 'draft' : 'version', version: source === 'draft' ? undefined : version || ed.resource?.latestVersion, environment: env, input: $state.snapshot(input), ctx: { now: now || undefined, unit: unit || undefined }, skipAI } });
      onpath(result.path.map((p) => p.nodeId));
    } catch (e) {
      error = e;
      result = null;
    } finally {
      busy = false;
    }
  }

  function saveAsTest(): void {
    if (!result) return;
    const name = `console ${new Date().toLocaleTimeString()}`;
    ed.spec.tests = [...(ed.spec.tests ?? []), { name, ctx: { now: now || undefined, unit: unit || undefined }, input: $state.snapshot(input), ...(result.error ? { expectError: result.error.code } : { expect: $state.snapshot(result.output) }) }];
    toast(`Added test “${name}”. Save the draft to keep it.`);
  }

  function useTest(i: string): void {
    const t = ed.spec.tests?.[Number(i)];
    if (!t) return;
    input = $state.snapshot(t.input);
    now = t.ctx?.now ?? now;
    unit = t.ctx?.unit ?? '';
  }
</script>

<div class="grid-2">
  <section class="panel">
    <header>
      <h2>Request</h2>
      {#if ed.spec.tests?.length}
        <select aria-label="Load a test case input" onchange={(e) => { useTest(e.currentTarget.value); e.currentTarget.value = ''; }} style="width:auto;max-width:60%">
          <option value="">Load a test case…</option>
          {#each ed.spec.tests as t, i (i)}<option value={String(i)}>{t.name}</option>{/each}
        </select>
      {/if}
    </header>
    <JsonEditor bind:value={input} rows={14} label="Input" id="console-input" />
    <div class="form-grid">
      <div class="field"><label for="c-now">ctx.now</label><input id="c-now" type="text" class="code" bind:value={now} placeholder="RFC 3339; empty = server clock" /></div>
      <div class="field"><label for="c-unit">ctx.unit</label><input id="c-unit" type="text" class="code" bind:value={unit} placeholder="unit id (rollouts, experiments)" /></div>
      <div class="field">
        <label for="c-env">Segments, lookups, gates from</label>
        <select id="c-env" bind:value={env}>{#each shell.envs as e (e)}<option value={e}>{e}</option>{/each}</select>
      </div>
      <div class="field">
        <label for="c-src">Run</label>
        <select id="c-src" bind:value={source}>
          <option value="draft">saved draft</option>
          <option value="version">a version</option>
        </select>
      </div>
      {#if source === 'version'}
        <div class="field"><label for="c-ver">Version</label><input id="c-ver" type="number" min="1" max={ed.resource?.latestVersion ?? 1} value={version || ed.resource?.latestVersion || 1} oninput={(e) => (version = Number(e.currentTarget.value))} /></div>
      {/if}
    </div>
    <label class="check"><input type="checkbox" bind:checked={skipAI} /> Skip AI calls (AI nodes take their onError path)</label>
    <div class="actions">
      <button type="button" class="btn btn-primary" onclick={run} disabled={busy}>{busy ? 'Running…' : 'Run'}</button>
      {#if ed.dirty && source === 'draft'}<span class="hint warn">Runs the saved draft; save to include your edits.</span>{/if}
    </div>
  </section>

  <section class="panel" aria-live="polite">
    <header>
      <h2>Result</h2>
      {#if result}
        <div class="row-sm">
          {#if result.fallbackUsed}<span class="pill pill-warn">fallback</span>{:else if result.error}<span class="pill pill-crit">{result.error.code}</span>{:else}<span class="pill pill-ok">ok</span>{/if}
          <span class="hint mono">{result.decisionVersion} · {fmtMicros(result.evalMicros)}</span>
        </div>
      {/if}
    </header>
    {#if error}
      <ErrorNote {error} />
    {:else if result}
      {#if result.error}<ErrorNote error={result.error} title={result.error.title} />{/if}
      <h3 class="sub-h">Output</h3>
      <JsonView value={result.output} scroll />
      <h3 class="sub-h">Path</h3>
      <PathTrace path={result.path} detail />
      <div class="actions">
        <button type="button" class="btn btn-sm" onclick={saveAsTest}>Save as a test case</button>
        <button type="button" class="btn btn-sm" onclick={() => onpath([])}>Clear graph highlight</button>
      </div>
    {:else}
      <p class="hint">Run the decision to see its output and the path it took. The path is highlighted on the graph.</p>
    {/if}
  </section>
</div>
