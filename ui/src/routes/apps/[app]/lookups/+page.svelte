<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { LookupSpec, SchemaRef } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import Field from '$lib/components/Field.svelte';
  import NewResourceDialog from '$lib/components/NewResourceDialog.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import ResourceList from '$lib/components/ResourceList.svelte';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import { qp } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';

  let app = $derived(page.params.app ?? '');
  let q = $derived(qp('q') ?? '');
  const list = query(() => api.listResources({ app, kind: 'lookups', query: { q: q || undefined, limit: 200 } }));
  let creating = $state(false);
  let keyType = $state<'string' | 'number'>('string');
  let valueSchema = $state<SchemaRef | undefined>();
  const buildSpec = (): LookupSpec => ({ keyType, valueSchema: valueSchema! });
</script>

<PageHeader title="Lookups">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet sub()}Key → value tables shipped inside the bundle. Use them with a lookup node or <code class="inl">lookup("city_zone", input.lead.city)</code> in any expression. Every value matches a registered value schema.{/snippet}
  {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)} disabled={!session.can('edit')} title={session.why('edit')}>New lookup</button>{/snippet}
</PageHeader>

<ResourceList kind="lookups" items={list.data?.items} error={list.error} reload={list.reload} noun="lookup" emptyText="Create a lookup, pick the schema its values follow, then upload rows as CSV (key,value) or JSON." oncreate={() => (creating = true)} />
<p class="hint">Bundles are limited per app (64 MB by default). Very large tables belong in your own service; send the looked-up value in the input instead.</p>

<NewResourceDialog {app} kind="lookups" bind:open={creating} title="New lookup" {buildSpec} valid={!!valueSchema}>
  {#snippet extra()}
    <Field label="Key type" id="nlk-kt"><select id="nlk-kt" bind:value={keyType}><option value="string">string</option><option value="number">number</option></select></Field>
    <SchemaRefPicker {app} bind:value={valueSchema} label="Value schema" required hint="Every value must match it; it may be a plain string or number schema." />
  {/snippet}
</NewResourceDialog>
