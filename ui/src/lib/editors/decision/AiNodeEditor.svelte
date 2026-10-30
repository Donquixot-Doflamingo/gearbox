<script lang="ts">
  import type { AINode, Node } from '$lib/api/types.gen';
  import ExprInput from '$lib/components/ExprInput.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import SchemaTreeEditor from '$lib/editors/schema/SchemaTreeEditor.svelte';
  import { appHref } from '$lib/nav.svelte';
  import type { ProblemSource } from './types';

  let { node = $bindable(), index, problems, decision }: { node: Node; index: number; problems: ProblemSource; decision: string } = $props();
  let ai = $derived(node as AINode);
  let base = $derived(`/nodes/${index}`);
  let userEl: HTMLTextAreaElement | undefined = $state();
  let newVar = $state('');

  $effect(() => {
    if (!ai.vars) ai.vars = {};
    if (!ai.prompt) ai.prompt = { user: '' };
    if (!ai.responseSchema) ai.responseSchema = { type: 'object', properties: {} };
  });

  let varNames = $derived(Object.keys(ai.vars ?? {}));
  let used = $derived(new Set([...(`${ai.prompt?.system ?? ''}\n${ai.prompt?.user ?? ''}`).matchAll(/\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g)].map((m) => m[1])));
  let unknown = $derived([...used].filter((u) => !varNames.includes(u)));

  function insertVar(v: string): void {
    const el = userEl;
    const text = ai.prompt.user ?? '';
    const token = `{{${v}}}`;
    if (!el) {
      ai.prompt.user = text + token;
      return;
    }
    const a = el.selectionStart ?? text.length, b = el.selectionEnd ?? text.length;
    ai.prompt.user = text.slice(0, a) + token + text.slice(b);
    queueMicrotask(() => {
      el.focus();
      el.setSelectionRange(a + token.length, a + token.length);
    });
  }
  function addVar(): void {
    const n = newVar.trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(n) || n in ai.vars!) return;
    ai.vars = { ...ai.vars, [n]: '' };
    newVar = '';
  }
  function removeVar(k: string): void {
    const v = { ...ai.vars };
    delete v[k];
    ai.vars = v;
  }
  const MODES = {
    shadow: 'Calls the model but never waits for it; the onError path decides. Use it to measure agreement first.',
    inline: 'Waits up to the timeout; the answer is the node output. Callers wait for it.',
    precompute: 'Calls ahead of time and caches; decisions read the cached answer.',
  } as const;
</script>

<section class="panel">
  <header><h2>AI call</h2><a class="hint" href={appHref('/ai', { decision, node: node.id })}>Shadow report</a></header>
  <div class="form-grid">
    <div class="field">
      <label for="{base}-model">Model alias</label>
      <input id="{base}-model" type="text" class="code" bind:value={ai.model} list="{base}-models" />
      <datalist id="{base}-models"><option value="reasoning-default"></option><option value="fast-default"></option></datalist>
    </div>
    <div class="field">
      <label for="{base}-aimode">Mode</label>
      <select id="{base}-aimode" bind:value={ai.mode}>
        <option value="shadow">shadow</option>
        <option value="inline">inline</option>
        <option value="precompute">precompute</option>
      </select>
    </div>
    <div class="field"><label for="{base}-timeout">Timeout (ms)</label><input id="{base}-timeout" type="number" min="50" max="10000" step="50" bind:value={ai.timeoutMs} aria-invalid={problems.problemsUnder(`${base}/timeoutMs`).length ? 'true' : undefined} /></div>
    <div class="field"><label for="{base}-tokens">Max output tokens</label><input id="{base}-tokens" type="number" min="1" value={ai.maxOutputTokens ?? 300} onchange={(e) => (ai.maxOutputTokens = Number(e.currentTarget.value) || undefined)} /></div>
  </div>
  <p class="hint">{MODES[ai.mode] ?? ''} Timeouts, Hub errors and answers that do not match the response schema take onError (required).</p>

  <div class="stack-sm">
    <h3 class="sub-h">Vars: the only values the prompt can use</h3>
    {#each varNames as k (k)}
      <div class="row-sm" style="flex-wrap:nowrap">
        <span class="mono" style="min-width:90px">{k}</span>
        <ExprInput value={ai.vars?.[k] ?? ''} onchange={(v) => (ai.vars = { ...ai.vars, [k]: v })} placeholder="nodes.normalize.inspectionAvg" error={problems.problemsUnder(`${base}/vars/${k}`)[0]?.message} />
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => removeVar(k)} aria-label="Remove var {k}">✕</button>
      </div>
    {/each}
    <div class="row-sm">
      <label class="sr" for="{base}-newvar">New var name</label>
      <input id="{base}-newvar" type="text" class="code" bind:value={newVar} placeholder="var_name" onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addVar(); } }} />
      <button type="button" class="btn btn-sm" onclick={addVar}>Add var</button>
    </div>
    <p class="hint">A var that reads a field marked PII fails validation: personal data never reaches a prompt.</p>
  </div>

  <div class="field">
    <label for="{base}-system">System prompt</label>
    <textarea id="{base}-system" rows="2" bind:value={ai.prompt.system}></textarea>
  </div>
  <div class="field">
    <label for="{base}-user">User prompt</label>
    <div class="prompt-chips" aria-label="Insert a var">
      {#each varNames as v (v)}<button type="button" class="prompt-chip" class:unused={!used.has(v)} onclick={() => insertVar(v)} title={used.has(v) ? 'Insert again' : 'Not used yet: insert'}>{`{{${v}}}`}</button>{/each}
    </div>
    <textarea id="{base}-user" rows="4" bind:this={userEl} bind:value={ai.prompt.user} aria-invalid={unknown.length ? 'true' : undefined}></textarea>
    {#if unknown.length}<span class="error-text">Not vars: {unknown.map((u) => `{{${u}}}`).join(', ')}</span>{/if}
  </div>

  <div class="stack-sm">
    <h3 class="sub-h">Response schema</h3>
    <p class="hint">The answer must be JSON matching this schema (inline, not from the registry).</p>
    <SchemaTreeEditor bind:value={ai.responseSchema} rootLabel="answer" />
  </div>

  <details>
    <summary class="hint">Cache and budget</summary>
    <div class="form-grid" style="margin-top:8px">
      <div class="field"><label for="{base}-ttl">Cache TTL (s)</label><input id="{base}-ttl" type="number" min="0" value={ai.cache?.ttlSeconds ?? ''} onchange={(e) => (ai.cache = { ...ai.cache, ttlSeconds: e.currentTarget.value ? Number(e.currentTarget.value) : undefined })} /></div>
      <div class="field"><label for="{base}-max">Cache entries</label><input id="{base}-max" type="number" min="0" value={ai.cache?.maxEntries ?? ''} onchange={(e) => (ai.cache = { ...ai.cache, maxEntries: e.currentTarget.value ? Number(e.currentTarget.value) : undefined })} /></div>
      <div class="field"><label for="{base}-cpm">Max calls / minute (app + env)</label><input id="{base}-cpm" type="number" min="0" value={ai.budget?.maxCallsPerMinute ?? ''} onchange={(e) => (ai.budget = { maxCallsPerMinute: e.currentTarget.value ? Number(e.currentTarget.value) : undefined })} /></div>
    </div>
  </details>
  <Problems problems={problems.problemsUnder(base).filter((p) => !p.path.includes('/vars/'))} />
</section>
