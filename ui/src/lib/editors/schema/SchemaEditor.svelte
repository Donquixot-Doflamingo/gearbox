<script lang="ts">
  import { api } from '$lib/api';
  import type { SchemaSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import DraftBar from '$lib/components/DraftBar.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import JsonEditor from '$lib/components/JsonEditor.svelte';
  import JsonView from '$lib/components/JsonView.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import UsagesPanel from '$lib/components/UsagesPanel.svelte';
  import VersionsPanel from '$lib/components/VersionsPanel.svelte';
  import { DraftEditor } from '$lib/draft.svelte';
  import { exampleFor, listFields, primaryType, validateDocument } from '$lib/model/schemaTree';
  import { appHref, qp } from '$lib/nav.svelte';
  import { session } from '$lib/stores/session.svelte';
  import SchemaTreeEditor from './SchemaTreeEditor.svelte';

  let { app, name }: { app: string; name: string } = $props();

  const ed = new DraftEditor<SchemaSpec>(app, 'schemas', name);
  void ed.load();

  $effect(() => {
    JSON.stringify(ed.spec);
    if (ed.spec) ed.scheduleValidate();
  });

  const TABS = [
    ['editor', 'Editor'],
    ['json', 'JSON'],
    ['examples', 'Examples'],
    ['validate', 'Validate a document'],
    ['versions', 'Versions'],
    ['usages', 'Used by'],
  ] as const;
  let tab = $derived(qp('tab') ?? 'editor');

  // validate a document
  let docVersion = $state('draft');
  let doc: unknown = $state({});
  let docResult: { valid: boolean; errors: { path: string; message: string }[] } | null = $state(null);
  let docBusy = $state(false);
  let docError: unknown = $state(null);
  async function checkDoc(): Promise<void> {
    docBusy = true;
    docError = null;
    try {
      if (docVersion === 'draft') {
        const errors = validateDocument(ed.spec.jsonSchema, $state.snapshot(doc)).map((e) => ({ path: e.path || '/', message: e.message }));
        docResult = { valid: errors.length === 0, errors };
      } else {
        docResult = await api.validateDocument({ app, name, version: Number(docVersion), body: { document: $state.snapshot(doc) } });
      }
    } catch (e) {
      docError = e;
    } finally {
      docBusy = false;
    }
  }

  let fields = $derived(ed.spec?.jsonSchema ? listFields(ed.spec.jsonSchema) : []);
  let canEdit = $derived(session.can('edit'));

  function addExample(): void {
    ed.spec.examples = [...(ed.spec.examples ?? []), exampleFor(ed.spec.jsonSchema)];
  }
</script>

<PageHeader title={name}>
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'schemas', href: appHref('/schemas') }]} />{/snippet}
  {#snippet sub()}{ed.description || 'Registered JSON Schema'}{/snippet}
</PageHeader>

{#if ed.loadError}
  <ErrorNote error={ed.loadError} retry={() => ed.load()} />
{:else if ed.loading && !ed.resource}
  <Loading />
{:else if ed.spec}
  <DraftBar {ed} />
  <nav class="tabs" aria-label="Schema sections">
    {#each TABS as [id, label] (id)}
      <a href={appHref(`/schemas/${name}`, { tab: id === 'editor' ? undefined : id })} aria-current={tab === id ? 'page' : undefined}>{label}{#if id === 'examples' && ed.spec.examples?.length}<span class="count">{ed.spec.examples.length}</span>{/if}</a>
    {/each}
  </nav>

  {#if tab === 'editor'}
    <div class="field" style="max-width:640px">
      <label for="schema-desc">Description</label>
      <input id="schema-desc" type="text" bind:value={ed.description} disabled={!canEdit} maxlength="500" />
    </div>
    <SchemaTreeEditor bind:value={ed.spec.jsonSchema} disabled={!canEdit} rootLabel={name} />
    {#if ed.problems.length}<section class="panel tight"><h3 class="sub-h">Validation</h3><Problems problems={ed.problems} showPath /></section>{/if}
    <p class="hint">{fields.length} fields · root type {primaryType(ed.spec.jsonSchema)}. Mark personal data as PII: it is stored only as a hash in logs and can never reach an AI prompt.</p>
  {:else if tab === 'json'}
    <section class="panel">
      <header><h2>JSON Schema</h2><span class="hint">2020-12, restricted subset</span></header>
      <JsonEditor bind:value={ed.spec.jsonSchema} rows={24} disabled={!canEdit} id="schema-json" />
      <Problems problems={ed.problems.filter((p) => p.path.startsWith('/jsonSchema'))} showPath />
      <p class="hint">Allowed: type (add "null" for nullable), properties, required, additionalProperties, items, prefixItems, enum, const, minimum, maximum, exclusiveMinimum, exclusiveMaximum, minLength, maxLength, pattern (RE2), format (date-time, date, email, uuid), minItems, maxItems, description, default, $defs with local $ref, and x-gearbox-pii.</p>
    </section>
  {:else if tab === 'examples'}
    <section class="panel">
      <header><h2>Examples</h2><button type="button" class="btn btn-sm" onclick={addExample} disabled={!canEdit}>Add example</button></header>
      <p class="hint">Sample documents; each must validate. Editors use them for previews, and test cases start from them.</p>
      {#if ed.spec.examples && ed.spec.examples.length}
        {#each ed.spec.examples as _, i (i)}
          <div class="stack-sm">
            <div class="row-sm"><h3 class="sub-h">Example {i + 1}</h3><span class="spacer"></span><button type="button" class="btn btn-xs" onclick={() => ed.spec.examples?.splice(i, 1)} disabled={!canEdit}>Remove</button></div>
            <JsonEditor bind:value={ed.spec.examples[i]} rows={10} schema={ed.spec.jsonSchema} disabled={!canEdit} />
          </div>
        {/each}
      {:else}
        <p class="hint">No examples yet. “Add example” starts from a skeleton built from the schema.</p>
      {/if}
    </section>
  {:else if tab === 'validate'}
    <div class="grid-2">
      <section class="panel">
        <header>
          <h2>Document</h2>
          <div class="row-sm">
            <label for="doc-ver" class="hint">Against</label>
            <select id="doc-ver" bind:value={docVersion}>
              <option value="draft">current draft (in the browser)</option>
              {#each Array.from({ length: ed.resource?.latestVersion ?? 0 }, (_, i) => (ed.resource?.latestVersion ?? 0) - i) as v (v)}<option value={String(v)}>v{v} (server)</option>{/each}
            </select>
          </div>
        </header>
        <JsonEditor bind:value={doc} rows={16} id="doc-json" />
        <div class="actions">
          <button type="button" class="btn btn-primary" onclick={checkDoc} disabled={docBusy}>Check</button>
          {#if ed.spec.examples?.length}<button type="button" class="btn" onclick={() => (doc = $state.snapshot(ed.spec.examples![0]))}>Use example 1</button>{/if}
          <button type="button" class="btn" onclick={() => (doc = exampleFor(ed.spec.jsonSchema))}>Skeleton</button>
        </div>
      </section>
      <section class="panel" aria-live="polite">
        <header><h2>Result</h2></header>
        {#if docError}<ErrorNote error={docError} />
        {:else if docResult}
          <p class="result-line" class:ok={docResult.valid} class:bad={!docResult.valid}>{docResult.valid ? 'Valid' : `${docResult.errors.length} problem${docResult.errors.length === 1 ? '' : 's'}`}</p>
          {#if docResult.errors.length}
            <ul class="problems">{#each docResult.errors as e (e.path + e.message)}<li class="error"><span class="pm" aria-hidden="true">✕</span><span><span class="mono">{e.path}</span> {e.message}</span></li>{/each}</ul>
          {/if}
        {:else}
          <p class="hint">Paste a document and press Check.</p>
        {/if}
        <h3 class="sub-h">Schema</h3>
        <JsonView value={ed.spec.jsonSchema} scroll />
      </section>
    </div>
  {:else if tab === 'versions'}
    <VersionsPanel {ed} kind="schemas" />
  {:else if tab === 'usages'}
    <UsagesPanel {app} kind="schemas" {name} />
  {/if}
{/if}
