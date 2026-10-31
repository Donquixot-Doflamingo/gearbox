<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import PathTrace from '$lib/components/PathTrace.svelte';
  import { fmtDateTime, fmtMicros, shortHash } from '$lib/model/format';
  import { appHref, currentEnv } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());
  let requestId = $derived(page.params.requestId ?? '');
  const rec = query(() => api.searchDecisionLogs({ app, env, query: { requestId, limit: 5 } }).then((r) => r.items.find((x) => x.requestId === requestId) ?? r.items[0] ?? null));
  let l = $derived(rec.data);
</script>

<PageHeader title={requestId}>
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'decision logs', href: appHref('/logs') }]} />{/snippet}
</PageHeader>

{#if rec.error}
  <ErrorNote error={rec.error} retry={rec.reload} />
{:else if rec.loading && !l}
  <Loading />
{:else if !l}
  <div class="empty"><h2>No record for {requestId} in {env}</h2><p>Decision logs are sampled; this request may not have been logged, or it ran in another environment. Records are kept for 14 days.</p></div>
{:else}
  <div class="grid-2">
    <section class="panel">
      <dl class="kv">
        <dt>Decision</dt><dd><a class="mono" href={appHref(`/decisions/${l.decision}`)}>{l.decisionVersion}</a></dd>
        <dt>When</dt><dd>{fmtDateTime(l.ts)}</dd>
        <dt>Status</dt><dd><span class="pill {l.status === 'OK' ? 'pill-ok' : 'pill-crit'}">{l.status}</span>{#if l.fallbackUsed} <span class="pill pill-warn">fallback used</span>{/if}</dd>
        <dt>Evaluation</dt><dd class="mono">{fmtMicros(l.evalMicros)}</dd>
        <dt>Generation</dt><dd class="mono">{l.generation}</dd>
        <dt>Unit</dt><dd class="mono">{l.unitId ?? '—'}</dd>
        <dt>Sidecar</dt><dd class="mono">{l.sidecarId ?? '—'}</dd>
        <dt>Trace</dt><dd class="mono">{l.traceId ?? '—'}</dd>
        <dt>Dependencies</dt><dd class="mono">{shortHash(l.dependencyHash)}</dd>
        <dt>Input / output hash</dt><dd class="mono">{shortHash(l.inputHmac)} / {shortHash(l.outputHmac)}</dd>
      </dl>
    </section>
    <section class="panel">
      <header><h2>Path</h2></header>
      <PathTrace path={l.path ?? []} />
      {#if l.ai?.length}
        <h3 class="sub-h">AI calls</h3>
        <ul class="list">{#each l.ai as a, i (i)}<li><div class="main"><span class="mono">{a.node} · {a.model}</span><span class="meta">{a.latencyMs} ms · {a.result}{a.cacheHit ? ' · cache hit' : ''}</span></div></li>{/each}</ul>
      {/if}
    </section>
  </div>
  <div class="grid-2">
    <section class="panel"><h3 class="sub-h">Logged input fields</h3><JsonView value={l.input ?? {}} /><p class="hint">Only fields listed in the decision's logging settings; PII is never stored in clear.</p></section>
    <section class="panel"><h3 class="sub-h">Output</h3><JsonView value={l.output} /></section>
  </div>
{/if}
