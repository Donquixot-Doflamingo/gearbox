// Decision graph model: edges derived from nodes, layout, and in-place edits (connect, add, remove, rename).
// Edges are not stored separately; they come from `next`, `onError`, switch `cases[].next` and `default`.

import type { DecisionSpec, Node, NodeType, SwitchNode, ValidationProblem } from '../api/types.gen';

export const END = 'end';
export const NODE_W = 172;
export const NODE_H = 66;
export const COL_W = 236;
export const ROW_H = 112;
export const PAD = 28;

export type EdgeKind = 'next' | 'case' | 'default' | 'error';

export interface Edge {
  id: string;
  from: string;
  to: string;
  kind: EdgeKind;
  label?: string;
  caseIndex?: number;
}

export interface Pos {
  x: number;
  y: number;
}

export const NODE_ID = /^[a-z][a-z0-9_]{0,62}$/;

export const NODE_LABELS: Record<NodeType, { label: string; hint: string }> = {
  rules: { label: 'Rules', hint: 'Ordered rules; first or all matches' },
  table: { label: 'Table', hint: 'Decision table with input and output columns' },
  transform: { label: 'Transform', hint: 'Reshape JSON field by field' },
  switch: { label: 'Switch', hint: 'Branch on conditions' },
  lookup: { label: 'Lookup', hint: 'Read a key from a lookup table' },
  gate: { label: 'Gate', hint: 'Feature gate for this unit' },
  experiment: { label: 'Experiment', hint: 'Experiment variant for this unit' },
  subdecision: { label: 'Subdecision', hint: 'Call another decision' },
  ai: { label: 'AI', hint: 'One call to the Integration Hub' },
  output: { label: 'Output', hint: 'Build the response' },
};

/** Nodes as stored in an editable draft: required schema refs may still be missing. */
export function nodeById(spec: DecisionSpec, id: string): Node | undefined {
  return spec.nodes.find((n) => n.id === id);
}

export function edgesOf(spec: DecisionSpec): Edge[] {
  const out: Edge[] = [];
  for (const n of spec.nodes) {
    if (n.type === 'switch') {
      (n.cases ?? []).forEach((c, i) => {
        if (c.next) out.push({ id: `${n.id}:case:${i}`, from: n.id, to: c.next, kind: 'case', label: c.id || `case ${i + 1}`, caseIndex: i });
      });
      if (n.default) out.push({ id: `${n.id}:default`, from: n.id, to: n.default, kind: 'default', label: 'default' });
    }
    if (n.next) out.push({ id: `${n.id}:next`, from: n.id, to: n.next, kind: 'next' });
    if (n.onError) out.push({ id: `${n.id}:error`, from: n.id, to: n.onError, kind: 'error', label: 'onError' });
  }
  return out;
}

/** Breadth-first columns from the entry; unreachable nodes go in a last column. */
export function autoLayout(spec: DecisionSpec): Record<string, Pos> {
  const edges = edgesOf(spec).filter((e) => e.to !== END);
  const col = new Map<string, number>();
  const entry = spec.entry && nodeById(spec, spec.entry) ? spec.entry : spec.nodes[0]?.id;
  if (entry) {
    col.set(entry, 0);
    const queue = [entry];
    for (let guard = 0; queue.length && guard < 10000; guard++) {
      const id = queue.shift()!;
      for (const e of edges.filter((x) => x.from === id)) {
        if (!col.has(e.to) && nodeById(spec, e.to)) {
          col.set(e.to, (col.get(id) ?? 0) + 1);
          queue.push(e.to);
        }
      }
    }
  }
  const maxCol = Math.max(0, ...col.values());
  for (const n of spec.nodes) if (!col.has(n.id)) col.set(n.id, maxCol + 1);
  const rows = new Map<number, number>();
  const out: Record<string, Pos> = {};
  for (const n of spec.nodes) {
    const c = col.get(n.id)!;
    const r = rows.get(c) ?? 0;
    rows.set(c, r + 1);
    out[n.id] = { x: PAD + c * COL_W, y: PAD + r * ROW_H };
  }
  return out;
}

/** Position of every node (stored position, or the auto layout for nodes without one). */
export function positions(spec: DecisionSpec): Record<string, Pos> {
  const auto = spec.nodes.some((n) => !hasPos(n)) ? autoLayout(spec) : {};
  const out: Record<string, Pos> = {};
  for (const n of spec.nodes) out[n.id] = hasPos(n) ? { x: n.position!.x!, y: n.position!.y! } : auto[n.id];
  return out;
}

function hasPos(n: Node): boolean {
  return typeof n.position?.x === 'number' && typeof n.position?.y === 'number';
}

/** Store the auto layout on every node (the "Tidy" action). */
export function applyAutoLayout(spec: DecisionSpec): void {
  for (const n of spec.nodes) delete n.position;
  const p = autoLayout(spec);
  for (const n of spec.nodes) n.position = { ...p[n.id] };
}

export function uniqueId(spec: DecisionSpec, base: string): string {
  const clean = base.toLowerCase().replace(/[^a-z0-9_]/g, '_').replace(/^[^a-z]+/, '') || 'node';
  let id = clean;
  for (let i = 2; spec.nodes.some((n) => n.id === id) || id === END; i++) id = `${clean}_${i}`;
  return id;
}

/** A new node of a type with valid empty configuration (rules/table still need schemas). */
export function defaultNode(type: NodeType, id: string): Node {
  const base = { id, type } as const;
  switch (type) {
    case 'rules':
      return { ...base, type, mode: 'first_match', rules: [] } as unknown as Node;
    case 'table':
      return { ...base, type, hitPolicy: 'first', inputs: [], outputs: [], rows: [] } as unknown as Node;
    case 'transform':
      return { ...base, type, mode: 'build', fields: [] };
    case 'switch':
      return { ...base, type, cases: [], default: '' };
    case 'lookup':
      return { ...base, type, lookup: '', key: 'input.' };
    case 'gate':
      return { ...base, type, gate: '' };
    case 'experiment':
      return { ...base, type, experiment: '' };
    case 'subdecision':
      return { ...base, type, decision: '' };
    case 'ai':
      return {
        ...base,
        type,
        model: 'reasoning-default',
        mode: 'shadow',
        timeoutMs: 1500,
        vars: {},
        prompt: { system: '', user: '' },
        responseSchema: { type: 'object', required: [], properties: {} },
        onError: '',
      };
    case 'output':
      return { ...base, type, fields: [] };
  }
}

/** Add a node at a position; the first node becomes the entry. Returns its id. */
export function addNode(spec: DecisionSpec, type: NodeType, pos?: Pos): string {
  const id = uniqueId(spec, type === 'output' ? 'output' : type);
  const n = defaultNode(type, id);
  if (pos) n.position = { x: Math.round(pos.x), y: Math.round(pos.y) };
  else {
    const p = positions(spec);
    const maxX = Math.max(PAD - COL_W, ...Object.values(p).map((q) => q.x));
    n.position = { x: maxX + COL_W, y: PAD };
  }
  spec.nodes.push(n);
  if (!spec.entry || !nodeById(spec, spec.entry)) spec.entry = id;
  return id;
}

/** Remove a node and every reference to it. */
export function removeNode(spec: DecisionSpec, id: string): void {
  const i = spec.nodes.findIndex((n) => n.id === id);
  if (i < 0) return;
  spec.nodes.splice(i, 1);
  for (const n of spec.nodes) {
    if (n.next === id) delete n.next;
    if (n.onError === id) n.onError = n.type === 'ai' ? '' : undefined;
    if (n.onError === undefined) delete n.onError;
    if (n.type === 'switch') {
      n.cases = (n.cases ?? []).filter((c) => c.next !== id);
      if (n.default === id) n.default = '';
    }
  }
  if (spec.entry === id) spec.entry = spec.nodes[0]?.id ?? '';
}

/** Rename a node and update next/onError/cases/entry. Expressions that read `nodes.<id>` are not rewritten. */
export function renameNode(spec: DecisionSpec, from: string, to: string): void {
  if (from === to) return;
  if (!NODE_ID.test(to)) throw new Error('IDs start with a lowercase letter and use a–z, 0–9 and _');
  if (to === END || spec.nodes.some((n) => n.id === to)) throw new Error(`${to} is already used`);
  const node = nodeById(spec, from);
  if (!node) return;
  node.id = to;
  for (const n of spec.nodes) {
    if (n.next === from) n.next = to;
    if (n.onError === from) n.onError = to;
    if (n.type === 'switch') {
      for (const c of n.cases ?? []) if (c.next === from) c.next = to;
      if (n.default === from) n.default = to;
    }
  }
  if (spec.entry === from) spec.entry = to;
}

/** Connect `from` to `to`. A switch gets its default first, then extra cases. */
export function connect(spec: DecisionSpec, from: string, to: string, kind: 'next' | 'error'): Edge['kind'] | null {
  const n = nodeById(spec, from);
  if (!n || from === to) return null;
  if (to !== END && !nodeById(spec, to)) return null;
  if (kind === 'error') {
    if (to === END) return null;
    n.onError = to;
    return 'error';
  }
  if (n.type === 'switch') {
    const sw = n as SwitchNode;
    if (!sw.default) {
      sw.default = to;
      return 'default';
    }
    sw.cases = [...(sw.cases ?? []), { id: `case_${(sw.cases?.length ?? 0) + 1}`, when: 'true', next: to }];
    return 'case';
  }
  n.next = to;
  return 'next';
}

export function disconnect(spec: DecisionSpec, edge: Edge): void {
  const n = nodeById(spec, edge.from);
  if (!n) return;
  if (edge.kind === 'next') delete n.next;
  if (edge.kind === 'error') {
    if (n.type === 'ai') n.onError = '';
    else delete n.onError;
  }
  if (n.type === 'switch') {
    if (edge.kind === 'default') n.default = '';
    if (edge.kind === 'case' && edge.caseIndex !== undefined) n.cases = n.cases.filter((_, i) => i !== edge.caseIndex);
  }
}

export function reachable(spec: DecisionSpec): Set<string> {
  const edges = edgesOf(spec);
  const seen = new Set<string>();
  const stack = spec.entry ? [spec.entry] : [];
  while (stack.length) {
    const id = stack.pop()!;
    if (seen.has(id) || !nodeById(spec, id)) continue;
    seen.add(id);
    for (const e of edges) if (e.from === id) stack.push(e.to);
  }
  return seen;
}

/** Node ids on cycles. */
export function cycleNodes(spec: DecisionSpec): Set<string> {
  const edges = edgesOf(spec).filter((e) => e.to !== END);
  const state = new Map<string, 1 | 2>();
  const onCycle = new Set<string>();
  const stack: string[] = [];
  const visit = (id: string) => {
    state.set(id, 1);
    stack.push(id);
    for (const e of edges.filter((x) => x.from === id)) {
      const s = state.get(e.to);
      if (s === 1) {
        const i = stack.indexOf(e.to);
        stack.slice(i).forEach((x) => onCycle.add(x));
      } else if (!s && nodeById(spec, e.to)) visit(e.to);
    }
    stack.pop();
    state.set(id, 2);
  };
  for (const n of spec.nodes) if (!state.has(n.id)) visit(n.id);
  return onCycle;
}

/** Graph-shape problems the editor can show before the server answers. Paths are JSON pointers. */
export function graphProblems(spec: DecisionSpec): ValidationProblem[] {
  const out: ValidationProblem[] = [];
  const idx = (id: string) => spec.nodes.findIndex((n) => n.id === id);
  if (spec.nodes.length === 0) return [{ code: 'EMPTY', severity: 'error', path: '/nodes', message: 'Add a node to start.' }];
  if (!spec.entry || idx(spec.entry) < 0) out.push({ code: 'UNKNOWN_NODE', severity: 'error', path: '/entry', message: 'Entry node does not exist.' });
  const ids = new Set<string>();
  spec.nodes.forEach((n, i) => {
    if (ids.has(n.id)) out.push({ code: 'DUPLICATE_ID', severity: 'error', path: `/nodes/${i}/id`, message: `Two nodes are called ${n.id}.` });
    ids.add(n.id);
  });
  for (const e of edgesOf(spec)) {
    if (e.to !== END && idx(e.to) < 0) {
      const i = idx(e.from);
      const where = e.kind === 'error' ? 'onError' : e.kind === 'default' ? 'default' : e.kind === 'case' ? `cases/${e.caseIndex}/next` : 'next';
      out.push({ code: 'UNKNOWN_NODE', severity: 'error', path: `/nodes/${i}/${where}`, message: `${e.from} points at ${e.to}, which does not exist.` });
    }
  }
  const cyc = cycleNodes(spec);
  for (const id of cyc) out.push({ code: 'CYCLE', severity: 'error', path: `/nodes/${idx(id)}`, message: `${id} is on a cycle.` });
  const reach = reachable(spec);
  spec.nodes.forEach((n, i) => {
    if (!reach.has(n.id)) out.push({ code: 'UNREACHABLE_NODE', severity: 'warning', path: `/nodes/${i}`, message: `${n.id} is never reached from ${spec.entry || 'the entry'}.` });
    if (n.type === 'ai' && !n.onError) out.push({ code: 'AI_NEEDS_ONERROR', severity: 'error', path: `/nodes/${i}/onError`, message: 'An AI node needs an onError node (timeouts and invalid answers go there).' });
    if (n.type === 'switch' && !n.default) out.push({ code: 'SWITCH_NEEDS_DEFAULT', severity: 'error', path: `/nodes/${i}/default`, message: 'A switch needs a default node.' });
  });
  return out;
}

/** Index of the node a JSON pointer points into (`/nodes/3/...` → 3), or -1. */
export function nodeIndexOfPath(path: string): number {
  const m = /^\/nodes\/(\d+)(?:\/|$)/.exec(path);
  return m ? Number(m[1]) : -1;
}

/** Problems grouped by node id (problems outside nodes go under ""). */
export function problemsByNode(spec: DecisionSpec, problems: ValidationProblem[]): Map<string, ValidationProblem[]> {
  const out = new Map<string, ValidationProblem[]>();
  for (const p of problems) {
    const i = nodeIndexOfPath(p.path);
    const id = i >= 0 ? (spec.nodes[i]?.id ?? '') : '';
    const list = out.get(id) ?? [];
    list.push(p);
    out.set(id, list);
  }
  return out;
}

/** Problems at or below a pointer prefix. */
export function problemsUnder<T extends { path: string }>(problems: T[], prefix: string): T[] {
  return problems.filter((p) => p.path === prefix || p.path.startsWith(prefix + '/'));
}

export function nodeSummary(n: Node): string {
  switch (n.type) {
    case 'rules':
      return `${(n.rules ?? []).length} rules · ${(n.mode ?? 'first_match').replace('_', ' ')}`;
    case 'table':
      return `${(n.rows ?? []).length} rows · ${n.hitPolicy ?? 'first'}`;
    case 'transform':
      return n.expression ? 'one expression' : `${(n.fields ?? []).length} fields · ${n.mode ?? 'build'}`;
    case 'switch':
      return `${(n.cases ?? []).length} cases + default`;
    case 'lookup':
      return n.lookup || 'choose a lookup';
    case 'gate':
      return n.gate || 'choose a gate';
    case 'experiment':
      return n.experiment || 'choose an experiment';
    case 'subdecision':
      return n.decision || 'choose a decision';
    case 'ai':
      return `${n.mode} · ${n.timeoutMs.toLocaleString()} ms`;
    case 'output':
      return n.expression ? 'one expression' : (n.fields ?? []).map((f) => f.target).join(', ') || 'no fields';
  }
}

/** Path of an SVG cubic edge between two node boxes, plus a label point. */
export function edgePath(a: Pos, b: Pos, toEnd = false): { d: string; lx: number; ly: number } {
  const x1 = a.x + NODE_W, y1 = a.y + NODE_H / 2;
  const x2 = toEnd ? b.x - 14 : b.x - 6, y2 = toEnd ? b.y : b.y + NODE_H / 2;
  if (x2 < x1 + 10) {
    // target is behind: loop below
    const dy = Math.max(y1, y2) + NODE_H;
    const d = `M${x1} ${y1} C${x1 + 60} ${y1}, ${x1 + 60} ${dy}, ${(x1 + x2) / 2} ${dy} S${x2 - 60} ${y2}, ${x2} ${y2}`;
    return { d, lx: (x1 + x2) / 2, ly: dy - 6 };
  }
  const m = Math.max(30, (x2 - x1) / 2);
  return { d: `M${x1} ${y1} C${x1 + m} ${y1}, ${x2 - m} ${y2}, ${x2} ${y2}`, lx: (x1 + x2) / 2, ly: (y1 + y2) / 2 - 6 };
}

/** Canvas size that fits every node with room to drag. */
export function canvasSize(pos: Record<string, Pos>): { w: number; h: number } {
  const xs = Object.values(pos).map((p) => p.x), ys = Object.values(pos).map((p) => p.y);
  return { w: Math.max(900, Math.max(0, ...xs) + NODE_W + 160), h: Math.max(360, Math.max(0, ...ys) + NODE_H + 120) };
}
