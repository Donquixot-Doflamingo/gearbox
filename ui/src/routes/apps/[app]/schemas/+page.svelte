<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import EnvHeads from '$lib/components/EnvHeads.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Field from '$lib/components/Field.svelte';
  import LiveCells from '$lib/components/LiveCells.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import NewResourceDialog from '$lib/components/NewResourceDialog.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { relTime } from '$lib/model/format';
  import { appHref, qp, setQuery } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';

  let app = $derived(page.params.app ?? '');
  let q = $derived(qp('q') ?? '');
  const list = query(() => api.listResources({ app, kind: 'schemas', query: { q: q || undefined, limit: 200 } }));

  let creating = $state(false);
  let rootType = $state('object');
  $effect(() => {
    if (qp('new') === '1') {
      creating = true;
      setQuery({ new: undefined });
    }
  });

  function buildSpec(): unknown {
    const jsonSchema = rootType === 'object' ? { type: 'object', required: [], properties: {} } : rootType === 'array' ? { type: 'array', maxItems: 100, items: { type: 'string' } } : { type: rootType };
    return { jsonSchema, examples: [] };
  }
</script>

<PageHeader title="Schemas">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet sub()}Registered, versioned JSON Schemas. Every <strong>rules</strong> and <strong>table</strong> node needs an input and an output schema from here before it can be saved.{/snippet}
  {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)} disabled={!session.can('edit')} title={session.why('edit')}>New schema</button>{/snippet}
</PageHeader>

<div class="filters">
  <label for="sq" class="sr">Filter schemas</label>
  <input id="sq" type="search" placeholder="Filter by name or description" value={q} oninput={(e) => setQuery({ q: e.currentTarget.value })} />
</div>

{#if list.error}
  <ErrorNote error={list.error} retry={list.reload} />
{:else if !list.data}
  <Loading />
{:else if list.data.items.length === 0}
  {#if q}
    <EmptyState title="No schema matches “{q}”">Clear the filter to see every schema.{#snippet actions()}<button type="button" class="btn" onclick={() => setQuery({ q: undefined })}>Clear filter</button>{/snippet}</EmptyState>
  {:else}
    <EmptyState title="Register your first schema">
      Describe the input your service sends (for example <code class="inl">lead_input</code>) and the output a rule set returns. Then cut v1 so decisions can use it.
      {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)}>New schema</button>{/snippet}
    </EmptyState>
  {/if}
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>Schema</th><th>Root</th><th>Latest</th><EnvHeads /><th>Updated</th></tr></thead>
      <tbody>
        {#each list.data.items as s (s.name)}
          <tr>
            <td><a class="name" href={appHref(`/schemas/${s.name}`)}>{s.name}</a><span class="desc">{s.description ?? ''}</span></td>
            <td class="mono">{s.subtype ?? '—'}</td>
            <td class="nowrap">{#if s.latestVersion}<span class="ver">v{s.latestVersion}</span>{:else}<span class="pill pill-warn">no version</span>{/if}{#if s.draftChanged && s.latestVersion}<span class="hint"> · draft changed</span>{/if}</td>
            <LiveCells live={s.live} draftChanged={s.draftChanged} latest={s.latestVersion} />
            <td class="nowrap muted">{relTime(s.updatedAt)}{s.updatedBy ? ` · ${s.updatedBy.split('@')[0]}` : ''}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<NewResourceDialog {app} kind="schemas" bind:open={creating} title="New schema" {buildSpec}>
  {#snippet extra()}
    <Field label="Root type" id="new-schema-root" hint="Most schemas are objects; an output schema may be a plain string or number.">
      <select id="new-schema-root" bind:value={rootType}>
        {#each ['object', 'array', 'string', 'integer', 'number', 'boolean'] as t (t)}<option value={t}>{t}</option>{/each}
      </select>
    </Field>
  {/snippet}
</NewResourceDialog>
