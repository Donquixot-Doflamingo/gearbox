<script lang="ts">
  // SVG graph canvas: drag nodes (positions saved to node.position), drag from a port to connect
  // (right port: next / switch branch, bottom port: onError), click an edge to disconnect.
  // Keyboard: Tab between nodes, arrow keys move (Shift = faster), Enter opens the inspector, Delete removes.
  import type { DecisionSpec, ValidationProblem } from '$lib/api/types.gen';
  import { canvasSize, connect, disconnect, edgePath, edgesOf, END, NODE_H, NODE_W, nodeSummary, positions, type Edge, type Pos } from '$lib/model/graph';

  let {
    spec = $bindable(),
    selected = $bindable(null),
    problems,
    pathIds = [],
    disabled = false,
    onopen,
    ondelete,
    onchange,
  }: {
    spec: DecisionSpec;
    selected?: string | null;
    problems: Map<string, ValidationProblem[]>;
    pathIds?: string[];
    disabled?: boolean;
    onopen?: (id: string) => void;
    ondelete?: (id: string) => void;
    onchange?: () => void;
  } = $props();

  const COLOR: Record<string, string> = { rules: '--t-rules', table: '--t-table', transform: '--t-transform', switch: '--t-switch', ai: '--t-ai', output: '--t-output', lookup: '--t-lookup', gate: '--t-gate', experiment: '--t-experiment', subdecision: '--t-subdecision' };

  let svg = $state<SVGSVGElement | undefined>();
  let pos = $derived(positions(spec));
  let edges = $derived(edgesOf(spec));
  let size = $derived(canvasSize(pos));
  let selectedEdge = $state<string | null>(null);
  let dragging: { id: string; dx: number; dy: number; moved: boolean } | null = $state(null);
  let linking: { from: string; kind: 'next' | 'error'; x: number; y: number; over: string | null } | null = $state(null);
  let onPath = $derived(new Set(pathIds));
  let pathEdges = $derived.by(() => {
    const s = new Set<string>();
    for (let i = 0; i + 1 < pathIds.length; i++) s.add(`${pathIds[i]}>${pathIds[i + 1]}`);
    return s;
  });

  function toSvg(e: PointerEvent): Pos {
    if (!svg) return { x: 0, y: 0 };
    const m = svg.getScreenCTM();
    if (!m) return { x: e.clientX, y: e.clientY };
    const p = new DOMPoint(e.clientX, e.clientY).matrixTransform(m.inverse());
    return { x: p.x, y: p.y };
  }

  /** Store every computed position so nodes without one do not jump when another moves. */
  function materialize(): void {
    for (const n of spec.nodes) if (typeof n.position?.x !== 'number') n.position = { ...pos[n.id] };
  }

  function nodeAt(p: Pos): string | null {
    for (const n of spec.nodes) {
      const q = pos[n.id];
      if (q && p.x >= q.x && p.x <= q.x + NODE_W && p.y >= q.y && p.y <= q.y + NODE_H) return n.id;
    }
    return null;
  }

  function endPos(from: string): Pos {
    const p = pos[from];
    return { x: p.x + NODE_W + 58, y: p.y + NODE_H / 2 };
  }

  function startDrag(e: PointerEvent, id: string): void {
    if (e.button !== 0) return;
    selected = id;
    selectedEdge = null;
    if (disabled) return;
    const p = toSvg(e);
    const q = pos[id];
    dragging = { id, dx: p.x - q.x, dy: p.y - q.y, moved: false };
    svg?.setPointerCapture(e.pointerId);
  }

  function startLink(e: PointerEvent, id: string, kind: 'next' | 'error'): void {
    e.stopPropagation();
    if (disabled || e.button !== 0) return;
    const p = toSvg(e);
    linking = { from: id, kind, x: p.x, y: p.y, over: null };
    svg?.setPointerCapture(e.pointerId);
  }

  function onmove(e: PointerEvent): void {
    if (dragging) {
      const p = toSvg(e);
      const n = spec.nodes.find((x) => x.id === dragging!.id);
      if (!n) return;
      if (!dragging.moved) materialize();
      dragging.moved = true;
      n.position = { x: Math.max(4, Math.round((p.x - dragging.dx) / 4) * 4), y: Math.max(4, Math.round((p.y - dragging.dy) / 4) * 4) };
    } else if (linking) {
      const p = toSvg(e);
      linking.x = p.x;
      linking.y = p.y;
      const over = nodeAt(p);
      linking.over = over && over !== linking.from ? over : null;
    }
  }

  function onup(e: PointerEvent): void {
    if (svg?.hasPointerCapture(e.pointerId)) svg.releasePointerCapture(e.pointerId);
    if (dragging) {
      if (dragging.moved) onchange?.();
      dragging = null;
    } else if (linking) {
      const target = linking.over;
      if (target) {
        materialize();
        const k = connect(spec, linking.from, target, linking.kind);
        if (k) onchange?.();
      }
      linking = null;
    }
  }

  function nodeKey(e: KeyboardEvent, id: string): void {
    const n = spec.nodes.find((x) => x.id === id);
    if (!n) return;
    const step = e.shiftKey ? 40 : 8;
    const moves: Record<string, [number, number]> = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] };
    if (e.key in moves && !disabled) {
      e.preventDefault();
      materialize();
      const [dx, dy] = moves[e.key];
      const p = n.position ?? pos[id];
      n.position = { x: Math.max(4, (p.x ?? 0) + dx), y: Math.max(4, (p.y ?? 0) + dy) };
      onchange?.();
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      selected = id;
      onopen?.(id);
    } else if ((e.key === 'Delete' || e.key === 'Backspace') && !disabled) {
      e.preventDefault();
      ondelete?.(id);
    }
  }

  function removeEdge(edge: Edge): void {
    disconnect(spec, edge);
    selectedEdge = null;
    onchange?.();
  }

  function edgeGeom(edge: Edge) {
    const a = pos[edge.from];
    if (!a) return null;
    if (edge.to === END) return edgePath(a, endPos(edge.from), true);
    const b = pos[edge.to];
    if (!b) return null;
    return edgePath(a, b);
  }

  let ghost = $derived.by(() => {
    if (!linking) return '';
    const a = pos[linking.from];
    if (!a) return '';
    const x1 = linking.kind === 'error' ? a.x + NODE_W / 2 : a.x + NODE_W;
    const y1 = linking.kind === 'error' ? a.y + NODE_H : a.y + NODE_H / 2;
    return `M${x1} ${y1} L${linking.x} ${linking.y}`;
  });

  let endNodes = $derived(edges.filter((e) => e.to === END).map((e) => e.from));
</script>

<div class="canvas" class:connecting={!!linking}>
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <svg
    bind:this={svg}
    class="graph"
    width={size.w}
    height={size.h}
    viewBox="0 0 {size.w} {size.h}"
    role="group"
    aria-label="Decision graph, {spec.nodes.length} nodes. Tab to a node; arrow keys move it, Enter opens it, Delete removes it."
    onpointermove={onmove}
    onpointerup={onup}
    onpointercancel={onup}
    onpointerdown={(e) => { if (e.target === svg) { selectedEdge = null; } }}
  >
    <defs>
      <marker id="arr" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="8" markerHeight="8" orient="auto"><path class="g-arrow" d="M0 0 L8 4 L0 8 Z" /></marker>
      <marker id="arr-err" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="8" markerHeight="8" orient="auto"><path class="g-arrow-err" d="M0 0 L8 4 L0 8 Z" /></marker>
      <marker id="arr-ok" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="8" markerHeight="8" orient="auto"><path class="g-arrow-ok" d="M0 0 L8 4 L0 8 Z" /></marker>
      <marker id="arr-sel" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="8" markerHeight="8" orient="auto"><path class="g-arrow-sel" d="M0 0 L8 4 L0 8 Z" /></marker>
    </defs>

    {#each edges as edge (edge.id)}
      {@const g = edgeGeom(edge)}
      {#if g}
        {@const hot = pathEdges.has(`${edge.from}>${edge.to}`)}
        {@const sel = selectedEdge === edge.id}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <path class="g-edge-hit" d={g.d} onclick={() => (selectedEdge = sel ? null : edge.id)} role="presentation" />
        <path class="g-edge" class:err={edge.kind === 'error'} class:on-path={hot} class:sel d={g.d} marker-end="url(#{sel ? 'arr-sel' : hot ? 'arr-ok' : edge.kind === 'error' ? 'arr-err' : 'arr'})" />
        {#if edge.label}<text class="g-label" class:err={edge.kind === 'error'} x={g.lx} y={g.ly} text-anchor="middle">{edge.label}</text>{/if}
        {#if sel && !disabled}
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <g class="g-del" transform="translate({g.lx},{g.ly + 14})" onclick={() => removeEdge(edge)} role="button" tabindex="-1" aria-label="Disconnect {edge.from} from {edge.to}">
            <circle r="9" /><text text-anchor="middle" y="4">×</text>
          </g>
        {/if}
      {/if}
    {/each}

    {#each endNodes as from (from)}
      {@const p = endPos(from)}
      <g aria-hidden="true"><circle cx={p.x} cy={p.y} r="13" class="g-port" style="cursor:default" /><text class="g-sub" x={p.x} y={p.y + 4} text-anchor="middle">end</text></g>
    {/each}

    {#if ghost}<path class="g-ghost" d={ghost} />{/if}

    {#each spec.nodes as n (n.id)}
      {@const p = pos[n.id]}
      {@const probs = problems.get(n.id) ?? []}
      {@const hasErr = probs.some((x) => x.severity === 'error')}
      {@const hasWarn = !hasErr && probs.length > 0}
      {#if p}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <g
          class="g-node"
          class:sel={selected === n.id}
          class:on-path={onPath.has(n.id)}
          class:has-error={hasErr}
          class:has-warning={hasWarn}
          class:dragging={dragging?.id === n.id}
          class:drop-target={linking?.over === n.id}
          transform="translate({p.x},{p.y})"
          tabindex="0"
          role="button"
          aria-label="{n.id}, {n.type} node{spec.entry === n.id ? ', entry' : ''}{hasErr ? `, ${probs.length} problems` : ''}"
          aria-pressed={selected === n.id}
          data-node={n.id}
          onpointerdown={(e) => startDrag(e, n.id)}
          onkeydown={(e) => nodeKey(e, n.id)}
          onfocus={() => (selected = n.id)}
          ondblclick={() => onopen?.(n.id)}
        >
          <rect class="box" width={NODE_W} height={NODE_H} rx="8" />
          <text class="g-type" x="12" y="19" style:fill="var({COLOR[n.type] ?? '--muted'})">{n.type}</text>
          {#if spec.entry === n.id}<text class="g-entry" x={NODE_W - 12} y="19" text-anchor="end">ENTRY</text>{/if}
          <text class="g-id" x="12" y="38">{n.id.length > 19 ? n.id.slice(0, 18) + '…' : n.id}</text>
          <text class="g-sub" x="12" y="54">{(() => { const s = nodeSummary(n); return s.length > 26 ? s.slice(0, 25) + '…' : s; })()}</text>
          {#if probs.length}
            <g transform="translate({NODE_W - 2},-2)"><circle r="9" fill="var({hasErr ? '--crit' : '--warn'})" /><text class="g-badge" text-anchor="middle" y="4" fill="var(--surface)">{probs.length}</text></g>
          {/if}
          {#if !disabled}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <circle class="g-port" class:active={linking?.from === n.id && linking.kind === 'next'} cx={NODE_W} cy={NODE_H / 2} r="6" onpointerdown={(e) => startLink(e, n.id, 'next')}><title>Drag to connect {n.type === 'switch' ? 'a branch' : 'next'}</title></circle>
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <circle class="g-port err" class:active={linking?.from === n.id && linking.kind === 'error'} cx={NODE_W / 2} cy={NODE_H} r="5" onpointerdown={(e) => startLink(e, n.id, 'error')}><title>Drag to connect onError</title></circle>
          {/if}
        </g>
      {/if}
    {/each}
  </svg>
</div>
