<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { ExperimentSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import NewResourceDialog from '$lib/components/NewResourceDialog.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import ResourceList from '$lib/components/ResourceList.svelte';
  import { qp } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';

  let app = $derived(page.params.app ?? '');
  let q = $derived(qp('q') ?? '');
  const list = query(() => api.listResources({ app, kind: 'experiments', query: { q: q || undefined, limit: 200 } }));
  let creating = $state(false);
  const buildSpec = (): ExperimentSpec => ({ status: 'draft', variants: [{ name: 'control', weight: 50, parameters: {} }, { name: 'treatment', weight: 50, parameters: {} }] });
  const PILL: Record<string, string> = { running: 'pill-ok', draft: 'pill-muted', stopped: 'pill-warn' };
</script>

<PageHeader title="Experiments">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet sub()}A/B experiments: variants with weights and parameters, holdouts, targeting and mutually exclusive layers. Assignment uses murmur3 buckets 0–9999, so a unit keeps its variant. Every exposure is logged, never sampled.{/snippet}
  {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)} disabled={!session.can('edit')} title={session.why('edit')}>New experiment</button>{/snippet}
</PageHeader>

<ResourceList kind="experiments" items={list.data?.items} error={list.error} reload={list.reload} noun="experiment" emptyText="A new experiment starts as a draft with control and treatment at 50/50." oncreate={() => (creating = true)}>
  {#snippet extraHead()}<th>Status</th>{/snippet}
  {#snippet extraCell(s)}<td>{#if s.subtype}<span class="pill {PILL[s.subtype] ?? 'pill-muted'}">{s.subtype}</span>{:else}<span class="hint">—</span>{/if}</td>{/snippet}
</ResourceList>

<NewResourceDialog {app} kind="experiments" bind:open={creating} title="New experiment" {buildSpec} />
