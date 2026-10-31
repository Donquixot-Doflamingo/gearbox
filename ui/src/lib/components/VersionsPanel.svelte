<script lang="ts">
  // Versions of any resource: history, diff (versions or draft), cut a version, ask for a release.
  import { untrack } from 'svelte';
  import { api } from '$lib/api';
  import { ApiError } from '$lib/api/client';
  import type { ResourceKind, SchemaSpec } from '$lib/api/types.gen';
  import type { DraftEditor } from '$lib/draft.svelte';
  import { fmtDateTime, shortHash } from '$lib/model/format';
  import { appHref } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import DiffView from './DiffView.svelte';
  import ErrorNote from './ErrorNote.svelte';
  import Loading from './Loading.svelte';

  let { ed, kind }: { ed: DraftEditor; kind: ResourceKind } = $props();

  const versions = query(() => {
    void ed.version;
    return api.listVersions({ app: ed.app, kind, name: ed.name, query: { limit: 200 } }).then((r) => r.items);
  });

  let from = $state('');
  let to = $state('draft');
  $effect(() => {
    if (!from && versions.data?.length) from = String(versions.data[0].version);
  });
  const diff = query(() => {
    void ed.version;
    if (!from || !to || from === to) return Promise.resolve(null);
    return api.diffVersions({ app: ed.app, kind, name: ed.name, query: { from, to } });
  });

  // cut a version
  let message = $state('');
  let allowBreaking = $state(false);
  let cutting = $state(false);
  let cutError: ApiError | null = $state(null);
  let compat: { compatible: boolean; changes: { path: string; kind: string; breaking: boolean }[] } | null = $state(null);
  let checking = $state(false);

  async function checkCompat(): Promise<void> {
    checking = true;
    try {
      compat = await api.checkSchemaCompatibility({ app: ed.app, name: ed.name, body: { spec: $state.snapshot(ed.spec) as SchemaSpec } });
    } catch (e) {
      cutError = e instanceof ApiError ? e : null;
    } finally {
      checking = false;
    }
  }

  $effect(() => {
    // re-check compatibility when the saved schema changes
    void ed.version;
    if (kind === 'schemas' && (ed.resource?.latestVersion ?? 0) > 0) untrack(() => void checkCompat());
  });

  async function cut(): Promise<void> {
    cutting = true;
    cutError = null;
    try {
      const v = await api.cutVersion({ app: ed.app, kind, name: ed.name, body: { revision: ed.revision, message: message || undefined, allowBreaking: kind === 'schemas' ? allowBreaking : undefined } });
      toast(`Cut ${ed.name} v${v.version}`);
      message = '';
      allowBreaking = false;
      registry.invalidate(ed.app, kind, ed.name);
      if (kind === 'schemas') delete registry.schemas[`${ed.app}/${ed.name}@${v.version}`];
      await ed.load();
      from = String(v.version);
      to = 'draft';
    } catch (e) {
      if (e instanceof ApiError) cutError = e;
      else throw e;
    } finally {
      cutting = false;
    }
  }

  let breaking = $derived(compat?.changes.filter((c) => c.breaking) ?? []);
  let liveByVersion = $derived.by(() => {
    const m = new Map<number, string[]>();
    for (const [env, st] of Object.entries(ed.resource?.live ?? {})) {
      m.set(st.version, [...(m.get(st.version) ?? []), env]);
      if (st.rollout) m.set(st.rollout.fromVersion, [...(m.get(st.rollout.fromVersion) ?? []), `${env} (rest)`]);
    }
    return m;
  });
  let cutBlocked = $derived(ed.dirty || cutting || !session.can('edit') || (kind === 'schemas' && breaking.length > 0 && !allowBreaking));
</script>

<div class="grid-2">
  <section class="panel">
    <header><h2>Cut a version</h2><span class="hint">from draft revision {ed.revision}</span></header>
    <p class="hint">Versions are immutable. {kind === 'decisions' ? 'The draft must validate and every test case must pass.' : 'The draft must validate.'} Only versions can be released.</p>
    {#if kind === 'schemas' && (ed.resource?.latestVersion ?? 0) > 0}
      <div class="stack-sm">
        <div class="row-sm"><h3 class="sub-h">Compatibility with v{ed.resource?.latestVersion}</h3><button type="button" class="btn btn-xs" onclick={checkCompat} disabled={checking}>{checking ? 'Checking…' : 'Check again'}</button></div>
        {#if compat}
          {#if compat.changes.length === 0}
            <p class="result-line ok">No changes since v{ed.resource?.latestVersion}.</p>
          {:else}
            <p class="result-line" class:ok={compat.compatible} class:bad={!compat.compatible}>{compat.compatible ? 'Backward compatible' : `${breaking.length} breaking change${breaking.length === 1 ? '' : 's'}`}</p>
            <ul class="problems">
              {#each compat.changes as c (c.path + c.kind)}
                <li class={c.breaking ? 'error' : 'warning'}><span class="pm" aria-hidden="true">{c.breaking ? '✕' : '+'}</span><span>{c.kind.replace(/_/g, ' ')} <span class="pp">{c.path}</span>{c.breaking ? '' : ' (compatible)'}</span></li>
              {/each}
            </ul>
            {#if breaking.length}
              <label class="check"><input type="checkbox" bind:checked={allowBreaking} /> Allow breaking changes. Decisions on v{ed.resource?.latestVersion} keep using it until they are updated.</label>
            {/if}
          {/if}
        {/if}
        <p class="hint">The check compares the saved draft; save first to include unsaved edits.</p>
      </div>
    {/if}
    <div class="field">
      <label for="cut-msg">What changed</label>
      <input id="cut-msg" type="text" bind:value={message} maxlength="500" placeholder="e.g. Widen band A to 8.5 lakh" />
    </div>
    <div class="actions">
      <button type="button" class="btn btn-primary" onclick={cut} disabled={cutBlocked}>{cutting ? 'Cutting…' : `Cut v${(ed.resource?.latestVersion ?? 0) + 1}`}</button>
      {#if ed.dirty}<span class="hint warn">Save the draft first.</span>{/if}
    </div>
    {#if cutError}<ErrorNote error={cutError} title="Version not cut: {cutError.title}" />{/if}
  </section>

  <section class="panel">
    <header><h2>Versions</h2><span class="hint">newest first</span></header>
    {#if versions.error}<ErrorNote error={versions.error} retry={versions.reload} />
    {:else if !versions.data}<Loading />
    {:else if versions.data.length === 0}<p class="hint">No versions yet. Cut v1 when the draft is ready.</p>
    {:else}
      <div class="tbl-wrap">
        <table class="compact">
          <thead><tr><th>Version</th><th>Message</th><th>By</th><th>Live</th><th></th></tr></thead>
          <tbody>
            {#each versions.data as v (v.version)}
              {@const envs = liveByVersion.get(v.version) ?? []}
              <tr>
                <td class="nowrap"><span class="ver" class:live={envs.some((e) => e.startsWith('prod'))}>v{v.version}</span>{#if v.breaking}<span class="pill pill-warn" style="margin-left:6px">breaking</span>{/if}</td>
                <td>{v.message ?? ''}<span class="desc mono" title={v.contentHash}>{shortHash(v.contentHash)} · {fmtDateTime(v.createdAt)}</span></td>
                <td class="nowrap">{v.createdBy}</td>
                <td class="mono nowrap">{envs.join(', ') || '—'}</td>
                <td class="nowrap"><a class="btn btn-xs" href={appHref('/changes/new', { kind, name: ed.name, version: v.version })}>Request release</a></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
</div>

<section class="panel">
  <header>
    <h2>Diff</h2>
    <div class="row-sm">
      <label for="diff-from" class="hint">From</label>
      <select id="diff-from" bind:value={from}>
        <option value="draft">draft</option>
        {#each versions.data ?? [] as v (v.version)}<option value={String(v.version)}>v{v.version}</option>{/each}
      </select>
      <label for="diff-to" class="hint">to</label>
      <select id="diff-to" bind:value={to}>
        <option value="draft">draft</option>
        {#each versions.data ?? [] as v (v.version)}<option value={String(v.version)}>v{v.version}</option>{/each}
      </select>
    </div>
  </header>
  {#if from === to}<p class="hint">Pick two different versions.</p>
  {:else if diff.error}<ErrorNote error={diff.error} />
  {:else if diff.loading && !diff.data}<Loading />
  {:else if diff.data}
    <DiffView diff={diff.data.unified} empty="These two are identical." />
    {#if ed.dirty && (from === 'draft' || to === 'draft')}<p class="hint">The draft side is the saved draft; unsaved edits are not included.</p>{/if}
  {/if}
</section>
