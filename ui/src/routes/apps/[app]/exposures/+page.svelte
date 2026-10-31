<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { ExposureRecord } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import { fmtDateTime } from '$lib/model/format';
  import { appHref, currentEnv, qp, setQuery } from '$lib/nav.svelte';
  import { Paged } from '$lib/query.svelte';
  import { registry } from '$lib/stores/registry.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());
  let experiment = $derived(qp('experiment') ?? '');
  let unitId = $derived(qp('unitId') ?? '');
  const list = new Paged<ExposureRecord>((cursor) => api.listExposureLogs({ app, env, query: { experiment: experiment || undefined, unitId: unitId || undefined, cursor, limit: 50 } }));
  let experiments = $derived(registry.list(app, 'experiments') ?? []);
</script>

<PageHeader title="Exposure logs" sub="Every experiment assignment (GetVariant calls and experiment nodes) writes an exposure record. They are never sampled; experiment analysis reads only this log.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
</PageHeader>

<div class="filters">
  <label class="sr" for="xp-x">Experiment</label>
  <select id="xp-x" value={experiment} onchange={(e) => setQuery({ experiment: e.currentTarget.value || undefined })} style="width:auto">
    <option value="">all experiments</option>
    {#each experiments as x (x.name)}<option value={x.name}>{x.name}</option>{/each}
  </select>
  <label class="sr" for="xp-u">Unit</label>
  <input id="xp-u" type="search" placeholder="unit ID" value={unitId} onchange={(e) => setQuery({ unitId: e.currentTarget.value.trim() || undefined })} />
</div>

{#if list.error}<ErrorNote error={list.error} retry={list.reset} />
{:else if list.loading && !list.items.length}<Loading />
{:else if !list.items.length}<p class="hint">No exposures in {env}{experiment ? ` for ${experiment}` : ''}.</p>
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>When</th><th>Unit</th><th>Experiment</th><th>Variant</th><th>Reason</th><th>Request</th></tr></thead>
      <tbody>
        {#each list.items as x, i (i)}
          <tr>
            <td class="nowrap">{fmtDateTime(x.ts)}</td>
            <td class="mono">{x.unitId}</td>
            <td><a class="mono" href={appHref(`/experiments/${x.experiment}`)}>{x.experimentVersion}</a></td>
            <td class="mono">{x.variant ?? '—'}</td>
            <td><span class="pill {x.reason === 'assigned' ? 'pill-ok' : 'pill-muted'}">{x.reason}</span></td>
            <td>{#if x.requestId}<a class="mono" href={appHref(`/logs/${x.requestId}`)}>{x.requestId}</a>{:else}—{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <Pager next={list.next} loading={list.loading} onmore={list.more} />
{/if}
