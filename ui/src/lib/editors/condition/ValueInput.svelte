<script lang="ts">
  // Value editor for a field condition: none, scalar, list, range or regex, typed by the field.
  import type { FieldOperator } from '$lib/api/types.gen';
  import { operatorInfo, parseValue, valueText } from '$lib/model/condition';
  import type { FieldInfo } from '$lib/model/schemaTree';

  let {
    op,
    field,
    value,
    valueTo,
    disabled = false,
    id,
    onchange,
  }: { op: FieldOperator; field: FieldInfo | undefined; value: unknown; valueTo: unknown; disabled?: boolean; id?: string; onchange: (value: unknown, valueTo: unknown) => void } = $props();

  const fallbackId = `val-${Math.random().toString(36).slice(2, 8)}`;
  let uid = $derived(id ?? fallbackId);
  let info = $derived(operatorInfo(op));
  let kind = $derived(info?.valueKind ?? 'scalar');
  let enumVals = $derived((field?.enum ?? []).filter((v) => v !== null));
  let useEnum = $derived(enumVals.length > 0 && (kind === 'scalar' || kind === 'list') && !info?.valueType);
  let isBool = $derived(field?.type === 'boolean' && kind === 'scalar');

  // Local text state starts from the props; the effect below re-syncs it when they change.
  // svelte-ignore state_referenced_locally
  let text = $state(valueText(value));
  // svelte-ignore state_referenced_locally
  let textTo = $state(valueText(valueTo));
  let error = $state<string | null>(null);
  // svelte-ignore state_referenced_locally
  let last = JSON.stringify([value, valueTo]);

  $effect(() => {
    const s = JSON.stringify([value, valueTo]);
    if (s !== last) {
      last = s;
      text = valueText(value);
      textTo = valueText(valueTo);
      error = null;
    }
  });

  function emit(): void {
    const r = parseValue(op, field?.type, text, textTo);
    error = r.error ?? null;
    if (!r.error || r.value !== undefined) {
      last = JSON.stringify([r.value, r.valueTo]);
      onchange(r.value, r.valueTo);
    }
  }

  function toggleEnum(v: unknown, on: boolean): void {
    const cur = Array.isArray(value) ? value : [];
    const next = on ? [...cur.filter((x) => x !== v), v] : cur.filter((x) => x !== v);
    last = JSON.stringify([next, undefined]);
    onchange(next, undefined);
  }

  let placeholder = $derived(kind === 'list' ? 'a, b, c' : kind === 'regex' ? '^[A-Z]+$' : field?.type === 'integer' || field?.type === 'number' || info?.valueType === 'number' ? '0' : 'value');
</script>

{#if kind === 'none'}
  <span class="hint" style="padding-top:8px">no value</span>
{:else if useEnum && kind === 'scalar'}
  <select id={uid} class="cb-val" {disabled} value={JSON.stringify(value ?? null)} onchange={(e) => onchange(JSON.parse(e.currentTarget.value), undefined)} aria-label="Value">
    <option value="null" disabled>choose…</option>
    {#each enumVals as v (JSON.stringify(v))}<option value={JSON.stringify(v)}>{String(v)}</option>{/each}
  </select>
{:else if useEnum && kind === 'list'}
  <fieldset class="cb-val row-sm" {disabled} aria-label="Values">
    {#each enumVals as v (JSON.stringify(v))}
      <label class="check sm"><input type="checkbox" checked={Array.isArray(value) && value.includes(v)} onchange={(e) => toggleEnum(v, e.currentTarget.checked)} />{String(v)}</label>
    {/each}
  </fieldset>
{:else if isBool}
  <select id={uid} class="cb-val" {disabled} value={String(value)} onchange={(e) => onchange(e.currentTarget.value === 'true', undefined)} aria-label="Value">
    <option value="true">true</option>
    <option value="false">false</option>
  </select>
{:else if kind === 'range'}
  <div class="cb-val row-sm">
    <input id={uid} type="text" inputmode="decimal" class="code" style="flex:1;min-width:80px" bind:value={text} oninput={emit} {disabled} aria-label="From (inclusive)" placeholder="from" aria-invalid={error ? 'true' : undefined} />
    <span class="hint">to</span>
    <input type="text" inputmode="decimal" class="code" style="flex:1;min-width:80px" bind:value={textTo} oninput={emit} {disabled} aria-label="To (inclusive)" placeholder="to" aria-invalid={error ? 'true' : undefined} />
  </div>
{:else}
  <input id={uid} type="text" class="cb-val code" bind:value={text} oninput={emit} {disabled} {placeholder} aria-label="Value" aria-invalid={error ? 'true' : undefined} inputmode={info?.valueType === 'number' || field?.type === 'integer' || field?.type === 'number' ? 'decimal' : undefined} />
{/if}
{#if error}<span class="error-text" style="flex-basis:100%">{error}</span>{/if}
