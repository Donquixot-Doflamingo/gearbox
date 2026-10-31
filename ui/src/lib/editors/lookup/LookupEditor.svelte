<script lang="ts">
  import { untrack } from 'svelte';
  import { api } from '$lib/api';
  import type { LookupRow, LookupSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import DraftBar from '$lib/components/DraftBar.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import UsagesPanel from '$lib/components/UsagesPanel.svelte';
  import VersionsPanel from '$lib/components/VersionsPanel.svelte';
  import { DraftEditor } from '$lib/draft.svelte';
  import { fmtNum } from '$lib/model/format';
  import { appHref, qp } from '$lib/nav.svelte';
  import { Paged } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast } from '$lib/stores/toast.svelte';

  let { app, name }: { app: string; name: string } = $props();
  const ed = new DraftEditor<LookupSpec>(app, 'lookups', name);
  void ed.load();
  $effect(() => {
    JSON.stringify(ed.spec);
    if (ed.spec) untrack(() => ed.scheduleValidate());
  });

  let tab = $derived(qp('tab') ?? 'rows');
  let canEdit = $derived(session.can('edit'));
  let total = $state(0);
  const rows = new Paged<LookupRow>((cursor) => {
    void ed.version;
    return api.listLookupRows({ app, name, query: { cursor, limit: 100 } }).then((r) => {
      total = r.total;
      return r;
    });
  });

  let key = $state('');
  let keyResult: LookupRow | null | undefined = $state(undefined);
  async function checkKey(): Promise<void> {
    const r = await api.listLookupRows({ app, name, query: { key } });
    keyResult = r.items[0] ?? null;
  }

  let uploading = $state(false);
  let uploadError: unknown = $state(null);
  let paste = $state('');
  async function upload(body: string | { rows: LookupRow[] }): Promise<void> {
    uploading = true;
    uploadError = null;
    try {
      const r = await api.replaceLookupRows({ app, name, body });
      toast(`Stored ${fmtNum(r.total)} rows in the draft. Cut a version to release them.`);
      paste = '';
      await ed.load();
    } catch (e) {
      uploadError = e;
    } finally {
      uploading = false;
    }
  }
  async function onFile(e: Event): Promise<void> {
    const input = e.currentTarget as HTMLInputElement;
    const f = input.files?.[0];
    input.value = '';
    if (!f) return;
    const text = await f.text();
    if (/\.json$/i.test(f.name) || text.trim().startsWith('[') || text.trim().startsWith('{')) {
      try {
        const v = JSON.parse(text);
        await upload({ rows: Array.isArray(v) ? v : v.rows });
      } catch (err) {
        uploadError = err;
      }
    } else await upload(text);
  }
  const TABS = [['rows', 'Rows'], ['settings', 'Settings'], ['versions', 'Versions'], ['usages', 'Used by']] as const;
</script>

<PageHeader title={name}>
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'lookups', href: appHref('/lookups') }]} />{/snippet}
  {#snippet sub()}{ed.description || 'Lookup table'}{#if ed.spec} · key {ed.spec.keyType} → {ed.spec.valueSchema?.name}@{ed.spec.valueSchema?.version}{/if}{/snippet}
</PageHeader>

{#if ed.loadError}
  <ErrorNote error={ed.loadError} retry={() => ed.load()} />
{:else if ed.loading && !ed.resource}
  <Loading />
{:else if ed.spec}
  <DraftBar {ed} />
  <nav class="tabs" aria-label="Lookup sections">
    {#each TABS as [id, label] (id)}<a href={appHref(`/lookups/${name}`, { tab: id === 'rows' ? undefined : id })} aria-current={tab === id ? 'page' : undefined}>{label}</a>{/each}
  </nav>
  {#if tab === 'rows'}
    <div class="grid-2">
      <section class="panel">
        <header><h2>Key preview</h2><span class="hint">{fmtNum(total)} rows in the draft</span></header>
        {#if rows.error}<ErrorNote error={rows.error} retry={rows.reset} />{/if}
        <div class="tbl-wrap"><table class="compact">
          <thead><tr><th>key</th><th>value</th></tr></thead>
          <tbody>
            {#each rows.items as r (r.key)}<tr><td class="mono">{r.key}</td><td class="mono wrap-any">{JSON.stringify(r.value)}</td></tr>{:else}<tr><td colspan="2" class="hint">{rows.loading ? 'Loading…' : 'No rows yet. Upload a CSV.'}</td></tr>{/each}
          </tbody>
        </table></div>
        <Pager next={rows.next} loading={rows.loading} onmore={rows.more} count={rows.items.length} {total} />
      </section>
      <div class="stack">
        <section class="panel">
          <header><h2>Check a key</h2></header>
          <div class="row-sm">
            <label class="sr" for="lk-key">Key</label>
            <input id="lk-key" type="text" class="code grow" bind:value={key} onkeydown={(e) => { if (e.key === 'Enter') void checkKey(); }} placeholder="Gurgaon" />
            <button type="button" class="btn" onclick={checkKey} disabled={!key}>Look up</button>
          </div>
          {#if keyResult !== undefined}
            <p class="result-line" class:ok={!!keyResult} aria-live="polite">{keyResult ? `lookup("${name}", "${keyResult.key}") = ${JSON.stringify(keyResult.value)}` : `${key} is not in the draft rows; a lookup node returns its default`}</p>
          {/if}
        </section>
        <section class="panel">
          <header><h2>Upload rows</h2><span class="hint">replaces the draft's rows</span></header>
          {#if ed.dirty}<p class="callout warn">Save or discard your draft edits first.</p>{/if}
          <fieldset disabled={!canEdit || uploading || ed.dirty} class="stack">
            <label class="btn" style="align-self:flex-start">Choose a CSV or JSON file<input type="file" accept=".csv,.json,text/csv,application/json" class="sr" onchange={onFile} /></label>
            <div class="field"><label for="lk-paste">…or paste CSV</label><textarea id="lk-paste" class="code" rows="5" bind:value={paste} placeholder={'key,value\nDelhi,{"zone":"NCR-1","hub":"Okhla"}'}></textarea></div>
            <div><button type="button" class="btn btn-primary" onclick={() => upload(paste)} disabled={!paste.trim()}>Replace rows</button></div>
          </fieldset>
          <p class="hint">CSV columns <code class="inl">key,value</code>; a value is JSON or plain text. Every value must match {ed.spec.valueSchema?.name}@{ed.spec.valueSchema?.version}.</p>
          {#if uploadError}<ErrorNote error={uploadError} title="Rows not stored" />{/if}
        </section>
      </div>
    </div>
  {:else if tab === 'settings'}
    <section class="panel">
      <fieldset disabled={!canEdit} class="stack" style="max-width:640px">
        <div class="field"><label for="lk-desc">Description</label><input id="lk-desc" type="text" bind:value={ed.description} /></div>
        <div class="field"><label for="lk-kt">Key type</label><select id="lk-kt" bind:value={ed.spec.keyType}><option value="string">string</option><option value="number">number</option></select></div>
        <SchemaRefPicker {app} bind:value={ed.spec.valueSchema} label="Value schema" required error={ed.problemsUnder('/valueSchema')[0]?.message} />
      </fieldset>
      <Problems problems={ed.problems} showPath />
    </section>
  {:else if tab === 'versions'}
    <VersionsPanel {ed} kind="lookups" />
  {:else if tab === 'usages'}
    <UsagesPanel {app} kind="lookups" {name} />
  {/if}
{/if}
