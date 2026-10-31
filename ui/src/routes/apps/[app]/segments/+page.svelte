<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { SchemaRef, SegmentSpec } from '$lib/api/types.gen';
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
  import { relTime } from '$lib/model/format';
  import { appHref, qp, setQuery } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';

  let app = $derived(page.params.app ?? '');
  let q = $derived(qp('q') ?? '');
  let type = $derived(qp('type') ?? 'all');
  const list = query(() => api.listResources({ app, kind: 'segments', query: { q: q || undefined, limit: 200 } }));
  let rows = $derived((list.data?.items ?? []).filter((s) => type === 'all' || s.subtype === type));

  let creating = $state(false);
  let newType = $state<'static' | 'dynamic' | 'rollout'>('static');
  let keyType = $state<'string' | 'number'>('string');
  let pii = $state(false);
  let attrs: SchemaRef | undefined = $state();
  let percent = $state(10);

  function buildSpec(): SegmentSpec {
    if (newType === 'static') return { type: 'static', static: { keyType, pii } };
    if (newType === 'dynamic') return { type: 'dynamic', dynamic: { attributesSchema: attrs!, when: { all: [{ field: '', op: 'EQ', value: '' }] } } };
    return { type: 'rollout', rollout: { percent } };
  }
  const HELP = {
    static: 'An uploaded list of members (CSV or JSON). Rules check a key against it.',
    dynamic: 'Membership computed at decision time from attributes the rule passes in, with conditions over a registered attributes schema.',
    rollout: 'A stable percentage of units: bucket of murmur3(salt + key) below percent × 100.',
  } as const;
</script>

<PageHeader title="Segments" sub="Sets of units used in rule conditions (IN_SEGMENT / NOT_IN_SEGMENT): static member lists, dynamic attribute conditions, or stable rollout percentages.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)} disabled={!session.can('edit')} title={session.why('edit')}>New segment</button>{/snippet}
</PageHeader>

<div class="filters">
  <label for="sgq" class="sr">Filter segments</label>
  <input id="sgq" type="search" placeholder="Filter by name or description" value={q} oninput={(e) => setQuery({ q: e.currentTarget.value })} />
  <div class="chips" role="group" aria-label="Type">
    {#each ['all', 'static', 'dynamic', 'rollout'] as k (k)}<button type="button" class="chip" aria-pressed={type === k} onclick={() => setQuery({ type: k === 'all' ? undefined : k })}>{k}</button>{/each}
  </div>
</div>

{#if list.error}
  <ErrorNote error={list.error} retry={list.reload} />
{:else if !list.data}
  <Loading />
{:else if rows.length === 0}
  <EmptyState title={q || type !== 'all' ? 'No segment matches' : 'No segments yet'}>
    {q || type !== 'all' ? 'Clear the filters to see every segment.' : 'Create a static list (for example blocked cities), a dynamic segment over attributes, or a rollout percentage.'}
    {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)}>New segment</button>{/snippet}
  </EmptyState>
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>Segment</th><th>Type</th><th>Latest</th><EnvHeads /><th>Updated</th></tr></thead>
      <tbody>
        {#each rows as s (s.name)}
          <tr>
            <td><a class="name" href={appHref(`/segments/${s.name}`)}>{s.name}</a><span class="desc">{s.description ?? ''}</span></td>
            <td><Kind type={s.subtype ?? 'static'} /></td>
            <td>{#if s.latestVersion}<span class="ver">v{s.latestVersion}</span>{:else}<span class="pill pill-warn">no version</span>{/if}</td>
            <LiveCells live={s.live} draftChanged={s.draftChanged} latest={s.latestVersion} />
            <td class="nowrap muted">{relTime(s.updatedAt)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<NewResourceDialog {app} kind="segments" bind:open={creating} title="New segment" {buildSpec} valid={newType !== 'dynamic' || !!attrs}>
  {#snippet extra()}
    <Field label="Type" id="nseg-type" hint={HELP[newType]}>
      <select id="nseg-type" bind:value={newType}>
        <option value="static">static: member list</option>
        <option value="dynamic">dynamic: conditions over attributes</option>
        <option value="rollout">rollout: stable percentage</option>
      </select>
    </Field>
    {#if newType === 'static'}
      <Field label="Key type" id="nseg-kt"><select id="nseg-kt" bind:value={keyType}><option value="string">string</option><option value="number">number</option></select></Field>
      <label class="check"><input type="checkbox" bind:checked={pii} /> Members are personal data (store and ship them as HMACs)</label>
    {:else if newType === 'dynamic'}
      <SchemaRefPicker {app} bind:value={attrs} label="Attributes schema" required hint="The attributes a rule passes in; conditions are written over them." />
    {:else}
      <Field label="Percent of units" id="nseg-pct"><input id="nseg-pct" type="number" min="0" max="100" step="0.01" bind:value={percent} /></Field>
    {/if}
  {/snippet}
</NewResourceDialog>
