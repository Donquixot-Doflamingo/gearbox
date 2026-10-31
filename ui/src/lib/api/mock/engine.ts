// In-browser decision evaluation for mock mode. Mirrors the engine's flow rules closely enough for the
// test console, test cases, transform previews and membership checks to behave sensibly.

import type {
  Condition,
  DecisionSpec,
  EvaluateResult,
  ExperimentSpec,
  FieldCondition,
  GateSpec,
  LookupRow,
  Node,
  PathStep,
  Problem,
  SegmentSpec,
  TestCase,
  TestRunResult,
} from '../types.gen';
import { bucket } from '../../model/buckets';
import { allocate } from '../../model/buckets';
import { partialMismatches, resolveData } from '../../model/dataSlot';
import { evaluate, ExprError, type Env, type Fn } from '../../model/expr';
import { validateDocument, type JsonSchema } from '../../model/schemaTree';
import { matchCell, parseCell } from '../../model/tableCell';

/** What the engine needs to read from the world (live resources in an environment). */
export interface World {
  decisionName: string;
  env: string;
  schema(name: string, version: number): JsonSchema | undefined;
  segment(name: string): { spec: SegmentSpec; members: string[] } | undefined;
  lookup(name: string): LookupRow[] | undefined;
  gate(name: string): { spec: GateSpec; killed: boolean } | undefined;
  experiment(name: string): ExperimentSpec | undefined;
  decision(name: string): DecisionSpec | undefined;
}

export interface EvalCtx {
  now?: string;
  unit?: string;
  requestId?: string;
}

class NodeError extends Error {
  constructor(
    message: string,
    readonly code = 'NODE_ERROR',
  ) {
    super(message);
  }
}

function getPath(obj: unknown, dotted: string): unknown {
  let cur = obj;
  for (const seg of dotted.split('.')) {
    if (cur === null || cur === undefined || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

function setPath(obj: Record<string, unknown>, target: string, value: unknown): void {
  const segs = target.split('.');
  let cur: Record<string, unknown> = obj;
  segs.forEach((seg, i) => {
    const m = /^(\w+)\[(\d+)\]$/.exec(seg);
    const last = i === segs.length - 1;
    if (m) {
      const arr = (Array.isArray(cur[m[1]]) ? cur[m[1]] : (cur[m[1]] = [])) as unknown[];
      const idx = Number(m[2]);
      if (last) arr[idx] = value;
      else cur = (arr[idx] ??= {}) as Record<string, unknown>;
      return;
    }
    if (seg.endsWith('[]')) throw new NodeError(`target ${target}: "[]" targets are not supported by the mock engine`);
    if (last) cur[seg] = value;
    else {
      if (!cur[seg] || typeof cur[seg] !== 'object') cur[seg] = {};
      cur = cur[seg] as Record<string, unknown>;
    }
  });
}

function deletePath(obj: Record<string, unknown>, target: string): void {
  const segs = target.split('.');
  let cur: unknown = obj;
  for (const seg of segs.slice(0, -1)) {
    if (!cur || typeof cur !== 'object') return;
    cur = (cur as Record<string, unknown>)[seg];
  }
  if (cur && typeof cur === 'object') delete (cur as Record<string, unknown>)[segs.at(-1)!];
}

function omitNil(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(omitNil);
  if (v && typeof v === 'object' && !(v instanceof Date)) {
    return Object.fromEntries(Object.entries(v).filter(([, x]) => x !== null && x !== undefined).map(([k, x]) => [k, omitNil(x)]));
  }
  return v;
}

const lower = (v: unknown) => (typeof v === 'string' ? v.toLowerCase() : v);
const asNum = (v: unknown) => (typeof v === 'number' ? v : typeof v === 'string' && v.trim() !== '' && !isNaN(Number(v)) ? Number(v) : null);
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b) || (asNum(a) !== null && asNum(a) === asNum(b));

/** Field operators with Gearbox nil semantics: a missing field makes everything false except NOT_EXISTS. */
export function fieldMatches(c: FieldCondition, input: unknown): boolean {
  const v = getPath(input, c.field);
  if (c.op === 'EXISTS') return v !== undefined && v !== null;
  if (c.op === 'NOT_EXISTS') return v === undefined || v === null;
  if (v === undefined || v === null) return false;
  const val = c.value;
  const list = Array.isArray(val) ? val : [val];
  const str = typeof v === 'string' ? v : JSON.stringify(v);
  switch (c.op) {
    case 'EQ':
      return same(v, val);
    case 'NEQ':
      return !same(v, val);
    case 'EQ_CASE_INSENSITIVE':
      return lower(v) === lower(val);
    case 'NEQ_CASE_INSENSITIVE':
      return lower(v) !== lower(val);
    case 'GT':
    case 'GTE':
    case 'LT':
    case 'LTE': {
      const a = asNum(v), b = asNum(val);
      if (a === null || b === null) return false;
      return c.op === 'GT' ? a > b : c.op === 'GTE' ? a >= b : c.op === 'LT' ? a < b : a <= b;
    }
    case 'BETWEEN': {
      const a = asNum(v), lo = asNum(val), hi = asNum(c.valueTo);
      return a !== null && lo !== null && hi !== null && a >= lo && a <= hi;
    }
    case 'IN':
      return list.some((x) => same(v, x));
    case 'NOT_IN':
      return !list.some((x) => same(v, x));
    case 'IN_CASE_INSENSITIVE':
      return list.some((x) => lower(x) === lower(v));
    case 'NOT_IN_CASE_INSENSITIVE':
      return !list.some((x) => lower(x) === lower(v));
    case 'IN_NUMERIC':
      return list.some((x) => asNum(x) !== null && asNum(x) === asNum(v));
    case 'NOT_IN_NUMERIC':
      return !list.some((x) => asNum(x) !== null && asNum(x) === asNum(v));
    case 'CONTAINS':
      return Array.isArray(v) ? v.some((x) => same(x, val)) : str.includes(String(val));
    case 'NOT_CONTAINS':
      return Array.isArray(v) ? !v.some((x) => same(x, val)) : !str.includes(String(val));
    case 'CONTAINS_CASE_INSENSITIVE':
      return Array.isArray(v) ? v.some((x) => lower(x) === lower(val)) : str.toLowerCase().includes(String(val).toLowerCase());
    case 'NOT_CONTAINS_CASE_INSENSITIVE':
      return Array.isArray(v) ? !v.some((x) => lower(x) === lower(val)) : !str.toLowerCase().includes(String(val).toLowerCase());
    case 'CONTAINS_NUMERIC':
      return Array.isArray(v) && v.some((x) => asNum(x) === asNum(val));
    case 'NOT_CONTAINS_NUMERIC':
      return Array.isArray(v) && !v.some((x) => asNum(x) === asNum(val));
    case 'CONTAINS_ANY':
      return Array.isArray(v) && list.some((x) => v.some((y) => same(x, y)));
    case 'CONTAINS_NONE':
      return Array.isArray(v) && !list.some((x) => v.some((y) => same(x, y)));
    case 'STARTS_WITH':
      return str.startsWith(String(val));
    case 'ENDS_WITH':
      return str.endsWith(String(val));
    case 'STARTS_WITH_CASE_INSENSITIVE':
      return str.toLowerCase().startsWith(String(val).toLowerCase());
    case 'ENDS_WITH_CASE_INSENSITIVE':
      return str.toLowerCase().endsWith(String(val).toLowerCase());
    case 'MATCHES':
      return new RegExp(String(val)).test(str);
    case 'NOT_MATCHES':
      return !new RegExp(String(val)).test(str);
    case 'MATCHES_CASE_INSENSITIVE':
      return new RegExp(String(val), 'i').test(str);
    case 'NOT_MATCHES_CASE_INSENSITIVE':
      return !new RegExp(String(val), 'i').test(str);
    case 'IS_TRUE':
      return v === true || v === 'true';
    case 'IS_FALSE':
      return v === false || v === 'false';
  }
  return false;
}

export class Engine {
  private depth = 0;
  constructor(
    readonly world: World,
    readonly ctx: EvalCtx,
  ) {}

  private ctxValue(): Record<string, unknown> {
    return { now: new Date(this.ctx.now ?? Date.now()), unit: this.ctx.unit ?? '', requestId: this.ctx.requestId ?? 'req_console', env: this.world.env };
  }

  fns(): Record<string, Fn> {
    return {
      lookup: (table, key, def) => {
        const rows = this.world.lookup(String(table));
        if (!rows) throw new ExprError(`lookup ${String(table)} does not exist`);
        const row = rows.find((r) => r.key === String(key));
        return row ? row.value : (def ?? null);
      },
      inSegment: (name, key) => this.inSegment(String(name), key, undefined),
      gate: (name) => this.gate(String(name), {}).value,
      variant: (name) => this.assign(String(name), {}).variant ?? null,
      bucket: (salt) => (this.ctx.unit ? bucket(String(salt), this.ctx.unit) : null),
    };
  }

  expr(src: string, env: Env): unknown {
    return evaluate(src, env, { fns: this.fns() });
  }

  inSegment(name: string, key: unknown, attributes: Record<string, unknown> | undefined): boolean {
    const seg = this.world.segment(name);
    if (!seg) throw new NodeError(`segment ${name} does not exist`, 'SEGMENT_NOT_FOUND');
    const spec = seg.spec;
    if (spec.type === 'static') return key !== null && key !== undefined && seg.members.includes(String(key));
    if (spec.type === 'dynamic') {
      if (!spec.dynamic) return false;
      return this.cond(spec.dynamic.when, attributes ?? {}, {});
    }
    const unit = key ?? this.ctx.unit;
    if (!unit) return false;
    const r = spec.rollout ?? { percent: 0 };
    if (r.include && !this.cond(r.include, attributes ?? {}, {})) return false;
    return bucket(r.salt || name, String(unit)) < Math.round((r.percent ?? 0) * 100);
  }

  gate(name: string, attrs: Record<string, unknown>): { value: boolean; rule: string } {
    const g = this.world.gate(name);
    if (!g) throw new NodeError(`gate ${name} does not exist`);
    if (g.killed) return { value: false, rule: 'kill_switch' };
    if (g.spec.enabled === false) return { value: false, rule: 'disabled' };
    for (const r of g.spec.rules ?? []) {
      if (r.rollout) {
        if (!this.ctx.unit) continue;
        if (bucket(r.rollout.salt || `${name}.${r.id}`, this.ctx.unit) >= Math.round((r.rollout.percent ?? 0) * 100)) continue;
      }
      if (this.cond(r.when, attrs, {})) return { value: r.value, rule: r.id };
    }
    return { value: !!g.spec.default, rule: 'default' };
  }

  assign(name: string, attrs: Record<string, unknown>): { in_experiment: boolean; variant?: string; parameters?: unknown; reason: string } {
    const e = this.world.experiment(name);
    if (!e) throw new NodeError(`experiment ${name} does not exist`);
    if (!this.ctx.unit) return { in_experiment: false, reason: 'no_unit' };
    if (e.status === 'stopped') {
      const v = e.variants.find((x) => x.name === e.stoppedVariant) ?? e.variants[0];
      return { in_experiment: false, variant: v?.name, parameters: v?.parameters, reason: 'stopped' };
    }
    if (e.status !== 'running') return { in_experiment: false, reason: 'not_targeted' };
    if (e.targeting && !this.cond(e.targeting, attrs, {})) return { in_experiment: false, reason: 'not_targeted' };
    const b = bucket(e.salt || name, this.ctx.unit);
    const hit = allocate(e).find((r) => b >= r.from && b <= r.to);
    if (!hit || hit.kind === 'holdout') return { in_experiment: false, reason: 'holdout' };
    const v = e.variants.find((x) => x.name === hit.name);
    return { in_experiment: true, variant: hit.name, parameters: v?.parameters ?? null, reason: 'assigned' };
  }

  cond(c: Condition, input: unknown, env: Env): boolean {
    if ('all' in c) return c.all.every((x) => this.cond(x, input, env));
    if ('any' in c) return c.any.some((x) => this.cond(x, input, env));
    if ('not' in c) return !this.cond(c.not, input, env);
    if ('field' in c) return fieldMatches(c, input);
    const exprEnv = { ...env, input };
    if ('segment' in c) {
      let attrs: Record<string, unknown> | undefined;
      if (c.attributes) attrs = Object.fromEntries(Object.entries(c.attributes).map(([k, e]) => [k, this.expr(e, exprEnv)]));
      const key = c.key ? this.expr(c.key, exprEnv) : undefined;
      const r = this.inSegment(c.segment, key, attrs);
      return c.op === 'IN_SEGMENT' ? r : !r;
    }
    if ('gate' in c) {
      const r = this.gate(c.gate, (input ?? {}) as Record<string, unknown>).value;
      return c.op === 'GATE_PASSES' ? r : !r;
    }
    if ('experiment' in c) {
      const a = this.assign(c.experiment, (input ?? {}) as Record<string, unknown>);
      return !!a.variant && a.in_experiment && c.variants.includes(a.variant);
    }
    const v = this.expr(c.expr, exprEnv);
    if (typeof v !== 'boolean' && v !== null) throw new NodeError(`expression ${c.expr} is not boolean`);
    return v === true;
  }

  private checkSchema(ref: { name: string; version: number } | undefined, doc: unknown, what: string): void {
    if (!ref) return;
    const sch = this.world.schema(ref.name, ref.version);
    if (!sch) throw new NodeError(`${what} schema ${ref.name}@${ref.version} is not registered`, 'SCHEMA_NOT_FOUND');
    const errs = validateDocument(sch, doc);
    if (errs.length) throw new NodeError(`${what} does not match ${ref.name}@${ref.version}: ${errs.slice(0, 3).map((e) => `${e.path || '/'} ${e.message}`).join('; ')}`, what === 'input' ? 'INPUT_SCHEMA' : 'OUTPUT_SCHEMA');
  }

  /** Run one node. Returns its output, outcome and (for switch) the chosen next node. */
  runNode(node: Node, input: unknown, env: Env, opts: { skipAI: boolean }): { output: unknown; outcome: string; next?: string } {
    const data = (v: unknown) => resolveData(v, (e) => this.expr(e, { ...env, input }));
    switch (node.type) {
      case 'rules': {
        this.checkSchema(node.inputSchema, input, 'input');
        const rules = [...(node.rules ?? [])].map((r, i) => ({ r, i })).sort((a, b) => (b.r.priority ?? 0) - (a.r.priority ?? 0) || a.i - b.i).map((x) => x.r);
        const matches: { id: string; then: unknown }[] = [];
        for (const r of rules) {
          if (r.enabled === false) continue;
          if (r.rollout) {
            if (!this.ctx.unit) continue;
            const salt = r.rollout.salt || `${this.world.decisionName}.${node.id}.${r.id}`;
            if (bucket(salt, this.ctx.unit) >= Math.round(r.rollout.percent * 100)) continue;
          }
          if (this.cond(r.when, input, env)) {
            matches.push({ id: r.id, then: data(r.then) });
            if ((node.mode ?? 'first_match') === 'first_match') break;
          }
        }
        if (node.mode === 'all_matches') return { output: { matches: matches.map((m) => m.then) }, outcome: matches.map((m) => m.id).join(',') || 'none' };
        if (matches.length) {
          this.checkSchema(node.outputSchema, matches[0].then, 'output');
          return { output: matches[0].then, outcome: matches[0].id };
        }
        const d = node.default === undefined ? null : data(node.default);
        if (d !== null) this.checkSchema(node.outputSchema, d, 'output');
        return { output: d, outcome: 'default' };
      }
      case 'table': {
        this.checkSchema(node.inputSchema, input, 'input');
        const values = Object.fromEntries((node.inputs ?? []).map((c) => [c.name, this.expr(c.expr, { ...env, input })]));
        const hits = (node.rows ?? []).filter((row) =>
          (node.inputs ?? []).every((c) => matchCell(parseCell(row.when?.[c.name]), values[c.name], (e, value) => this.expr(e, { ...env, input, value })) === true),
        );
        const pick = (row: (typeof hits)[number]) => Object.fromEntries((node.outputs ?? []).map((o) => [o, data((row.then as Record<string, unknown>)?.[o])]));
        const policy = node.hitPolicy ?? 'first';
        if (!hits.length) return { output: node.default === undefined ? null : data(node.default), outcome: 'default' };
        if (policy === 'unique' && hits.length > 1) throw new NodeError(`rows ${hits.map((h) => h.id).join(', ')} all match; hit policy is unique`);
        if (policy === 'collect') return { output: { matches: hits.map(pick) }, outcome: hits.map((h) => h.id).join(',') };
        const row = policy === 'priority' ? [...hits].sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0))[0] : hits[0];
        const out = pick(row);
        this.checkSchema(node.outputSchema, out, 'output');
        return { output: out, outcome: row.id };
      }
      case 'transform':
      case 'output': {
        let out: Record<string, unknown> | unknown;
        if (node.expression) out = this.expr(node.expression, { ...env, input });
        else {
          const o: Record<string, unknown> = node.type === 'transform' && node.mode === 'patch' ? JSON.parse(JSON.stringify(input ?? {})) : {};
          for (const f of node.fields ?? []) {
            if (!f.target) continue;
            const v = this.expr(f.expr, { ...env, input, out: o });
            setPath(o, f.target, v);
          }
          if (node.type === 'transform') {
            for (const d of node.defaults ?? []) if (getPath(o, d.target) === undefined || getPath(o, d.target) === null) setPath(o, d.target, data(d.value));
            for (const p of node.drop ?? []) deletePath(o, p);
          }
          out = node.type === 'transform' && node.omitNil === false ? o : omitNil(o);
        }
        if (node.type === 'transform') this.checkSchema(node.outputSchema, out, 'output');
        return { output: out, outcome: 'ok' };
      }
      case 'switch': {
        for (const [i, c] of (node.cases ?? []).entries()) {
          const v = this.expr(c.when, { ...env, input });
          if (v === true) return { output: { case: c.id || `case_${i + 1}` }, outcome: c.id || `case ${i + 1}`, next: c.next };
        }
        return { output: { case: 'default' }, outcome: 'default', next: node.default };
      }
      case 'lookup': {
        const rows = this.world.lookup(node.lookup);
        if (!rows) throw new NodeError(`lookup ${node.lookup} does not exist`);
        const key = this.expr(node.key, { ...env, input });
        const row = rows.find((r) => r.key === String(key));
        return row ? { output: row.value, outcome: 'hit' } : { output: node.default === undefined ? null : data(node.default), outcome: 'miss' };
      }
      case 'gate': {
        const g = this.gate(node.gate, (input ?? {}) as Record<string, unknown>);
        return { output: g, outcome: String(g.value) };
      }
      case 'experiment': {
        const a = this.assign(node.experiment, (input ?? {}) as Record<string, unknown>);
        return { output: a, outcome: a.variant ?? a.reason };
      }
      case 'subdecision': {
        const spec = this.world.decision(node.decision);
        if (!spec) throw new NodeError(`decision ${node.decision} does not exist`);
        if (this.depth > 4) throw new NodeError('subdecisions nest too deep');
        const sub = new Engine({ ...this.world, decisionName: node.decision }, this.ctx);
        sub.depth = this.depth + 1;
        const r = sub.run(spec, node.input ? this.expr(node.input, { ...env, input }) : input, opts);
        if (r.error) throw new NodeError(`${node.decision}: ${r.error.detail ?? r.error.title}`);
        return { output: r.output, outcome: r.fallbackUsed ? 'fallback' : 'ok' };
      }
      case 'ai': {
        if (opts.skipAI) throw new NodeError('AI call skipped in the test console (skipAI); took onError', 'AI_SKIPPED');
        if (node.mode === 'shadow') return { output: null, outcome: 'shadow' };
        // mock answer: the first value the schema allows
        const props = (node.responseSchema as JsonSchema).properties as Record<string, JsonSchema> | undefined;
        const answer = Object.fromEntries(Object.entries(props ?? {}).map(([k, v]) => [k, Array.isArray(v.enum) ? v.enum[0] : v.type === 'number' || v.type === 'integer' ? 0 : 'mock answer']));
        return { output: answer, outcome: 'ok' };
      }
    }
  }

  run(spec: DecisionSpec, request: unknown, opts: { skipAI: boolean } = { skipAI: true }): EvaluateResult {
    const started = performance.now();
    const path: PathStep[] = [];
    const nodes: Record<string, unknown> = {};
    const env: Env = { request, nodes, ctx: this.ctxValue() };
    const byId = new Map(spec.nodes.map((n) => [n.id, n]));
    const result = (output: unknown, fallbackUsed: boolean, error?: Problem): EvaluateResult => ({
      output,
      decisionVersion: `${this.world.decisionName}@draft`,
      dependencyHash: 'sha256:mock',
      fallbackUsed,
      evalMicros: Math.max(1, Math.round((performance.now() - started) * 1000)),
      evaluatedAt: (env.ctx as { now: Date }).now.toISOString(),
      path,
      ...(error ? { error } : {}),
    });
    const fallback = (msg: string, code: string): EvaluateResult => {
      if (spec.fallback !== undefined) return result(resolveData(spec.fallback, (e) => this.expr(e, env)), true);
      return result(null, true, { title: 'Decision failed', status: 422, code, detail: msg });
    };
    // entry input check
    const entry = byId.get(spec.entry);
    if (!entry) return result(null, false, { title: 'Entry node not found', status: 422, code: 'VALIDATION_FAILED', detail: `entry ${spec.entry} is not a node` });
    const entrySchema = entry.type === 'rules' || entry.type === 'table' ? entry.inputSchema : spec.inputSchema;
    if (entrySchema && !entry.from) {
      const sch = this.world.schema(entrySchema.name, entrySchema.version);
      if (sch) {
        const errs = validateDocument(sch, request);
        if (errs.length) return result(null, false, { title: 'Input does not match the schema', status: 400, code: 'INVALID_ARGUMENT', detail: `${entrySchema.name}@${entrySchema.version}`, errors: errs.map((e) => ({ path: e.path || '/', message: e.message })) });
      }
    }
    let cur: string | undefined = spec.entry;
    let prev: unknown = request;
    let last: unknown = null;
    for (let steps = 0; cur && steps < 220; steps++) {
      if (cur === 'end') return result(last, false);
      const node = byId.get(cur);
      if (!node) return fallback(`node ${cur} does not exist`, 'NODE_NOT_FOUND');
      const t0 = performance.now();
      let input: unknown = prev;
      try {
        if (node.from) input = this.expr(node.from, env);
        const r = this.runNode(node, input, env, opts);
        nodes[node.id] = r.output;
        path.push({ nodeId: node.id, nodeType: node.type, outcome: r.outcome, micros: Math.round((performance.now() - t0) * 1000), input, output: r.output });
        last = r.output;
        prev = r.output;
        if (node.type === 'ai' && r.outcome === 'shadow') {
          cur = node.onError;
          prev = input;
          continue;
        }
        cur = node.type === 'switch' ? r.next : node.next;
        if (!cur) break;
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        path.push({ nodeId: node.id, nodeType: node.type, outcome: 'error', micros: Math.round((performance.now() - t0) * 1000), input, error: msg });
        if (node.onError) {
          cur = node.onError;
          prev = input;
          continue;
        }
        if (e instanceof NodeError && e.code === 'INPUT_SCHEMA' && node.id === spec.entry) {
          return result(null, false, { title: 'Input does not match the schema', status: 400, code: 'INVALID_ARGUMENT', detail: msg });
        }
        return fallback(msg, e instanceof NodeError ? e.code : 'EVAL_ERROR');
      }
    }
    if (spec.output?.schema) {
      const sch = this.world.schema(spec.output.schema.name, spec.output.schema.version);
      const errs = sch ? validateDocument(sch, last) : [];
      if (errs.length) return fallback(`output does not match ${spec.output.schema.name}@${spec.output.schema.version}: ${errs[0].path} ${errs[0].message}`, 'OUTPUT_SCHEMA');
    }
    return result(last, false);
  }
}

export function runTests(world: World, spec: DecisionSpec, tests: TestCase[] = spec.tests ?? []): TestRunResult {
  const results: TestRunResult['results'] = tests.map((t) => {
    const engine = new Engine(world, { now: t.ctx?.now ?? '2026-10-30T10:00:00Z', unit: t.ctx?.unit });
    const t0 = performance.now();
    const r = engine.run(spec, t.input);
    const micros = Math.max(1, Math.round((performance.now() - t0) * 1000));
    const path = r.path.map(({ nodeId, nodeType, outcome, micros: m, error }) => ({ nodeId, nodeType, outcome, micros: m, error }));
    if (t.expectError) {
      const ok = r.error?.code === t.expectError;
      return { name: t.name, ok, expected: { error: t.expectError }, actual: r.error ? { error: r.error.code } : r.output, error: ok ? undefined : `expected error ${t.expectError}, got ${r.error?.code ?? 'a result'}`, micros, path };
    }
    if (r.error) return { name: t.name, ok: false, error: `${r.error.code}: ${r.error.detail ?? r.error.title}`, micros, path };
    const mismatches = t.expect === undefined ? [] : partialMismatches(t.expect, r.output);
    let error: string | undefined;
    if (t.expectPath) {
      const got = r.path.map((p) => `${p.nodeId}:${p.outcome}`);
      if (JSON.stringify(got) !== JSON.stringify(t.expectPath)) error = `path was ${got.join(' → ')}`;
    }
    if (r.fallbackUsed && !error) error = `fallback used: ${r.path.find((p) => p.error)?.error ?? 'a node failed'}`;
    return { name: t.name, ok: mismatches.length === 0 && !error, expected: t.expect, actual: r.output, mismatches, error, micros, path };
  });
  return { passed: results.filter((r) => r.ok).length, failed: results.filter((r) => !r.ok).length, results };
}
