<script lang="ts">
  // One value of a data slot, edited according to its schema. A value can switch to an `=expression`.
  import { untrack } from 'svelte';
  import ExprInput from '$lib/components/ExprInput.svelte';
  import { displayString, isExprSlot, literalString } from '$lib/model/dataSlot';
  import { isNullable, isObj, primaryType, resolveRef, typeLabel, type JsonSchema } from '$lib/model/schemaTree';
  import DataField from './DataField.svelte';

  let {
    schema,
    root,
    value,
    name,
    required = false,
    disabled = false,
    allowExpr = true,
    depth = 0,
    error,
    onchange,
  }: {
    schema: JsonSchema | undefined;
    root: JsonSchema | undefined;
    value: unknown;
    name: string;
    required?: boolean;
    disabled?: boolean;
    allowExpr?: boolean;
    depth?: number;
    error?: string;
    onchange: (v: unknown) => void;
  } = $props();

  const uid = `df-${Math.random().toString(36).slice(2, 8)}`;
  let s = $derived((root && schema ? resolveRef(root, schema) : schema) ?? {});
  let t = $derived(primaryType(s));
  let nullable = $derived(isNullable(s));
  let enumVals = $derived(Array.isArray(s.enum) ? (s.enum as unknown[]).filter((v) => v !== null) : null);
  let isExpr = $derived(isExprSlot(value));
  let props = $derived(isObj(s.properties) ? Object.entries(s.properties as Record<string, JsonSchema>) : []);
  let req = $derived(Array.isArray(s.required) ? (s.required as string[]) : []);
  let obj = $derived(isObj(value) ? (value as Record<string, unknown>) : {});

  let numText = $state('');
  let numError: string | null = $state(null);
  // follow outside changes without rewriting what is being typed ("5." stays "5.")
  $effect(() => {
    const v = value;
    untrack(() => {
      if (typeof v === 'number') {
        if (numText.trim() === '' || Number(numText) !== v) numText = String(v);
      } else if ((v === undefined || v === null) && !numError) numText = '';
    });
  });

  function setNum(text: string): void {
    numText = text;
    if (text.trim() === '') {
      numError = null;
      onchange(undefined);
      return;
    }
    const n = Number(text);
    if (isNaN(n) || (t === 'integer' && !Number.isInteger(n))) numError = t === 'integer' ? 'Whole number' : 'Number';
    else {
      numError = null;
      onchange(n);
    }
  }

  function toExpr(): void {
    onchange(isExprSlot(value) ? value : '=' + (value === undefined ? '' : typeof value === 'string' ? `"${value}"` : JSON.stringify(value)));
  }
  function toLiteral(): void {
    onchange(undefined);
  }

  function setChild(k: string, v: unknown): void {
    const next: Record<string, unknown> = { ...obj };
    if (v === undefined) delete next[k];
    else next[k] = v;
    onchange(Object.keys(next).length || required ? next : undefined);
  }

  let arrText = $state('');
  let arrError: string | null = $state(null);
  $effect(() => {
    const v = value;
    if (t !== 'array' || isExpr) return;
    untrack(() => {
      let same = false;
      try {
        same = JSON.stringify(JSON.parse(arrText)) === JSON.stringify(v);
      } catch {
        same = false;
      }
      if (!same && !arrError) arrText = v === undefined ? '' : JSON.stringify(v);
    });
  });
  function setArr(text: string): void {
    arrText = text;
    if (!text.trim()) {
      arrError = null;
      onchange(undefined);
      return;
    }
    try {
      const v = JSON.parse(text);
      if (!Array.isArray(v)) throw new Error('not a list');
      arrError = null;
      onchange(v);
    } catch {
      arrError = 'Enter a JSON list, e.g. ["a", "b"]';
    }
  }
</script>

<div class="data-row">
  <div class="k">
    <label for={uid}>{name}{#if required}<span class="req" aria-label="required"> *</span>{/if}</label>
    <span class="t">{typeLabel(s, root)}</span>
  </div>
  <div class="v">
    {#if isExpr}
      <ExprInput value={String(value).slice(1)} id={uid} {disabled} placeholder="input.reason" onchange={(v) => onchange('=' + v)} />
    {:else if t === 'object' && props.length}
      <span class="hint" style="padding-top:8px">{value === undefined ? 'not set' : `${Object.keys(obj).length} of ${props.length} set`}</span>
      {#if value === undefined && !disabled}<button type="button" class="btn btn-xs" onclick={() => onchange({})}>Set</button>{/if}
    {:else if enumVals}
      <select id={uid} {disabled} value={value === undefined ? '__unset' : JSON.stringify(value)} onchange={(e) => onchange(e.currentTarget.value === '__unset' ? undefined : JSON.parse(e.currentTarget.value))} aria-invalid={error ? 'true' : undefined}>
        <option value="__unset">{required ? 'choose…' : 'not set'}</option>
        {#each enumVals as v (JSON.stringify(v))}<option value={JSON.stringify(v)}>{String(v)}</option>{/each}
        {#if nullable}<option value="null">null</option>{/if}
      </select>
    {:else if t === 'boolean'}
      <select id={uid} {disabled} value={value === undefined ? '__unset' : JSON.stringify(value)} onchange={(e) => onchange(e.currentTarget.value === '__unset' ? undefined : JSON.parse(e.currentTarget.value))}>
        <option value="__unset">{required ? 'choose…' : 'not set'}</option>
        <option value="true">true</option>
        <option value="false">false</option>
        {#if nullable}<option value="null">null</option>{/if}
      </select>
    {:else if t === 'integer' || t === 'number'}
      <input id={uid} type="text" inputmode="decimal" class="code" {disabled} value={numText} oninput={(e) => setNum(e.currentTarget.value)} placeholder={required ? 'required' : 'not set'} aria-invalid={numError || error ? 'true' : undefined} />
      {#if nullable}<button type="button" class="mode-toggle" aria-pressed={value === null} {disabled} onclick={() => onchange(value === null ? undefined : null)}>null</button>{/if}
    {:else if t === 'array'}
      <input id={uid} type="text" class="code" {disabled} value={arrText} oninput={(e) => setArr(e.currentTarget.value)} placeholder="[ … ]" aria-invalid={arrError ? 'true' : undefined} />
    {:else}
      <input id={uid} type="text" {disabled} value={typeof value === 'string' ? displayString(value) : value === undefined || value === null ? '' : JSON.stringify(value)} oninput={(e) => onchange(e.currentTarget.value === '' && !required ? undefined : literalString(e.currentTarget.value))} placeholder={required ? 'required' : 'not set'} aria-invalid={error ? 'true' : undefined} />
      {#if nullable}<button type="button" class="mode-toggle" aria-pressed={value === null} {disabled} onclick={() => onchange(value === null ? undefined : null)}>null</button>{/if}
    {/if}
    {#if allowExpr && !disabled && !(t === 'object' && props.length && !isExpr)}
      <button type="button" class="mode-toggle" aria-pressed={isExpr} onclick={isExpr ? toLiteral : toExpr} title={isExpr ? 'Back to a literal value' : 'Compute this value with an expression ("=…")'}>=</button>
    {/if}
    {#if numError || arrError || error}<span class="error-text" style="flex-basis:100%">{numError ?? arrError ?? error}</span>{/if}
  </div>
  {#if t === 'object' && props.length && !isExpr && value !== undefined}
    <div class="data-nest">
      {#each props as [k, ps] (k)}
        <DataField schema={ps} {root} value={obj[k]} name={k} required={req.includes(k)} {disabled} {allowExpr} depth={depth + 1} onchange={(v) => setChild(k, v)} />
      {/each}
    </div>
  {/if}
</div>
