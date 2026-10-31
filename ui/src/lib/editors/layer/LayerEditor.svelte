<script lang="ts">
  import { untrack } from 'svelte';
  import type { LayerSpec } from '$lib/api/types.gen';
  import BucketBar from '$lib/components/BucketBar.svelte';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import DraftBar from '$lib/components/DraftBar.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import UsagesPanel from '$lib/components/UsagesPanel.svelte';
  import VersionsPanel from '$lib/components/VersionsPanel.svelte';
  import { DraftEditor } from '$lib/draft.svelte';
  import type { BucketRange } from '$lib/model/buckets';
  import { appHref, qp } from '$lib/nav.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import { session } from '$lib/stores/session.svelte';

  let { app, name }: { app: string; name: string } = $props();
  const ed = new DraftEditor<LayerSpec>(app, 'layers', name);
  void ed.load();
  $effect(() => {
    JSON.stringify(ed.spec);
    if (ed.spec) untrack(() => ed.scheduleValidate());
  });

  let tab = $derived(qp('tab') ?? 'allocations');
  let canEdit = $derived(session.can('edit'));
  let l = $derived(ed.spec);
  let experiments = $derived(registry.list(app, 'experiments') ?? []);
  let sum = $derived((l?.allocations ?? []).reduce((s, a) => s + (a.percent || 0), 0));
  let ranges: BucketRange[] = $derived.by(() => {
    let at = 0;
    return (l?.allocations ?? []).map((a) => {
      const n = Math.round((a.percent || 0) * 100);
      const r = { name: a.experiment || '?', kind: 'variant' as const, from: at, to: at + n - 1, share: n / 10000 };
      at += n;
      return r;
    });
  });
  const TABS = [['allocations', 'Allocations'], ['versions', 'Versions'], ['usages', 'Used by']] as const;
</script>

<PageHeader title={name}>
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'layers', href: appHref('/layers') }]} />{/snippet}
  {#snippet sub()}{ed.description || 'Layer'}{/snippet}
</PageHeader>

{#if ed.loadError}
  <ErrorNote error={ed.loadError} retry={() => ed.load()} />
{:else if ed.loading && !ed.resource}
  <Loading />
{:else if l}
  <DraftBar {ed} />
  <nav class="tabs" aria-label="Layer sections">
    {#each TABS as [id, label] (id)}<a href={appHref(`/layers/${name}`, { tab: id === 'allocations' ? undefined : id })} aria-current={tab === id ? 'page' : undefined}>{label}</a>{/each}
  </nav>
  {#if tab === 'allocations'}
    <section class="panel">
      <header><h2>Allocations</h2><span class="hint" class:warn={sum > 100}>{sum}% of 100% allocated</span></header>
      <BucketBar {ranges} label="Layer buckets" />
      <fieldset disabled={!canEdit} class="stack">
        <div class="form-grid">
          <div class="field"><label for="ly-desc">Description</label><input id="ly-desc" type="text" bind:value={ed.description} /></div>
          <div class="field"><label for="ly-salt">Salt</label><input id="ly-salt" type="text" class="code" value={l.salt ?? ''} placeholder={name} oninput={(e) => (l.salt = e.currentTarget.value || undefined)} /></div>
        </div>
        {#each l.allocations as a, i (i)}
          <div class="row-sm">
            <label class="sr" for="ly-x-{i}">Experiment</label>
            <select id="ly-x-{i}" bind:value={a.experiment} style="flex:1 1 200px">
              <option value="">— choose —</option>
              {#each experiments as x (x.name)}<option value={x.name}>{x.name}</option>{/each}
            </select>
            <label class="sr" for="ly-p-{i}">Percent</label>
            <input id="ly-p-{i}" type="number" min="0" max="100" step="0.5" value={a.percent} oninput={(e) => (a.percent = Number(e.currentTarget.value) || 0)} style="width:100px" />
            <span class="hint">%</span>
            <button type="button" class="btn btn-xs btn-ghost" onclick={() => l.allocations.splice(i, 1)} aria-label="Remove allocation">✕</button>
          </div>
        {:else}
          <p class="hint">No allocations yet.</p>
        {/each}
        <div><button type="button" class="btn btn-sm" onclick={() => (l.allocations = [...l.allocations, { experiment: '', percent: Math.max(0, 100 - sum) }])}>Add allocation</button></div>
      </fieldset>
      {#if sum > 100}<p class="error-text">Shares add up to {sum}%; they must be 100% or less.</p>{/if}
      <Problems problems={ed.problems} showPath />
    </section>
  {:else if tab === 'versions'}
    <VersionsPanel {ed} kind="layers" />
  {:else if tab === 'usages'}
    <UsagesPanel {app} kind="layers" {name} />
  {/if}
{/if}
