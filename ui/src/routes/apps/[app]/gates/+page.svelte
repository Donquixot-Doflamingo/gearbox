<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { GateSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import NewResourceDialog from '$lib/components/NewResourceDialog.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import ResourceList from '$lib/components/ResourceList.svelte';
  import { killStates } from '$lib/killswitch';
  import { currentEnv, qp } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());
  let q = $derived(qp('q') ?? '');
  const list = query(() => api.listResources({ app, kind: 'gates', query: { q: q || undefined, limit: 200 } }));
  const releases = query(() => api.listReleases({ app, env, query: { limit: 200 } }).then((r) => killStates(r.items)));
  let creating = $state(false);
  const buildSpec = (): GateSpec => ({ enabled: true, rules: [], default: false });
</script>

<PageHeader title="Gates">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet sub()}Feature flags: ordered rules that return true or false for a unit, with stable percentage rollouts. The kill switch forces a gate off for everyone through a release.{/snippet}
  {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (creating = true)} disabled={!session.can('edit')} title={session.why('edit')}>New gate</button>{/snippet}
</PageHeader>

<ResourceList kind="gates" items={list.data?.items} error={list.error} reload={list.reload} noun="gate" emptyText="A new gate starts on, with no rules and default false." oncreate={() => (creating = true)}>
  {#snippet extraHead()}<th>Kill switch ({env})</th>{/snippet}
  {#snippet extraCell(s)}
    {@const k = releases.data?.get(s.name)}
    <td>{#if k?.killed === true}<span class="pill pill-crit">on · gen {k.generation}</span>{:else if k?.killed === false}<span class="pill pill-muted">off</span>{:else if k}<span class="pill pill-warn">used · gen {k.generation}</span>{:else}<span class="hint">off</span>{/if}</td>
  {/snippet}
</ResourceList>

<NewResourceDialog {app} kind="gates" bind:open={creating} title="New gate" {buildSpec} />
