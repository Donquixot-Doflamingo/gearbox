// Decision-table cell syntax:
//   -            any value
//   < 300000     comparison (<, <=, >, >=, =, !=) with a number or a "quoted" string
//   [a..b]       inclusive numeric range
//   "text"       equals this text; plain numbers, true, false, null are literals too
//   ["a","b"]    a JSON list means "one of"
//   anything else is an Expr expression where `value` is the column value (value % 2 == 0)

export type CompareOp = '<' | '<=' | '>' | '>=' | '==' | '!=';

export type Cell =
  | { kind: 'any' }
  | { kind: 'compare'; op: CompareOp; value: number | string }
  | { kind: 'range'; lo: number; hi: number }
  | { kind: 'list'; values: unknown[] }
  | { kind: 'literal'; value: string | number | boolean | null }
  | { kind: 'expr'; expr: string }
  | { kind: 'invalid'; text: string; error: string };

const NUM = '-?\\d+(?:\\.\\d+)?(?:[eE][-+]?\\d+)?';
const COMPARE = new RegExp(`^(<=|>=|<|>|==|=|!=)\\s*(${NUM}|"(?:[^"\\\\]|\\\\.)*")$`);
const RANGE = new RegExp(`^\\[\\s*(${NUM})\\s*\\.\\.\\s*(${NUM})\\s*\\]$`);
const NUMBER = new RegExp(`^${NUM}$`);

export function parseCell(text: string | undefined | null): Cell {
  const t = (text ?? '').trim();
  if (t === '' || t === '-') return { kind: 'any' };
  let m = COMPARE.exec(t);
  if (m) {
    const op = (m[1] === '=' ? '==' : m[1]) as CompareOp;
    const raw = m[2];
    const value = raw.startsWith('"') ? (JSON.parse(raw) as string) : Number(raw);
    if (typeof value === 'string' && !['==', '!='].includes(op)) return { kind: 'compare', op, value };
    return { kind: 'compare', op, value };
  }
  m = RANGE.exec(t);
  if (m) {
    const lo = Number(m[1]), hi = Number(m[2]);
    if (lo > hi) return { kind: 'invalid', text: t, error: `range start ${lo} is greater than end ${hi}` };
    return { kind: 'range', lo, hi };
  }
  if (t.startsWith('[')) {
    try {
      const v = JSON.parse(t);
      if (Array.isArray(v)) {
        if (v.length === 0) return { kind: 'invalid', text: t, error: 'empty list never matches' };
        return { kind: 'list', values: v };
      }
    } catch {
      return { kind: 'invalid', text: t, error: 'not a valid JSON list or [a..b] range' };
    }
  }
  if (t.startsWith('"')) {
    try {
      const v = JSON.parse(t);
      if (typeof v === 'string') return { kind: 'literal', value: v };
    } catch {
      return { kind: 'invalid', text: t, error: 'unterminated "quoted text"' };
    }
  }
  if (NUMBER.test(t)) return { kind: 'literal', value: Number(t) };
  if (t === 'true' || t === 'false') return { kind: 'literal', value: t === 'true' };
  if (t === 'null' || t === 'nil') return { kind: 'literal', value: null };
  return { kind: 'expr', expr: t };
}

/** Error text for a cell, or a warning for likely mistakes; null when fine. */
export function cellIssue(text: string): { level: 'error' | 'warning'; message: string } | null {
  const c = parseCell(text);
  if (c.kind === 'invalid') return { level: 'error', message: c.error };
  if (c.kind === 'expr') {
    if (/^[A-Za-z_][A-Za-z0-9_ -]*$/.test(c.expr) && !/\bvalue\b/.test(c.expr)) {
      return { level: 'warning', message: `${c.expr} is read as an expression; quote it to match text: "${c.expr}"` };
    }
    let depth = 0;
    for (const ch of c.expr) {
      if (ch === '(') depth++;
      if (ch === ')') depth--;
      if (depth < 0) break;
    }
    if (depth !== 0) return { level: 'error', message: 'unbalanced parentheses' };
  }
  return null;
}

/** Short human text for a cell. */
export function describeCell(c: Cell): string {
  switch (c.kind) {
    case 'any':
      return 'any';
    case 'compare':
      return `${c.op} ${typeof c.value === 'string' ? JSON.stringify(c.value) : c.value}`;
    case 'range':
      return `${c.lo} to ${c.hi}`;
    case 'list':
      return `one of ${c.values.map((v) => JSON.stringify(v)).join(', ')}`;
    case 'literal':
      return `= ${JSON.stringify(c.value)}`;
    case 'expr':
      return c.expr;
    case 'invalid':
      return `invalid: ${c.error}`;
  }
}

function eq(a: unknown, b: unknown): boolean {
  if (typeof a === 'number' && typeof b === 'string' && b.trim() !== '' && !isNaN(Number(b))) return a === Number(b);
  if (typeof b === 'number' && typeof a === 'string' && a.trim() !== '' && !isNaN(Number(a))) return b === Number(a);
  return JSON.stringify(a) === JSON.stringify(b);
}

function num(v: unknown): number | null {
  if (typeof v === 'number') return v;
  if (typeof v === 'string' && v.trim() !== '' && !isNaN(Number(v))) return Number(v);
  return null;
}

/**
 * Does `value` satisfy the cell? Returns undefined when the cell is an expression and no evaluator is given.
 * A missing (null/undefined) value matches only `-`.
 */
export function matchCell(c: Cell, value: unknown, evalExpr?: (expr: string, value: unknown) => unknown): boolean | undefined {
  if (c.kind === 'any') return true;
  if (c.kind === 'invalid') return false;
  if (c.kind === 'expr') {
    if (!evalExpr) return undefined;
    try {
      return evalExpr(c.expr, value) === true;
    } catch {
      return false;
    }
  }
  if (value === null || value === undefined) return c.kind === 'literal' && c.value === null;
  switch (c.kind) {
    case 'compare': {
      if (c.op === '==') return eq(value, c.value);
      if (c.op === '!=') return !eq(value, c.value);
      const a = num(value), b = num(c.value);
      if (a !== null && b !== null) return c.op === '<' ? a < b : c.op === '<=' ? a <= b : c.op === '>' ? a > b : a >= b;
      if (typeof value === 'string' && typeof c.value === 'string') return c.op === '<' ? value < c.value : c.op === '<=' ? value <= c.value : c.op === '>' ? value > c.value : value >= c.value;
      return false;
    }
    case 'range': {
      const a = num(value);
      return a !== null && a >= c.lo && a <= c.hi;
    }
    case 'list':
      return c.values.some((v) => eq(value, v));
    case 'literal':
      return eq(value, c.value);
  }
}

/** Expr equivalent of a cell for the column `name` (preview). */
export function cellToExpr(c: Cell, name: string): string {
  switch (c.kind) {
    case 'any':
      return 'true';
    case 'compare':
      return `${name} ${c.op} ${JSON.stringify(c.value)}`;
    case 'range':
      return `${name} >= ${c.lo} && ${name} <= ${c.hi}`;
    case 'list':
      return `${name} in ${JSON.stringify(c.values)}`;
    case 'literal':
      return `${name} == ${JSON.stringify(c.value)}`;
    case 'expr':
      return c.expr.replace(/\bvalue\b/g, name);
    case 'invalid':
      return 'false';
  }
}

/** Parse an output cell typed in the grid: JSON literal when it parses, otherwise text. `=…` stays an expression. */
export function parseOutputCell(text: string): unknown {
  const t = text.trim();
  if (t === '') return undefined;
  if (t.startsWith('=')) return t;
  try {
    return JSON.parse(t);
  } catch {
    return text;
  }
}

export function outputCellText(v: unknown): string {
  if (v === undefined) return '';
  if (typeof v === 'string') {
    if (v.startsWith('=')) return v;
    // text that would parse as JSON keeps its quotes so it round-trips
    try {
      JSON.parse(v);
      return JSON.stringify(v);
    } catch {
      return v;
    }
  }
  return JSON.stringify(v);
}
