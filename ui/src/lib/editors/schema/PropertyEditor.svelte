<script lang="ts">
  // Edits the keywords of one schema node: name, type, nullable, required, enum, bounds, format, PII…
  import {
    FORMATS,
    PII,
    STYPES,
    getAt,
    isNullable,
    isObj,
    isPii,
    primaryType,
    renameProperty,
    setAt,
    setRequired,
    withKeyword,
    withNullable,
    withType,
    type JsonSchema,
    type SchemaPath,
    type SType,
  } from '$lib/model/schemaTree';

  let { root = $bindable(), path = $bindable(), disabled = false }: { root: JsonSchema; path: SchemaPath; disabled?: boolean } = $props();

  let node = $derived(getAt(root, path) ?? {});
  let t = $derived(primaryType(node));
  let isProp = $derived(path.length >= 2 && path[path.length - 2] === 'properties');
  let propName = $derived(isProp ? path[path.length - 1] : null);
  let parentPath = $derived(isProp ? path.slice(0, -2) : null);
  let parent = $derived(parentPath ? getAt(root, parentPath) : undefined);
  let isRequired = $derived(!!propName && Array.isArray(parent?.required) && (parent!.required as string[]).includes(propName));
  let title = $derived(path.length === 0 ? 'Root' : propName ?? (path[path.length - 1] === 'items' ? 'Array items' : path.join('.')));

  let nameDraft = $state('');
  let nameError = $state<string | null>(null);
  $effect(() => {
    nameDraft = propName ?? '';
    nameError = null;
  });

  function update(fn: (s: JsonSchema) => JsonSchema): void {
    root = setAt(root, path, fn(getAt(root, path) ?? {}));
  }
  function kw(key: string, value: unknown): void {
    update((s) => withKeyword(s, key, value));
  }
  function numv(k: string): number | string {
    const v = node[k];
    return typeof v === 'number' ? v : '';
  }
  function strv(k: string): string {
    const v = node[k];
    return typeof v === 'string' ? v : '';
  }
  function num(v: string): number | undefined {
    return v.trim() === '' || isNaN(Number(v)) ? undefined : Number(v);
  }

  function commitName(): void {
    if (!propName || !parentPath || nameDraft === propName) return;
    try {
      root = renameProperty(root, parentPath, propName, nameDraft.trim());
      path = [...parentPath, 'properties', nameDraft.trim()];
      nameError = null;
    } catch (e) {
      nameError = (e as Error).message;
    }
  }

  function setTypeTo(v: string): void {
    update((s) => withType(s, v as Exclude<SType, 'null'>));
  }

  let enumText = $derived(Array.isArray(node.enum) ? (node.enum as unknown[]).filter((x) => x !== null).map((x) => (typeof x === 'string' ? x : JSON.stringify(x))).join('\n') : '');
  function setEnum(text: string): void {
    const lines = text.split('\n').map((l) => l.trim()).filter(Boolean);
    const vals: unknown[] = lines.map((l) => (t === 'integer' || t === 'number' ? (isNaN(Number(l)) ? l : Number(l)) : l));
    update((s) => {
      let next = withKeyword(s, 'enum', vals.length ? (isNullable(s) ? [...vals, null] : vals) : undefined);
      if (vals.length && next.type === undefined) next = withKeyword(next, 'type', isNullable(s) ? [t === 'any' ? 'string' : t, 'null'] : t === 'any' ? 'string' : t);
      return next;
    });
  }

  let defaultText = $derived(node.default === undefined ? '' : JSON.stringify(node.default));
  let defaultError = $state<string | null>(null);
  function setDefault(text: string): void {
    if (text.trim() === '') {
      defaultError = null;
      kw('default', undefined);
      return;
    }
    try {
      kw('default', JSON.parse(text));
      defaultError = null;
    } catch {
      defaultError = 'Enter JSON: "text", 42, true…';
    }
  }
</script>

<div class="stack">
  <header class="row"><h2>{title}</h2><span class="hint mono">/{path.join('/')}</span></header>
  <fieldset {disabled} class="prop-editor">
    {#if isProp}
      <div class="field">
        <label for="pe-name">Name</label>
        <input id="pe-name" type="text" class="code" bind:value={nameDraft} onblur={commitName} onkeydown={(e) => { if (e.key === 'Enter') commitName(); }} aria-invalid={nameError ? 'true' : undefined} />
        {#if nameError}<span class="error-text">{nameError}</span>{/if}
      </div>
    {/if}
    <div class="field">
      <label for="pe-type">Type</label>
      <select id="pe-type" value={t === 'any' || t === 'null' ? '' : t} onchange={(e) => setTypeTo(e.currentTarget.value)}>
        {#if t === 'any' || t === 'null'}<option value="">any</option>{/if}
        {#each STYPES as st (st)}<option value={st}>{st}</option>{/each}
      </select>
    </div>
    <label class="check"><input type="checkbox" checked={isNullable(node)} onchange={(e) => update((s) => withNullable(s, e.currentTarget.checked))} /> Nullable</label>
    {#if isProp && parentPath && propName}
      <label class="check"><input type="checkbox" checked={isRequired} onchange={(e) => (root = setRequired(root, parentPath!, propName!, e.currentTarget.checked))} /> Required</label>
    {/if}
    <label class="check" title="Personal data: stored as a hash in logs, never sent to AI prompts"><input type="checkbox" checked={isPii(node)} onchange={(e) => kw(PII, e.currentTarget.checked ? true : undefined)} /> PII</label>

    <div class="field wide">
      <label for="pe-desc">Description</label>
      <input id="pe-desc" type="text" value={strv('description')} onchange={(e) => kw('description', e.currentTarget.value || undefined)} />
    </div>

    {#if t === 'string' || t === 'integer' || t === 'number' || t === 'any'}
      <div class="field wide">
        <label for="pe-enum">Allowed values (one per line, empty for any)</label>
        <textarea id="pe-enum" class="code" rows="3" value={enumText} onchange={(e) => setEnum(e.currentTarget.value)}></textarea>
      </div>
    {/if}

    {#if t === 'integer' || t === 'number'}
      <div class="field"><label for="pe-min">Minimum</label><input id="pe-min" type="number" value={numv('minimum')} onchange={(e) => kw('minimum', num(e.currentTarget.value))} /></div>
      <div class="field"><label for="pe-max">Maximum</label><input id="pe-max" type="number" value={numv('maximum')} onchange={(e) => kw('maximum', num(e.currentTarget.value))} /></div>
      <div class="field"><label for="pe-xmin">Exclusive min</label><input id="pe-xmin" type="number" value={numv('exclusiveMinimum')} onchange={(e) => kw('exclusiveMinimum', num(e.currentTarget.value))} /></div>
      <div class="field"><label for="pe-xmax">Exclusive max</label><input id="pe-xmax" type="number" value={numv('exclusiveMaximum')} onchange={(e) => kw('exclusiveMaximum', num(e.currentTarget.value))} /></div>
    {/if}
    {#if t === 'string'}
      <div class="field"><label for="pe-minl">Min length</label><input id="pe-minl" type="number" min="0" value={numv('minLength')} onchange={(e) => kw('minLength', num(e.currentTarget.value))} /></div>
      <div class="field"><label for="pe-maxl">Max length</label><input id="pe-maxl" type="number" min="0" value={numv('maxLength')} onchange={(e) => kw('maxLength', num(e.currentTarget.value))} /></div>
      <div class="field">
        <label for="pe-format">Format</label>
        <select id="pe-format" value={strv('format')} onchange={(e) => kw('format', e.currentTarget.value || undefined)}>
          <option value="">none</option>
          {#each FORMATS as f (f)}<option value={f}>{f}</option>{/each}
        </select>
      </div>
      <div class="field wide"><label for="pe-pattern">Pattern (RE2)</label><input id="pe-pattern" type="text" class="code" value={strv('pattern')} onchange={(e) => kw('pattern', e.currentTarget.value || undefined)} placeholder="^[A-Z]-\d+$" /></div>
    {/if}
    {#if t === 'array'}
      <div class="field"><label for="pe-mini">Min items</label><input id="pe-mini" type="number" min="0" value={numv('minItems')} onchange={(e) => kw('minItems', num(e.currentTarget.value))} /></div>
      <div class="field">
        <label for="pe-maxi">Max items</label>
        <input id="pe-maxi" type="number" min="0" value={numv('maxItems')} onchange={(e) => kw('maxItems', num(e.currentTarget.value))} />
        {#if node.maxItems === undefined}<span class="hint warn">Needed when a rule or transform loops over this array.</span>{/if}
      </div>
      <p class="hint wide">Select <span class="mono">[ ] items</span> in the tree to edit the item type.</p>
    {/if}
    {#if t === 'object'}
      <label class="check wide"><input type="checkbox" checked={node.additionalProperties === false} onchange={(e) => kw('additionalProperties', e.currentTarget.checked ? false : undefined)} /> Reject properties not listed here</label>
    {/if}
    {#if t !== 'object' && t !== 'array'}
      <div class="field wide">
        <label for="pe-default">Default (JSON)</label>
        <input id="pe-default" type="text" class="code" value={defaultText} onchange={(e) => setDefault(e.currentTarget.value)} aria-invalid={defaultError ? 'true' : undefined} />
        {#if defaultError}<span class="error-text">{defaultError}</span>{/if}
      </div>
    {/if}
  </fieldset>
  {#if isObj(node) && strv('$ref')}<p class="hint">This node is a reference ({strv('$ref')}); edit it in the JSON tab.</p>{/if}
</div>
