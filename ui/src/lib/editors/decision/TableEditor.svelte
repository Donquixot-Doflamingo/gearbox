<script lang="ts">
  // Decision table: input columns (name + expression), output columns from the output schema,
  // rows of cells, hit policy, CSV import/export and a local "try an input" with the same cell rules.
  import type { Node } from '$lib/api/types.gen';
  import Problems from '$lib/components/Problems.svelte';
  import DataEditor from '$lib/editors/data/DataEditor.svelte';
  import { parseCsv, toCsv } from '$lib/model/csv';
  import { evaluate } from '$lib/model/expr';
  import { isObj, type JsonSchema } from '$lib/model/schemaTree';
  import { cellIssue, describeCell, matchCell, outputCellText, parseCell, parseOutputCell } from '$lib/model/tableCell';
  import { registry } from '$lib/stores/registry.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import SchemaGate from './SchemaGate.svelte';
  import type { ProblemSource, TableDraft } from './types';

  let { app, node = $bindable(), index, problems, disabled = false }: { app: string; node: Node; index: number; problems: ProblemSource; disabled?: boolean } = $props();

  let t = $derived(node as unknown as TableDraft);
  let base = $derived(`/nodes/${index}`);
  let outSchema = $derived(registry.schema(app, t.outputSchema?.name, t.outputSchema?.version));
  let outProps = $derived(outSchema && isObj(outSchema.properties) ? Object.keys(outSchema.properties as Record<string, JsonSchema>) : []);

  $effect(() => {
    if (!t.inputs) t.inputs = [];
    if (!t.outputs) t.outputs = [];
    if (!t.rows) t.rows = [];
  });

  function addInput(): void {
    let i = t.inputs.length + 1;
    while (t.inputs.some((c) => c.name === `col${i}`)) i++;
    t.inputs.push({ name: `col${i}`, expr: 'input.' });
  }
  function removeInput(i: number): void {
    const name = t.inputs[i].name;
    t.inputs.splice(i, 1);
    for (const r of t.rows) delete r.when[name];
  }
  function renameInput(i: number, to: string): void {
    const from = t.inputs[i].name;
    if (!to || to === from || t.inputs.some((c) => c.name === to)) return;
    t.inputs[i].name = to;
    for (const r of t.rows) {
      if (from in r.when) {
        r.when[to] = r.when[from];
        delete r.when[from];
      }
    }
  }
  function toggleOutput(name: string, on: boolean): void {
    t.outputs = on ? outProps.filter((p) => p === name || t.outputs.includes(p)) : t.outputs.filter((o) => o !== name);
  }
  function addRow(): void {
    let i = t.rows.length + 1;
    while (t.rows.some((r) => r.id === `row_${i}`)) i++;
    t.rows.push({ id: `row_${i}`, when: Object.fromEntries(t.inputs.map((c) => [c.name, '-'])), then: {} });
  }
  function moveRow(i: number, d: number): void {
    const j = i + d;
    if (j < 0 || j >= t.rows.length) return;
    const [x] = t.rows.splice(i, 1);
    t.rows.splice(j, 0, x);
  }
  function setThen(i: number, o: string, text: string): void {
    const v = parseOutputCell(text);
    const then = { ...(t.rows[i].then as Record<string, unknown>) };
    if (v === undefined) delete then[o];
    else then[o] = v;
    t.rows[i].then = then;
  }

  // try an input
  let trial = $state<Record<string, string>>({});
  function trialValue(name: string): unknown {
    const raw = trial[name] ?? '';
    if (raw.trim() === '') return undefined;
    try {
      return JSON.parse(raw);
    } catch {
      return raw;
    }
  }
  let anyTrial = $derived(Object.values(trial).some((v) => v.trim() !== ''));
  let hits = $derived.by(() => {
    if (!anyTrial) return new Set<string>();
    const out = new Set<string>();
    for (const r of t.rows ?? []) {
      const ok = (t.inputs ?? []).every((c) => matchCell(parseCell(r.when?.[c.name]), trialValue(c.name), (e, value) => evaluate(e, { value })) === true);
      if (ok) out.add(r.id);
    }
    return out;
  });
  let firstHit = $derived((t.rows ?? []).find((r) => hits.has(r.id)));
  let resultText = $derived.by(() => {
    if (!anyTrial) return 'Type values to see which rows match.';
    if (!firstHit) return 'No row matched; the node returns its default.';
    const pol = t.hitPolicy ?? 'first';
    const many = hits.size > 1;
    const out = JSON.stringify(Object.fromEntries((t.outputs ?? []).map((o) => [o, (firstHit!.then as Record<string, unknown>)[o]])));
    if (pol === 'unique' && many) return `${hits.size} rows match; hit policy unique fails here.`;
    if (pol === 'collect') return `${hits.size} row${many ? 's' : ''} collected: ${[...hits].join(', ')}`;
    return `Matched ${firstHit.id} → ${out}${many ? ` (${hits.size} rows match; ${pol === 'priority' ? 'highest priority wins' : 'first wins'})` : ''}`;
  });

  // CSV
  function exportCsv(): void {
    const head = ['id', ...(t.hitPolicy === 'priority' ? ['priority'] : []), ...t.inputs.map((c) => c.name), ...t.outputs.map((o) => `=>${o}`)];
    const rows = t.rows.map((r) => [r.id, ...(t.hitPolicy === 'priority' ? [r.priority ?? ''] : []), ...t.inputs.map((c) => r.when[c.name] ?? '-'), ...t.outputs.map((o) => outputCellText((r.then as Record<string, unknown>)[o]))]);
    const blob = new Blob([toCsv([head, ...rows])], { type: 'text/csv' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `${node.id}.csv`;
    a.click();
    URL.revokeObjectURL(a.href);
  }
  async function importCsv(e: Event): Promise<void> {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    const rows = parseCsv(await file.text());
    const head = rows.shift() ?? [];
    if (head[0] !== 'id') {
      toast('The first column must be "id". Export a CSV to see the expected header.', { kind: 'crit' });
      return;
    }
    const inputs = head.filter((h, i) => i > 0 && h !== 'priority' && !h.startsWith('=>'));
    const outputs = head.filter((h) => h.startsWith('=>')).map((h) => h.slice(2));
    const missing = inputs.filter((n) => !t.inputs.some((c) => c.name === n));
    for (const n of missing) t.inputs.push({ name: n, expr: `input.${n}` });
    t.outputs = [...new Set([...t.outputs, ...outputs])];
    t.rows = rows.map((cols) => {
      const rec = Object.fromEntries(head.map((h, i) => [h, cols[i] ?? '']));
      const then: Record<string, unknown> = {};
      for (const o of outputs) {
        const v = parseOutputCell(rec[`=>${o}`]);
        if (v !== undefined) then[o] = v;
      }
      return { id: rec.id, priority: rec.priority ? Number(rec.priority) : undefined, when: Object.fromEntries(inputs.map((n) => [n, rec[n] || '-'])), then };
    });
    toast(`Imported ${t.rows.length} rows${missing.length ? `; added columns ${missing.join(', ')} (check their expressions)` : ''}`);
  }
</script>

<SchemaGate {app} bind:input={t.inputSchema} bind:output={t.outputSchema} pointer={base} {problems} {disabled} what="table">
  <section class="panel">
    <header>
      <h2>{node.id}</h2>
      <div class="row-sm">
        <label for="{base}-hp" class="hint">Hit policy</label>
        <select id="{base}-hp" bind:value={t.hitPolicy} style="width:auto">
          <option value="first">first</option>
          <option value="unique">unique</option>
          <option value="collect">collect</option>
          <option value="priority">priority</option>
        </select>
        <label class="btn btn-sm">Import CSV<input type="file" accept=".csv,text/csv" class="sr" onchange={importCsv} /></label>
        <button type="button" class="btn btn-sm" onclick={exportCsv}>Export CSV</button>
      </div>
    </header>

    <div class="stack-sm">
      <h3 class="sub-h">Output columns</h3>
      {#if outProps.length}
        <div class="row-sm">
          {#each outProps as p (p)}<label class="check sm"><input type="checkbox" checked={(t.outputs ?? []).includes(p)} onchange={(e) => toggleOutput(p, e.currentTarget.checked)} />{p}</label>{/each}
        </div>
      {:else}<span class="hint">The output schema has no properties to choose from.</span>{/if}
    </div>

    <div class="tbl-wrap">
      <table class="dt">
        <thead>
          <tr>
            <th>Row</th>
            {#if t.hitPolicy === 'priority'}<th>Priority</th>{/if}
            {#each t.inputs ?? [] as c, ci (ci)}
              <th>
                <div class="col-head">
                  <div class="row-sm" style="flex-wrap:nowrap">
                    <label class="sr" for="{base}-cn-{ci}">Column name</label>
                    <input id="{base}-cn-{ci}" type="text" value={c.name} onchange={(e) => renameInput(ci, e.currentTarget.value.trim())} />
                    <button type="button" class="btn btn-xs btn-ghost" onclick={() => removeInput(ci)} aria-label="Remove column {c.name}">✕</button>
                  </div>
                  <label class="sr" for="{base}-ce-{ci}">Expression for {c.name}</label>
                  <input id="{base}-ce-{ci}" type="text" class="code" bind:value={t.inputs[ci].expr} placeholder="input.price" />
                </div>
              </th>
            {/each}
            <th><button type="button" class="btn btn-xs" onclick={addInput}>+ input</button></th>
            {#each t.outputs ?? [] as o, oi (o)}<th class:out={oi === 0}>→ {o}</th>{/each}
            <th></th>
          </tr>
        </thead>
        <tbody>
          {#each t.rows ?? [] as r, ri (ri)}
            <tr class:hit={hits.has(r.id)}>
              <td class="rowid"><input type="text" class="code" value={r.id} onchange={(e) => (t.rows[ri].id = e.currentTarget.value.trim())} aria-label="Row id" style="min-width:70px" /></td>
              {#if t.hitPolicy === 'priority'}<td><input type="number" value={r.priority ?? ''} onchange={(e) => (t.rows[ri].priority = e.currentTarget.value === '' ? undefined : Number(e.currentTarget.value))} aria-label="Priority of {r.id}" style="min-width:60px" /></td>{/if}
              {#each t.inputs ?? [] as c (c.name)}
                {@const text = r.when?.[c.name] ?? '-'}
                {@const issue = cellIssue(text)}
                <td>
                  <input type="text" class:any={text.trim() === '-' || text.trim() === ''} class:warn={issue?.level === 'warning'} class:bad={issue?.level === 'error'} value={text} oninput={(e) => (t.rows[ri].when = { ...t.rows[ri].when, [c.name]: e.currentTarget.value })} aria-label="{r.id} {c.name}" title={issue?.message ?? describeCell(parseCell(text))} aria-invalid={issue?.level === 'error' ? 'true' : undefined} />
                </td>
              {/each}
              <td></td>
              {#each t.outputs ?? [] as o, oi (o)}
                <td class:out={oi === 0}><input type="text" value={outputCellText((r.then as Record<string, unknown>)?.[o])} onchange={(e) => setThen(ri, o, e.currentTarget.value)} aria-label="{r.id} → {o}" /></td>
              {/each}
              <td class="nowrap">
                <button type="button" class="btn btn-xs btn-ghost" onclick={() => moveRow(ri, -1)} disabled={ri === 0} aria-label="Move {r.id} up">↑</button>
                <button type="button" class="btn btn-xs btn-ghost" onclick={() => moveRow(ri, 1)} disabled={ri === t.rows.length - 1} aria-label="Move {r.id} down">↓</button>
                <button type="button" class="btn btn-xs btn-ghost" onclick={() => t.rows.splice(ri, 1)} aria-label="Delete {r.id}">✕</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <div class="actions"><button type="button" class="btn btn-sm" onclick={addRow} disabled={!(t.inputs ?? []).length}>Add row</button>{#if !(t.inputs ?? []).length}<span class="hint">Add an input column first.</span>{/if}</div>
    <p class="hint">Cells: <code class="inl">-</code> any · <code class="inl">&lt; 300000</code>, <code class="inl">&gt;= 5</code> · <code class="inl">[a..b]</code> inclusive range · <code class="inl">"text"</code> · a JSON list <code class="inl">["a","b"]</code> means one of · anything else is an expression where <code class="inl">value</code> is the column value, e.g. <code class="inl">value % 2 == 0</code>. Output cells are JSON (or text); start with <code class="inl">=</code> for an expression.</p>
    <Problems problems={problems.problemsUnder(base).filter((p) => !p.path.endsWith('Schema'))} showPath />
  </section>

  <div class="grid-2">
    <section class="panel">
      <header><h2>Try an input</h2><span class="hint">evaluated in the browser with the same cell rules</span></header>
      <div class="form-grid">
        {#each t.inputs ?? [] as c (c.name)}
          <div class="field"><label for="{base}-try-{c.name}">{c.name}</label><input id="{base}-try-{c.name}" type="text" class="code" value={trial[c.name] ?? ''} oninput={(e) => (trial[c.name] = e.currentTarget.value)} placeholder="JSON or text" /></div>
        {/each}
      </div>
      <p class="result-line" class:ok={!!firstHit} aria-live="polite">{resultText}</p>
    </section>
    <section class="panel">
      <DataEditor bind:value={t.default} schema={outSchema} label="Default (no row matched)" />
    </section>
  </div>
</SchemaGate>
