<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import { ApiError } from '$lib/api/client';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Field from '$lib/components/Field.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import { fmtDate } from '$lib/model/format';
  import { href } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast } from '$lib/stores/toast.svelte';

  let app = $derived(page.params.app ?? '');
  const info = query(() => api.getApp({ app }));
  const all = query(() => api.listApps({ query: { limit: 200 } }).then((r) => r.items));
  let description = $state('');
  let owners = $state('');
  let limit = $state(64);
  let err = $state<ApiError | null>(null);
  let busy = $state(false);
  $effect(() => {
    const a = info.data;
    if (!a) return;
    description = a.description ?? '';
    owners = (a.owners ?? []).join(', ');
    limit = a.bundleLimitMB ?? 64;
  });

  async function save(): Promise<void> {
    busy = true;
    err = null;
    try {
      info.data = await api.updateApp({ app, body: { description, owners: owners.split(',').map((s) => s.trim()).filter(Boolean), bundleLimitMB: limit } });
      toast(`Saved ${app}`);
    } catch (e) {
      if (e instanceof ApiError) err = e;
    } finally {
      busy = false;
    }
  }
  let admin = $derived(session.can('admin'));
</script>

<div class="grid-2">
  <section class="panel">
    <header><h2>{app}</h2>{#if info.data}<span class="hint">created {fmtDate(info.data.createdAt)} · {info.data.environments.join(' · ')}</span>{/if}</header>
    {#if info.error}<ErrorNote error={info.error} retry={info.reload} />
    {:else if !info.data}<Loading />
    {:else}
      <form class="stack" onsubmit={(e) => { e.preventDefault(); void save(); }}>
        <fieldset disabled={!admin || busy} class="stack">
          <Field label="Description" id="as-desc"><input id="as-desc" type="text" bind:value={description} /></Field>
          <Field label="Owners" id="as-owners" hint="Comma-separated team names"><input id="as-owners" type="text" bind:value={owners} /></Field>
          <Field label="Bundle limit (MB)" id="as-limit" error={err?.fieldError('/bundleLimitMB')} hint="Publishing fails with BUNDLE_TOO_LARGE above this."><input id="as-limit" type="number" min="1" max="256" bind:value={limit} /></Field>
        </fieldset>
        {#if err && !err.errors.length}<ErrorNote error={err} />{/if}
        <div class="actions"><button type="submit" class="btn btn-primary" disabled={!admin || busy}>Save</button>{#if !admin}<span class="hint">{session.why('admin')}</span>{/if}</div>
      </form>
    {/if}
  </section>
  <section class="panel">
    <header><h2>All apps</h2><a class="hint" href={href('/apps')}>Manage apps</a></header>
    <ul class="list">
      {#each all.data ?? [] as a (a.name)}<li><div class="main"><a class="mono" href={href(`/apps/${a.name}?env=prod`)}>{a.name}</a><span class="meta">{a.description ?? ''}</span></div><span class="hint">{(a.owners ?? []).join(', ')}</span></li>{/each}
    </ul>
  </section>
</div>
