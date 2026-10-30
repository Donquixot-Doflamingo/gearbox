<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { DecisionLogRecord } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import PathTrace from '$lib/components/PathTrace.svelte';
  import { fmtMicros, fmtTime } from '$lib/model/format';
  import { appHref, currentEnv, qp, setQuery } from '$lib/nav.svelte';
  import { Paged } from '$lib/query.svelte';
  import { registry } from '$lib/stores/registry.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());
  let decision = $derived(qp('decision') ?? '');
  let version = $derived(qp('version') ?? '');
  let requestId = $derived(qp('requestId') ?? '');
  let unitId = $derived(qp('unitId') ?? '');
  let fallback = $derived(qp('fallback') === '1');
  let from = $derived(qp('from') ?? '');
  let to = $derived(qp('to') ?? '');

  const logs = new Paged<DecisionLogRecord>((cursor) =>
    api.searchDecisionLogs({ app, env, query: { decision: decision || undefined, version: version ? Number(version) : undefined, requestId: requestId || undefined, unitId: unitId || undefined, fallback: fallback || undefined, from: from ? new Date(from).toISOString() : undefined, to: to ? new Date(to).toISOString() : undefined, cursor, limit: 50 } }),
  );
  let selId: string | null = $state(null);
  let sel = $derived(logs.items.find((l) => l.requestId === selId) ?? logs.items[0]);
  let decisions = $derived(registry.list(app, 'decisions') ?? []);

  function outcome(l: DecisionLogRecord): string {
    const o = l.output as Record<string, unknown> | undefined;
    if (o && typeof o === 'object') {
      const first = Object.values(o)[0];
      return typeof first === 'string' || typeof first === 'number' || typeof first === 'boolean' ? String(first) : JSON.stringify(o).slice(0, 40);
    }
    return o === undefined ? (l.outputHmac ? 'hashed' : '—') : String(o);
  }
  function key(e: KeyboardEvent, id: string): void {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      selId = id;
    }
  }
</script>

<PageHeader title="Decision logs" sub="Sampled records of individual calls: version, path through the graph and timing. Only fields listed in a decision's logging settings are stored; everything else is a hash.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
</PageHeader>

<form class="filters" onsubmit={(e) => e.preventDefault()} aria-label="Search decision logs">
  <label class="sr" for="lg-dec">Decision</label>
  <select id="lg-dec" value={decision} onchange={(e) => setQuery({ decision: e.currentTarget.value || undefined, version: undefined })} style="width:auto">
    <option value="">all decisions</option>
    {#each decisions as d (d.name)}<option value={d.name}>{d.name}</option>{/each}
  </select>
  <label class="sr" for="lg-ver">Version</label>
  <input id="lg-ver" type="number" min="1" placeholder="version" value={version} onchange={(e) => setQuery({ version: e.currentTarget.value || undefined })} style="width:100px" />
  <label class="sr" for="lg-req">Request ID</label>
  <input id="lg-req" type="search" placeholder="request ID (prefix)" value={requestId} onchange={(e) => setQuery({ requestId: e.currentTarget.value.trim() || undefined })} />
  <label class="sr" for="lg-unit">Unit</label>
  <input id="lg-unit" type="search" placeholder="unit ID" value={unitId} onchange={(e) => setQuery({ unitId: e.currentTarget.value.trim() || undefined })} style="width:160px" />
  <label class="sr" for="lg-from">From</label>
  <input id="lg-from" type="datetime-local" value={from} onchange={(e) => setQuery({ from: e.currentTarget.value || undefined })} />
  <label class="sr" for="lg-to">To</label>
  <input id="lg-to" type="datetime-local" value={to} onchange={(e) => setQuery({ to: e.currentTarget.value || undefined })} />
  <div class="chips"><button type="button" class="chip" aria-pressed={fallback} onclick={() => setQuery({ fallback: fallback ? undefined : '1' })}>Fallback only</button></div>
</form>

{#if logs.error}
  <ErrorNote error={logs.error} retry={logs.reset} />
{:else if logs.loading && !logs.items.length}
  <Loading />
{:else if !logs.items.length}
  <p class="hint">No records match in {env}. Records are sampled (see the environment's log sample rate) and kept for 14 days.</p>
{:else}
  <div class="grid-2">
    <div class="stack-sm">
      <div class="tbl-wrap">
        <table>
          <thead><tr><th>Time</th><th>Request</th><th>Decision</th><th>Outcome</th><th class="num">Eval</th></tr></thead>
          <tbody>
            {#each logs.items as l (l.requestId)}
              <tr class="clickable" class:selected={sel?.requestId === l.requestId} tabindex="0" onclick={() => (selId = l.requestId)} onkeydown={(e) => key(e, l.requestId)} aria-selected={sel?.requestId === l.requestId}>
                <td class="mono nowrap">{fmtTime(l.ts)}</td>
                <td class="mono">{l.requestId}</td>
                <td class="mono nowrap">{l.decisionVersion}</td>
                <td>{outcome(l)}{#if l.fallbackUsed} <span class="pill pill-warn">fallback</span>{/if}</td>
                <td class="num mono nowrap">{fmtMicros(l.evalMicros)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <Pager next={logs.next} loading={logs.loading} onmore={logs.more} count={logs.items.length} />
    </div>
    {#if sel}
      <section class="panel" aria-live="polite">
        <header><h2><a class="mono" href={appHref(`/logs/${sel.requestId}`)}>{sel.requestId}</a></h2><span class="hint">{sel.decisionVersion} · generation {sel.generation}</span></header>
        <PathTrace path={sel.path ?? []} />
        <div class="grid-2">
          <div class="stack-sm"><h3 class="sub-h">Logged input fields</h3><JsonView value={sel.input ?? {}} /></div>
          <div class="stack-sm"><h3 class="sub-h">Output</h3><JsonView value={sel.output} /></div>
        </div>
        <a class="hint" href={appHref(`/logs/${sel.requestId}`)}>Full record</a>
      </section>
    {/if}
  </div>
{/if}
