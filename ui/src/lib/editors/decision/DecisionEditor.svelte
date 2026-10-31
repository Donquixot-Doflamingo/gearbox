<script lang="ts">
  import { untrack } from 'svelte';
  import type { DecisionSpec, NodeType, ValidationProblem } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import DraftBar from '$lib/components/DraftBar.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Kind from '$lib/components/Kind.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import UsagesPanel from '$lib/components/UsagesPanel.svelte';
  import VersionsPanel from '$lib/components/VersionsPanel.svelte';
  import { DraftEditor } from '$lib/draft.svelte';
  import { addNode, applyAutoLayout, graphProblems, NODE_LABELS, nodeIndexOfPath, problemsByNode, removeNode } from '$lib/model/graph';
  import { appHref, qp, setQuery } from '$lib/nav.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import DecisionSettings from './DecisionSettings.svelte';
  import GraphCanvas from './GraphCanvas.svelte';
  import NodeInspector from './NodeInspector.svelte';
  import TestCases from './TestCases.svelte';
  import TestConsole from './TestConsole.svelte';

  let { app, name }: { app: string; name: string } = $props();
  // Routes remount this editor per resource ({#key}), so reading the props once is intended.
  // svelte-ignore state_referenced_locally

  const ed = new DraftEditor<DecisionSpec>(app, 'decisions', name);
  void ed.load();

  // validate on every change (debounced)
  $effect(() => {
    JSON.stringify(ed.spec);
    if (ed.spec) untrack(() => ed.scheduleValidate());
  });

  const TABS = [
    ['graph', 'Graph'],
    ['console', 'Test console'],
    ['tests', 'Test cases'],
    ['settings', 'Settings'],
    ['versions', 'Versions'],
    ['usages', 'Used by'],
  ] as const;
  let tab = $derived(qp('tab') ?? 'graph');
  let selected = $state<string | null>(qp('node'));
  let pathIds = $state<string[]>([]);
  let canEdit = $derived(session.can('edit'));

  // keep ?node= in the URL so a selection is shareable
  $effect(() => {
    const s = selected;
    untrack(() => {
      if (tab === 'graph' && (qp('node') ?? null) !== s) setQuery({ node: s ?? undefined });
    });
  });
  $effect(() => {
    if (!ed.spec || !selected) return;
    if (!ed.spec.nodes.some((n) => n.id === selected)) selected = ed.spec.nodes[0]?.id ?? null;
  });
  $effect(() => {
    if (ed.spec && !selected && ed.spec.nodes.length) untrack(() => (selected = ed.spec.entry || ed.spec.nodes[0].id));
  });

  /** Server problems, plus local graph-shape problems while the server has not answered. */
  let problems: ValidationProblem[] = $derived.by(() => {
    if (!ed.spec) return [];
    const server = ed.problems;
    const save = (ed.saveError?.errors ?? []).map((x) => ({ code: x.code ?? ed.saveError!.code, severity: 'error' as const, path: x.path.replace(/^\/spec/, ''), message: x.message }));
    const local = graphProblems(ed.spec).filter((l) => !server.some((s) => s.code === l.code && s.path === l.path));
    return [...save, ...server, ...(ed.validation ? [] : local)];
  });
  const source = {
    problemsUnder: (ptr: string) => problems.filter((p) => p.path === ptr || p.path.startsWith(ptr + '/')),
  };
  let byNode = $derived(ed.spec ? problemsByNode(ed.spec, problems) : new Map<string, ValidationProblem[]>());
  let globalProblems = $derived(problems.filter((p) => nodeIndexOfPath(p.path) < 0));

  const PALETTE: NodeType[] = ['rules', 'table', 'transform', 'switch', 'lookup', 'gate', 'experiment', 'subdecision', 'ai', 'output'];
  const COLOR: Record<string, string> = { rules: '--t-rules', table: '--t-table', transform: '--t-transform', switch: '--t-switch', ai: '--t-ai', output: '--t-output', lookup: '--t-lookup', gate: '--t-gate', experiment: '--t-experiment', subdecision: '--t-subdecision' };

  function add(type: NodeType): void {
    const id = addNode(ed.spec, type);
    selected = id;
    toast(`Added ${id}. Drag from its right port to connect it.`);
    queueMicrotask(() => document.querySelector<SVGGElement>(`[data-node="${id}"]`)?.focus());
  }

  function del(id: string): void {
    const before = $state.snapshot(ed.spec);
    removeNode(ed.spec, id);
    if (selected === id) selected = ed.spec.entry || ed.spec.nodes[0]?.id || null;
    toast(`Deleted ${id}`, { action: { label: 'Undo', run: () => { ed.spec = before; selected = id; } } });
  }

  function openInspector(id: string): void {
    selected = id;
    queueMicrotask(() => {
      const el = document.getElementById('inspector');
      el?.scrollIntoView({ behavior: 'smooth', block: 'start' });
      el?.focus({ preventScroll: true });
    });
  }

  function pickProblem(p: ValidationProblem): void {
    const i = nodeIndexOfPath(p.path);
    if (i >= 0 && ed.spec.nodes[i]) openInspector(ed.spec.nodes[i].id);
    else if (p.path.startsWith('/tests')) setQuery({ tab: 'tests', node: undefined });
    else if (['/fallback', '/output', '/inputSchema', '/entry', '/logging'].some((x) => p.path.startsWith(x))) setQuery({ tab: 'settings', node: undefined });
  }

  let failingTests = $derived(problems.filter((p) => p.path.startsWith('/tests')).length);
</script>

<PageHeader title={name}>
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'decisions', href: appHref('/decisions') }]} />{/snippet}
  {#snippet sub()}{ed.description || 'Decision'}{/snippet}
</PageHeader>

{#if ed.loadError}
  <ErrorNote error={ed.loadError} retry={() => ed.load()} />
{:else if ed.loading && !ed.resource}
  <Loading />
{:else if ed.spec}
  <DraftBar {ed} />
  <nav class="tabs" aria-label="Decision sections">
    {#each TABS as [id, label] (id)}
      <a href={appHref(`/decisions/${name}`, { tab: id === 'graph' ? undefined : id, node: id === 'graph' ? (selected ?? undefined) : undefined })} aria-current={tab === id ? 'page' : undefined}>
        {label}
        {#if id === 'tests'}<span class="count" class:warn={failingTests > 0}>{ed.spec.tests?.length ?? 0}</span>{/if}
      </a>
    {/each}
  </nav>

  {#if tab === 'graph'}
    <div class="editor-layout">
      <div class="palette" role="group" aria-label="Add a node">
        <span class="sub-h">Add node</span>
        {#each PALETTE as t (t)}
          <button type="button" style:--k="var({COLOR[t]})" onclick={() => add(t)} disabled={!canEdit} title={NODE_LABELS[t].hint}>{t}</button>
        {/each}
      </div>
      <div class="stack">
        {#if ed.spec.nodes.length === 0}
          <div class="empty"><h2>Empty decision</h2><p>Add a node from the palette. A single rules node is a complete decision.</p></div>
        {:else}
          <GraphCanvas bind:spec={ed.spec} bind:selected problems={byNode} {pathIds} disabled={!canEdit} onopen={openInspector} ondelete={del} />
        {/if}
        <div class="row">
          <div class="legend">{#each ['transform', 'rules', 'table', 'switch', 'ai', 'output'] as k (k)}<Kind type={k} />{/each}</div>
          <span class="spacer"></span>
          {#if pathIds.length}<button type="button" class="btn btn-xs" onclick={() => (pathIds = [])}>Clear path highlight</button>{/if}
          <button type="button" class="btn btn-xs" onclick={() => applyAutoLayout(ed.spec)} disabled={!canEdit}>Tidy layout</button>
        </div>
        <p class="hint">Drag nodes to arrange them. Drag from a node's right port to connect it (a switch gets a new branch), from the bottom port to set onError. Click an edge to disconnect it. With the keyboard: Tab to a node, arrows move it, Enter opens it, Delete removes it.</p>
      </div>
    </div>

    {#if globalProblems.length}
      <section class="panel tight"><h3 class="sub-h">Decision problems</h3><Problems problems={globalProblems} onpick={pickProblem} showPath /></section>
    {/if}

    {#if selected && ed.spec.nodes.some((n) => n.id === selected)}
      <NodeInspector {app} decision={name} bind:spec={ed.spec} id={selected} {ed} problems={source} disabled={!canEdit} onrenamed={(to) => (selected = to)} ondelete={del} />
    {/if}
  {:else if tab === 'console'}
    <TestConsole {app} {ed} onpath={(ids) => (pathIds = ids)} />
    {#if pathIds.length}<p class="hint">The path is highlighted on the <a href={appHref(`/decisions/${name}`)}>graph</a>.</p>{/if}
  {:else if tab === 'tests'}
    <TestCases {app} {ed} />
  {:else if tab === 'settings'}
    <DecisionSettings {app} {ed} disabled={!canEdit} />
  {:else if tab === 'versions'}
    <VersionsPanel {ed} kind="decisions" />
  {:else if tab === 'usages'}
    <UsagesPanel {app} kind="decisions" {name} />
  {/if}
{/if}
