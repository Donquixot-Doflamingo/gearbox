<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { FleetSummary, Sidecar } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import SpreadBar from '$lib/components/SpreadBar.svelte';
  import { fmtMicros, fmtNum, relTime } from '$lib/model/format';
  import { currentEnv } from '$lib/nav.svelte';
  import { Paged, query } from '$lib/query.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());
  let summary: FleetSummary | null = $state(null);
  const list = new Paged<Sidecar>((cursor) =>
    api.listSidecars({ app, env, query: { cursor, limit: 50 } }).then((r) => {
      summary = r.summary;
      return r;
    }),
  );
  const envs = query(() => api.listEnvironments({ app }).then((r) => r.items));
  let gen = $derived(envs.data?.find((e) => e.name === env)?.generation ?? 0);
  let behind = $derived(summary ? summary.total - summary.onLatest : 0);
  let versions = $derived(Object.entries(summary?.versions ?? {}));
</script>

<PageHeader title="Sidecar fleet" sub="Every sidecar reports its generation, version and last sync every 30 seconds. Decisions never wait on this page's data; it is for spotting pods that fell behind. Lagging sidecars are listed first.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet actions()}<button type="button" class="btn" onclick={() => list.reset()}>Refresh</button>{/snippet}
</PageHeader>

{#if list.error}
  <ErrorNote error={list.error} retry={list.reset} />
{:else if !summary}
  <Loading />
{:else if summary.total === 0}
  <EmptyState title="No sidecars in {env}">No sidecar has reported in the last 10 minutes. Create a sidecar credential in Settings, mount it into the pod and start the sidecar next to your service.</EmptyState>
{:else}
  <section class="tiles">
    <div class="tile"><span class="label">Sidecars</span><span class="value">{fmtNum(summary.total)}</span><span class="note">{app} · {env}</span></div>
    <div class="tile"><span class="label"><span class="dot {behind ? 'warn' : 'ok'}"></span>On generation {gen}</span><span class="value">{fmtNum(summary.onLatest)}</span><span class="note">{behind ? `${behind} behind` : 'all in sync'}</span></div>
    <div class="tile"><span class="label">Median sync lag</span><span class="value">{summary.medianSyncLagSeconds ?? '—'}<small>s</small></span><span class="note">publish to swap</span></div>
    <div class="tile"><span class="label">Sidecar versions</span><span class="value">{versions.length || '—'}</span><span class="note">{versions.map(([v, n]) => `${v} × ${n}`).join(' · ')}</span></div>
  </section>
  <section class="panel"><SpreadBar {summary} generation={gen} /></section>
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>Pod</th><th>Node</th><th>Sidecar</th><th class="num">Gen</th><th>Last sync</th><th class="num">p99</th><th>Status</th></tr></thead>
      <tbody>
        {#each list.items as s (s.id)}
          <tr>
            <td class="mono">{s.id}</td>
            <td class="mono">{s.node ?? '—'}</td>
            <td class="mono">{s.sidecarVersion ?? '—'}</td>
            <td class="num mono">{s.generation}</td>
            <td class="nowrap">{relTime(s.lastSyncAt)}</td>
            <td class="num mono">{fmtMicros(s.p99Micros)}</td>
            <td>
              <span class="pill {s.status === 'in_sync' ? 'pill-ok' : s.status === 'behind' ? 'pill-warn' : 'pill-crit'}">{s.status.replace('_', ' ')}</span>
              {#if s.lastError}<span class="desc" style="color:var(--warn);max-width:36ch;margin-top:4px">{s.lastError}</span>{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <Pager next={list.next} loading={list.loading} onmore={list.more} count={list.items.length} total={summary.total} />
{/if}
