<script lang="ts">
  // Generic list for versioned resources (lookups, gates, experiments, layers).
  import type { Snippet } from 'svelte';
  import type { ResourceKind, ResourceSummary } from '$lib/api/types.gen';
  import { relTime } from '$lib/model/format';
  import { appHref, qp, setQuery } from '$lib/nav.svelte';
  import EmptyState from './EmptyState.svelte';
  import EnvHeads from './EnvHeads.svelte';
  import ErrorNote from './ErrorNote.svelte';
  import LiveCells from './LiveCells.svelte';
  import Loading from './Loading.svelte';

  let {
    kind,
    items,
    error,
    reload,
    noun,
    emptyText,
    oncreate,
    extraHead,
    extraCell,
  }: {
    kind: ResourceKind;
    items: ResourceSummary[] | undefined;
    error: unknown;
    reload: () => void;
    noun: string;
    emptyText: string;
    oncreate?: () => void;
    extraHead?: Snippet;
    extraCell?: Snippet<[ResourceSummary]>;
  } = $props();

  let q = $derived(qp('q') ?? '');
</script>

<div class="filters">
  <label for="{kind}-q" class="sr">Filter {kind}</label>
  <input id="{kind}-q" type="search" placeholder="Filter by name or description" value={q} oninput={(e) => setQuery({ q: e.currentTarget.value })} />
</div>
{#if error}
  <ErrorNote {error} retry={reload} />
{:else if !items}
  <Loading />
{:else if items.length === 0}
  <EmptyState title={q ? `No ${noun} matches “${q}”` : `No ${noun}s yet`}>
    {q ? 'Clear the filter to see everything.' : emptyText}
    {#snippet actions()}{#if q}<button type="button" class="btn" onclick={() => setQuery({ q: undefined })}>Clear filter</button>{:else if oncreate}<button type="button" class="btn btn-primary" onclick={oncreate}>New {noun}</button>{/if}{/snippet}
  </EmptyState>
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>{noun[0].toUpperCase() + noun.slice(1)}</th>{@render extraHead?.()}<th>Latest</th><EnvHeads /><th>Updated</th></tr></thead>
      <tbody>
        {#each items as s (s.name)}
          <tr>
            <td><a class="name" href={appHref(`/${kind}/${s.name}`)}>{s.name}</a><span class="desc">{s.description ?? ''}</span></td>
            {@render extraCell?.(s)}
            <td>{#if s.latestVersion}<span class="ver">v{s.latestVersion}</span>{:else}<span class="pill pill-warn">no version</span>{/if}</td>
            <LiveCells live={s.live} draftChanged={s.draftChanged} latest={s.latestVersion} />
            <td class="nowrap muted">{relTime(s.updatedAt)}{s.updatedBy ? ` · ${s.updatedBy.split('@')[0]}` : ''}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
