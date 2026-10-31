<script lang="ts">
  // Builds all/any/not condition trees with field, segment, gate, variant and expression leaves.
  import type { Condition, ValidationProblem } from '$lib/api/types.gen';
  import JsonView from '$lib/components/JsonView.svelte';
  import {
    appendChild,
    checkCondition,
    condKind,
    condPointer,
    isGroup,
    moveChild,
    newGroup,
    newLeaf,
    removeCond,
    replaceCond,
    setGroupKind,
    setLeafKind,
    toExpr,
    unwrapCond,
    wrapCond,
    type CondPath,
  } from '$lib/model/condition';
  import type { FieldInfo } from '$lib/model/schemaTree';
  import ConditionNode from './ConditionNode.svelte';
  import type { CondCtx } from './context';

  let {
    value = $bindable(),
    app,
    fields,
    subject = 'the input',
    pointer = '',
    problems = [],
    disabled = false,
    allowEmpty = false,
    exprBase = 'input',
  }: {
    value: Condition | undefined;
    app: string;
    fields: FieldInfo[] | null;
    subject?: string;
    /** JSON pointer of this condition in the spec, to match server problems. */
    pointer?: string;
    problems?: ValidationProblem[];
    disabled?: boolean;
    allowEmpty?: boolean;
    exprBase?: string;
  } = $props();

  let showExpr = $state(false);
  let local = $derived(value ? checkCondition(value, { fields }) : []);

  function set(next: Condition | null): void {
    value = next ?? (allowEmpty ? undefined : newGroup('all', [newLeaf('field')]));
  }

  const ctx: CondCtx = {
    get app() {
      return app;
    },
    get fields() {
      return fields;
    },
    get disabled() {
      return disabled;
    },
    get subject() {
      return subject;
    },
    replace: (p, next) => value && set(replaceCond(value, p, next)),
    remove: (p) => value && set(removeCond(value, p)),
    append: (p, child) => value && set(appendChild(value, p, child)),
    wrap: (p, k) => value && set(wrapCond(value, p, k)),
    unwrap: (p) => value && set(unwrapCond(value, p)),
    move: (p, a, b) => value && set(moveChild(value, p, a, b)),
    setGroupKind: (p, k) => value && set(setGroupKind(value, p, k)),
    setLeafKind: (p, k) => value && set(setLeafKind(value, p, k)),
    problemsAt: (p: CondPath) => {
      if (!value) return [];
      const ptr = condPointer(value, pointer, p);
      const server = problems.filter((x) => x.path === ptr);
      const mine = local.filter((x) => x.path.join('/') === p.join('/')).map((x) => ({ message: x.message, severity: 'error' as const }));
      // prefer the server's wording when both report something here
      return server.length ? server : mine;
    },
  };

  function start(kind: 'all' | 'any' | 'expr'): void {
    value = kind === 'expr' ? newLeaf('expr') : newGroup(kind, [newLeaf('field')]);
  }
</script>

<div class="cb">
  {#if !value}
    <div class="row-sm">
      <span class="hint">No condition.</span>
      {#if !disabled}
        <button type="button" class="btn btn-sm" onclick={() => start('all')}>Add conditions</button>
        <button type="button" class="btn btn-sm" onclick={() => start('expr')}>Write an expression</button>
      {/if}
    </div>
  {:else}
    {#if !isGroup(value) && !disabled}
      <div class="row-sm">
        <button type="button" class="btn btn-xs" onclick={() => value && set(newGroup('all', [value, newLeaf('field')]))}>+ AND</button>
        <button type="button" class="btn btn-xs" onclick={() => value && set(newGroup('any', [value, newLeaf('field')]))}>+ OR</button>
      </div>
    {/if}
    <ConditionNode cond={value} path={[]} {ctx} />
    <div class="row-sm">
      <button type="button" class="link-btn hint" onclick={() => (showExpr = !showExpr)} aria-expanded={showExpr}>{showExpr ? 'Hide' : 'Show'} compiled expression and JSON</button>
      {#if allowEmpty && !disabled}<button type="button" class="link-btn hint" onclick={() => (value = undefined)}>Remove condition</button>{/if}
      {#if condKind(value) !== 'expr' && !fields && !disabled}<span class="hint">No schema chosen: type field paths by hand.</span>{/if}
    </div>
    {#if showExpr}
      <div class="grid-2">
        <div class="stack-sm"><span class="sub-h">Compiles to (approximately)</span><pre class="code">{toExpr(value, exprBase)}</pre></div>
        <div class="stack-sm"><span class="sub-h">JSON</span><JsonView value={value} /></div>
      </div>
    {/if}
  {/if}
</div>
