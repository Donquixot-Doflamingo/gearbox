<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { LayerSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import NewResourceDialog from '$lib/components/NewResourceDialog.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import ResourceList from '$lib/components/ResourceList.svelte';
  import { qp } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';

  let app = $derived(page.params.app ?? '');
  let q = $derived(qp('q') ?? '');
  const list = query(() => api.listResources({ app, kind: 'layers', query: { q: q || undefined, limit: 200 } }));
  let creating = $state(false);
  const buildSpec = (): LayerSpec => ({ allocations: [] });
</script>

<PageHeader title="Layers" sub="Mutual exclusion between experiments that share traffic: each experiment in a layer gets its own slice of the layer's buckets.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)} disabled={!session.can('edit')} title={session.why('edit')}>New layer</button>{/snippet}
</PageHeader>

<ResourceList kind="layers" items={list.data?.items} error={list.error} reload={list.reload} noun="layer" emptyText="Create a layer, then allocate a share of it to each experiment." oncreate={() => (creating = true)} />
<NewResourceDialog {app} kind="layers" bind:open={creating} title="New layer" {buildSpec} />
