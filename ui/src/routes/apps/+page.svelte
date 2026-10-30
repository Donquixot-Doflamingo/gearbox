<script lang="ts">
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';
  import { ApiError } from '$lib/api/client';
  import Dialog from '$lib/components/Dialog.svelte';
  import EmptyState from '$lib/components/EmptyState.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Field from '$lib/components/Field.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import SessionMenu from '$lib/components/SessionMenu.svelte';
  import { fmtDate } from '$lib/model/format';
  import { href } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { mode } from '$lib/stores/mode.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast } from '$lib/stores/toast.svelte';

  const apps = query(() => api.listApps({ query: { limit: 200 } }).then((r) => r.items));
  $effect(() => {
    void session.load();
  });

  let open = $state(false);
  let name = $state('');
  let description = $state('');
  let owners = $state('');
  let busy = $state(false);
  let err: ApiError | null = $state(null);

  async function create(): Promise<void> {
    busy = true;
    err = null;
    try {
      const a = await api.createApp({ body: { name: name.trim(), description: description.trim() || undefined, owners: owners.split(',').map((s) => s.trim()).filter(Boolean) } });
      toast(`Created ${a.name} with ${a.environments.join(', ')}`);
      open = false;
      void goto(href(`/apps/${a.name}?env=dev`));
    } catch (e) {
      if (e instanceof ApiError) err = e;
    } finally {
      busy = false;
    }
  }
</script>

<header class="topbar">
  <a class="brand" href={href('/apps')}>
    <svg width="22" height="22" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="4" stroke-dasharray="3.1 3.18" /><circle cx="12" cy="12" r="6.2" fill="none" stroke="currentColor" stroke-width="2.2" /><circle cx="12" cy="12" r="2" fill="currentColor" /></svg>
    <span>Gearbox</span>
  </a>
  <span class="spacer"></span>
  {#if mode.mock}<span class="sample-tag">Sample data</span>{/if}
  <SessionMenu />
</header>
<main id="main" class="layout" style="grid-template-columns:minmax(0,1fr);max-width:960px">
  <div class="page-head">
    <div>
      <h1>Apps</h1>
      <p class="sub">An app is a namespace and a bundle: every service that uses the same decisions shares one app.</p>
    </div>
    <div class="actions">
      <button type="button" class="btn btn-primary" onclick={() => (open = true)} disabled={!session.can('admin')} title={session.why('admin')}>New app</button>
    </div>
  </div>
  {#if apps.error}
    <ErrorNote error={apps.error} retry={apps.reload} />
  {:else if !apps.data}
    <Loading />
  {:else if apps.data.length === 0}
    <EmptyState title="No apps yet">
      Create the first app. It gets dev, stage and prod environments with default approval policies.
      {#snippet actions()}<button type="button" class="btn btn-primary" onclick={() => (open = true)} disabled={!session.can('admin')}>New app</button>{/snippet}
    </EmptyState>
  {:else}
    <div class="tbl-wrap">
      <table>
        <thead><tr><th>App</th><th>Environments</th><th>Owners</th><th>Created</th></tr></thead>
        <tbody>
          {#each apps.data as a (a.name)}
            <tr>
              <td><a class="name" href={href(`/apps/${a.name}?env=${a.environments.includes('prod') ? 'prod' : a.environments[0]}`)}>{a.name}</a><span class="desc">{a.description ?? ''}</span></td>
              <td class="mono">{a.environments.join(' · ')}</td>
              <td>{(a.owners ?? []).join(', ') || '—'}</td>
              <td class="muted nowrap">{fmtDate(a.createdAt)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</main>

<Dialog bind:open title="New app">
  <form class="stack" onsubmit={(e) => { e.preventDefault(); void create(); }} id="new-app">
    <Field label="Name" id="app-name" required hint="a–z, 0–9 and _; e.g. lead_service" error={err?.fieldError('/name')}>
      <input id="app-name" type="text" bind:value={name} required pattern={'[a-z][a-z0-9_]{1,62}'} autocomplete="off" data-autofocus />
    </Field>
    <Field label="Description" id="app-desc"><input id="app-desc" type="text" bind:value={description} /></Field>
    <Field label="Owners" id="app-owners" hint="Comma-separated team names"><input id="app-owners" type="text" bind:value={owners} /></Field>
    {#if err && !err.errors.length}<ErrorNote error={err} />{/if}
  </form>
  {#snippet actions()}
    <button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
    <button type="submit" form="new-app" class="btn btn-primary" disabled={busy || !name.trim()}>Create app</button>
  {/snippet}
</Dialog>
