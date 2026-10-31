<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { UpdateLogRecord, UpdateResult } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import { fmtDateTime } from '$lib/model/format';
  import { currentEnv, qp, setQuery } from '$lib/nav.svelte';
  import { Paged } from '$lib/query.svelte';

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());
  let result = $derived((qp('result') ?? '') as UpdateResult | '');
  const list = new Paged<UpdateLogRecord>((cursor) => api.listUpdateLogs({ app, env, query: { result: result || undefined, cursor, limit: 50 } }));
  const RESULTS: ['' | UpdateResult, string][] = [['', 'all'], ['swapped', 'swapped'], ['kept_previous', 'kept previous'], ['loaded_from_disk', 'loaded from disk'], ['up_to_date', 'up to date']];
</script>

<PageHeader title="Update logs" sub="What each sidecar did when a new generation appeared: swapped to it, kept the previous bundle (and why), or loaded from disk after a restart.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
</PageHeader>

<div class="chips" role="group" aria-label="Result">
  {#each RESULTS as [v, l] (v)}<button type="button" class="chip" aria-pressed={result === v} onclick={() => setQuery({ result: v || undefined })}>{l}</button>{/each}
</div>

{#if list.error}<ErrorNote error={list.error} retry={list.reset} />
{:else if list.loading && !list.items.length}<Loading />
{:else if !list.items.length}<p class="hint">No update records in {env}.</p>
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>When</th><th>Sidecar</th><th>Generation</th><th>Result</th><th>Changed</th><th class="num">Download</th><th class="num">Compile</th><th>Signature</th></tr></thead>
      <tbody>
        {#each list.items as u, i (i)}
          <tr>
            <td class="nowrap">{fmtDateTime(u.ts)}</td>
            <td class="mono">{u.sidecarId}</td>
            <td class="mono nowrap">{u.fromGeneration} → {u.toGeneration}</td>
            <td><span class="pill {u.result === 'swapped' ? 'pill-ok' : u.result === 'kept_previous' ? 'pill-crit' : 'pill-muted'}">{u.result.replace(/_/g, ' ')}</span>{#if u.error}<span class="desc" style="color:var(--crit)">{u.error}</span>{/if}</td>
            <td class="mono">{(u.changed ?? []).map((c) => `${c.name} ${c.from ?? '∅'}→${c.to}`).join(', ') || '—'}</td>
            <td class="num mono">{u.downloadMs ?? '—'} ms</td>
            <td class="num mono">{u.compileMs ?? '—'} ms</td>
            <td><span class="pill {u.signature === 'valid' ? 'pill-ok' : u.signature ? 'pill-crit' : 'pill-muted'}">{u.signature ?? '—'}</span></td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <Pager next={list.next} loading={list.loading} onmore={list.more} />
{/if}
