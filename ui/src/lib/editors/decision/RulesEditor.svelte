<script lang="ts">
  import type { Node, Rule } from '$lib/api/types.gen';
  import Problems from '$lib/components/Problems.svelte';
  import ConditionBuilder from '$lib/editors/condition/ConditionBuilder.svelte';
  import DataEditor from '$lib/editors/data/DataEditor.svelte';
  import { describe, newGroup, newLeaf } from '$lib/model/condition';
  import { exampleFor, listFields } from '$lib/model/schemaTree';
  import { registry } from '$lib/stores/registry.svelte';
  import SchemaGate from './SchemaGate.svelte';
  import type { ProblemSource, RulesDraft } from './types';

  let { app, node = $bindable(), index, problems, disabled = false, decision }: { app: string; node: Node; index: number; problems: ProblemSource; disabled?: boolean; decision: string } = $props();

  let r = $derived(node as unknown as RulesDraft);
  let base = $derived(`/nodes/${index}`);
  let inSchema = $derived(registry.schema(app, r.inputSchema?.name, r.inputSchema?.version));
  let outSchema = $derived(registry.schema(app, r.outputSchema?.name, r.outputSchema?.version));
  let fields = $derived(inSchema ? listFields(inSchema) : null);
  let selectedId: string | null = $state(null);
  let selIndex = $derived(Math.max(0, (r.rules ?? []).findIndex((x) => x.id === selectedId)));
  let rule = $derived((r.rules ?? [])[selIndex] as Rule | undefined);

  $effect(() => {
    if (!r.rules) r.rules = [];
  });

  function nextId(): string {
    const ids = new Set((r.rules ?? []).map((x) => x.id));
    let i = (r.rules ?? []).length + 1;
    while (ids.has(`r${i}`)) i++;
    return `r${i}`;
  }

  function addRule(): void {
    const then = outSchema ? exampleFor(outSchema, outSchema, 0, true) : {};
    const nr: Rule = { id: nextId(), name: '', when: newGroup('all', [newLeaf('field')]), then };
    r.rules = [...(r.rules ?? []), nr];
    selectedId = nr.id;
  }
  function duplicate(i: number): void {
    const copy = JSON.parse(JSON.stringify($state.snapshot(r.rules[i]))) as Rule;
    copy.id = nextId();
    copy.name = `${copy.name ?? ''} (copy)`.trim();
    r.rules.splice(i + 1, 0, copy);
    selectedId = copy.id;
  }
  function removeRule(i: number): void {
    r.rules.splice(i, 1);
    selectedId = r.rules[Math.min(i, r.rules.length - 1)]?.id ?? null;
  }
  function move(i: number, d: number): void {
    const j = i + d;
    if (j < 0 || j >= r.rules.length) return;
    const [x] = r.rules.splice(i, 1);
    r.rules.splice(j, 0, x);
  }
  function rename(i: number, id: string): void {
    r.rules[i].id = id;
    selectedId = id;
  }
  function errCount(i: number): number {
    return problems.problemsUnder(`${base}/rules/${i}`).filter((p) => p.severity === 'error').length;
  }
  let ruleProblems = $derived(problems.problemsUnder(`${base}/rules/${selIndex}`));
  let thenErrors = $derived(problems.problemsUnder(`${base}/rules/${selIndex}/then`).map((p) => ({ path: p.path.replace(`${base}/rules/${selIndex}/then`, ''), message: p.message })));
  let ruleIdOk = $derived(!rule || /^[a-zA-Z0-9_]{1,40}$/.test(rule.id));
</script>

<SchemaGate {app} bind:input={r.inputSchema} bind:output={r.outputSchema} pointer={base} {problems} {disabled} what="rules">
  <div class="grid-side">
    <section class="panel tight">
      <header>
        <h2>Rules</h2>
        <label class="row-sm hint" for="{base}-mode">Mode
          <select id="{base}-mode" bind:value={r.mode} style="width:auto">
            <option value="first_match">first match</option>
            <option value="all_matches">all matches</option>
          </select>
        </label>
      </header>
      <ol class="rule-list">
        {#each r.rules ?? [] as x, i (i)}
          <li class:disabled={x.enabled === false}>
            <button type="button" class="pick" aria-current={i === selIndex} onclick={() => (selectedId = x.id)}>
              <span class="rid">{x.id}{#if errCount(i)}<span class="err-dot" title="{errCount(i)} problems"> !</span>{/if}</span>
              <span class="rname">{x.name || 'Untitled rule'}{x.rollout ? ` · ${x.rollout.percent}%` : ''}</span>
              <span class="rsum" title={describe(x.when)}>{describe(x.when)}</span>
            </button>
            <div class="reorder">
              <button type="button" class="btn btn-xs btn-ghost" onclick={() => move(i, -1)} disabled={i === 0} aria-label="Move {x.id} up">↑</button>
              <button type="button" class="btn btn-xs btn-ghost" onclick={() => move(i, 1)} disabled={i === r.rules.length - 1} aria-label="Move {x.id} down">↓</button>
            </div>
          </li>
        {/each}
      </ol>
      <button type="button" class="btn btn-sm" onclick={addRule}>Add rule</button>
      <p class="hint">{r.mode === 'all_matches' ? 'Every matching rule contributes; the output is {"matches": [...]}.' : 'Rules run top to bottom (higher priority first); the first match wins.'}</p>
      <hr class="sep" />
      <DataEditor bind:value={r.default} schema={outSchema} label="Default (no rule matched)" errors={problems.problemsUnder(`${base}/default`).map((p) => ({ path: p.path.replace(`${base}/default`, ''), message: p.message }))} />
      <Problems problems={problems.problemsUnder(`${base}/default`).filter((p) => p.path === `${base}/default`)} />
    </section>

    {#if rule}
      <section class="panel" aria-label="Rule {rule.id}">
        <header>
          <h2><span class="mono">{rule.id}</span> · {rule.name || 'Untitled rule'}</h2>
          <div class="row-sm">
            <button type="button" class="btn btn-xs" onclick={() => duplicate(selIndex)}>Duplicate</button>
            <button type="button" class="btn btn-xs" onclick={() => removeRule(selIndex)}>Delete</button>
          </div>
        </header>
        <div class="form-grid">
          <div class="field">
            <label for="{base}-rid">ID</label>
            <input id="{base}-rid" type="text" class="code" value={rule.id} onchange={(e) => rename(selIndex, e.currentTarget.value.trim())} aria-invalid={ruleIdOk ? undefined : 'true'} />
            {#if !ruleIdOk}<span class="error-text">Letters, digits and _ (up to 40).</span>{/if}
          </div>
          <div class="field"><label for="{base}-rname">Name</label><input id="{base}-rname" type="text" bind:value={r.rules[selIndex].name} /></div>
          <div class="field"><label for="{base}-rprio">Priority</label><input id="{base}-rprio" type="number" step="1" value={rule.priority ?? 0} onchange={(e) => (r.rules[selIndex].priority = Number(e.currentTarget.value) || undefined)} /></div>
          <label class="check" style="align-self:end;padding-bottom:8px"><input type="checkbox" checked={rule.enabled !== false} onchange={(e) => (r.rules[selIndex].enabled = e.currentTarget.checked ? undefined : false)} /> Enabled</label>
        </div>
        <div class="field"><label for="{base}-rdesc">Description</label><input id="{base}-rdesc" type="text" bind:value={r.rules[selIndex].description} /></div>

        <div class="stack-sm">
          <label class="check"><input type="checkbox" checked={!!rule.rollout} onchange={(e) => (r.rules[selIndex].rollout = e.currentTarget.checked ? { percent: 10 } : undefined)} /> Limit to a share of units (rollout)</label>
          {#if rule.rollout}
            <div class="form-row">
              <div class="field" style="width:120px"><label for="{base}-rpct">Percent</label><input id="{base}-rpct" type="number" min="0" max="100" step="0.01" value={rule.rollout.percent} oninput={(e) => (r.rules[selIndex].rollout = { ...rule!.rollout, percent: Math.min(100, Math.max(0, Number(e.currentTarget.value) || 0)) })} /></div>
              <div class="field grow"><label for="{base}-rsalt">Salt</label><input id="{base}-rsalt" type="text" class="code" placeholder="{decision}.{node.id}.{rule.id}" value={rule.rollout.salt ?? ''} onchange={(e) => (r.rules[selIndex].rollout = { percent: rule!.rollout!.percent, salt: e.currentTarget.value || undefined })} /></div>
            </div>
            <p class="hint">Units with <code class="inl">bucket(salt) &lt; {Math.round((rule.rollout.percent ?? 0) * 100)}</code> match; buckets run 0–9999 (murmur3 of the unit), so a unit always lands in the same bucket. Calls without a unit never match.</p>
          {/if}
        </div>

        <div class="stack-sm">
          <h3 class="sub-h">When</h3>
          <ConditionBuilder bind:value={r.rules[selIndex].when} {app} {fields} subject={r.inputSchema ? `${r.inputSchema.name}@${r.inputSchema.version}` : 'the input'} pointer="{base}/rules/{selIndex}/when" problems={ruleProblems} />
        </div>
        <DataEditor bind:value={r.rules[selIndex].then} schema={outSchema} label="Then return" errors={thenErrors} />
        <Problems problems={ruleProblems.filter((p) => !p.path.includes('/when') && !p.path.includes('/then/'))} />
      </section>
    {:else}
      <section class="panel">
        <h2>No rules yet</h2>
        <p class="hint">Add a rule: a condition over {r.inputSchema?.name ?? 'the input'} and the value to return from {r.outputSchema?.name ?? 'the output schema'}.</p>
        <div><button type="button" class="btn btn-primary" onclick={addRule}>Add the first rule</button></div>
      </section>
    {/if}
  </div>
</SchemaGate>
