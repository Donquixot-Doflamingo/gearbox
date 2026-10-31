<script lang="ts">
  import type { DecisionSpec } from '$lib/api/types.gen';
  import ExprInput from '$lib/components/ExprInput.svelte';
  import Kind from '$lib/components/Kind.svelte';
  import type { DraftEditor } from '$lib/draft.svelte';
  import { END, NODE_ID, NODE_LABELS, renameNode } from '$lib/model/graph';
  import AiNodeEditor from './AiNodeEditor.svelte';
  import RefNodeEditor from './RefNodeEditor.svelte';
  import RulesEditor from './RulesEditor.svelte';
  import SwitchEditor from './SwitchEditor.svelte';
  import TableEditor from './TableEditor.svelte';
  import TransformEditor from './TransformEditor.svelte';
  import type { ProblemSource } from './types';

  let {
    app,
    decision,
    spec = $bindable(),
    id,
    ed,
    problems,
    disabled = false,
    onrenamed,
    ondelete,
  }: { app: string; decision: string; spec: DecisionSpec; id: string; ed: DraftEditor<DecisionSpec>; problems: ProblemSource; disabled?: boolean; onrenamed: (to: string) => void; ondelete: (id: string) => void } = $props();

  let index = $derived(spec.nodes.findIndex((n) => n.id === id));
  let node = $derived(spec.nodes[index]);
  let others = $derived(spec.nodes.filter((n) => n.id !== id).map((n) => n.id));
  let base = $derived(`/nodes/${index}`);

  let idDraft = $state('');
  let idError: string | null = $state(null);
  $effect(() => {
    idDraft = id;
    idError = null;
  });

  function commitId(): void {
    const to = idDraft.trim();
    if (to === id) return;
    if (!NODE_ID.test(to)) {
      idError = 'Start with a lowercase letter; use a–z, 0–9 and _.';
      return;
    }
    try {
      renameNode(spec, id, to);
      idError = null;
      onrenamed(to);
    } catch (e) {
      idError = (e as Error).message;
    }
  }
  const err = (p: string) => problems.problemsUnder(`${base}/${p}`)[0]?.message;
</script>

{#if node}
  <section class="panel" id="inspector" tabindex="-1" aria-label="Node {node.id}">
    <header>
      <h2><span class="mono">{node.id}</span></h2>
      <div class="row-sm">
        <Kind type={node.type} label="{node.type} · {NODE_LABELS[node.type].hint}" />
        {#if !disabled}<button type="button" class="btn btn-xs" onclick={() => ondelete(node.id)}>Delete node</button>{/if}
      </div>
    </header>
    <fieldset {disabled} class="form-grid">
      <div class="field">
        <label for="ni-id">ID</label>
        <input id="ni-id" type="text" class="code" bind:value={idDraft} onblur={commitId} onkeydown={(e) => { if (e.key === 'Enter') commitId(); }} aria-invalid={idError ? 'true' : undefined} />
        {#if idError}<span class="error-text">{idError}</span>{:else}<span class="hint">Expressions that read nodes.{id} are not renamed.</span>{/if}
      </div>
      {#if node.type !== 'switch'}
        <div class="field">
          <label for="ni-next">Next</label>
          <select id="ni-next" value={node.next ?? ''} onchange={(e) => { const v = e.currentTarget.value; if (v) node.next = v; else delete node.next; }} aria-invalid={err('next') ? 'true' : undefined}>
            <option value="">— none: the decision ends here —</option>
            <option value={END}>end (return this node's output)</option>
            {#each others as o (o)}<option value={o}>{o}</option>{/each}
          </select>
          {#if err('next')}<span class="error-text">{err('next')}</span>{/if}
        </div>
      {/if}
      <div class="field">
        <label for="ni-err">On error{node.type === 'ai' ? ' *' : ''}</label>
        <select id="ni-err" value={node.onError ?? ''} onchange={(e) => { const v = e.currentTarget.value; if (v) node.onError = v; else if (node.type === 'ai') node.onError = ''; else delete node.onError; }} aria-invalid={err('onError') ? 'true' : undefined}>
          <option value="">{node.type === 'ai' ? '— choose (required) —' : '— none: return the fallback —'}</option>
          {#each others as o (o)}<option value={o}>{o}</option>{/each}
        </select>
        {#if err('onError')}<span class="error-text">{err('onError')}</span>{/if}
      </div>
      <label class="check" style="align-self:end;padding-bottom:8px"><input type="checkbox" checked={spec.entry === node.id} onchange={(e) => { if (e.currentTarget.checked) spec.entry = node.id; }} /> Entry node</label>
      <div class="field" style="grid-column:1/-1">
        <label for="ni-desc">Description</label>
        <input id="ni-desc" type="text" value={node.description ?? ''} oninput={(e) => (node.description = e.currentTarget.value || undefined)} />
      </div>
      <div style="grid-column:1/-1">
        <ExprInput value={node.from ?? ''} label="Input from (optional)" placeholder="default: the output of the node that ran before" onchange={(v) => (node.from = v || undefined)} error={err('from')} />
      </div>
    </fieldset>
  </section>

  {#if node.type === 'rules'}
    <RulesEditor {app} bind:node={spec.nodes[index]} {index} {problems} {disabled} {decision} />
  {:else if node.type === 'table'}
    <TableEditor {app} bind:node={spec.nodes[index]} {index} {problems} {disabled} />
  {:else if node.type === 'transform' || node.type === 'output'}
    <TransformEditor {app} bind:node={spec.nodes[index]} {index} {problems} {ed} {disabled} />
  {:else if node.type === 'switch'}
    <SwitchEditor bind:node={spec.nodes[index]} {spec} {index} {problems} />
  {:else if node.type === 'ai'}
    <AiNodeEditor bind:node={spec.nodes[index]} {index} {problems} {decision} />
  {:else}
    <RefNodeEditor {app} bind:node={spec.nodes[index]} {index} {problems} {decision} />
  {/if}
{/if}
