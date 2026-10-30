<script lang="ts">
  import { untrack } from 'svelte';
  import { api } from '$lib/api';
  import type { SegmentSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import DraftBar from '$lib/components/DraftBar.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import JsonEditor from '$lib/components/JsonEditor.svelte';
  import Kind from '$lib/components/Kind.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import UsagesPanel from '$lib/components/UsagesPanel.svelte';
  import VersionsPanel from '$lib/components/VersionsPanel.svelte';
  import ConditionBuilder from '$lib/editors/condition/ConditionBuilder.svelte';
  import { DraftEditor } from '$lib/draft.svelte';
  import { parseMemberList } from '$lib/model/csv';
  import { fmtNum } from '$lib/model/format';
  import { exampleFor, listFields } from '$lib/model/schemaTree';
  import { appHref, qp } from '$lib/nav.svelte';
  import { Paged } from '$lib/query.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  let { app, name }: { app: string; name: string } = $props();

  const ed = new DraftEditor<SegmentSpec>(app, 'segments', name);
  void ed.load();
  $effect(() => {
    JSON.stringify(ed.spec);
    if (ed.spec) untrack(() => ed.scheduleValidate());
  });

  let tab = $derived(qp('tab') ?? 'definition');
  let canEdit = $derived(session.can('edit'));
  let s = $derived(ed.spec);
  let attrRef = $derived(s?.dynamic?.attributesSchema);
  let attrSchema = $derived(registry.schema(app, attrRef?.name, attrRef?.version));
  let attrFields = $derived(attrSchema ? listFields(attrSchema) : null);
  let TABS = $derived([
    ['definition', 'Definition'],
    ...(s?.type === 'static' ? [['members', 'Members']] : []),
    ['check', 'Check membership'],
    ['versions', 'Versions'],
    ['usages', 'Used by'],
  ] as [string, string][]);

  // members (static)
  let prefix = $state('');
  let total = $state(0);
  const members = new Paged<string>((cursor) => {
    void ed.version;
    return api.listSegmentMembers({ app, name, query: { cursor, limit: 100, prefix: prefix || undefined } }).then((r) => {
      total = r.total;
      return r;
    });
  });
  let paste = $state('');
  let uploading = $state(false);
  let uploadError: unknown = $state(null);

  async function upload(body: string | { members: string[] }): Promise<void> {
    uploading = true;
    uploadError = null;
    try {
      const r = await api.replaceSegmentMembers({ app, name, body });
      toast(`Stored ${fmtNum(r.total)} members in the draft (revision ${r.revision}). Cut a version to release them.`);
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
        await upload({ members: parseMemberList(text) });
      } catch (err) {
        toastError(err, 'Could not read the file');
      }
    } else await upload(text);
  }

  // membership check
  let checkSource = $state('draft');
  let checkKey = $state('');
  let checkAttrs: unknown = $state(undefined);
  let checkResult: { member: boolean; reason?: string } | null = $state(null);
  let checkError: unknown = $state(null);
  $effect(() => {
    if (checkAttrs === undefined && attrSchema) checkAttrs = exampleFor(attrSchema);
  });
  async function check(): Promise<void> {
    checkError = null;
    try {
      const body = s.type === 'static' ? { key: checkKey } : s.type === 'dynamic' ? { attributes: $state.snapshot(checkAttrs) as Record<string, unknown> } : { unit: checkKey };
      checkResult = await api.checkSegmentMembership({ app, name, body: { ...body, source: checkSource === 'draft' ? 'draft' : 'version', version: checkSource === 'draft' ? undefined : Number(checkSource) } });
    } catch (e) {
      checkError = e;
      checkResult = null;
    }
  }
</script>

<PageHeader title={name}>
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'segments', href: appHref('/segments') }]} />{/snippet}
  {#snippet sub()}{#if s}<Kind type={s.type} label="{s.type} segment" />{/if} {ed.description}{/snippet}
</PageHeader>

{#if ed.loadError}
  <ErrorNote error={ed.loadError} retry={() => ed.load()} />
{:else if ed.loading && !ed.resource}
  <Loading />
{:else if s}
  <DraftBar {ed} />
  <nav class="tabs" aria-label="Segment sections">
    {#each TABS as [id, label] (id)}<a href={appHref(`/segments/${name}`, { tab: id === 'definition' ? undefined : id })} aria-current={tab === id ? 'page' : undefined}>{label}</a>{/each}
  </nav>

  {#if tab === 'definition'}
    <section class="panel">
      <fieldset disabled={!canEdit} class="stack">
        <div class="field" style="max-width:640px"><label for="sg-desc">Description</label><input id="sg-desc" type="text" bind:value={ed.description} /></div>
        {#if s.type === 'static'}
          <div class="form-grid">
            <div class="field">
              <label for="sg-kt">Key type</label>
              <select id="sg-kt" value={s.static?.keyType ?? 'string'} onchange={(e) => (s.static = { ...s.static, keyType: e.currentTarget.value as 'string' | 'number' })}><option value="string">string</option><option value="number">number</option></select>
            </div>
            <label class="check" style="align-self:end;padding-bottom:8px"><input type="checkbox" checked={!!s.static?.pii} onchange={(e) => (s.static = { ...s.static, pii: e.currentTarget.checked })} /> PII: store and ship members as HMACs</label>
          </div>
          <p class="hint">{fmtNum(s.static?.memberCount ?? 0)} members in the draft. Upload them on the <a href={appHref(`/segments/${name}`, { tab: 'members' })}>Members</a> tab. Changing PII applies to the next upload.</p>
        {:else if s.type === 'dynamic'}
          <SchemaRefPicker {app} value={s.dynamic?.attributesSchema} onchange={(v) => (s.dynamic = { when: s.dynamic?.when ?? { all: [] }, attributesSchema: v! })} label="Attributes schema" required error={ed.problemsUnder('/dynamic/attributesSchema')[0]?.message} hint="Rules map each attribute to an expression over their input." />
          <div class="stack-sm">
            <h3 class="sub-h">Member when</h3>
            {#if s.dynamic}
              <ConditionBuilder bind:value={s.dynamic.when} {app} fields={attrFields} subject={attrRef ? `${attrRef.name}@${attrRef.version}` : 'the attributes'} pointer="/dynamic/when" problems={ed.problems} exprBase="attributes" />
            {/if}
          </div>
        {:else}
          <div class="form-row">
            <div class="field" style="flex:1 1 260px">
              <label for="sg-pct">Percent of units: {s.rollout?.percent ?? 0}%</label>
              <input id="sg-pct" type="range" min="0" max="100" step="0.5" value={s.rollout?.percent ?? 0} oninput={(e) => (s.rollout = { ...s.rollout, percent: Number(e.currentTarget.value) })} />
            </div>
            <div class="field" style="width:120px"><label for="sg-pctn">Exact</label><input id="sg-pctn" type="number" min="0" max="100" step="0.01" value={s.rollout?.percent ?? 0} oninput={(e) => (s.rollout = { ...s.rollout, percent: Number(e.currentTarget.value) })} /></div>
            <div class="field grow"><label for="sg-salt">Salt</label><input id="sg-salt" type="text" class="code" value={s.rollout?.salt ?? ''} placeholder={name} oninput={(e) => (s.rollout = { percent: s.rollout?.percent ?? 0, ...s.rollout, salt: e.currentTarget.value || undefined })} /></div>
          </div>
          <p class="hint">Units with <code class="inl">bucket = murmur3(salt + key) mod 10000 &lt; {Math.round((s.rollout?.percent ?? 0) * 100)}</code> are members. Raising the percent keeps existing members; changing the salt reshuffles everyone.</p>
          <div class="stack-sm">
            <h3 class="sub-h">Only units matching (optional targeting, over attributes)</h3>
            {#if s.rollout}
              <ConditionBuilder bind:value={s.rollout.include} {app} fields={null} subject="the attributes" pointer="/rollout/include" problems={ed.problems} allowEmpty exprBase="attributes" />
            {/if}
          </div>
        {/if}
      </fieldset>
      <Problems problems={ed.problems.filter((p) => !p.path.includes('/when') && !p.path.includes('/include'))} showPath />
    </section>
  {:else if tab === 'members' && s.type === 'static'}
    <div class="grid-2">
      <section class="panel">
        <header><h2>Upload members</h2><span class="hint">replaces the draft's members</span></header>
        {#if ed.dirty}<p class="callout warn">Save or discard your draft edits first: uploading creates a new draft revision.</p>{/if}
        <fieldset disabled={!canEdit || uploading || ed.dirty} class="stack">
          <label class="btn" style="align-self:flex-start">Choose a CSV or JSON file<input type="file" accept=".csv,.txt,.json,text/csv,application/json" class="sr" onchange={onFile} /></label>
          <div class="field">
            <label for="sg-paste">…or paste one member per line</label>
            <textarea id="sg-paste" class="code" rows="6" bind:value={paste} placeholder={'key\nLeh\nKargil'}></textarea>
          </div>
          <div><button type="button" class="btn btn-primary" onclick={() => upload(paste)} disabled={!paste.trim()}>{uploading ? 'Uploading…' : 'Replace members'}</button></div>
        </fieldset>
        <p class="hint">CSV: one member per line, optional header <code class="inl">key</code>. JSON: a list or <code class="inl">{'{"members": [...]}'}</code>. Up to 1,000,000 members.{s.static?.pii ? ' This segment is PII: members are stored as HMACs, so the list below shows hashes.' : ''}</p>
        {#if uploadError}<ErrorNote error={uploadError} />{/if}
      </section>
      <section class="panel">
        <header><h2>Members</h2><span class="hint">{fmtNum(total)} in the draft</span></header>
        <div class="field"><label for="sg-prefix">Starts with</label><input id="sg-prefix" type="search" bind:value={prefix} /></div>
        {#if members.error}<ErrorNote error={members.error} retry={members.reset} />{/if}
        <div class="tbl-wrap"><table class="compact"><tbody>
          {#each members.items as m (m)}<tr><td class="mono">{m}</td></tr>{:else}<tr><td class="hint">{members.loading ? 'Loading…' : 'No members.'}</td></tr>{/each}
        </tbody></table></div>
        <Pager next={members.next} loading={members.loading} onmore={members.more} count={members.items.length} {total} />
      </section>
    </div>
  {:else if tab === 'check'}
    <div class="grid-2">
      <section class="panel">
        <header>
          <h2>Is it a member?</h2>
          <select aria-label="Check against" bind:value={checkSource} style="width:auto">
            <option value="draft">saved draft</option>
            {#each Array.from({ length: ed.resource?.latestVersion ?? 0 }, (_, i) => (ed.resource?.latestVersion ?? 0) - i) as v (v)}<option value={String(v)}>v{v}</option>{/each}
          </select>
        </header>
        {#if s.type === 'dynamic'}
          <JsonEditor bind:value={checkAttrs} label="Attributes" rows={8} schema={attrSchema ?? null} />
        {:else}
          <div class="field"><label for="sg-key">{s.type === 'static' ? 'Key' : 'Unit'}</label><input id="sg-key" type="text" class="code" bind:value={checkKey} onkeydown={(e) => { if (e.key === 'Enter') void check(); }} /></div>
        {/if}
        <div><button type="button" class="btn btn-primary" onclick={check}>Check</button></div>
      </section>
      <section class="panel" aria-live="polite">
        <header><h2>Result</h2></header>
        {#if checkError}<ErrorNote error={checkError} />
        {:else if checkResult}
          <p class="result-line" class:ok={checkResult.member} class:bad={!checkResult.member}>{checkResult.member ? 'member' : 'not a member'}{checkResult.reason ? ` · ${checkResult.reason}` : ''}</p>
        {:else}<p class="hint">Enter a {s.type === 'static' ? 'key' : s.type === 'rollout' ? 'unit' : 'set of attributes'} and press Check.</p>{/if}
      </section>
    </div>
  {:else if tab === 'versions'}
    <VersionsPanel {ed} kind="segments" />
  {:else if tab === 'usages'}
    <UsagesPanel {app} kind="segments" {name} />
  {/if}
{/if}
