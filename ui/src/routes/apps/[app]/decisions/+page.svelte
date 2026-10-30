<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { DecisionSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import EnvHeads from '$lib/components/EnvHeads.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Field from '$lib/components/Field.svelte';
  import Kind from '$lib/components/Kind.svelte';
  import LiveCells from '$lib/components/LiveCells.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import NewResourceDialog from '$lib/components/NewResourceDialog.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import type { SchemaRef } from '$lib/api/types.gen';
  import { fmtMicros, fmtNum, fmtPct, relTime } from '$lib/model/format';
  import { appHref, qp, setQuery } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';

  let app = $derived(page.params.app ?? '');
  let q = $derived(qp('q') ?? '');
  let kind = $derived(qp('kind') ?? 'all');
  const list = query(() => api.listResources({ app, kind: 'decisions', query: { q: q || undefined, limit: 200 } }));
  let rows = $derived((list.data?.items ?? []).filter((d) => kind === 'all' || d.subtype === kind));
  const KINDS = ['all', 'graph', 'rules', 'table', 'transform'];

  // new decision
  let creating = $state(false);
  let shape = $state('rules');
  let inputSchema: SchemaRef | undefined = $state();
  let outputSchema: SchemaRef | undefined = $state();
  let needsSchemas = $derived(shape === 'rules' || shape === 'table');

  function buildSpec(): DecisionSpec {
    const pos = { x: 28, y: 60 };
    if (shape === 'rules') return { entry: 'rules', nodes: [{ id: 'rules', type: 'rules', inputSchema: inputSchema!, outputSchema: outputSchema!, mode: 'first_match', rules: [], position: pos }], output: outputSchema ? { schema: outputSchema } : undefined, tests: [] };
    if (shape === 'table') return { entry: 'table', nodes: [{ id: 'table', type: 'table', inputSchema: inputSchema!, outputSchema: outputSchema!, hitPolicy: 'first', inputs: [], outputs: [], rows: [], position: pos }], output: outputSchema ? { schema: outputSchema } : undefined, tests: [] };
    if (shape === 'transform') return { entry: 'transform', nodes: [{ id: 'transform', type: 'transform', mode: 'build', fields: [], position: pos }], tests: [] };
    return { entry: 'start', nodes: [{ id: 'start', type: 'transform', mode: 'build', fields: [], next: 'output', position: pos }, { id: 'output', type: 'output', fields: [], position: { x: 264, y: 60 } }], tests: [] };
  }
</script>

<PageHeader title="Decisions" sub="Every rule set, table, transform and graph this app can call. Versions are immutable; each environment runs the version its last release named.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)} disabled={!session.can('edit')} title={session.why('edit')}>New decision</button>{/snippet}
</PageHeader>

<div class="filters">
  <label for="dq" class="sr">Filter decisions</label>
  <input id="dq" type="search" placeholder="Filter by name or description" value={q} oninput={(e) => setQuery({ q: e.currentTarget.value })} />
  <div class="chips" role="group" aria-label="Kind">
    {#each KINDS as k (k)}<button type="button" class="chip" aria-pressed={kind === k} onclick={() => setQuery({ kind: k === 'all' ? undefined : k })}>{k}</button>{/each}
  </div>
</div>

{#if list.error}
  <ErrorNote error={list.error} retry={list.reload} />
{:else if !list.data}
  <Loading />
{:else if rows.length === 0}
  {#if q || kind !== 'all'}
    <EmptyState title="No decision matches">Nothing matches {q ? `“${q}”` : ''}{kind !== 'all' ? ` of kind ${kind}` : ''}.{#snippet actions()}<button type="button" class="btn" onclick={() => setQuery({ q: undefined, kind: undefined })}>Clear filters</button>{/snippet}</EmptyState>
  {:else}
    <EmptyState title="No decisions yet">
      Start with a rule set: register its input and output schemas under <a href={appHref('/schemas')}>Schemas</a>, then create the decision and add rules.
      {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)}>New decision</button><a class="btn" href={appHref('/schemas', { new: 1 })}>Register a schema</a>{/snippet}
    </EmptyState>
  {/if}
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>Decision</th><th>Kind</th><EnvHeads /><th class="num">p99</th><th class="num">Calls/min</th><th class="num">Fallback</th><th>Updated</th></tr></thead>
      <tbody>
        {#each rows as d (d.name)}
          <tr>
            <td><a class="name" href={appHref(`/decisions/${d.name}`)}>{d.name}</a><span class="desc">{d.description ?? ''}</span></td>
            <td><Kind type={d.subtype ?? 'graph'} /></td>
            <LiveCells live={d.live} draftChanged={d.draftChanged} latest={d.latestVersion} />
            <td class="num mono">{fmtMicros(d.stats?.p99Micros)}</td>
            <td class="num mono">{fmtNum(d.stats?.callsPerMinute)}</td>
            <td class="num mono">{fmtPct(d.stats?.fallbackRate)}</td>
            <td class="nowrap muted">{relTime(d.updatedAt)}{d.updatedBy ? ` · ${d.updatedBy.split('@')[0]}` : ''}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <p class="hint">Stats are the last hour in prod.</p>
{/if}

<NewResourceDialog {app} kind="decisions" bind:open={creating} title="New decision" {buildSpec} valid={!needsSchemas || (!!inputSchema && !!outputSchema)}>
  {#snippet extra()}
    <Field label="Start with" id="new-dec-shape">
      <select id="new-dec-shape" bind:value={shape}>
        <option value="rules">A rule set</option>
        <option value="table">A decision table</option>
        <option value="transform">A transform</option>
        <option value="graph">A graph (transform → output)</option>
      </select>
    </Field>
    {#if needsSchemas}
      <SchemaRefPicker {app} bind:value={inputSchema} label="Input schema" id="new-dec-in" required hint="What the rules read." />
      <SchemaRefPicker {app} bind:value={outputSchema} label="Output schema" id="new-dec-out" required hint="What every rule returns." />
      <p class="hint">A {shape === 'rules' ? 'rule set' : 'table'} cannot be saved without registered schemas. <a href={appHref('/schemas', { new: 1 })}>Register a schema</a> if the one you need is missing.</p>
    {/if}
  {/snippet}
</NewResourceDialog>
