<script lang="ts">
  import type { Condition, FieldCondition, FieldOperator, SegmentCondition } from '$lib/api/types.gen';
  import ExprInput from '$lib/components/ExprInput.svelte';
  import { children, condKind, isGroup, LEAF_KINDS, newGroup, newLeaf, operatorInfo, operatorsFor, withOperator, type CondPath, type LeafKind } from '$lib/model/condition';
  import { isObj, type JsonSchema } from '$lib/model/schemaTree';
  import { registry } from '$lib/stores/registry.svelte';
  import ConditionNode from './ConditionNode.svelte';
  import type { CondCtx } from './context';
  import FieldPicker from './FieldPicker.svelte';
  import ValueInput from './ValueInput.svelte';

  let { cond, path, ctx, depth = 0 }: { cond: Condition; path: CondPath; ctx: CondCtx; depth?: number } = $props();

  let kind = $derived(condKind(cond));
  let kids = $derived(children(cond));
  let problems = $derived(ctx.problemsAt(path));
  let uid = $derived(`c-${path.join('-') || 'root'}`);

  function addChild(v: string): void {
    if (!v) return;
    ctx.append(path, v === 'all' || v === 'any' ? newGroup(v, [newLeaf('field')]) : newLeaf(v as LeafKind));
  }

  // field leaf helpers
  let fieldCond = $derived(kind === 'field' ? (cond as FieldCondition) : null);
  let fieldInfo = $derived(fieldCond && ctx.fields ? ctx.fields.find((f) => f.path === fieldCond!.field) : undefined);
  let ops = $derived(operatorsFor(fieldInfo?.type ?? 'any'));

  function setField(p: string): void {
    if (!fieldCond) return;
    const info = ctx.fields?.find((f) => f.path === p);
    const allowed = operatorsFor(info?.type ?? 'any');
    const op: FieldOperator = allowed.some((o) => o.op === fieldCond.op) ? fieldCond.op : (allowed[0]?.op ?? 'EQ');
    ctx.replace(path, { ...withOperator({ ...fieldCond, field: p }, op, info?.type) });
  }

  // segment leaf helpers
  let segCond = $derived(kind === 'segment' ? (cond as SegmentCondition) : null);
  let segments = $derived(registry.list(ctx.app, 'segments'));
  let segSummary = $derived(segments?.find((s) => s.name === segCond?.segment));
  let segType = $derived(segSummary?.subtype);
  let segRes = $derived(segType === 'dynamic' ? registry.resource(ctx.app, 'segments', segCond?.segment) : null);
  let attrRef = $derived.by(() => {
    const spec = segRes?.draft.spec as { dynamic?: { attributesSchema?: { name: string; version: number } } } | undefined;
    return spec?.dynamic?.attributesSchema;
  });
  let attrSchema = $derived(attrRef ? registry.schema(ctx.app, attrRef.name, attrRef.version) : null);
  let attrNames = $derived(attrSchema && isObj(attrSchema.properties) ? Object.keys(attrSchema.properties as Record<string, JsonSchema>) : []);

  function setSegment(name: string): void {
    if (!segCond) return;
    const t = segments?.find((s) => s.name === name)?.subtype;
    const next: SegmentCondition = { segment: name, op: segCond.op };
    if (t === 'static') next.key = segCond.key ?? '';
    if (t === 'rollout' && segCond.key) next.key = segCond.key;
    if (t === 'dynamic') next.attributes = segCond.attributes ?? {};
    ctx.replace(path, next);
  }

  // when the dynamic segment's attributes are known, fill in suggestions for missing ones
  $effect(() => {
    if (!segCond || segType !== 'dynamic' || !attrNames.length || ctx.disabled) return;
    const cur = segCond.attributes ?? {};
    const missing = attrNames.filter((a) => !(a in cur));
    if (!missing.length) return;
    const add = Object.fromEntries(missing.map((a) => [a, ctx.fields?.some((f) => f.path === a) ? `input.${a}` : '']));
    ctx.replace(path, { ...segCond, attributes: { ...cur, ...add } });
  });

  // gate and variant
  let gates = $derived(kind === 'gate' ? registry.list(ctx.app, 'gates') : undefined);
  let experiments = $derived(kind === 'variant' ? registry.list(ctx.app, 'experiments') : undefined);
  let expName = $derived(kind === 'variant' && 'experiment' in cond ? cond.experiment : '');
  let expRes = $derived(expName ? registry.resource(ctx.app, 'experiments', expName) : null);
  let variants = $derived(((expRes?.draft.spec as { variants?: { name: string }[] } | undefined)?.variants ?? []).map((v) => v.name));
</script>

{#if kind === 'all' || kind === 'any'}
  <div class="cb-group depth-{depth % 4}" role="group" aria-label="{kind === 'all' ? 'All' : 'Any'} of these conditions">
    <div class="cb-head">
      <label class="sr" for="{uid}-kind">Group type</label>
      <select id="{uid}-kind" value={kind} disabled={ctx.disabled} onchange={(e) => ctx.setGroupKind(path, e.currentTarget.value as 'all' | 'any')} style="width:auto">
        <option value="all">All of these (AND)</option>
        <option value="any">Any of these (OR)</option>
      </select>
      <span class="spacer"></span>
      {#if !ctx.disabled}
        <label class="sr" for="{uid}-add">Add to group</label>
        <select id="{uid}-add" value="" onchange={(e) => { addChild(e.currentTarget.value); e.currentTarget.value = ''; }} style="width:auto">
          <option value="">+ Add…</option>
          {#each LEAF_KINDS as l (l.kind)}<option value={l.kind}>{l.label} condition</option>{/each}
          <option value="all">Group: all of</option>
          <option value="any">Group: any of</option>
        </select>
        <button type="button" class="btn btn-xs" onclick={() => ctx.wrap(path, 'not')} title="Negate this group">NOT</button>
        {#if path.length}
          {#if kids.length === 1}<button type="button" class="btn btn-xs" onclick={() => ctx.unwrap(path)} title="Replace the group with its only condition">Ungroup</button>{/if}
          <button type="button" class="btn btn-xs" onclick={() => ctx.remove(path)} aria-label="Remove group">✕</button>
        {/if}
      {/if}
    </div>
    {#each kids as child, i (i)}
      {#if i > 0}<span class="cb-join">{kind === 'all' ? 'AND' : 'OR'}</span>{/if}
      <div class="row-sm" style="align-items:stretch;flex-wrap:nowrap">
        {#if !ctx.disabled && kids.length > 1}
          <div class="reorder">
            <button type="button" class="btn btn-xs btn-ghost" disabled={i === 0} onclick={() => ctx.move(path, i, i - 1)} aria-label="Move up">↑</button>
            <button type="button" class="btn btn-xs btn-ghost" disabled={i === kids.length - 1} onclick={() => ctx.move(path, i, i + 1)} aria-label="Move down">↓</button>
          </div>
        {/if}
        <div class="grow"><ConditionNode cond={child} path={[...path, i]} {ctx} depth={depth + 1} /></div>
      </div>
    {:else}
      <p class="hint">Empty group: add a condition.</p>
    {/each}
    {#each problems as p, i (i)}<span class="cb-problem">{p.message}</span>{/each}
  </div>
{:else if kind === 'not'}
  <div class="cb-group is-not depth-{depth % 4}" role="group" aria-label="Not">
    <div class="cb-head">
      <span class="cb-join" style="color:var(--t-transform)">NOT</span>
      <span class="spacer"></span>
      {#if !ctx.disabled}<button type="button" class="btn btn-xs" onclick={() => ctx.unwrap(path)}>Remove NOT</button>{/if}
    </div>
    {#if kids[0]}<ConditionNode cond={kids[0]} path={[...path, 0]} {ctx} depth={depth + 1} />{/if}
  </div>
{:else}
  <div class="cb-leaf" class:has-problem={problems.some((p) => p.severity === 'error')}>
    <div>
      <label class="sr" for="{uid}-lk">Condition type</label>
      <select id="{uid}-lk" value={kind} disabled={ctx.disabled} onchange={(e) => ctx.setLeafKind(path, e.currentTarget.value as LeafKind)} style="width:100%">
        {#each LEAF_KINDS as l (l.kind)}<option value={l.kind} title={l.hint}>{l.label}</option>{/each}
      </select>
    </div>
    <div class="cb-body">
      {#if fieldCond}
        <div class="cb-field"><FieldPicker value={fieldCond.field} fields={ctx.fields} id="{uid}-f" disabled={ctx.disabled} onpick={setField} /></div>
        <div class="cb-op">
          <label class="sr" for="{uid}-op">Operator</label>
          <select id="{uid}-op" value={fieldCond.op} disabled={ctx.disabled} onchange={(e) => ctx.replace(path, withOperator(fieldCond!, e.currentTarget.value as FieldOperator, fieldInfo?.type))} style="width:100%">
            {#each ops as o (o.op)}<option value={o.op}>{o.label}</option>{/each}
            {#if !ops.some((o) => o.op === fieldCond.op)}<option value={fieldCond.op}>{operatorInfo(fieldCond.op)?.label ?? fieldCond.op} (not for {fieldInfo?.type})</option>{/if}
          </select>
        </div>
        <ValueInput op={fieldCond.op} field={fieldInfo} value={fieldCond.value} valueTo={fieldCond.valueTo} disabled={ctx.disabled} id="{uid}-v" onchange={(v, vt) => ctx.replace(path, { ...fieldCond!, value: v, ...(vt !== undefined ? { valueTo: vt } : {}) })} />
      {:else if segCond}
        <div class="cb-field">
          <label class="sr" for="{uid}-seg">Segment</label>
          <select id="{uid}-seg" value={segCond.segment} disabled={ctx.disabled} onchange={(e) => setSegment(e.currentTarget.value)} style="width:100%">
            <option value="">{segments ? 'Choose a segment' : 'Loading…'}</option>
            {#each segments ?? [] as s (s.name)}<option value={s.name}>{s.name} · {s.subtype}</option>{/each}
          </select>
        </div>
        <div class="cb-op">
          <label class="sr" for="{uid}-sop">Membership</label>
          <select id="{uid}-sop" value={segCond.op} disabled={ctx.disabled} onchange={(e) => ctx.replace(path, { ...segCond!, op: e.currentTarget.value as 'IN_SEGMENT' | 'NOT_IN_SEGMENT' })} style="width:100%">
            <option value="IN_SEGMENT">is in</option>
            <option value="NOT_IN_SEGMENT">is not in</option>
          </select>
        </div>
        {#if segType === 'static' || segType === 'rollout'}
          <div class="cb-val">
            <ExprInput value={segCond.key ?? ''} id="{uid}-key" disabled={ctx.disabled} placeholder={segType === 'rollout' ? 'ctx.unit (default)' : 'input.dealer.id'} onchange={(v) => ctx.replace(path, { ...segCond!, key: v || undefined })} />
            <span class="hint">{segType === 'rollout' ? 'Key to bucket (empty: the request unit).' : 'Member key: an expression over the node input.'}</span>
          </div>
        {:else if segType === 'dynamic'}
          <div class="cb-attr">
            {#if attrRef}<span class="hint" style="grid-column:1/-1">Map each attribute of {attrRef.name}@{attrRef.version} to an expression over {ctx.subject}:</span>{/if}
            {#each attrNames as a (a)}
              <label class="mono" for="{uid}-a-{a}">{a}</label>
              <ExprInput value={segCond.attributes?.[a] ?? ''} id="{uid}-a-{a}" disabled={ctx.disabled} placeholder="input.{a}" onchange={(v) => ctx.replace(path, { ...segCond!, attributes: { ...(segCond!.attributes ?? {}), [a]: v } })} />
            {:else}
              <span class="hint" style="grid-column:1/-1">{attrRef ? 'Loading the attributes schema…' : 'Loading the segment…'}</span>
            {/each}
          </div>
        {/if}
      {:else if kind === 'gate' && 'gate' in cond}
        <div class="cb-field">
          <label class="sr" for="{uid}-g">Gate</label>
          <select id="{uid}-g" value={cond.gate} disabled={ctx.disabled} onchange={(e) => ctx.replace(path, { gate: e.currentTarget.value, op: 'gate' in cond ? cond.op : 'GATE_PASSES' })} style="width:100%">
            <option value="">{gates ? 'Choose a gate' : 'Loading…'}</option>
            {#each gates ?? [] as g (g.name)}<option value={g.name}>{g.name}</option>{/each}
          </select>
        </div>
        <div class="cb-op">
          <label class="sr" for="{uid}-gop">Result</label>
          <select id="{uid}-gop" value={cond.op} disabled={ctx.disabled} onchange={(e) => ctx.replace(path, { gate: 'gate' in cond ? cond.gate : '', op: e.currentTarget.value as 'GATE_PASSES' | 'GATE_FAILS' })} style="width:100%">
            <option value="GATE_PASSES">passes</option>
            <option value="GATE_FAILS">fails</option>
          </select>
        </div>
      {:else if kind === 'variant' && 'experiment' in cond}
        <div class="cb-field">
          <label class="sr" for="{uid}-x">Experiment</label>
          <select id="{uid}-x" value={cond.experiment} disabled={ctx.disabled} onchange={(e) => ctx.replace(path, { experiment: e.currentTarget.value, variants: [] })} style="width:100%">
            <option value="">{experiments ? 'Choose an experiment' : 'Loading…'}</option>
            {#each experiments ?? [] as x (x.name)}<option value={x.name}>{x.name}</option>{/each}
          </select>
        </div>
        <fieldset class="cb-val row-sm" disabled={ctx.disabled} aria-label="Variants">
          <span class="hint">variant is one of</span>
          {#each variants as v (v)}
            <label class="check sm"><input type="checkbox" checked={cond.variants.includes(v)} onchange={(e) => { const c = cond as { experiment: string; variants: string[] }; ctx.replace(path, { experiment: c.experiment, variants: e.currentTarget.checked ? [...c.variants, v] : c.variants.filter((x) => x !== v) }); }} />{v}</label>
          {:else}
            {#if expName}<span class="hint">Loading variants…</span>{/if}
          {/each}
        </fieldset>
      {:else if kind === 'expr' && 'expr' in cond}
        <ExprInput value={cond.expr} id="{uid}-e" multiline disabled={ctx.disabled} placeholder={'input.car.km < 60000 && "certified" in input.tags'} onchange={(v) => ctx.replace(path, { expr: v })} />
      {/if}
    </div>
    {#if !ctx.disabled}
      <div class="cb-tools">
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => ctx.wrap(path, 'not')} title="Negate">NOT</button>
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => ctx.wrap(path, isGroup(cond) ? 'all' : 'any')} title="Put in a group">( )</button>
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => ctx.remove(path)} aria-label="Remove condition">✕</button>
      </div>
    {/if}
    {#each problems as p, i (i)}<span class="cb-problem" class:hint={p.severity === 'warning'}>{p.message}</span>{/each}
  </div>
{/if}
