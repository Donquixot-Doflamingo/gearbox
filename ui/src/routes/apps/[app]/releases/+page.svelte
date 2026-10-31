<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { ChangeItem, Release, ResourceKind } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Kind from '$lib/components/Kind.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import ReleaseLabel from '$lib/components/ReleaseLabel.svelte';
  import ReleaseState from '$lib/components/ReleaseState.svelte';
  import RolloutStepper from '$lib/components/RolloutStepper.svelte';
  import { killStates } from '$lib/killswitch';
  import { fmtBytes, fmtDateTime, relTime, shortHash } from '$lib/model/format';
  import { appHref, currentEnv } from '$lib/nav.svelte';
  import { Paged, query } from '$lib/query.svelte';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());

  const envs = query(() => api.listEnvironments({ app }).then((r) => r.items));
  let envInfo = $derived(envs.data?.find((e) => e.name === env));
  const history = new Paged<Release>((cursor) => api.listReleases({ app, env, query: { cursor, limit: 25 } }));
  const approved = query(() => api.listChangeRequests({ app, query: { status: 'approved', environment: env, limit: 50 } }).then((r) => r.items));
  const decisions = query(() => api.listResources({ app, kind: 'decisions', query: { limit: 200 } }).then((r) => r.items));
  const data = query(async () => {
    const [s, l] = await Promise.all([api.listResources({ app, kind: 'segments', query: { limit: 200 } }), api.listResources({ app, kind: 'lookups', query: { limit: 200 } })]);
    return [...s.items, ...l.items].filter((r) => r.latestVersion > 0);
  });
  let rolling = $derived((decisions.data ?? []).filter((d) => d.live[env]?.rollout));
  let kills = $derived(killStates(history.items));
  let killed = $derived([...kills.entries()].filter(([, k]) => k.killed === true));

  function refresh(): void {
    void envs.reload();
    void history.reset();
    void approved.reload();
    void decisions.reload();
    void data.reload();
  }

  async function publishCr(id: string, title: string): Promise<void> {
    const r = await confirmAction({ title: `Publish ${id} to ${env}?`, env, envDetail: `generation ${(envInfo?.generation ?? 0) + 1}`, body: [title, `Every ${app} sidecar in ${env} swaps to the new bundle within seconds.`], ok: `Publish to ${env}`, danger: true });
    if (!r) return;
    try {
      const rel = await api.publishRelease({ app, env, body: { type: 'change_request', changeRequestId: id } });
      toast(`Published generation ${rel.generation} to ${env}`);
      refresh();
    } catch (e) {
      toastError(e, 'Not published');
    }
  }

  async function step(decision: string, percent: number, from: number, to: number): Promise<void> {
    const back = percent === 0;
    const r = await confirmAction({
      title: back ? `Roll ${decision} back to v${from}?` : percent === 100 ? `Complete the rollout of ${decision} v${to}?` : `Go to ${percent}%?`,
      env,
      envDetail: `generation ${(envInfo?.generation ?? 0) + 1}`,
      body: back ? [`All units go back to v${from}. v${to} stays available for a later rollout.`] : percent === 100 ? [`Every unit gets v${to}; v${from} leaves the bundle.`] : [`${percent}% of units get ${decision} v${to}; the rest stay on v${from}. Both versions are in the bundle, so every sidecar gives a unit the same answer.`],
      ok: back ? `Roll back to v${from}` : percent === 100 ? 'Complete rollout' : `Go to ${percent}%`,
      danger: back || env === 'prod',
    });
    if (!r) return;
    try {
      const rel = await api.publishRelease({ app, env, body: { type: 'rollout', decision, percent } });
      toast(`Published generation ${rel.generation}: ${decision} ${back ? `back on v${from}` : `v${to} at ${percent}%`}`);
      refresh();
    } catch (e) {
      toastError(e, 'Rollout not changed');
    }
  }
  let customPct = $state<Record<string, number>>({});

  // rollback to a generation
  let target = $state(0);
  async function rollback(): Promise<void> {
    const rel = history.items.find((x) => x.generation === target);
    if (!rel) return;
    const r = await confirmAction({
      title: `Roll ${env} back to generation ${target}?`,
      env,
      envDetail: `generation ${(envInfo?.generation ?? 0) + 1}`,
      body: [`Every resource returns to the versions of generation ${target} (${fmtDateTime(rel.publishedAt)}). This is a new generation, recorded as break-glass when no change request covers it.`],
      ok: `Roll back ${env}`,
      danger: true,
      reason: { label: 'Reason', required: true, placeholder: 'e.g. v17 misroutes Pune leads' },
    });
    if (!r) return;
    try {
      const out = await api.publishRelease({ app, env, body: { type: 'rollback', toGeneration: target, reason: r.reason } });
      toast(`Published generation ${out.generation}: back to generation ${target}`);
      refresh();
    } catch (e) {
      toastError(e, 'Not rolled back');
    }
  }

  // data release
  let picked = $state<Record<string, number>>({});
  let dataItems = $derived(Object.entries(picked).filter(([, v]) => v > 0).map(([k, v]) => {
    const [kind, name] = k.split('/');
    return { kind: kind as ResourceKind, name, version: v } satisfies ChangeItem;
  }));
  async function publishData(): Promise<void> {
    const r = await confirmAction({ title: `Publish data to ${env}?`, env, envDetail: `generation ${(envInfo?.generation ?? 0) + 1}`, body: [dataItems.map((i) => `${i.name} v${i.version}`).join(', '), 'Allowed without review because every dependent test passes. Logic changes still need a change request.'], ok: `Publish data to ${env}`, danger: env === 'prod' });
    if (!r) return;
    try {
      const rel = await api.publishRelease({ app, env, body: { type: 'data', items: dataItems } });
      toast(`Published generation ${rel.generation} with ${dataItems.length} data item${dataItems.length === 1 ? '' : 's'}`);
      picked = {};
      refresh();
    } catch (e) {
      toastError(e, 'Not published');
    }
  }

  let open = $state<number | null>(null);
  const manifest = query(() => (open ? api.getRelease({ app, env, generation: open }) : Promise.resolve(null)));
</script>

<PageHeader title="Releases · {env}">
  {#snippet eyebrow()}<Crumbs />{/snippet}
  {#snippet sub()}Publishing builds a signed bundle with a new generation number. Rollout steps, rollbacks, kill switches and data updates are new generations too. {#if envInfo}{env} is on <strong>generation {envInfo.generation}</strong>{envInfo.publishedAt ? `, published ${relTime(envInfo.publishedAt)}` : ''}.{/if}{/snippet}
</PageHeader>

{#if approved.data?.length}
  <section class="panel">
    <header><h2>Approved, waiting to publish</h2></header>
    <ul class="list">
      {#each approved.data as cr (cr.id)}
        <li>
          <div class="main"><a class="mono" href={appHref(`/changes/${cr.id}`)}>{cr.id}</a><span>{cr.title}</span><span class="meta">{cr.items.map((i) => `${i.name} v${i.version}${i.rollout ? ` from ${i.rollout.percent}%` : ''}`).join(', ')}</span></div>
          <button type="button" class="btn btn-danger btn-sm" onclick={() => publishCr(cr.id, cr.title)} disabled={!session.can('release')} title={session.why('release')}>Publish to {env}</button>
        </li>
      {/each}
    </ul>
  </section>
{/if}

{#each rolling as d (d.name)}
  {@const st = d.live[env]}
  {#if st?.rollout}
    {@const steps = [5, 25, 50, 100]}
    {@const next = steps.find((s) => s > st.rollout!.percent)}
    <section class="panel">
      <header><h2><span class="mono">{d.name}</span> v{st.version} rollout</h2><span class="hint">{env} · v{st.rollout.fromVersion} for the rest</span></header>
      <RolloutStepper percent={st.rollout.percent} {steps} />
      <p class="hint">Units with <code class="inl">bucket(release salt) &lt; {st.rollout.percent * 100}</code> get v{st.version}, everyone else v{st.rollout.fromVersion}.</p>
      <div class="actions">
        {#if next}<button type="button" class="btn btn-primary" onclick={() => step(d.name, next, st.rollout!.fromVersion, st.version)} disabled={!session.can('release')}>{next === 100 ? 'Complete (100%)' : `Go to ${next}%`}</button>{/if}
        <label class="sr" for="pct-{d.name}">Custom percent</label>
        <input id="pct-{d.name}" type="number" min="1" max="100" value={customPct[d.name] ?? ''} oninput={(e) => (customPct[d.name] = Number(e.currentTarget.value))} placeholder="%" style="width:80px" />
        <button type="button" class="btn" onclick={() => step(d.name, customPct[d.name], st.rollout!.fromVersion, st.version)} disabled={!session.can('release') || !(customPct[d.name] >= 1 && customPct[d.name] <= 100)}>Set</button>
        <button type="button" class="btn" onclick={() => step(d.name, 0, st.rollout!.fromVersion, st.version)} disabled={!session.can('release')}>Roll back to v{st.rollout.fromVersion}</button>
      </div>
    </section>
  {/if}
{/each}

<div class="grid-2">
  <section class="panel">
    <header><h2>Roll back to a generation</h2><span class="hint">break-glass</span></header>
    <div class="row-sm">
      <label for="rb-gen" class="sr">Generation</label>
      <select id="rb-gen" bind:value={target} style="flex:1">
        <option value={0}>— choose an earlier generation —</option>
        {#each history.items.slice(1) as r (r.generation)}<option value={r.generation}>gen {r.generation} · {fmtDateTime(r.publishedAt)} · {r.type}</option>{/each}
      </select>
      <button type="button" class="btn btn-danger" onclick={rollback} disabled={!target || !session.can('release')} title={session.why('release')}>Roll back</button>
    </div>
    <p class="hint">Approver role. Use it when the current bundle misbehaves and there is no time for review; it is recorded in the audit trail.</p>
    {#if killed.length}
      <hr class="sep" />
      <h3 class="sub-h">Kill switches on in {env}</h3>
      <ul class="list">{#each killed as [g, k] (g)}<li><div class="main"><a class="mono" href={appHref(`/gates/${g}`, { tab: 'kill' })}>{g}</a><span class="meta">since generation {k.generation}{k.reason ? ` · ${k.reason}` : ''}</span></div><span class="pill pill-crit">killed</span></li>{/each}</ul>
    {:else}
      <p class="hint">Kill switches live on each gate's <a href={appHref('/gates')}>Kill switch</a> tab.</p>
    {/if}
  </section>
  <section class="panel">
    <header><h2>Data release</h2><span class="hint">segments and lookups without review</span></header>
    {#if envInfo && !envInfo.policy.allowDataReleases}
      <p class="callout warn">{env} does not allow data releases. Open a change request instead, or turn them on in <a href={appHref('/settings/environments')}>environment settings</a>.</p>
    {:else if data.data}
      <div class="tbl-wrap"><table class="compact">
        <thead><tr><th>Resource</th><th>Live</th><th>Publish</th></tr></thead>
        <tbody>
          {#each data.data as r (r.kind + r.name)}
            {@const k = `${r.kind}/${r.name}`}
            <tr>
              <td><Kind type={r.kind} label={r.name} /></td>
              <td class="mono">{r.live[env] ? `v${r.live[env].version}` : '—'}</td>
              <td>
                <label class="sr" for="dr-{k}">Version of {r.name}</label>
                <select id="dr-{k}" value={picked[k] ?? 0} onchange={(e) => (picked[k] = Number(e.currentTarget.value))} style="width:auto">
                  <option value={0}>keep</option>
                  {#each Array.from({ length: r.latestVersion }, (_, i) => r.latestVersion - i) as v (v)}<option value={v} disabled={r.live[env]?.version === v}>v{v}{r.live[env]?.version === v ? ' (live)' : ''}</option>{/each}
                </select>
              </td>
            </tr>
          {/each}
        </tbody>
      </table></div>
      <div><button type="button" class="btn btn-primary" onclick={publishData} disabled={!dataItems.length || !session.can('edit')}>Publish {dataItems.length || ''} data item{dataItems.length === 1 ? '' : 's'}</button></div>
    {:else}<Loading />{/if}
  </section>
</div>

<section class="stack">
  <h2>Release history</h2>
  {#if history.error}<ErrorNote error={history.error} retry={history.reset} />
  {:else if !history.items.length && history.loading}<Loading />
  {:else if !history.items.length}<p class="hint">Nothing published to {env} yet. Approve a change request, then publish it here.</p>
  {:else}
    <div class="tbl-wrap">
      <table>
        <thead><tr><th class="num">Gen</th><th>When</th><th>What</th><th>By</th><th>State</th><th></th></tr></thead>
        <tbody>
          {#each history.items as r, i (r.generation)}
            <tr>
              <td class="num mono">{r.generation}</td>
              <td class="nowrap">{fmtDateTime(r.publishedAt)}</td>
              <td><ReleaseLabel release={r} />{#if r.changeRequestId}<span class="desc"><a href={appHref(`/changes/${r.changeRequestId}`)}>{r.changeRequestId}</a></span>{/if}</td>
              <td class="nowrap">{r.publishedBy.split('@')[0]}</td>
              <td><ReleaseState release={r} latest={i === 0} /></td>
              <td><button type="button" class="btn btn-xs" onclick={() => (open = open === r.generation ? null : r.generation)} aria-expanded={open === r.generation}>Manifest</button></td>
            </tr>
            {#if open === r.generation}
              <tr><td colspan="6">
                {#if manifest.data}
                  <p class="hint mono">{shortHash(manifest.data.manifestHash)} · {fmtBytes(manifest.data.bundleBytes)} · engine ≥ {manifest.data.minEngineVersion ?? '—'}</p>
                  <div class="chips" style="margin-top:6px">{#each manifest.data.contents as c (c.kind + c.name)}<span class="ver" class:live={!!c.changed}>{c.kind}/{c.name} v{c.version}{c.rollout?.rollout ? ` @${c.rollout.rollout.percent}%` : ''}</span>{/each}</div>
                {:else if manifest.error}<ErrorNote error={manifest.error} />{:else}<Loading />{/if}
              </td></tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
    <Pager next={history.next} loading={history.loading} onmore={history.more} />
  {/if}
</section>
