<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { AINode, DecisionSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { fmtNum, fmtPct } from '$lib/model/format';
  import { appHref, currentEnv, qp, setQuery } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());
  let days = $derived(Number(qp('days') ?? 7));

  const nodes = query(async () => {
    const list = await api.listResources({ app, kind: 'decisions', query: { limit: 200 } });
    const res = await Promise.all(list.items.map((d) => api.getResource({ app, kind: 'decisions', name: d.name })));
    return res.flatMap((r) => ((r.draft.spec as DecisionSpec).nodes ?? []).filter((n): n is AINode => n.type === 'ai').map((n) => ({ decision: r.name, node: n, live: r.live[env]?.version })));
  });

  let sel = $derived.by(() => {
    const d = qp('decision'), n = qp('node');
    const all = nodes.data ?? [];
    return all.find((x) => x.decision === d && x.node.id === n) ?? all[0];
  });

  const report = query(() => (sel ? api.getShadowReport({ app, name: sel.decision, nodeId: sel.node.id, query: { environment: env, days } }) : Promise.resolve(null)));
  let r = $derived(report.data);
  let worst = $derived.by(() => {
    if (!r) return null;
    let best: { i: number; j: number; v: number } | null = null;
    r.matrix.rows.forEach((row, i) => row.forEach((v, j) => {
      if (i !== j && (!best || v > best.v)) best = { i, j, v };
    }));
    return best as { i: number; j: number; v: number } | null;
  });
</script>

<PageHeader title="AI nodes" sub="Each AI node makes one call to the Integration Hub and must return JSON that matches its response schema. Timeouts, errors and invalid answers take the node's onError path. Run new nodes in shadow first and compare them with the path that decided.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
</PageHeader>

{#if nodes.error}
  <ErrorNote error={nodes.error} retry={nodes.reload} />
{:else if !nodes.data}
  <Loading />
{:else if nodes.data.length === 0}
  <EmptyState title="No AI nodes yet">Add an <strong>ai</strong> node to a decision graph, in shadow mode with an onError node that decides meanwhile.{#snippet actions()}<a class="btn btn-primary" href={appHref('/decisions')}>Open decisions</a>{/snippet}</EmptyState>
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>Node</th><th>Decision</th><th>Mode</th><th>Model alias</th><th class="num">Timeout</th><th>Live in {env}</th></tr></thead>
      <tbody>
        {#each nodes.data as a (a.decision + a.node.id)}
          <tr class="clickable" class:selected={sel === a} onclick={() => setQuery({ decision: a.decision, node: a.node.id })}>
            <td><button type="button" class="link-btn mono" onclick={() => setQuery({ decision: a.decision, node: a.node.id })}>{a.node.id}</button></td>
            <td><a class="mono" href={appHref(`/decisions/${a.decision}`, { node: a.node.id })}>{a.decision}</a></td>
            <td><span class="pill {a.node.mode === 'shadow' ? 'pill-info' : 'pill-warn'}">{a.node.mode}</span></td>
            <td class="mono">{a.node.model}</td>
            <td class="num mono">{fmtNum(a.node.timeoutMs)} ms</td>
            <td class="mono">{a.live ? `v${a.live}` : '—'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>

  {#if sel}
    <div class="row">
      <h2>Shadow report · <span class="mono">{sel.node.id}</span> in {sel.decision}</h2>
      <span class="spacer"></span>
      <label for="ai-days" class="hint">Window</label>
      <select id="ai-days" value={String(days)} onchange={(e) => setQuery({ days: e.currentTarget.value === '7' ? undefined : e.currentTarget.value })} style="width:auto">
        {#each [1, 7, 14, 30, 90] as d (d)}<option value={String(d)}>last {d} day{d === 1 ? '' : 's'}</option>{/each}
      </select>
    </div>
    {#if report.error}<ErrorNote error={report.error} retry={report.reload} />
    {:else if !r}<Loading />
    {:else}
      <section class="tiles" aria-label="Shadow report figures">
        <div class="tile"><span class="label">Agreement with the deciding path</span><span class="value">{(r.agreement * 100).toFixed(1)}<small>%</small></span><span class="note">{fmtNum(r.calls)} calls · {env}</span></div>
        <div class="tile"><span class="label">Hub latency</span><span class="value">{r.latency?.p50Ms ?? '—'}<small>ms p50</small></span><span class="note">{r.latency?.p95Ms ?? '—'} ms p95{sel.node.mode === 'shadow' ? ' · callers wait 0 ms' : ''}</span></div>
        <div class="tile"><span class="label"><span class="dot {(r.timeoutRate ?? 0) > 0.01 ? 'warn' : 'ok'}"></span>Timeouts</span><span class="value">{fmtPct(r.timeoutRate, 1)}</span><span class="note">over the {fmtNum(sel.node.timeoutMs)} ms timeout</span></div>
        <div class="tile"><span class="label">Schema failures</span><span class="value">{fmtPct(r.schemaFailureRate, 1)}</span><span class="note">answer did not match the response schema</span></div>
      </section>
      <div class="grid-2">
        <section class="panel">
          <header><h2>Deciding path vs AI</h2><span class="hint">{fmtNum(r.calls)} calls</span></header>
          <div class="tbl-wrap">
            <table class="cm">
              <thead><tr><th>decided ↓ · AI said →</th>{#each r.matrix.labels as l (l)}<th class="num">{l}</th>{/each}</tr></thead>
              <tbody>
                {#each r.matrix.rows as row, i (i)}
                  <tr><th class="rh mono" scope="row">{r.matrix.labels[i]}</th>{#each row as v, j (j)}<td class:diag={i === j}>{fmtNum(v)}</td>{/each}</tr>
                {/each}
              </tbody>
            </table>
          </div>
          {#if worst}<p class="hint">Biggest gap: {fmtNum(worst.v)} calls decided as <span class="mono">{r.matrix.labels[worst.i]}</span> that the model would answer <span class="mono">{r.matrix.labels[worst.j]}</span>.</p>{/if}
          {#if sel.node.mode === 'shadow'}<p class="hint">To let the model decide, set the node's mode to inline in a new version and release it through review.</p>{/if}
        </section>
        <section class="panel">
          <header><h2>Sample disagreements</h2><span class="hint">vars only; PII never reaches a prompt</span></header>
          <ul class="list">
            {#each r.disagreements ?? [] as d (d.requestId)}
              <li><div class="main"><a class="mono" href={appHref(`/logs/${d.requestId}`)}>{d.requestId}</a><span>decided <b class="mono">{d.decided}</b> · AI <b class="mono">{d.ai}</b></span><span class="meta mono">{JSON.stringify(d.vars ?? {})}</span></div></li>
            {:else}<li class="hint">No disagreements sampled.</li>{/each}
          </ul>
        </section>
      </div>
    {/if}
  {/if}
{/if}
