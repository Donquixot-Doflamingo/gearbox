// A small evaluator for the Expr subset Gearbox decisions use. It powers the in-browser mock API
// (test console, tests, transform previews) so the UI is usable with no backend. The engine's real
// compiler is authoritative; anything unsupported here throws ExprError with a clear message.
//
// Gearbox semantics kept: member access is nil-safe (`a.b.c` is nil when `a.b` is missing),
// ordering comparisons with nil are false, arithmetic with nil is an error, `now()` is disabled.

export class ExprError extends Error {
  constructor(message: string, readonly pos?: number) {
    super(message);
    this.name = 'ExprError';
  }
}

type Tok = { k: 'num' | 'str' | 'id' | 'op' | 'eof'; v: string; pos: number };

const OPS = ['?.', '??', '==', '!=', '<=', '>=', '&&', '||', '..', '**', '+', '-', '*', '/', '%', '^', '<', '>', '!', '?', ':', '.', ',', '(', ')', '[', ']', '{', '}', '|', '#', ';', '='];

function lex(src: string): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  while (i < src.length) {
    const c = src[i];
    if (/\s/.test(c)) {
      i++;
      continue;
    }
    if (c === '/' && src[i + 1] === '/') {
      while (i < src.length && src[i] !== '\n') i++;
      continue;
    }
    if (/[0-9]/.test(c) || (c === '.' && /[0-9]/.test(src[i + 1] ?? '') && src[i - 1] !== '.')) {
      const m = /^(\d[\d_]*)?(\.\d+)?([eE][-+]?\d+)?/.exec(src.slice(i))!;
      let s = m[0];
      // `1..5` is a range, not 1. followed by .5
      if (s.endsWith('.') ) s = s.slice(0, -1);
      out.push({ k: 'num', v: s.replace(/_/g, ''), pos: i });
      i += s.length;
      continue;
    }
    if (c === '"' || c === "'" || c === '`') {
      let j = i + 1;
      let s = '';
      while (j < src.length && src[j] !== c) {
        if (src[j] === '\\' && c !== '`') {
          const n = src[j + 1];
          s += n === 'n' ? '\n' : n === 't' ? '\t' : n === 'r' ? '\r' : n;
          j += 2;
        } else s += src[j++];
      }
      if (j >= src.length) throw new ExprError('unterminated string', i);
      out.push({ k: 'str', v: s, pos: i });
      i = j + 1;
      continue;
    }
    if (/[A-Za-z_$]/.test(c)) {
      const m = /^[A-Za-z_$][A-Za-z0-9_$]*/.exec(src.slice(i))!;
      out.push({ k: 'id', v: m[0], pos: i });
      i += m[0].length;
      continue;
    }
    const op = OPS.find((o) => src.startsWith(o, i));
    if (!op) throw new ExprError(`unexpected character ${c}`, i);
    out.push({ k: 'op', v: op, pos: i });
    i += op.length;
  }
  out.push({ k: 'eof', v: '', pos: src.length });
  return out;
}

export type Ast =
  | { t: 'lit'; v: unknown }
  | { t: 'id'; name: string }
  | { t: 'hash' }
  | { t: 'member'; obj: Ast; prop: string | Ast }
  | { t: 'call'; callee: Ast; args: Ast[] }
  | { t: 'unary'; op: string; arg: Ast }
  | { t: 'bin'; op: string; l: Ast; r: Ast }
  | { t: 'cond'; test: Ast; a: Ast; b: Ast }
  | { t: 'arr'; items: Ast[] }
  | { t: 'map'; entries: [Ast, Ast][] }
  | { t: 'let'; name: string; value: Ast; body: Ast };

const WORD_OPS: Record<string, string> = { and: '&&', or: '||' };

class Parser {
  i = 0;
  constructor(readonly toks: Tok[]) {}
  peek(o = 0): Tok {
    return this.toks[Math.min(this.i + o, this.toks.length - 1)];
  }
  next(): Tok {
    return this.toks[this.i++];
  }
  is(v: string, o = 0): boolean {
    const t = this.peek(o);
    return (t.k === 'op' || t.k === 'id') && t.v === v;
  }
  eat(v: string): boolean {
    if (this.is(v)) {
      this.i++;
      return true;
    }
    return false;
  }
  expect(v: string): void {
    if (!this.eat(v)) throw new ExprError(`expected ${v} but found ${this.peek().v || 'end of expression'}`, this.peek().pos);
  }

  program(): Ast {
    const e = this.expr();
    if (this.peek().k !== 'eof') throw new ExprError(`unexpected ${this.peek().v}`, this.peek().pos);
    return e;
  }
  expr(): Ast {
    if (this.is('let')) {
      this.next();
      const name = this.next();
      if (name.k !== 'id') throw new ExprError('expected a name after let', name.pos);
      this.expect('=');
      const value = this.expr();
      this.expect(';');
      return { t: 'let', name: name.v, value, body: this.expr() };
    }
    return this.pipe();
  }
  pipe(): Ast {
    let l = this.ternary();
    while (this.is('|') && !this.is('||')) {
      this.next();
      const r = this.postfix(this.primary());
      if (r.t !== 'call') throw new ExprError('a pipe needs a function call on the right');
      l = { t: 'call', callee: r.callee, args: [l, ...r.args] };
    }
    return l;
  }
  ternary(): Ast {
    if (this.is('if')) {
      this.next();
      const test = this.ternary();
      this.expect('{');
      const a = this.expr();
      this.expect('}');
      this.expect('else');
      let b: Ast;
      if (this.is('if')) b = this.ternary();
      else {
        this.expect('{');
        b = this.expr();
        this.expect('}');
      }
      return { t: 'cond', test, a, b };
    }
    const test = this.nullish();
    if (this.eat('?')) {
      const a = this.ternary();
      this.expect(':');
      const b = this.ternary();
      return { t: 'cond', test, a, b };
    }
    return test;
  }
  nullish(): Ast {
    let l = this.or();
    while (this.eat('??')) l = { t: 'bin', op: '??', l, r: this.or() };
    return l;
  }
  or(): Ast {
    let l = this.and();
    while (this.is('||') || this.is('or')) {
      this.next();
      l = { t: 'bin', op: '||', l, r: this.and() };
    }
    return l;
  }
  and(): Ast {
    let l = this.cmp();
    while (this.is('&&') || this.is('and')) {
      this.next();
      l = { t: 'bin', op: '&&', l, r: this.cmp() };
    }
    return l;
  }
  cmp(): Ast {
    let l = this.range();
    for (;;) {
      const t = this.peek();
      if (t.k === 'op' && ['==', '!=', '<', '>', '<=', '>='].includes(t.v)) {
        this.next();
        l = { t: 'bin', op: t.v, l, r: this.range() };
      } else if (t.k === 'id' && ['in', 'matches', 'contains', 'startsWith', 'endsWith'].includes(t.v)) {
        this.next();
        l = { t: 'bin', op: t.v, l, r: this.range() };
      } else if (t.k === 'id' && t.v === 'not' && this.peek(1).k === 'id' && this.peek(1).v === 'in') {
        this.next();
        this.next();
        l = { t: 'unary', op: '!', arg: { t: 'bin', op: 'in', l, r: this.range() } };
      } else return l;
    }
  }
  range(): Ast {
    const l = this.add();
    if (this.eat('..')) return { t: 'bin', op: '..', l, r: this.add() };
    return l;
  }
  add(): Ast {
    let l = this.mul();
    while (this.is('+') || this.is('-')) l = { t: 'bin', op: this.next().v, l, r: this.mul() };
    return l;
  }
  mul(): Ast {
    let l = this.pow();
    while (this.is('*') || this.is('/') || this.is('%')) l = { t: 'bin', op: this.next().v, l, r: this.pow() };
    return l;
  }
  pow(): Ast {
    const l = this.unary();
    if (this.is('^') || this.is('**')) {
      this.next();
      return { t: 'bin', op: '**', l, r: this.pow() };
    }
    return l;
  }
  unary(): Ast {
    if (this.is('!') || this.is('not')) {
      this.next();
      return { t: 'unary', op: '!', arg: this.unary() };
    }
    if (this.is('-')) {
      this.next();
      return { t: 'unary', op: '-', arg: this.unary() };
    }
    if (this.is('+')) {
      this.next();
      return this.unary();
    }
    return this.postfix(this.primary());
  }
  postfix(e: Ast): Ast {
    for (;;) {
      if (this.is('.') || this.is('?.')) {
        this.next();
        const name = this.next();
        if (name.k !== 'id') throw new ExprError('expected a field name after .', name.pos);
        e = { t: 'member', obj: e, prop: name.v };
      } else if (this.is('[')) {
        this.next();
        // slices a[1:3]
        let idx: Ast = { t: 'lit', v: null };
        if (!this.is(':')) idx = this.expr();
        if (this.eat(':')) {
          const end: Ast = this.is(']') ? { t: 'lit', v: null } : this.expr();
          this.expect(']');
          e = { t: 'call', callee: { t: 'id', name: '$slice' }, args: [e, idx, end] };
          continue;
        }
        this.expect(']');
        e = { t: 'member', obj: e, prop: idx };
      } else if (this.is('(')) {
        this.next();
        const args: Ast[] = [];
        while (!this.is(')')) {
          args.push(this.expr());
          if (!this.eat(',')) break;
        }
        this.expect(')');
        e = { t: 'call', callee: e, args };
      } else return e;
    }
  }
  primary(): Ast {
    const t = this.next();
    if (t.k === 'num') return { t: 'lit', v: Number(t.v) };
    if (t.k === 'str') return { t: 'lit', v: t.v };
    if (t.k === 'id') {
      if (t.v === 'true') return { t: 'lit', v: true };
      if (t.v === 'false') return { t: 'lit', v: false };
      if (t.v === 'nil' || t.v === 'null') return { t: 'lit', v: null };
      if (t.v in WORD_OPS) throw new ExprError(`unexpected ${t.v}`, t.pos);
      return { t: 'id', name: t.v };
    }
    if (t.k === 'op') {
      if (t.v === '(') {
        const e = this.expr();
        this.expect(')');
        return e;
      }
      if (t.v === '[') {
        const items: Ast[] = [];
        while (!this.is(']')) {
          items.push(this.expr());
          if (!this.eat(',')) break;
        }
        this.expect(']');
        return { t: 'arr', items };
      }
      if (t.v === '{') {
        const entries: [Ast, Ast][] = [];
        while (!this.is('}')) {
          const k = this.next();
          let key: Ast;
          if (k.k === 'id' || k.k === 'str' || k.k === 'num') key = { t: 'lit', v: k.v };
          else if (k.k === 'op' && k.v === '(') {
            key = this.expr();
            this.expect(')');
          } else throw new ExprError('expected a key in the map', k.pos);
          this.expect(':');
          entries.push([key, this.expr()]);
          if (!this.eat(',')) break;
        }
        this.expect('}');
        return { t: 'map', entries };
      }
      if (t.v === '#') return { t: 'hash' };
      if (t.v === '.') {
        // `.field` inside a predicate means `#.field`
        const name = this.next();
        if (name.k !== 'id') throw new ExprError('expected a field name after .', name.pos);
        return { t: 'member', obj: { t: 'hash' }, prop: name.v };
      }
    }
    throw new ExprError(t.k === 'eof' ? 'expression ends too early' : `unexpected ${t.v}`, t.pos);
  }
}

const cache = new Map<string, Ast>();
export function parse(src: string): Ast {
  let ast = cache.get(src);
  if (!ast) {
    ast = new Parser(lex(src)).program();
    if (cache.size > 2000) cache.clear();
    cache.set(src, ast);
  }
  return ast;
}

/** Syntax check; returns the error message or null. */
export function syntaxError(src: string): string | null {
  try {
    parse(src);
    return null;
  } catch (e) {
    return (e as Error).message;
  }
}

export type Fn = (...args: unknown[]) => unknown;
export interface Env {
  [name: string]: unknown;
}
export interface EvalOptions {
  /** Extra functions (lookup, inSegment, gate, variant, bucket…). */
  fns?: Record<string, Fn>;
}

function isNil(v: unknown): v is null | undefined {
  return v === null || v === undefined;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (isNil(a) && isNil(b)) return true;
  if (typeof a !== typeof b || isNil(a) || isNil(b)) return false;
  if (a instanceof Date && b instanceof Date) return a.getTime() === b.getTime();
  if (typeof a === 'object') return JSON.stringify(sortKeys(a)) === JSON.stringify(sortKeys(b));
  return false;
}

function sortKeys(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(sortKeys);
  if (v && typeof v === 'object' && !(v instanceof Date)) {
    return Object.fromEntries(Object.keys(v).sort().map((k) => [k, sortKeys((v as Record<string, unknown>)[k])]));
  }
  return v;
}

function num(v: unknown, what: string): number {
  if (typeof v === 'number') return v;
  if (isNil(v)) throw new ExprError(`${what}: value is nil (use ?? to give a default)`);
  throw new ExprError(`${what}: expected a number, got ${typeof v}`);
}

function list(v: unknown, what: string): unknown[] {
  if (Array.isArray(v)) return v;
  if (isNil(v)) return [];
  throw new ExprError(`${what}: expected a list`);
}

function str(v: unknown): string {
  if (typeof v === 'string') return v;
  if (isNil(v)) return '';
  if (v instanceof Date) return v.toISOString();
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

const DATE_METHODS: Record<string, (d: Date) => unknown> = {
  Year: (d) => d.getUTCFullYear(),
  Month: (d) => d.getUTCMonth() + 1,
  Day: (d) => d.getUTCDate(),
  Hour: (d) => d.getUTCHours(),
  Minute: (d) => d.getUTCMinutes(),
  Second: (d) => d.getUTCSeconds(),
  Weekday: (d) => d.getUTCDay(),
  YearDay: (d) => Math.floor((d.getTime() - Date.UTC(d.getUTCFullYear(), 0, 1)) / 86400000) + 1,
  Unix: (d) => Math.floor(d.getTime() / 1000),
  UnixMilli: (d) => d.getTime(),
};

function toDate(v: unknown): Date {
  if (v instanceof Date) return v;
  if (typeof v === 'string' || typeof v === 'number') {
    const d = new Date(typeof v === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(v) ? v + 'T00:00:00Z' : v);
    if (!isNaN(d.getTime())) return d;
  }
  throw new ExprError(`not a date: ${String(v)}`);
}

const PREDICATE_FNS = new Set(['map', 'filter', 'all', 'any', 'none', 'one', 'count', 'find', 'findIndex', 'findLast', 'findLastIndex', 'sum', 'groupBy', 'sortBy', 'reduce']);

const BUILTINS: Record<string, Fn> = {
  len: (v) => (typeof v === 'string' ? v.length : Array.isArray(v) ? v.length : v && typeof v === 'object' ? Object.keys(v).length : 0),
  abs: (v) => Math.abs(num(v, 'abs')),
  ceil: (v) => Math.ceil(num(v, 'ceil')),
  floor: (v) => Math.floor(num(v, 'floor')),
  round: (v) => Math.round(num(v, 'round')),
  int: (v) => (typeof v === 'string' ? parseInt(v, 10) : Math.trunc(num(v, 'int'))),
  float: (v) => (typeof v === 'string' ? parseFloat(v) : num(v, 'float')),
  string: (v) => str(v),
  type: (v) => (isNil(v) ? 'nil' : Array.isArray(v) ? 'array' : v instanceof Date ? 'time' : typeof v === 'object' ? 'map' : typeof v === 'number' ? (Number.isInteger(v) ? 'int' : 'float') : typeof v),
  max: (...a) => Math.max(...(a.length === 1 && Array.isArray(a[0]) ? (a[0] as unknown[]) : a).map((x) => num(x, 'max'))),
  min: (...a) => Math.min(...(a.length === 1 && Array.isArray(a[0]) ? (a[0] as unknown[]) : a).map((x) => num(x, 'min'))),
  mean: (v) => {
    const l = list(v, 'mean').map((x) => num(x, 'mean'));
    return l.length ? l.reduce((a, b) => a + b, 0) / l.length : null;
  },
  median: (v) => {
    const l = list(v, 'median').map((x) => num(x, 'median')).sort((a, b) => a - b);
    if (!l.length) return null;
    const m = Math.floor(l.length / 2);
    return l.length % 2 ? l[m] : (l[m - 1] + l[m]) / 2;
  },
  trim: (v, c) => (c === undefined ? str(v).trim() : str(v).replace(new RegExp(`^[${String(c)}]+|[${String(c)}]+$`, 'g'), '')),
  trimPrefix: (v, p) => (str(v).startsWith(str(p)) ? str(v).slice(str(p).length) : str(v)),
  trimSuffix: (v, p) => (str(v).endsWith(str(p)) ? str(v).slice(0, -str(p).length || undefined) : str(v)),
  upper: (v) => str(v).toUpperCase(),
  lower: (v) => str(v).toLowerCase(),
  split: (v, sep) => str(v).split(str(sep)),
  replace: (v, a, b) => str(v).split(str(a)).join(str(b)),
  repeat: (v, n) => str(v).repeat(num(n, 'repeat')),
  join: (v, sep) => list(v, 'join').map(str).join(sep === undefined ? '' : str(sep)),
  indexOf: (v, s) => str(v).indexOf(str(s)),
  hasPrefix: (v, p) => str(v).startsWith(str(p)),
  hasSuffix: (v, p) => str(v).endsWith(str(p)),
  first: (v) => list(v, 'first')[0] ?? null,
  last: (v) => list(v, 'last').at(-1) ?? null,
  take: (v, n) => list(v, 'take').slice(0, num(n, 'take')),
  reverse: (v) => [...list(v, 'reverse')].reverse(),
  uniq: (v) => list(v, 'uniq').filter((x, i, a) => a.findIndex((y) => deepEqual(x, y)) === i),
  concat: (...a) => a.flatMap((x) => list(x, 'concat')),
  flatten: (v) => list(v, 'flatten').flat(Infinity),
  sort: (v, dir) => [...list(v, 'sort')].sort((a, b) => (a! < b! ? -1 : a! > b! ? 1 : 0) * (dir === 'desc' ? -1 : 1)),
  keys: (v) => (v && typeof v === 'object' ? Object.keys(v).sort() : []),
  values: (v) => (v && typeof v === 'object' ? Object.keys(v).sort().map((k) => (v as Record<string, unknown>)[k]) : []),
  toPairs: (v) => (v && typeof v === 'object' ? Object.keys(v).sort().map((k) => [k, (v as Record<string, unknown>)[k]]) : []),
  fromPairs: (v) => Object.fromEntries(list(v, 'fromPairs') as [string, unknown][]),
  get: (v, k) => (v && typeof v === 'object' ? ((v as Record<string, unknown>)[str(k)] ?? null) : null),
  toJSON: (v) => JSON.stringify(sortKeys(v)),
  fromJSON: (v) => JSON.parse(str(v)),
  coalesce: (...a) => a.find((x) => !isNil(x)) ?? null,
  date: (v) => toDate(v),
  duration: (v) => {
    const m = /^(\d+)(ms|s|m|h)$/.exec(str(v));
    if (!m) throw new ExprError(`bad duration ${str(v)}`);
    return Number(m[1]) * { ms: 1, s: 1000, m: 60000, h: 3600000 }[m[2] as 'ms']!;
  },
  daysBetween: (a, b) => Math.round((toDate(b).getTime() - toDate(a).getTime()) / 86400000),
  now: () => {
    throw new ExprError('now() is disabled; use ctx.now');
  },
};

export function evaluate(src: string, env: Env, opts: EvalOptions = {}): unknown {
  return evalAst(parse(src), env, opts, undefined);
}

function evalAst(a: Ast, env: Env, opts: EvalOptions, hash: unknown): unknown {
  const ev = (x: Ast, h = hash) => evalAst(x, env, opts, h);
  switch (a.t) {
    case 'lit':
      return a.v;
    case 'hash':
      return hash;
    case 'id':
      if (a.name in env) return env[a.name];
      if (a.name in BUILTINS || (opts.fns && a.name in opts.fns)) throw new ExprError(`${a.name} is a function; call it with ()`);
      throw new ExprError(`unknown name ${a.name}`);
    case 'member': {
      const o = ev(a.obj);
      const key = typeof a.prop === 'string' ? a.prop : ev(a.prop);
      if (isNil(o)) return null;
      if (o instanceof Date && typeof key === 'string' && key in DATE_METHODS) return () => DATE_METHODS[key](o);
      if (Array.isArray(o)) {
        if (typeof key !== 'number') return null;
        return o[key < 0 ? o.length + key : key] ?? null;
      }
      if (typeof o === 'object') return (o as Record<string, unknown>)[String(key)] ?? null;
      if (typeof o === 'string' && typeof key === 'number') return o[key < 0 ? o.length + key : key] ?? null;
      return null;
    }
    case 'call': {
      if (a.callee.t === 'id') {
        const name = a.callee.name;
        if (name === '$slice') {
          const v = ev(a.args[0]);
          const s = ev(a.args[1]), e = ev(a.args[2]);
          if (Array.isArray(v) || typeof v === 'string') return v.slice(isNil(s) ? 0 : num(s, 'slice'), isNil(e) ? undefined : num(e, 'slice'));
          return null;
        }
        if (PREDICATE_FNS.has(name) && a.args.length >= 2) return predicate(name, a.args, env, opts, hash);
        if (name === 'sum' && a.args.length === 1) return list(ev(a.args[0]), 'sum').reduce((s: number, x) => s + num(x, 'sum'), 0);
        if (name === 'count' && a.args.length === 1) return list(ev(a.args[0]), 'count').filter((x) => x === true).length;
        const fn = opts.fns?.[name] ?? BUILTINS[name];
        if (!fn) throw new ExprError(`unknown function ${name}`);
        return fn(...a.args.map((x) => ev(x)));
      }
      const callee = ev(a.callee);
      if (typeof callee !== 'function') throw new ExprError('not a function');
      return (callee as Fn)(...a.args.map((x) => ev(x)));
    }
    case 'unary': {
      const v = ev(a.arg);
      if (a.op === '!') return !truthy(v, '!');
      return -num(v, 'negation');
    }
    case 'cond':
      return truthy(ev(a.test), 'condition') ? ev(a.a) : ev(a.b);
    case 'arr':
      return a.items.map((x) => ev(x));
    case 'map':
      return Object.fromEntries(a.entries.map(([k, v]) => [str(ev(k)), ev(v)]));
    case 'let':
      return evalAst(a.body, { ...env, [a.name]: ev(a.value) }, opts, hash);
    case 'bin':
      return binary(a, env, opts, hash);
  }
}

function truthy(v: unknown, what: string): boolean {
  if (typeof v === 'boolean') return v;
  if (isNil(v)) return false;
  throw new ExprError(`${what}: expected a boolean, got ${typeof v}`);
}

function binary(a: Extract<Ast, { t: 'bin' }>, env: Env, opts: EvalOptions, hash: unknown): unknown {
  const ev = (x: Ast) => evalAst(x, env, opts, hash);
  if (a.op === '&&') return truthy(ev(a.l), 'and') ? truthy(ev(a.r), 'and') : false;
  if (a.op === '||') return truthy(ev(a.l), 'or') ? true : truthy(ev(a.r), 'or');
  if (a.op === '??') {
    const l = ev(a.l);
    return isNil(l) ? ev(a.r) : l;
  }
  const l = ev(a.l), r = ev(a.r);
  switch (a.op) {
    case '==':
      return deepEqual(l, r);
    case '!=':
      return !deepEqual(l, r);
    case '<':
    case '>':
    case '<=':
    case '>=': {
      if (isNil(l) || isNil(r)) return false;
      const x = l instanceof Date ? l.getTime() : l, y = r instanceof Date ? r.getTime() : r;
      if (typeof x !== typeof y) return false;
      const xx = x as number | string, yy = y as number | string;
      return a.op === '<' ? xx < yy : a.op === '>' ? xx > yy : a.op === '<=' ? xx <= yy : xx >= yy;
    }
    case 'in':
      if (Array.isArray(r)) return r.some((x) => deepEqual(x, l));
      if (r && typeof r === 'object') return str(l) in r;
      return false;
    case 'contains':
      if (isNil(l)) return false;
      if (Array.isArray(l)) return l.some((x) => deepEqual(x, r));
      return str(l).includes(str(r));
    case 'startsWith':
      return !isNil(l) && str(l).startsWith(str(r));
    case 'endsWith':
      return !isNil(l) && str(l).endsWith(str(r));
    case 'matches': {
      if (isNil(l)) return false;
      let pat = str(r), flags = '';
      if (pat.startsWith('(?i)')) {
        pat = pat.slice(4);
        flags = 'i';
      }
      return new RegExp(pat, flags).test(str(l));
    }
    case '..': {
      const s = num(l, 'range'), e = num(r, 'range');
      const out: number[] = [];
      for (let i = s; i <= e && out.length < 100000; i++) out.push(i);
      return out;
    }
    case '+':
      if (typeof l === 'string' || typeof r === 'string') {
        if (isNil(l) || isNil(r)) throw new ExprError('+: value is nil (use ?? to give a default)');
        return str(l) + str(r);
      }
      if (Array.isArray(l) && Array.isArray(r)) return [...l, ...r];
      return num(l, '+') + num(r, '+');
    case '-':
      return num(l, '-') - num(r, '-');
    case '*':
      return num(l, '*') * num(r, '*');
    case '/': {
      const d = num(r, '/');
      if (d === 0) throw new ExprError('division by zero');
      return num(l, '/') / d;
    }
    case '%':
      return num(l, '%') % num(r, '%');
    case '**':
      return num(l, '^') ** num(r, '^');
  }
  throw new ExprError(`unsupported operator ${a.op}`);
}

function predicate(name: string, args: Ast[], env: Env, opts: EvalOptions, hash: unknown): unknown {
  const items = list(evalAst(args[0], env, opts, hash), name);
  const run = (x: unknown) => evalAst(args[1], env, opts, x);
  switch (name) {
    case 'map':
      return items.map(run);
    case 'filter':
      return items.filter((x) => truthy(run(x), 'filter'));
    case 'all':
      return items.every((x) => truthy(run(x), 'all'));
    case 'any':
      return items.some((x) => truthy(run(x), 'any'));
    case 'none':
      return !items.some((x) => truthy(run(x), 'none'));
    case 'one':
      return items.filter((x) => truthy(run(x), 'one')).length === 1;
    case 'count':
      return items.filter((x) => truthy(run(x), 'count')).length;
    case 'find':
      return items.find((x) => truthy(run(x), 'find')) ?? null;
    case 'findIndex':
      return items.findIndex((x) => truthy(run(x), 'findIndex'));
    case 'findLast':
      return [...items].reverse().find((x) => truthy(run(x), 'findLast')) ?? null;
    case 'findLastIndex': {
      for (let i = items.length - 1; i >= 0; i--) if (truthy(run(items[i]), 'findLastIndex')) return i;
      return -1;
    }
    case 'sum':
      return items.reduce((s: number, x) => s + num(run(x), 'sum'), 0);
    case 'groupBy': {
      const out: Record<string, unknown[]> = {};
      for (const x of items) (out[str(run(x))] ??= []).push(x);
      return out;
    }
    case 'sortBy':
      return [...items].sort((a, b) => {
        const x = run(a) as number, y = run(b) as number;
        return x < y ? -1 : x > y ? 1 : 0;
      });
    case 'reduce': {
      let acc: unknown = args[2] ? evalAst(args[2], env, opts, hash) : items[0];
      for (const x of args[2] ? items : items.slice(1)) acc = evalAst(args[1], { ...env, acc }, opts, x);
      return acc;
    }
  }
  throw new ExprError(`unknown function ${name}`);
}

/** Names an expression reads at the top level (`input`, `nodes`, `ctx`…), for quick checks. */
export function rootNames(src: string): string[] {
  const out = new Set<string>();
  const walk = (a: Ast) => {
    switch (a.t) {
      case 'id':
        out.add(a.name);
        break;
      case 'member':
        walk(a.obj);
        if (typeof a.prop !== 'string') walk(a.prop);
        break;
      case 'call':
        if (a.callee.t !== 'id') walk(a.callee);
        a.args.forEach(walk);
        break;
      case 'unary':
        walk(a.arg);
        break;
      case 'bin':
        walk(a.l);
        walk(a.r);
        break;
      case 'cond':
        walk(a.test);
        walk(a.a);
        walk(a.b);
        break;
      case 'arr':
        a.items.forEach(walk);
        break;
      case 'map':
        a.entries.forEach(([k, v]) => {
          walk(k);
          walk(v);
        });
        break;
      case 'let':
        walk(a.value);
        walk(a.body);
        out.delete(a.name);
        break;
    }
  };
  try {
    walk(parse(src));
  } catch {
    /* ignore */
  }
  return [...out];
}
