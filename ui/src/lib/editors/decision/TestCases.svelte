<script lang="ts">
  import { api } from '$lib/api';
  import type { DecisionSpec, TestRunResult } from '$lib/api/types.gen';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import JsonEditor from '$lib/components/JsonEditor.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import type { DraftEditor } from '$lib/draft.svelte';
  import { fmtMicros } from '$lib/model/format';
  import { currentEnv } from '$lib/nav.svelte';
  import { shell } from '$lib/stores/shell.svelte';

  let { app, ed }: { app: string; ed: DraftEditor<DecisionSpec> } = $props();

  let env = $state(currentEnv() === 'prod' ? 'dev' : currentEnv());
  let result: TestRunResult | null = $state(null);
  let error: unknown = $state(null);
  let busy = $state(false);
  let openIdx: number | null = $state(null);

  $effect(() => {
    if (!ed.spec.tests) ed.spec.tests = [];
  });

  async function run(saveFirst: boolean): Promise<void> {
    busy = true;
    error = null;
    try {
      if (saveFirst && ed.dirty && !(await ed.save())) return;
      result = await api.runDecisionTests({ app, name: ed.name, body: { source: 'draft', environment: env } });
    } catch (e) {
      error = e;
    } finally {
      busy = false;
    }
  }

  function add(): void {
    const first = ed.spec.tests?.[0];
    ed.spec.tests = [...(ed.spec.tests ?? []), { name: `case ${(ed.spec.tests?.length ?? 0) + 1}`, ctx: { now: '2026-10-30T10:00:00Z', unit: 'L-1' }, input: first ? $state.snapshot(first.input) : {}, expect: {} }];
    openIdx = (ed.spec.tests?.length ?? 1) - 1;
  }
  function dup(i: number): void {
    const t = $state.snapshot(ed.spec.tests![i]);
    ed.spec.tests!.splice(i + 1, 0, { ...t, name: `${t.name} (copy)` });
    openIdx = i + 1;
  }
  let byName = $derived(new Map((result?.results ?? []).map((r) => [r.name, r])));
</script>

<section class="panel">
  <header>
    <h2>Test cases</h2>
    <div class="row-sm">
      <label for="t-env" class="hint">against</label>
      <select id="t-env" bind:value={env} style="width:auto">{#each shell.envs as e (e)}<option value={e}>{e}</option>{/each}</select>
      <button type="button" class="btn btn-sm btn-primary" onclick={() => run(true)} disabled={busy}>{busy ? 'Running…' : ed.dirty ? 'Save and run tests' : 'Run tests'}</button>
    </div>
  </header>
  <p class="hint">Every test must pass to cut a version. <code class="inl">expect</code> is a partial match: listed paths must equal, others are ignored. <code class="inl">ctx</code> is fixed so tests never depend on the clock or a random unit.</p>
  {#if error}<ErrorNote {error} />{/if}
  {#if result}<p class="result-line" class:ok={result.failed === 0} class:bad={result.failed > 0}>{result.passed} passed · {result.failed} failed</p>{/if}

  <ul class="tests">
    {#each ed.spec.tests ?? [] as t, i (i)}
      {@const r = byName.get(t.name)}
      <li class={r ? (r.ok ? 'pass' : 'fail') : ''}>
        <span class="mark" aria-hidden="true">{r ? (r.ok ? '✓' : '✕') : '·'}</span>
        <span>
          <button type="button" class="link-btn" onclick={() => (openIdx = openIdx === i ? null : i)} aria-expanded={openIdx === i}>{t.name}</button>
          {#if r && !r.ok}
            <span class="detail">{r.error ?? ''}{(r.mismatches ?? []).map((m) => ` ${m.path}: expected ${JSON.stringify(m.expected)}, got ${JSON.stringify(m.actual)}`).join(';')}</span>
          {/if}
        </span>
        <span class="mono muted">{r ? fmtMicros(r.micros) : ''}</span>
      </li>
      {#if openIdx === i}
        <li style="display:block">
          <div class="stack" style="padding:6px 0 12px 28px">
            <div class="form-grid">
              <div class="field"><label for="tc-name-{i}">Name</label><input id="tc-name-{i}" type="text" bind:value={t.name} /></div>
              <div class="field"><label for="tc-now-{i}">ctx.now</label><input id="tc-now-{i}" type="text" class="code" value={t.ctx?.now ?? ''} oninput={(e) => (t.ctx = { ...t.ctx, now: e.currentTarget.value || undefined })} /></div>
              <div class="field"><label for="tc-unit-{i}">ctx.unit</label><input id="tc-unit-{i}" type="text" class="code" value={t.ctx?.unit ?? ''} oninput={(e) => (t.ctx = { ...t.ctx, unit: e.currentTarget.value || undefined })} /></div>
              <div class="field"><label for="tc-err-{i}">Expect error code</label><input id="tc-err-{i}" type="text" class="code" value={t.expectError ?? ''} oninput={(e) => (t.expectError = e.currentTarget.value || undefined)} placeholder="e.g. INVALID_ARGUMENT" /></div>
            </div>
            <div class="grid-2">
              <JsonEditor bind:value={t.input} label="Input" rows={10} />
              <div class="stack">
                <JsonEditor bind:value={t.expect} label="Expect (partial)" rows={5} allowUndefined />
                <div class="field">
                  <label for="tc-path-{i}">Expect path (optional)</label>
                  <input id="tc-path-{i}" type="text" class="code" value={(t.expectPath ?? []).join(', ')} oninput={(e) => (t.expectPath = e.currentTarget.value.trim() ? e.currentTarget.value.split(',').map((s) => s.trim()).filter(Boolean) : undefined)} placeholder="eligibility:r4, output:ok" />
                </div>
              </div>
            </div>
            {#if r?.actual !== undefined}<div class="stack-sm"><span class="sub-h">Last actual output</span><JsonView value={r.actual} /></div>{/if}
            <div class="actions">
              <button type="button" class="btn btn-xs" onclick={() => dup(i)}>Duplicate</button>
              <button type="button" class="btn btn-xs" onclick={() => { ed.spec.tests!.splice(i, 1); openIdx = null; }}>Delete</button>
            </div>
          </div>
        </li>
      {/if}
    {:else}
      <li><span></span><span class="hint">No test cases yet. Add one here, or run the console and “Save as a test case”.</span><span></span></li>
    {/each}
  </ul>
  <div><button type="button" class="btn btn-sm" onclick={add}>Add test case</button></div>
</section>
