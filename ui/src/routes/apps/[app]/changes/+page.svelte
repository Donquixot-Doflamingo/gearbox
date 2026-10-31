<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { ChangeRequest, ChangeRequestStatus } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import CrStatus from '$lib/components/CrStatus.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import { relTime } from '$lib/model/format';
  import { appHref, qp, setQuery } from '$lib/nav.svelte';
  import { Paged } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { shell } from '$lib/stores/shell.svelte';

  let app = $derived(page.params.app ?? '');
  let status = $derived((qp('status') ?? '') as ChangeRequestStatus | '');
  let target = $derived(qp('target') ?? '');
  const list = new Paged<ChangeRequest>((cursor) => api.listChangeRequests({ app, query: { status: status || undefined, environment: target || undefined, cursor, limit: 50 } }));
  const STATUSES: ['' | ChangeRequestStatus, string][] = [['', 'all'], ['open', 'open'], ['approved', 'approved'], ['published', 'published'], ['rejected', 'rejected'], ['withdrawn', 'withdrawn']];
</script>

<PageHeader title="Change requests" sub="A change request asks to make versions live in one environment. It needs approval when the environment's policy says so; then anyone with the approver role publishes it from here or from Releases.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet actions()}<a class="btn btn-primary" href={appHref('/changes/new')} aria-disabled={!session.can('edit')}>New change request</a>{/snippet}
</PageHeader>

<div class="filters">
  <div class="chips" role="group" aria-label="Status">
    {#each STATUSES as [v, l] (v)}<button type="button" class="chip" aria-pressed={status === v} onclick={() => setQuery({ status: v || undefined })}>{l}</button>{/each}
  </div>
  <label for="cr-env" class="hint">Target</label>
  <select id="cr-env" value={target} onchange={(e) => setQuery({ target: e.currentTarget.value || undefined })} style="width:auto">
    <option value="">any environment</option>
    {#each shell.envs as e (e)}<option value={e}>{e}</option>{/each}
  </select>
</div>

{#if list.error}
  <ErrorNote error={list.error} retry={list.reset} />
{:else if list.loading && !list.items.length}
  <Loading />
{:else if list.items.length === 0}
  <EmptyState title="No change requests{status ? ` that are ${status}` : ''}">Cut a version of a decision, schema or segment, then ask to release it.{#snippet actions()}<a class="btn btn-primary" href={appHref('/changes/new')}>New change request</a>{/snippet}</EmptyState>
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>Request</th><th>Target</th><th>Status</th><th>Items</th><th>Approvals</th><th>Tests</th><th>Opened</th></tr></thead>
      <tbody>
        {#each list.items as cr (cr.id)}
          <tr>
            <td><a class="name" href={appHref(`/changes/${cr.id}`)}>{cr.id}</a><span class="desc">{cr.title}</span></td>
            <td><span class="pill {cr.environment === 'prod' ? 'pill-crit' : 'pill-muted'}">{cr.environment}</span></td>
            <td><CrStatus {cr} /></td>
            <td class="mono">{cr.items.map((i) => `${i.name} v${i.version}`).join(', ')}</td>
            <td class="mono">{(cr.approvals ?? []).length}/{cr.requiredApprovals ?? 0}</td>
            <td>{#if cr.tests}<span class="pill {cr.tests.failed ? 'pill-crit' : 'pill-ok'}">{cr.tests.passed}/{(cr.tests.passed ?? 0) + (cr.tests.failed ?? 0)}</span>{:else}—{/if}</td>
            <td class="nowrap muted">{relTime(cr.createdAt)} · {cr.author.split('@')[0]}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <Pager next={list.next} loading={list.loading} onmore={list.more} />
{/if}
