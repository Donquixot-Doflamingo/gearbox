<script lang="ts">
  // Data slot editor (rule `then`, `default`, decision `fallback`…) generated from a schema,
  // with a JSON view. Strings starting with "=" are expressions.
  import JsonEditor from '$lib/components/JsonEditor.svelte';
  import { isExprSlot } from '$lib/model/dataSlot';
  import { isObj, primaryType, validateDocument, type JsonSchema } from '$lib/model/schemaTree';
  import DataField from './DataField.svelte';

  let {
    value = $bindable(),
    schema,
    label,
    disabled = false,
    allowExpr = true,
    errors = [],
  }: { value: unknown; schema: JsonSchema | null | undefined; label?: string; disabled?: boolean; allowExpr?: boolean; errors?: { path: string; message: string }[] } = $props();

  let mode = $state<'form' | 'json'>('form');
  let isObject = $derived(!!schema && primaryType(schema) === 'object' && isObj(schema.properties));
  let propEntries = $derived(isObject ? Object.entries(schema!.properties as Record<string, JsonSchema>) : []);
  let req = $derived(isObject && Array.isArray(schema!.required) ? (schema!.required as string[]) : []);
  let obj = $derived(isObj(value) ? (value as Record<string, unknown>) : {});

  /** Schema problems on literal values (expressions are checked at runtime). */
  let local = $derived.by(() => {
    if (!schema || value === undefined) return [];
    return validateDocument(schema, value).filter((e) => {
      let cur: unknown = value;
      for (const seg of e.path.split('/').slice(1)) cur = isObj(cur) ? cur[seg] : Array.isArray(cur) ? cur[Number(seg)] : undefined;
      return !isExprSlot(cur);
    });
  });

  function setChild(k: string, v: unknown): void {
    const next: Record<string, unknown> = { ...obj };
    if (v === undefined) delete next[k];
    else next[k] = v;
    value = next;
  }
</script>

<div class="data-ed">
  <div class="row-sm">
    {#if label}<h3 class="sub-h">{label}</h3>{/if}
    <span class="spacer"></span>
    <div class="seg" role="radiogroup" aria-label="Edit {label ?? 'value'} as">
      <button type="button" role="radio" aria-checked={mode === 'form'} onclick={() => (mode = 'form')} disabled={!schema}>form</button>
      <button type="button" role="radio" aria-checked={mode === 'json' || !schema} onclick={() => (mode = 'json')}>json</button>
    </div>
  </div>
  {#if mode === 'json' || !schema}
    <JsonEditor bind:value rows={6} {disabled} allowUndefined />
    {#if !schema}<span class="hint">No output schema: edit the value as JSON.</span>{/if}
  {:else if isObject}
    {#each propEntries as [k, ps] (k)}
      <DataField schema={ps} root={schema} value={obj[k]} name={k} required={req.includes(k)} {disabled} {allowExpr} error={[...errors, ...local].find((e) => e.path === `/${k}`)?.message} onchange={(v) => setChild(k, v)} />
    {/each}
    {#if value === undefined}<span class="hint">Not set.</span>{/if}
  {:else}
    <DataField {schema} root={schema} {value} name="value" required {disabled} {allowExpr} onchange={(v) => (value = v)} />
  {/if}
  {#if local.length}
    <ul class="problems">
      {#each local.slice(0, 5) as e (e.path + e.message)}<li class="warning"><span class="pm" aria-hidden="true">!</span><span><span class="mono">{e.path || 'value'}</span> {e.message}</span></li>{/each}
    </ul>
  {/if}
  {#if allowExpr}<span class="hint">Press <span class="mono">=</span> next to a value to compute it with an expression, e.g. <code class="inl">=input.reason</code>.</span>{/if}
</div>
