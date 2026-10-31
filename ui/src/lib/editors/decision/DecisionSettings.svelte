<script lang="ts">
  import type { DecisionSpec } from '$lib/api/types.gen';
  import Problems from '$lib/components/Problems.svelte';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import DataEditor from '$lib/editors/data/DataEditor.svelte';
  import type { DraftEditor } from '$lib/draft.svelte';
  import { registry } from '$lib/stores/registry.svelte';

  let { app, ed, disabled = false }: { app: string; ed: DraftEditor<DecisionSpec>; disabled?: boolean } = $props();
  let s = $derived(ed.spec);
  let outRef = $derived(s.output?.schema);
  let outSchema = $derived(registry.schema(app, outRef?.name, outRef?.version));
  let fieldsText = $derived((s.logging?.fields ?? []).join(', '));
</script>

<div class="grid-2">
  <section class="panel">
    <header><h2>Decision</h2></header>
    <fieldset {disabled} class="stack">
      <div class="field"><label for="ds-desc">Description</label><input id="ds-desc" type="text" bind:value={ed.description} maxlength="500" /></div>
      <div class="field">
        <label for="ds-entry">Entry node</label>
        <select id="ds-entry" bind:value={s.entry}>
          {#each s.nodes as n (n.id)}<option value={n.id}>{n.id} · {n.type}</option>{/each}
        </select>
      </div>
      <SchemaRefPicker {app} value={s.inputSchema} onchange={(v) => (s.inputSchema = v)} label="Request input schema (optional)" hint="Checked before the first node runs. When the entry node is a rules or table node, its input schema is used instead." />
      <SchemaRefPicker {app} value={s.output?.schema} onchange={(v) => (s.output = v ? { schema: v } : undefined)} label="Output schema (optional)" hint="The response is checked against it; failing that, the fallback is returned." />
      <div class="field">
        <label for="ds-budget">Budget (µs, AI time excluded)</label>
        <input id="ds-budget" type="number" min="1" value={s.budgetMicros ?? 200} onchange={(e) => (s.budgetMicros = Number(e.currentTarget.value) || undefined)} />
        <span class="hint">Checked at publish (p99 of the test cases); the runtime breaker opens at 10×.</span>
      </div>
    </fieldset>
    <Problems problems={ed.problemsUnder('/entry').concat(ed.problemsUnder('/inputSchema'), ed.problemsUnder('/output'))} />
  </section>
  <section class="panel">
    <header><h2>Fallback</h2><span class="hint">returned with fallbackUsed=true when a node fails without onError</span></header>
    <DataEditor bind:value={s.fallback} schema={outSchema} {disabled} />
    <Problems problems={ed.problemsUnder('/fallback')} />
    <hr class="sep" />
    <h2>Logging</h2>
    <fieldset {disabled} class="form-grid">
      <div class="field">
        <label for="ds-sample">Sample rate (0–1)</label>
        <input id="ds-sample" type="number" min="0" max="1" step="0.01" value={s.logging?.sample ?? ''} placeholder="environment default" onchange={(e) => (s.logging = { ...s.logging, sample: e.currentTarget.value === '' ? undefined : Number(e.currentTarget.value) })} />
      </div>
      <label class="check" style="align-self:end;padding-bottom:8px"><input type="checkbox" checked={!!s.logging?.aiPayloads} onchange={(e) => (s.logging = { ...s.logging, aiPayloads: e.currentTarget.checked || undefined })} /> Log AI payloads</label>
      <div class="field" style="grid-column:1/-1">
        <label for="ds-fields">Fields stored in clear (comma-separated paths)</label>
        <input id="ds-fields" type="text" class="code" value={fieldsText} onchange={(e) => (s.logging = { ...s.logging, fields: e.currentTarget.value.split(',').map((x) => x.trim()).filter(Boolean) })} placeholder="lead.city, car.year" />
        <span class="hint">Everything else is stored as an HMAC. PII fields are never stored in clear.</span>
      </div>
    </fieldset>
  </section>
</div>
