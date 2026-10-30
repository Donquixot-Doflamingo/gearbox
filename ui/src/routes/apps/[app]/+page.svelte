<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import LatencyChart from '$lib/components/LatencyChart.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import ReleaseLabel from '$lib/components/ReleaseLabel.svelte';
  import ReleaseState from '$lib/components/ReleaseState.svelte';
  import SpreadBar from '$lib/components/SpreadBar.svelte';
  import { fmtMicros, fmtNum, fmtPct, relTime } from '$lib/model/format';
  import { appHref, currentEnv } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());

  const overview = query(() => api.getOverview({ app, env }));
  const reviews = query(() => api.listChangeRequests({ app, query: { status: 'open', limit: 6 } }).then((r) => r.items));

  let o = $derived(overview.data);
  let behind = $derived(o ? o.sidecars.total - o.sidecars.onLatest : 0);
</script>

<PageHeader title="Overview" sub="How decisions in this app are behaving right now, and what is waiting on you.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet actions()}
    <a class="btn" href={appHref('/changes')}>Review changes</a>
    <a class="btn btn-primary" href={appHref('/decisions')}>Open decisions</a>
  {/snippet}
</PageHeader>

{#if overview.error}
  <ErrorNote error={overview.error} retry={overview.reload} />
{:else if !o}
  <Loading />
{:else}
  <section class="tiles" aria-label="Key figures">
    <div class="tile">
      <span class="label"><span class="dot {o.p99Micros === undefined ? '' : o.p99Micros < 1000 ? 'ok' : 'warn'}"></span>Decide p99</span>
      <span class="value">{o.p99Micros === undefined ? '—' : fmtMicros(o.p99Micros)}</span>
      <span class="note">Target under 1 ms · excludes inline AI</span>
    </div>
    <div class="tile">
      <span class="label">Calls per minute</span>
      <span class="value">{fmtNum(o.callsPerMinute)}</span>
      <span class="note">{o.decisions} decisions · {fmtNum(o.sidecars.total)} sidecars</span>
    </div>
    <div class="tile">
      <span class="label"><span class="dot {(o.fallbackRate ?? 0) < 0.001 ? 'ok' : 'warn'}"></span>Fallback rate</span>
      <span class="value">{fmtPct(o.fallbackRate)}</span>
      <span class="note">Last hour</span>
    </div>
    <div class="tile">
      <span class="label"><span class="dot {behind ? 'warn' : 'ok'}"></span>Sidecars on latest</span>
      <span class="value">{fmtNum(o.sidecars.onLatest)}<small>/ {fmtNum(o.sidecars.total)}</small></span>
      <span class="note">Generation {o.generation}{behind ? ` · ${behind} behind` : ''}</span>
    </div>
  </section>

  <div class="grid-2">
    <section class="panel">
      <header><h2>Decide p99, last 24 hours</h2><span class="hint">all sidecars · {env}</span></header>
      <LatencyChart series={o.latencySeries ?? []} />
    </section>
    <section class="panel">
      <header><h2>Waiting for review</h2><a href={appHref('/changes')} class="hint">All change requests</a></header>
      {#if reviews.data && reviews.data.length}
        <ul class="list">
          {#each reviews.data as r (r.id)}
            <li>
              <div class="main">
                <a class="mono" href={appHref(`/changes/${r.id}`)}>{r.id}</a>
                <span>{r.title}</span>
                <span class="meta">by {r.author} · {r.tests ? `${r.tests.passed} of ${r.tests.passed + (r.tests.failed ?? 0)} tests passed` : 'no tests'} · {relTime(r.createdAt)}</span>
              </div>
              <span class="pill {r.environment === 'prod' ? 'pill-crit' : 'pill-muted'}">to {r.environment}</span>
            </li>
          {/each}
        </ul>
      {:else if reviews.data}
        <p class="hint">Nothing waiting. Open a change request from a decision's Versions tab to ask for a release.</p>
      {:else}<Loading />{/if}
    </section>
  </div>

  <div class="grid-2">
    <section class="panel">
      <header><h2>Recent releases</h2><a href={appHref('/releases')} class="hint">{env} history</a></header>
      {#if o.recentReleases?.length}
        <ul class="list">
          {#each o.recentReleases as r, i (r.generation)}
            <li>
              <div class="main"><span><ReleaseLabel release={r} /></span><span class="meta">generation {r.generation} · {relTime(r.publishedAt)} · {r.publishedBy}</span></div>
              <ReleaseState release={r} latest={i === 0} />
            </li>
          {/each}
        </ul>
      {:else}
        <p class="hint">Nothing published to {env} yet. Approve a change request, then publish it from Releases.</p>
      {/if}
    </section>
    <section class="panel">
      <header><h2>Sidecar fleet</h2><a href={appHref('/fleet')} class="hint">Open fleet</a></header>
      <SpreadBar summary={o.sidecars} generation={o.generation} />
      {#if behind}
        <p class="hint">{behind} sidecar{behind === 1 ? ' is' : 's are'} behind. They keep answering with their current generation until the next successful poll.</p>
      {:else if o.sidecars.total}
        <p class="hint">Every sidecar runs generation {o.generation}{o.sidecars.medianSyncLagSeconds ? `; median sync lag ${o.sidecars.medianSyncLagSeconds} s` : ''}.</p>
      {/if}
    </section>
  </div>
{/if}
