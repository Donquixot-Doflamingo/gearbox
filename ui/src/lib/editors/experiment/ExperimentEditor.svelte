<script lang="ts">
  import { untrack } from 'svelte';
  import { api } from '$lib/api';
  import type { ExperimentSpec, LayerSpec, VariantResult } from '$lib/api/types.gen';
  import BucketBar from '$lib/components/BucketBar.svelte';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import DraftBar from '$lib/components/DraftBar.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import ResourcePicker from '$lib/components/ResourcePicker.svelte';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import UsagesPanel from '$lib/components/UsagesPanel.svelte';
  import VersionsPanel from '$lib/components/VersionsPanel.svelte';
  import ConditionBuilder from '$lib/editors/condition/ConditionBuilder.svelte';
  import { DraftEditor } from '$lib/draft.svelte';
  import { allocate } from '$lib/model/buckets';
  import { listFields } from '$lib/model/schemaTree';
  import { appHref, currentEnv, qp } from '$lib/nav.svelte';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import { session } from '$lib/stores/session.svelte';

  let { app, name }: { app: string; name: string } = $props();
  // Routes remount this editor per resource ({#key}), so reading the props once is intended.
  // svelte-ignore state_referenced_locally
  const ed = new DraftEditor<ExperimentSpec>(app, 'experiments', name);
  void ed.load();
  $effect(() => {
    JSON.stringify(ed.spec);
    if (ed.spec) untrack(() => ed.scheduleValidate());
  });

  let tab = $derived(qp('tab') ?? 'design');
  let canEdit = $derived(session.can('edit'));
  let x = $derived(ed.spec);
  let ranges = $derived(x ? allocate(x) : []);
  let attrSchema = $derived(registry.schema(app, x?.attributesSchema?.name, x?.attributesSchema?.version));
  let fields = $derived(attrSchema ? listFields(attrSchema) : null);
  let layer = $derived(x?.layer ? registry.resource(app, 'layers', x.layer) : null);
  let layerShare = $derived(((layer?.draft.spec as LayerSpec | undefined)?.allocations ?? []).find((a) => a.experiment === name)?.percent);
  let totalWeight = $derived((x?.variants ?? []).reduce((s, v) => s + (v.weight || 0), 0));

  function addVariant(): void {
    let i = x.variants.length;
    while (x.variants.some((v) => v.name === `variant_${i}`)) i++;
    x.variants = [...x.variants, { name: `variant_${i}`, weight: 0, parameters: {} }];
  }
  function setParams(i: number, text: string): void {
    try {
      x.variants[i].parameters = text.trim() ? JSON.parse(text) : undefined;
      paramErr[i] = '';
    } catch {
      paramErr[i] = 'Not valid JSON';
    }
  }
  let paramErr = $state<Record<number, string>>({});

  async function stop(): Promise<void> {
    const r = await confirmAction({ title: `Stop ${name}?`, body: [`Every unit gets the ${x.stoppedVariant ?? x.variants[0]?.name} parameters once a version with status stopped is released. Logged exposures are kept.`, 'This changes the draft; save, cut a version and release it through a change request.'], ok: 'Mark as stopped', danger: false });
    if (r) x.status = 'stopped';
  }

  // assignment check (runtime API against the live bundle)
  let unit = $state('L-1029384');
  let assign = $state<VariantResult | null>(null);
  let assignErr = $state<unknown>(null);
  async function check(): Promise<void> {
    assignErr = null;
    try {
      assign = await api.assignVariant({ app, env: currentEnv(), name, body: { unitId: unit } });
    } catch (e) {
      assignErr = e;
      assign = null;
    }
  }
  const TABS = [['design', 'Design'], ['versions', 'Versions'], ['usages', 'Used by']] as const;
</script>

<PageHeader title={name}>
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'experiments', href: appHref('/experiments') }]} />{/snippet}
  {#snippet sub()}{ed.description || 'Experiment'}{#if x} · <span class="pill {x.status === 'running' ? 'pill-ok' : x.status === 'stopped' ? 'pill-warn' : 'pill-muted'}">{x.status}</span>{/if}{/snippet}
</PageHeader>

{#if ed.loadError}
  <ErrorNote error={ed.loadError} retry={() => ed.load()} />
{:else if ed.loading && !ed.resource}
  <Loading />
{:else if x}
  <DraftBar {ed}>{#if x.status === 'running'}<button type="button" class="btn" onclick={stop} disabled={!canEdit}>Stop experiment</button>{/if}</DraftBar>
  <nav class="tabs" aria-label="Experiment sections">
    {#each TABS as [id, label] (id)}<a href={appHref(`/experiments/${name}`, { tab: id === 'design' ? undefined : id })} aria-current={tab === id ? 'page' : undefined}>{label}</a>{/each}
  </nav>
  {#if tab === 'design'}
    <section class="panel">
      <header><h2>Buckets</h2><span class="hint">{x.layer ? `layer ${x.layer}${layerShare !== undefined ? ` · ${layerShare}% of layer traffic` : ''}` : 'no layer: all traffic'}</span></header>
      <BucketBar {ranges} />
      <div class="tbl-wrap">
        <table>
          <thead><tr><th>Variant</th><th class="num">Weight</th><th>Buckets</th><th class="num">Share</th><th>Parameters (data)</th><th></th></tr></thead>
          <tbody>
            {#if (x.holdoutPercent ?? 0) > 0}
              {@const h = ranges.find((r) => r.kind === 'holdout')}
              <tr><td><span class="row-sm"><i class="swatch b-hold"></i>holdout</span></td><td class="num mono">—</td><td class="mono">{h ? `${h.from}–${h.to}` : ''}</td><td class="num mono">{x.holdoutPercent}%</td><td class="muted">not in the experiment</td><td></td></tr>
            {/if}
            {#each x.variants as v, i (i)}
              {@const r = ranges.filter((q) => q.kind === 'variant')[i]}
              <tr>
                <td><span class="row-sm" style="flex-wrap:nowrap"><i class="swatch b-{i % 6}"></i><input type="text" class="code" bind:value={v.name} disabled={!canEdit} aria-label="Variant {i + 1} name" style="min-width:110px" /></span></td>
                <td class="num"><input type="number" min="0" step="1" value={v.weight} oninput={(e) => (v.weight = Number(e.currentTarget.value) || 0)} disabled={!canEdit} aria-label="Weight of {v.name}" style="width:80px" /></td>
                <td class="mono nowrap">{r && r.to >= r.from ? `${r.from}–${r.to}` : '—'}</td>
                <td class="num mono">{r ? `${(r.share * 100).toFixed(2).replace(/\.?0+$/, '')}%` : '—'}</td>
                <td><input type="text" class="code" value={v.parameters === undefined ? '' : JSON.stringify(v.parameters)} onchange={(e) => setParams(i, e.currentTarget.value)} disabled={!canEdit} aria-label="Parameters of {v.name}" aria-invalid={paramErr[i] ? 'true' : undefined} style="min-width:180px" />{#if paramErr[i]}<span class="error-text">{paramErr[i]}</span>{/if}</td>
                <td><button type="button" class="btn btn-xs btn-ghost" onclick={() => x.variants.splice(i, 1)} disabled={!canEdit || x.variants.length <= 2} aria-label="Remove {v.name}">✕</button></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <div class="actions"><button type="button" class="btn btn-sm" onclick={addVariant} disabled={!canEdit}>Add variant</button><span class="hint">Weights are relative ({totalWeight} total); they split the buckets after the holdout.</span></div>
    </section>
    <div class="grid-2">
      <section class="panel">
        <header><h2>Setup</h2></header>
        <fieldset disabled={!canEdit} class="stack">
          <div class="field"><label for="ex-desc">Description</label><input id="ex-desc" type="text" bind:value={ed.description} /></div>
          <div class="form-grid">
            <div class="field"><label for="ex-status">Status</label><select id="ex-status" bind:value={x.status}><option value="draft">draft</option><option value="running">running</option><option value="stopped">stopped</option></select></div>
            <div class="field"><label for="ex-hold">Holdout %</label><input id="ex-hold" type="number" min="0" max="50" step="0.5" value={x.holdoutPercent ?? 0} oninput={(e) => (x.holdoutPercent = Number(e.currentTarget.value) || 0)} /></div>
            <div class="field"><label for="ex-salt">Salt</label><input id="ex-salt" type="text" class="code" value={x.salt ?? ''} placeholder={name} oninput={(e) => (x.salt = e.currentTarget.value || undefined)} /></div>
            <div class="field"><label for="ex-stop">After stop, everyone gets</label><select id="ex-stop" value={x.stoppedVariant ?? ''} onchange={(e) => (x.stoppedVariant = e.currentTarget.value || undefined)}><option value="">{x.variants[0]?.name ?? 'first'} (first)</option>{#each x.variants.slice(1) as v (v.name)}<option value={v.name}>{v.name}</option>{/each}</select></div>
          </div>
          <div class="field"><label for="ex-hyp">Hypothesis</label><textarea id="ex-hyp" rows="2" value={x.hypothesis ?? ''} oninput={(e) => (x.hypothesis = e.currentTarget.value || undefined)}></textarea></div>
          <ResourcePicker {app} kind="layers" value={x.layer ?? ''} onchange={(v) => (x.layer = v || undefined)} label="Layer (mutual exclusion)" hint="Experiments in one layer never share a unit." error={ed.problemsUnder('/layer')[0]?.message} />
        </fieldset>
        <p class="hint">Changing the salt or weights of a running experiment moves units between variants.</p>
      </section>
      <section class="panel">
        <header><h2>Targeting</h2><span class="hint">optional; units outside are not in the experiment</span></header>
        <fieldset disabled={!canEdit} class="stack">
          <SchemaRefPicker {app} value={x.attributesSchema} onchange={(v) => (x.attributesSchema = v)} label="Attributes schema (optional)" />
          <ConditionBuilder bind:value={x.targeting} {app} {fields} subject="the attributes" pointer="/targeting" problems={ed.problems} allowEmpty exprBase="attributes" />
        </fieldset>
        <hr class="sep" />
        <h3 class="sub-h">Check an assignment ({currentEnv()}, live bundle)</h3>
        <div class="row-sm">
          <label class="sr" for="ex-unit">Unit</label>
          <input id="ex-unit" type="text" class="code grow" bind:value={unit} onkeydown={(e) => { if (e.key === 'Enter') void check(); }} />
          <button type="button" class="btn" onclick={check}>Assign</button>
        </div>
        {#if assignErr}<ErrorNote error={assignErr} />{:else if assign}<p class="result-line" class:ok={assign.inExperiment} aria-live="polite">{assign.variant ?? 'no variant'} · {assign.reason}{assign.parameters !== undefined && assign.parameters !== null ? ` · ${JSON.stringify(assign.parameters)}` : ''}</p>{/if}
      </section>
    </div>
    <Problems problems={ed.problems.filter((p) => !p.path.includes('/targeting/'))} showPath />
  {:else if tab === 'versions'}
    <VersionsPanel {ed} kind="experiments" />
  {:else if tab === 'usages'}
    <UsagesPanel {app} kind="experiments" {name} />
  {/if}
{/if}
