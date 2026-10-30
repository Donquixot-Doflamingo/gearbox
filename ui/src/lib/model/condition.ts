// Condition builder model: the all/any/not tree with field, segment, gate, variant and expr leaves.
// Edits are immutable and addressed by a child-index path from the root.

import type { Condition, FieldCondition, FieldOperator, SegmentCondition } from '../api/types.gen';
import type { FieldInfo, SType } from './schemaTree';

export type CondKind = 'all' | 'any' | 'not' | 'field' | 'segment' | 'gate' | 'variant' | 'expr';
export type GroupKind = 'all' | 'any' | 'not';
export type LeafKind = Exclude<CondKind, GroupKind>;
/** Child indexes from the root; `not` has one child at index 0. */
export type CondPath = number[];

export const LEAF_KINDS: { kind: LeafKind; label: string; hint: string }[] = [
  { kind: 'field', label: 'Field', hint: 'Compare a field of the node input' },
  { kind: 'segment', label: 'Segment', hint: 'Unit or value is (not) in a segment' },
  { kind: 'gate', label: 'Gate', hint: 'Another gate passes or fails for this unit' },
  { kind: 'variant', label: 'Variant', hint: 'Unit is assigned one of these experiment variants' },
  { kind: 'expr', label: 'Expression', hint: 'Any boolean Expr expression' },
];

export function condKind(c: Condition): CondKind {
  if ('all' in c) return 'all';
  if ('any' in c) return 'any';
  if ('not' in c) return 'not';
  if ('field' in c) return 'field';
  if ('segment' in c) return 'segment';
  if ('gate' in c) return 'gate';
  if ('experiment' in c) return 'variant';
  return 'expr';
}

export function isGroup(c: Condition): boolean {
  const k = condKind(c);
  return k === 'all' || k === 'any' || k === 'not';
}

export function children(c: Condition): Condition[] {
  if ('all' in c) return c.all;
  if ('any' in c) return c.any;
  if ('not' in c) return [c.not];
  return [];
}

function withChildren(c: Condition, kids: Condition[]): Condition {
  if ('all' in c) return { all: kids };
  if ('any' in c) return { any: kids };
  if ('not' in c) return { not: kids[0] ?? newLeaf('expr') };
  return c;
}

export function newLeaf(kind: LeafKind): Condition {
  switch (kind) {
    case 'field':
      return { field: '', op: 'EQ', value: '' };
    case 'segment':
      return { segment: '', op: 'IN_SEGMENT' };
    case 'gate':
      return { gate: '', op: 'GATE_PASSES' };
    case 'variant':
      return { experiment: '', variants: [] as string[] };
    case 'expr':
      return { expr: 'true' };
  }
}

export function newGroup(kind: 'all' | 'any', kids: Condition[] = [newLeaf('field')]): Condition {
  return kind === 'all' ? { all: kids } : { any: kids };
}

export function getCond(root: Condition, path: CondPath): Condition | undefined {
  let cur: Condition | undefined = root;
  for (const i of path) {
    if (!cur) return undefined;
    cur = children(cur)[i];
  }
  return cur;
}

/** Replace the node at `path`. */
export function replaceCond(root: Condition, path: CondPath, next: Condition): Condition {
  if (path.length === 0) return next;
  const [i, ...rest] = path;
  const kids = [...children(root)];
  if (i < 0 || i >= kids.length) return root;
  kids[i] = replaceCond(kids[i], rest, next);
  return withChildren(root, kids);
}

/** Remove the node at `path`. Removing the root returns null; a `not` whose child is removed goes too. */
export function removeCond(root: Condition, path: CondPath): Condition | null {
  if (path.length === 0) return null;
  const parentPath = path.slice(0, -1);
  const parent = getCond(root, parentPath);
  if (!parent) return root;
  if ('not' in parent) return removeCond(root, parentPath);
  const kids = children(parent).filter((_, j) => j !== path[path.length - 1]);
  if (kids.length === 0) return removeCond(root, parentPath);
  return replaceCond(root, parentPath, withChildren(parent, kids));
}

export function appendChild(root: Condition, groupPath: CondPath, child: Condition): Condition {
  const g = getCond(root, groupPath);
  if (!g || !isGroup(g) || 'not' in g) return root;
  return replaceCond(root, groupPath, withChildren(g, [...children(g), child]));
}

export function moveChild(root: Condition, groupPath: CondPath, from: number, to: number): Condition {
  const g = getCond(root, groupPath);
  if (!g) return root;
  const kids = [...children(g)];
  if (from < 0 || from >= kids.length || to < 0 || to >= kids.length) return root;
  const [x] = kids.splice(from, 1);
  kids.splice(to, 0, x);
  return replaceCond(root, groupPath, withChildren(g, kids));
}

/** Wrap the node at `path` in a new group. */
export function wrapCond(root: Condition, path: CondPath, kind: GroupKind): Condition {
  const c = getCond(root, path);
  if (!c) return root;
  const wrapped: Condition = kind === 'not' ? { not: c } : kind === 'all' ? { all: [c] } : { any: [c] };
  return replaceCond(root, path, wrapped);
}

/** Replace a group with its only child (or a `not` with its child). */
export function unwrapCond(root: Condition, path: CondPath): Condition {
  const c = getCond(root, path);
  if (!c) return root;
  const kids = children(c);
  if (kids.length !== 1) return root;
  return replaceCond(root, path, kids[0]);
}

/** Switch a group between all (AND) and any (OR). */
export function setGroupKind(root: Condition, path: CondPath, kind: 'all' | 'any'): Condition {
  const c = getCond(root, path);
  if (!c || !('all' in c || 'any' in c)) return root;
  return replaceCond(root, path, newGroup(kind, children(c)));
}

/** Change a leaf's kind (keeps nothing; a new empty leaf of that kind). */
export function setLeafKind(root: Condition, path: CondPath, kind: LeafKind): Condition {
  return replaceCond(root, path, newLeaf(kind));
}

/** Count leaves (for summaries). */
export function leafCount(c: Condition): number {
  return isGroup(c) ? children(c).reduce((n, k) => n + leafCount(k), 0) : 1;
}

// ---------------------------------------------------------------- operators

export type ValueKind = 'none' | 'scalar' | 'list' | 'range' | 'regex';
export type OpTarget = 'string' | 'number' | 'boolean' | 'array' | 'any';

export interface OperatorInfo {
  op: FieldOperator;
  label: string;
  valueKind: ValueKind;
  /** Field types the operator applies to. */
  targets: OpTarget[];
  /** Element type of list values (for IN_NUMERIC etc.). */
  valueType?: 'string' | 'number';
}

const S: OpTarget[] = ['string'];
const N: OpTarget[] = ['number'];
const A: OpTarget[] = ['array'];
const SA: OpTarget[] = ['string', 'array'];
const ANY: OpTarget[] = ['any'];

export const OPERATORS: OperatorInfo[] = [
  { op: 'EQ', label: 'equals', valueKind: 'scalar', targets: ['string', 'number', 'boolean'] },
  { op: 'NEQ', label: 'does not equal', valueKind: 'scalar', targets: ['string', 'number', 'boolean'] },
  { op: 'EQ_CASE_INSENSITIVE', label: 'equals (any case)', valueKind: 'scalar', targets: S },
  { op: 'NEQ_CASE_INSENSITIVE', label: 'does not equal (any case)', valueKind: 'scalar', targets: S },
  { op: 'GT', label: '>', valueKind: 'scalar', targets: N, valueType: 'number' },
  { op: 'GTE', label: '≥', valueKind: 'scalar', targets: N, valueType: 'number' },
  { op: 'LT', label: '<', valueKind: 'scalar', targets: N, valueType: 'number' },
  { op: 'LTE', label: '≤', valueKind: 'scalar', targets: N, valueType: 'number' },
  { op: 'BETWEEN', label: 'between', valueKind: 'range', targets: N, valueType: 'number' },
  { op: 'IN', label: 'is one of', valueKind: 'list', targets: ['string', 'number'] },
  { op: 'NOT_IN', label: 'is not one of', valueKind: 'list', targets: ['string', 'number'] },
  { op: 'IN_CASE_INSENSITIVE', label: 'is one of (any case)', valueKind: 'list', targets: S, valueType: 'string' },
  { op: 'NOT_IN_CASE_INSENSITIVE', label: 'is not one of (any case)', valueKind: 'list', targets: S, valueType: 'string' },
  { op: 'IN_NUMERIC', label: 'is one of (numbers)', valueKind: 'list', targets: ['number', 'string'], valueType: 'number' },
  { op: 'NOT_IN_NUMERIC', label: 'is not one of (numbers)', valueKind: 'list', targets: ['number', 'string'], valueType: 'number' },
  { op: 'CONTAINS', label: 'contains', valueKind: 'scalar', targets: SA },
  { op: 'NOT_CONTAINS', label: 'does not contain', valueKind: 'scalar', targets: SA },
  { op: 'CONTAINS_CASE_INSENSITIVE', label: 'contains (any case)', valueKind: 'scalar', targets: SA, valueType: 'string' },
  { op: 'NOT_CONTAINS_CASE_INSENSITIVE', label: 'does not contain (any case)', valueKind: 'scalar', targets: SA, valueType: 'string' },
  { op: 'CONTAINS_NUMERIC', label: 'contains number', valueKind: 'scalar', targets: A, valueType: 'number' },
  { op: 'NOT_CONTAINS_NUMERIC', label: 'does not contain number', valueKind: 'scalar', targets: A, valueType: 'number' },
  { op: 'CONTAINS_ANY', label: 'contains any of', valueKind: 'list', targets: A },
  { op: 'CONTAINS_NONE', label: 'contains none of', valueKind: 'list', targets: A },
  { op: 'STARTS_WITH', label: 'starts with', valueKind: 'scalar', targets: S, valueType: 'string' },
  { op: 'ENDS_WITH', label: 'ends with', valueKind: 'scalar', targets: S, valueType: 'string' },
  { op: 'STARTS_WITH_CASE_INSENSITIVE', label: 'starts with (any case)', valueKind: 'scalar', targets: S, valueType: 'string' },
  { op: 'ENDS_WITH_CASE_INSENSITIVE', label: 'ends with (any case)', valueKind: 'scalar', targets: S, valueType: 'string' },
  { op: 'MATCHES', label: 'matches regex', valueKind: 'regex', targets: S, valueType: 'string' },
  { op: 'NOT_MATCHES', label: 'does not match regex', valueKind: 'regex', targets: S, valueType: 'string' },
  { op: 'MATCHES_CASE_INSENSITIVE', label: 'matches regex (any case)', valueKind: 'regex', targets: S, valueType: 'string' },
  { op: 'NOT_MATCHES_CASE_INSENSITIVE', label: 'does not match regex (any case)', valueKind: 'regex', targets: S, valueType: 'string' },
  { op: 'EXISTS', label: 'exists', valueKind: 'none', targets: ANY },
  { op: 'NOT_EXISTS', label: 'is missing', valueKind: 'none', targets: ANY },
  { op: 'IS_TRUE', label: 'is true', valueKind: 'none', targets: ['boolean', 'string'] },
  { op: 'IS_FALSE', label: 'is false', valueKind: 'none', targets: ['boolean', 'string'] },
];

const BY_OP = new Map(OPERATORS.map((o) => [o.op, o]));

export function operatorInfo(op: FieldOperator | string): OperatorInfo | undefined {
  return BY_OP.get(op as FieldOperator);
}

export function targetOf(t: SType | 'any' | undefined): OpTarget {
  if (t === 'integer' || t === 'number') return 'number';
  if (t === 'string' || t === 'boolean' || t === 'array') return t;
  return 'any';
}

/** Operators that make sense for a field type ('any' → every operator). */
export function operatorsFor(t: SType | 'any' | undefined): OperatorInfo[] {
  if (t === 'object' || t === 'null') return OPERATORS.filter((o) => o.targets.includes('any'));
  const target = targetOf(t);
  if (target === 'any') return OPERATORS;
  return OPERATORS.filter((o) => o.targets.includes('any') || o.targets.includes(target));
}

// ---------------------------------------------------------------- values

export interface ParsedValue {
  value?: unknown;
  valueTo?: unknown;
  error?: string;
}

function parseScalar(text: string, type: 'string' | 'number' | 'boolean' | 'any'): { v?: unknown; error?: string } {
  const t = text.trim();
  if (type === 'number') {
    if (t === '') return { error: 'enter a number' };
    const n = Number(t.replace(/[_,](?=\d{2,3}\b)/g, ''));
    return Number.isFinite(n) ? { v: n } : { error: `"${t}" is not a number` };
  }
  if (type === 'boolean') {
    if (t === 'true' || t === 'false') return { v: t === 'true' };
    return { error: 'enter true or false' };
  }
  if (type === 'any') {
    if (/^-?\d+(\.\d+)?$/.test(t)) return { v: Number(t) };
    if (t === 'true' || t === 'false') return { v: t === 'true' };
  }
  // strip one pair of quotes so "Pune" and Pune both mean the text
  const m = /^"(.*)"$/.exec(t);
  return { v: m ? m[1] : text };
}

function splitList(text: string): string[] {
  const t = text.trim();
  if (t.startsWith('[')) {
    try {
      const arr = JSON.parse(t);
      if (Array.isArray(arr)) return arr.map((x) => (typeof x === 'string' ? JSON.stringify(x) : String(x)));
    } catch {
      /* fall through to comma split */
    }
  }
  const out: string[] = [];
  let cur = '';
  let q = false;
  for (const ch of t) {
    if (ch === '"') q = !q;
    if (ch === ',' && !q) {
      out.push(cur.trim());
      cur = '';
    } else cur += ch;
  }
  if (cur.trim() !== '') out.push(cur.trim());
  return out.filter((x) => x !== '');
}

function elementType(info: OperatorInfo, field: SType | 'any' | undefined): 'string' | 'number' | 'boolean' | 'any' {
  if (info.valueType) return info.valueType;
  const t = targetOf(field);
  if (t === 'number' || t === 'string' || t === 'boolean') return t;
  return 'any';
}

/** Parse what the author typed into `value`/`valueTo` for an operator and field type. */
export function parseValue(op: FieldOperator, field: SType | 'any' | undefined, text: string, textTo = ''): ParsedValue {
  const info = operatorInfo(op);
  if (!info) return { error: `unknown operator ${op}` };
  const et = elementType(info, field);
  switch (info.valueKind) {
    case 'none':
      return {};
    case 'scalar': {
      const r = parseScalar(text, et);
      return r.error ? { error: r.error } : { value: r.v };
    }
    case 'regex': {
      try {
        new RegExp(text);
      } catch {
        return { value: text, error: 'not a valid regular expression' };
      }
      return { value: text };
    }
    case 'range': {
      const a = parseScalar(text, 'number');
      const b = parseScalar(textTo, 'number');
      if (a.error) return { error: `from: ${a.error}` };
      if (b.error) return { error: `to: ${b.error}` };
      if ((a.v as number) > (b.v as number)) return { value: a.v, valueTo: b.v, error: 'from is greater than to' };
      return { value: a.v, valueTo: b.v };
    }
    case 'list': {
      const parts = splitList(text);
      if (parts.length === 0) return { error: 'enter at least one value' };
      const vals: unknown[] = [];
      for (const p of parts) {
        const r = parseScalar(p, et === 'boolean' ? 'any' : et);
        if (r.error) return { error: r.error };
        vals.push(r.v);
      }
      return { value: vals };
    }
  }
}

/** Text for an input box from a stored value. */
export function valueText(v: unknown): string {
  if (v === undefined || v === null) return '';
  if (Array.isArray(v)) return v.map((x) => (typeof x === 'string' && /[,"]/.test(x) ? JSON.stringify(x) : String(x))).join(', ');
  return String(v);
}

/** Make a field leaf consistent after the operator changes (drop or reshape value). */
export function withOperator(c: FieldCondition, op: FieldOperator, fieldType?: SType | 'any'): FieldCondition {
  const info = operatorInfo(op);
  const out: FieldCondition = { field: c.field, op };
  if (!info || info.valueKind === 'none') return out;
  const prev = operatorInfo(c.op);
  if (prev?.valueKind === info.valueKind) {
    if ('value' in c) out.value = c.value;
    if (info.valueKind === 'range' && 'valueTo' in c) out.valueTo = c.valueTo;
    return out;
  }
  if (info.valueKind === 'list') out.value = c.value === undefined || c.value === '' ? [] : Array.isArray(c.value) ? c.value : [c.value];
  else if (info.valueKind === 'range') {
    out.value = typeof c.value === 'number' ? c.value : 0;
    out.valueTo = typeof c.valueTo === 'number' ? c.valueTo : 0;
  } else out.value = Array.isArray(c.value) ? (c.value[0] ?? '') : (c.value ?? (targetOf(fieldType) === 'number' ? 0 : ''));
  return out;
}

// ---------------------------------------------------------------- text and expressions

function lit(v: unknown): string {
  return typeof v === 'string' ? JSON.stringify(v) : JSON.stringify(v ?? null);
}

/** One-line human summary of a condition. */
export function describe(c: Condition, depth = 0): string {
  const k = condKind(c);
  const wrap = (s: string) => (depth > 0 ? `(${s})` : s);
  switch (k) {
    case 'all':
    case 'any': {
      const kids = children(c);
      if (kids.length === 1) return describe(kids[0], depth);
      return wrap(kids.map((x) => describe(x, depth + 1)).join(k === 'all' ? ' and ' : ' or '));
    }
    case 'not':
      return `not ${describe(children(c)[0], depth + 1)}`;
    case 'field': {
      const f = c as FieldCondition;
      const info = operatorInfo(f.op);
      const label = info?.label ?? f.op;
      if (!info || info.valueKind === 'none') return `${f.field || '?'} ${label}`;
      if (info.valueKind === 'range') return `${f.field || '?'} between ${valueText(f.value)} and ${valueText(f.valueTo)}`;
      if (info.valueKind === 'list') return `${f.field || '?'} ${label} [${valueText(f.value)}]`;
      return `${f.field || '?'} ${label} ${lit(f.value)}`;
    }
    case 'segment': {
      const s = c as SegmentCondition;
      return `${s.key ? s.key + ' ' : s.attributes ? 'attributes ' : 'unit '}${s.op === 'IN_SEGMENT' ? 'in' : 'not in'} ${s.segment || '?'}`;
    }
    case 'gate':
      return 'gate' in c ? `gate ${c.gate || '?'} ${c.op === 'GATE_PASSES' ? 'passes' : 'fails'}` : '';
    case 'variant':
      return 'experiment' in c ? `${c.experiment || '?'} variant in [${c.variants.join(', ')}]` : '';
    case 'expr':
      return 'expr' in c ? c.expr : '';
  }
}

const OP_EXPR: Partial<Record<FieldOperator, (f: string, v: string, v2: string) => string>> = {
  EQ: (f, v) => `${f} == ${v}`,
  NEQ: (f, v) => `${f} != ${v}`,
  EQ_CASE_INSENSITIVE: (f, v) => `lower(${f}) == lower(${v})`,
  NEQ_CASE_INSENSITIVE: (f, v) => `lower(${f}) != lower(${v})`,
  GT: (f, v) => `${f} > ${v}`,
  GTE: (f, v) => `${f} >= ${v}`,
  LT: (f, v) => `${f} < ${v}`,
  LTE: (f, v) => `${f} <= ${v}`,
  BETWEEN: (f, v, v2) => `${f} >= ${v} && ${f} <= ${v2}`,
  IN: (f, v) => `${f} in ${v}`,
  NOT_IN: (f, v) => `${f} not in ${v}`,
  IN_CASE_INSENSITIVE: (f, v) => `lower(${f}) in map(${v}, lower(#))`,
  NOT_IN_CASE_INSENSITIVE: (f, v) => `lower(${f}) not in map(${v}, lower(#))`,
  IN_NUMERIC: (f, v) => `float(${f}) in ${v}`,
  NOT_IN_NUMERIC: (f, v) => `float(${f}) not in ${v}`,
  CONTAINS: (f, v) => `${f} contains ${v}`,
  NOT_CONTAINS: (f, v) => `not (${f} contains ${v})`,
  CONTAINS_CASE_INSENSITIVE: (f, v) => `lower(${f}) contains lower(${v})`,
  NOT_CONTAINS_CASE_INSENSITIVE: (f, v) => `not (lower(${f}) contains lower(${v}))`,
  CONTAINS_NUMERIC: (f, v) => `${v} in ${f}`,
  NOT_CONTAINS_NUMERIC: (f, v) => `${v} not in ${f}`,
  CONTAINS_ANY: (f, v) => `any(${v}, # in ${f})`,
  CONTAINS_NONE: (f, v) => `none(${v}, # in ${f})`,
  STARTS_WITH: (f, v) => `${f} startsWith ${v}`,
  ENDS_WITH: (f, v) => `${f} endsWith ${v}`,
  STARTS_WITH_CASE_INSENSITIVE: (f, v) => `lower(${f}) startsWith lower(${v})`,
  ENDS_WITH_CASE_INSENSITIVE: (f, v) => `lower(${f}) endsWith lower(${v})`,
  MATCHES: (f, v) => `${f} matches ${v}`,
  NOT_MATCHES: (f, v) => `not (${f} matches ${v})`,
  MATCHES_CASE_INSENSITIVE: (f, v) => `${f} matches ("(?i)" + ${v})`,
  NOT_MATCHES_CASE_INSENSITIVE: (f, v) => `not (${f} matches ("(?i)" + ${v}))`,
  EXISTS: (f) => `${f} != nil`,
  NOT_EXISTS: (f) => `${f} == nil`,
  IS_TRUE: (f) => `${f} in [true, "true"]`,
  IS_FALSE: (f) => `${f} in [false, "false"]`,
};

/** Approximate Expr the condition compiles to (a preview; the engine's compiler is authoritative). */
export function toExpr(c: Condition, base = 'input'): string {
  const k = condKind(c);
  switch (k) {
    case 'all':
    case 'any': {
      const kids = children(c);
      if (kids.length === 0) return k === 'all' ? 'true' : 'false';
      const parts = kids.map((x) => (isGroup(x) && children(x).length > 1 ? `(${toExpr(x, base)})` : toExpr(x, base)));
      return parts.join(k === 'all' ? ' && ' : ' || ');
    }
    case 'not':
      return `!(${toExpr(children(c)[0], base)})`;
    case 'field': {
      const f = c as FieldCondition;
      const path = f.field ? `${base}.${f.field}` : `${base}.?`;
      const fn = OP_EXPR[f.op];
      return fn ? fn(path, lit(f.value), lit(f.valueTo)) : `${path} ${f.op} ${lit(f.value)}`;
    }
    case 'segment': {
      const s = c as SegmentCondition;
      const call = s.attributes
        ? `inSegment(${lit(s.segment)}, {${Object.entries(s.attributes).map(([a, e]) => `${JSON.stringify(a)}: ${e}`).join(', ')}})`
        : `inSegment(${lit(s.segment)}, ${s.key || 'ctx.unit'})`;
      return s.op === 'NOT_IN_SEGMENT' ? `!${call}` : call;
    }
    case 'gate':
      return 'gate' in c ? `${c.op === 'GATE_FAILS' ? '!' : ''}gate(${lit(c.gate)})` : '';
    case 'variant':
      return 'experiment' in c ? `variant(${lit(c.experiment)}) in ${JSON.stringify(c.variants)}` : '';
    case 'expr':
      return 'expr' in c ? c.expr : '';
  }
}

// ---------------------------------------------------------------- local checks

export interface CondProblem {
  path: CondPath;
  message: string;
}

export interface CondContext {
  /** Fields of the input schema; null when the schema is unknown (skip field checks). */
  fields: FieldInfo[] | null;
  segments?: { name: string; type?: string }[];
  gates?: string[];
  experiments?: { name: string; variants: string[] }[];
}

/** Problems the editor can find without the server (unknown field, operator/type mismatch, empty values). */
export function checkCondition(root: Condition, ctx: CondContext): CondProblem[] {
  const out: CondProblem[] = [];
  const walk = (c: Condition, path: CondPath) => {
    const k = condKind(c);
    if (k === 'all' || k === 'any') {
      if (children(c).length === 0) out.push({ path, message: `${k} group is empty` });
    }
    children(c).forEach((x, i) => walk(x, [...path, i]));
    if (k === 'field') {
      const f = c as FieldCondition;
      if (!f.field) out.push({ path, message: 'choose a field' });
      else if (ctx.fields) {
        const info = ctx.fields.find((x) => x.path === f.field);
        if (!info) out.push({ path, message: `${f.field} is not in the input schema` });
        else if (info.inArray) out.push({ path, message: `${f.field} is inside an array; use an expression` });
        else {
          const ops = operatorsFor(info.type);
          if (!ops.some((o) => o.op === f.op)) out.push({ path, message: `${operatorInfo(f.op)?.label ?? f.op} does not apply to ${info.type}` });
          if (info.pii && ['MATCHES', 'MATCHES_CASE_INSENSITIVE'].includes(f.op)) out.push({ path, message: `${f.field} is PII` });
        }
      }
      const info = operatorInfo(f.op);
      if (info && info.valueKind !== 'none') {
        if (f.value === undefined || f.value === '' || (Array.isArray(f.value) && f.value.length === 0)) out.push({ path, message: 'enter a value' });
        if (info.valueKind === 'range' && (typeof f.value !== 'number' || typeof f.valueTo !== 'number')) out.push({ path, message: 'enter both bounds' });
      }
    }
    if (k === 'segment') {
      const s = c as SegmentCondition;
      if (!s.segment) out.push({ path, message: 'choose a segment' });
      else if (ctx.segments) {
        const seg = ctx.segments.find((x) => x.name === s.segment);
        if (!seg) out.push({ path, message: `segment ${s.segment} does not exist` });
        else if (seg.type === 'dynamic' && !s.attributes) out.push({ path, message: 'map the segment attributes' });
        else if (seg.type === 'static' && !s.key) out.push({ path, message: 'enter the member key expression' });
      }
    }
    if (k === 'gate' && 'gate' in c) {
      if (!c.gate) out.push({ path, message: 'choose a gate' });
      else if (ctx.gates && !ctx.gates.includes(c.gate)) out.push({ path, message: `gate ${c.gate} does not exist` });
    }
    if (k === 'variant' && 'experiment' in c) {
      if (!c.experiment) out.push({ path, message: 'choose an experiment' });
      else if (c.variants.length === 0) out.push({ path, message: 'pick at least one variant' });
    }
    if (k === 'expr' && 'expr' in c && !c.expr.trim()) out.push({ path, message: 'enter an expression' });
  };
  walk(root, []);
  return out;
}

/** JSON pointer of a condition node below a base pointer (`/nodes/1/rules/0/when` + [0,2] → …/all/0/any/2). */
export function condPointer(root: Condition, base: string, path: CondPath): string {
  let ptr = base;
  let cur: Condition | undefined = root;
  for (const i of path) {
    if (!cur) break;
    const k = condKind(cur);
    ptr += k === 'not' ? '/not' : `/${k}/${i}`;
    cur = children(cur)[i];
  }
  return ptr;
}
