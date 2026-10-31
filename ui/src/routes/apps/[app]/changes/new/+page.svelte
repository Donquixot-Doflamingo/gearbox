<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import { ApiError } from '$lib/api/client';
  import type { ResourceKind } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Field from '$lib/components/Field.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { appHref, currentEnv, qp } from '$lib/nav.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { shell } from '$lib/stores/shell.svelte';
  import { toast } from '$lib/stores/toast.svelte';

  interface Item { kind: ResourceKind; name: string; version: number; rollout: boolean; percent: number }

  let app = $derived(page.params.app ?? '');
  let environment = $state(currentEnv());
  let title = $state('');
  let description = $state('');
  const KINDS: ResourceKind[] = ['decisions', 'schemas', 'segments', 'lookups', 'gates', 'experiments', 'layers'];
  let items = $state<Item[]>([]);
  let busy = $state(false);
  let err = $state<ApiError | null>(null);

  $effect(() => {
    const k = qp('kind') as ResourceKind | null, n = qp('name'), v = qp('version');
    if (k && n && items.length === 0) {
      items.push({ kind: k, name: n, version: Number(v) || 1, rollout: false, percent: 5 });
      if (!title) title = `${n} v${v}`;
    }
  });

  function summary(kind: ResourceKind, name: string) {
    return registry.list(app, kind)?.find((s) => s.name === name);
  }
  function add(): void {
    items.push({ kind: 'decisions', name: '', version: 1, rollout: false, percent: 5 });
  }
  function setName(i: number, name: string): void {
    items[i].name = name;
    items[i].version = summary(items[i].kind, name)?.latestVersion || 1;
  }
  function fieldErr(i: number, f?: string): string | undefined {
    if (!err) return undefined;
    return err.errors.filter((e) => e.path === `/items/${i}${f ? `/${f}` : ''}` || (!f && e.path.startsWith(`/items/${i}/`))).map((e) => e.message).join('; ') || undefined;
  }

  async function submit(): Promise<void> {
    busy = true;
    err = null;
    try {
      const cr = await api.createChangeRequest({
        app,
        body: { environment, title, description: description || undefined, items: items.map((it) => ({ kind: it.kind, name: it.name, version: it.version, rollout: it.kind === 'decisions' && it.rollout ? { percent: it.percent } : undefined })) },
      });
      toast(`Opened ${cr.id}${cr.status === 'approved' ? ' (no approval needed in ' + environment + ')' : ''}`);
      void goto(appHref(`/changes/${cr.id}`));
    } catch (e) {
      if (e instanceof ApiError) err = e;
      else throw e;
    } finally {
      busy = false;
    }
  }
</script>

<PageHeader title="New change request" sub="Pick the versions to make live in one environment. The server builds the would-be bundle, compiles every decision against the versions it would run with and runs every test before opening the request.">
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'change requests', href: appHref('/changes') }]} />{/snippet}
</PageHeader>

<form class="stack" onsubmit={(e) => { e.preventDefault(); void submit(); }}>
  <section class="panel">
    <div class="form-grid">
      <Field label="Target environment" id="cr-env" required>
        <select id="cr-env" bind:value={environment}>{#each shell.envs as e (e)}<option value={e}>{e}</option>{/each}</select>
      </Field>
      <Field label="Title" id="cr-title" required error={err?.fieldError('/title')} class="wide">
        <input id="cr-title" type="text" bind:value={title} maxlength="200" />
      </Field>
    </div>
    <Field label="What and why" id="cr-desc"><textarea id="cr-desc" rows="3" bind:value={description} maxlength="5000"></textarea></Field>
    {#if environment === 'prod'}<p class="env-callout" style="font:600 13px/1.4 var(--f-mono);color:var(--crit);background:var(--crit-soft);padding:8px 10px;border-radius:6px">Target: prod</p>{/if}
  </section>

  <section class="panel">
    <header><h2>Versions</h2><button type="button" class="btn btn-sm" onclick={add}>Add a version</button></header>
    {#if err?.fieldError('/items')}<span class="error-text">{err.fieldError('/items')}</span>{/if}
    {#each items as it, i (i)}
      {@const s = summary(it.kind, it.name)}
      {@const live = s?.live[environment]}
      <div class="panel tight" style="background:var(--bg)">
        <div class="form-row">
          <div class="field" style="width:150px">
            <label for="cri-k-{i}">Kind</label>
            <select id="cri-k-{i}" value={it.kind} onchange={(e) => { it.kind = e.currentTarget.value as ResourceKind; it.name = ''; }}>{#each KINDS as k (k)}<option value={k}>{k}</option>{/each}</select>
          </div>
          <div class="field" style="flex:1 1 200px">
            <label for="cri-n-{i}">Name</label>
            <select id="cri-n-{i}" value={it.name} onchange={(e) => setName(i, e.currentTarget.value)} aria-invalid={fieldErr(i, 'name') ? 'true' : undefined}>
              <option value="">— choose —</option>
              {#each registry.list(app, it.kind) ?? [] as r (r.name)}<option value={r.name} disabled={!r.latestVersion}>{r.name}{r.latestVersion ? '' : ' (no version)'}</option>{/each}
            </select>
          </div>
          <div class="field" style="width:110px">
            <label for="cri-v-{i}">Version</label>
            <select id="cri-v-{i}" value={it.version} onchange={(e) => (it.version = Number(e.currentTarget.value))} aria-invalid={fieldErr(i, 'version') ? 'true' : undefined}>
              {#each Array.from({ length: s?.latestVersion || it.version }, (_, k) => (s?.latestVersion || it.version) - k) as v (v)}<option value={v}>v{v}</option>{/each}
            </select>
          </div>
          <div class="field"><span class="label">Live in {environment}</span><span class="ver" class:none={!live}>{live ? `v${live.version}${live.rollout ? ` at ${live.rollout.percent}%` : ''}` : 'not live'}</span></div>
          <span class="spacer"></span>
          <button type="button" class="btn btn-xs" onclick={() => items.splice(i, 1)} aria-label="Remove item">Remove</button>
        </div>
        {#if it.kind === 'decisions' && live}
          <div class="row-sm">
            <label class="check"><input type="checkbox" bind:checked={it.rollout} /> Gradual rollout from v{live.version}, starting at</label>
            <input type="number" min="1" max="100" bind:value={it.percent} disabled={!it.rollout} aria-label="Start percent" style="width:80px" /><span class="hint">% of units</span>
          </div>
        {/if}
        {#if fieldErr(i)}<span class="error-text">{fieldErr(i)}</span>{/if}
      </div>
    {:else}
      <p class="hint">Add the versions to release. Cut a version first from a resource's Versions tab.</p>
    {/each}
  </section>

  {#if err && !err.errors.length}<ErrorNote error={err} />{:else if err}<p class="error-text">{err.title}: {err.detail ?? 'fix the items above'}</p>{/if}
  <div class="actions">
    <button type="submit" class="btn btn-primary" disabled={busy || !title.trim() || !items.length || items.some((it) => !it.name) || !session.can('edit')}>{busy ? 'Checking the bundle…' : `Open change request for ${environment}`}</button>
    <a class="btn" href={appHref('/changes')}>Cancel</a>
  </div>
</form>
