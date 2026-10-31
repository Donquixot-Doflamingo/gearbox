// Schema tree model: read and edit the restricted JSON Schema 2020-12 subset the registry accepts.
// All edit functions are immutable (they copy along the edited path), so they work on plain objects
// and on Svelte state proxies alike.

export type JsonSchema = { [key: string]: unknown };
export type SType = 'object' | 'array' | 'string' | 'integer' | 'number' | 'boolean' | 'null';
/** A path into a schema: keyword/name pairs such as ['properties', 'lead', 'properties', 'id'] or ['items']. */
export type SchemaPath = string[];

export const STYPES: Exclude<SType, 'null'>[] = ['object', 'array', 'string', 'integer', 'number', 'boolean'];
export const FORMATS = ['date-time', 'date', 'email', 'uuid'] as const;
export const PII = 'x-gearbox-pii';
export const MAX_DEPTH = 32;

/** Keywords that only make sense for one type (dropped when the type changes). */
const TYPE_KEYWORDS: Record<SType, string[]> = {
  object: ['properties', 'required', 'additionalProperties'],
  array: ['items', 'prefixItems', 'minItems', 'maxItems'],
  string: ['minLength', 'maxLength', 'pattern', 'format'],
  integer: ['minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum'],
  number: ['minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum'],
  boolean: [],
  null: [],
};

export function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function jsonType(v: unknown): SType {
  if (v === null) return 'null';
  if (Array.isArray(v)) return 'array';
  if (typeof v === 'number') return Number.isInteger(v) ? 'integer' : 'number';
  if (typeof v === 'string') return 'string';
  if (typeof v === 'boolean') return 'boolean';
  return 'object';
}

/** Resolve a local `$ref` (`#/$defs/x`) against the root schema. */
export function resolveRef(root: JsonSchema, s: JsonSchema | undefined): JsonSchema | undefined {
  let cur = s;
  for (let i = 0; i < 16 && cur && typeof cur.$ref === 'string'; i++) {
    const ref = cur.$ref as string;
    if (!ref.startsWith('#/')) return cur;
    let node: unknown = root;
    for (const seg of ref.slice(2).split('/')) node = isObj(node) ? node[seg.replace(/~1/g, '/').replace(/~0/g, '~')] : undefined;
    cur = isObj(node) ? node : undefined;
  }
  return cur;
}

/** Every type the schema allows (null included). Infers from enum/const when `type` is absent. */
export function schemaTypes(s: JsonSchema | undefined): SType[] {
  if (!s) return [];
  const t = s.type;
  if (typeof t === 'string') return [t as SType];
  if (Array.isArray(t)) return t.filter((x): x is SType => typeof x === 'string') as SType[];
  if (Array.isArray(s.enum)) {
    const set = new Set<SType>();
    for (const v of s.enum) set.add(jsonType(v) === 'integer' ? 'number' : jsonType(v));
    return [...set];
  }
  if ('const' in s) return [jsonType(s.const)];
  if (isObj(s.properties)) return ['object'];
  if (s.items !== undefined) return ['array'];
  return [];
}

/** The main (non-null) type, or 'any' when the schema does not say. */
export function primaryType(s: JsonSchema | undefined): SType | 'any' {
  return schemaTypes(s).find((t) => t !== 'null') ?? (schemaTypes(s).includes('null') ? 'null' : 'any');
}

export function isNullable(s: JsonSchema | undefined): boolean {
  if (!s) return false;
  if (schemaTypes(s).includes('null')) return true;
  return Array.isArray(s.enum) && s.enum.includes(null);
}

export function isPii(s: JsonSchema | undefined): boolean {
  return !!s && s[PII] === true;
}

function typeValue(types: SType[]): string | string[] | undefined {
  if (types.length === 0) return undefined;
  return types.length === 1 ? types[0] : types;
}

/** Turn `null` on or off for a schema (`type: [x, "null"]`). */
export function withNullable(s: JsonSchema, on: boolean): JsonSchema {
  const types = schemaTypes(s).filter((t) => t !== 'null');
  const out: JsonSchema = { ...s };
  if (Array.isArray(s.enum) && s.type === undefined) {
    const values = s.enum.filter((v) => v !== null);
    out.enum = on ? [...values, null] : values;
    return out;
  }
  const next = on ? [...types, 'null' as SType] : types;
  const tv = typeValue(next.length ? next : on ? ['null'] : []);
  if (tv === undefined) delete out.type;
  else out.type = tv;
  return out;
}

/** Change the main type, keeping description, PII, nullability and keywords that still apply. */
export function withType(s: JsonSchema, t: Exclude<SType, 'null'>): JsonSchema {
  const nullable = isNullable(s);
  const out: JsonSchema = {};
  const keep = new Set(['description', 'default', PII, ...TYPE_KEYWORDS[t]]);
  for (const [k, v] of Object.entries(s)) if (keep.has(k)) out[k] = v;
  if (t === 'object' && !isObj(out.properties)) out.properties = {};
  if (t === 'array' && out.items === undefined) out.items = { type: 'string' };
  out.type = nullable ? [t, 'null'] : t;
  if ((t === 'integer' || t === 'number') && 'default' in out && typeof out.default !== 'number') delete out.default;
  return reorder(out);
}

/** Stable keyword order so saved schemas diff nicely. */
const ORDER = ['type', 'description', 'enum', 'const', 'format', 'pattern', 'minLength', 'maxLength', 'minimum', 'maximum', 'exclusiveMinimum', 'exclusiveMaximum', 'minItems', 'maxItems', 'required', 'properties', 'additionalProperties', 'items', 'prefixItems', 'default', PII, '$defs'];
function reorder(s: JsonSchema): JsonSchema {
  const out: JsonSchema = {};
  for (const k of ORDER) if (k in s) out[k] = s[k];
  for (const [k, v] of Object.entries(s)) if (!(k in out)) out[k] = v;
  return out;
}

/** Set (or with `undefined`, delete) one keyword. */
export function withKeyword(s: JsonSchema, key: string, value: unknown): JsonSchema {
  const out: JsonSchema = { ...s };
  if (value === undefined || value === '' || (Array.isArray(value) && value.length === 0 && key === 'enum')) delete out[key];
  else out[key] = value;
  return reorder(out);
}

// ---------------------------------------------------------------- path editing

export function getAt(root: JsonSchema, path: SchemaPath): JsonSchema | undefined {
  let cur: unknown = root;
  for (const seg of path) {
    if (!isObj(cur)) return undefined;
    cur = cur[seg];
  }
  return isObj(cur) ? cur : undefined;
}

/** Replace the schema at `path` (or delete it when `value` is undefined). Returns a new root. */
export function setAt(root: JsonSchema, path: SchemaPath, value: JsonSchema | undefined): JsonSchema {
  if (path.length === 0) return value ?? {};
  const [head, ...rest] = path;
  const out: JsonSchema = { ...root };
  if (rest.length === 0) {
    if (value === undefined) delete out[head];
    else out[head] = value;
    return out;
  }
  const child = isObj(root[head]) ? (root[head] as JsonSchema) : {};
  out[head] = setAt(child, rest, value);
  return out;
}

export function updateAt(root: JsonSchema, path: SchemaPath, fn: (s: JsonSchema) => JsonSchema): JsonSchema {
  return setAt(root, path, fn(getAt(root, path) ?? {}));
}

export const PROP_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;
export function isValidPropName(n: string): boolean {
  return PROP_NAME.test(n);
}

function props(s: JsonSchema | undefined): Record<string, JsonSchema> {
  return isObj(s?.properties) ? (s!.properties as Record<string, JsonSchema>) : {};
}
function required(s: JsonSchema | undefined): string[] {
  return Array.isArray(s?.required) ? (s!.required as string[]) : [];
}

/** Add a property to the object at `objPath`. Returns the new root and the name used. */
export function addProperty(root: JsonSchema, objPath: SchemaPath, name = 'field', schema: JsonSchema = { type: 'string' }): { root: JsonSchema; name: string } {
  const obj = getAt(root, objPath) ?? {};
  const existing = props(obj);
  let n = name;
  for (let i = 2; n in existing; i++) n = `${name}_${i}`;
  const next = withKeyword(obj, 'properties', { ...existing, [n]: schema });
  return { root: setAt(root, objPath, next), name: n };
}

export function removeProperty(root: JsonSchema, objPath: SchemaPath, name: string): JsonSchema {
  return updateAt(root, objPath, (obj) => {
    const p = { ...props(obj) };
    delete p[name];
    const req = required(obj).filter((r) => r !== name);
    return withKeyword(withKeyword(obj, 'properties', p), 'required', req.length ? req : undefined);
  });
}

/** Rename keeping the property's position and required flag. Throws on an invalid or taken name. */
export function renameProperty(root: JsonSchema, objPath: SchemaPath, from: string, to: string): JsonSchema {
  if (from === to) return root;
  if (!isValidPropName(to)) throw new Error(`"${to}" is not a valid property name (letters, digits, _; start with a letter or _)`);
  const obj = getAt(root, objPath) ?? {};
  const p = props(obj);
  if (to in p) throw new Error(`"${to}" already exists`);
  const next: Record<string, JsonSchema> = {};
  for (const [k, v] of Object.entries(p)) next[k === from ? to : k] = v;
  const req = required(obj).map((r) => (r === from ? to : r));
  return setAt(root, objPath, withKeyword(withKeyword(obj, 'properties', next), 'required', req.length ? req : undefined));
}

export function setRequired(root: JsonSchema, objPath: SchemaPath, name: string, on: boolean): JsonSchema {
  return updateAt(root, objPath, (obj) => {
    const req = required(obj).filter((r) => r !== name);
    if (on) {
      // keep the property order
      const order = Object.keys(props(obj));
      req.push(name);
      req.sort((a, b) => order.indexOf(a) - order.indexOf(b));
    }
    return withKeyword(obj, 'required', req.length ? req : undefined);
  });
}

export function moveProperty(root: JsonSchema, objPath: SchemaPath, name: string, delta: number): JsonSchema {
  return updateAt(root, objPath, (obj) => {
    const entries = Object.entries(props(obj));
    const i = entries.findIndex(([k]) => k === name);
    const j = i + delta;
    if (i < 0 || j < 0 || j >= entries.length) return obj;
    const [e] = entries.splice(i, 1);
    entries.splice(j, 0, e);
    return withKeyword(obj, 'properties', Object.fromEntries(entries));
  });
}

export function propPath(objPath: SchemaPath, name: string): SchemaPath {
  return [...objPath, 'properties', name];
}

// ---------------------------------------------------------------- fields (for pickers)

export interface FieldInfo {
  /** Dotted path into a document (`lead.city`); array items as `inspections[]`. */
  path: string;
  name: string;
  type: SType | 'any';
  types: SType[];
  nullable: boolean;
  /** Required at every level from the root. */
  required: boolean;
  pii: boolean;
  enum?: unknown[];
  format?: string;
  description?: string;
  depth: number;
  /** Below an array: not addressable with a plain dotted path. */
  inArray: boolean;
  hasChildren: boolean;
  maxItems?: number;
}

/** Flatten a schema into its fields, depth first, in property order. */
export function listFields(root: JsonSchema, maxDepth = 12): FieldInfo[] {
  const out: FieldInfo[] = [];
  const walk = (s: JsonSchema | undefined, prefix: string, depth: number, req: boolean, inArray: boolean) => {
    const r = resolveRef(root, s);
    if (!r || depth > maxDepth) return;
    const ps = props(r);
    const reqs = required(r);
    for (const [name, raw] of Object.entries(ps)) {
      const child = resolveRef(root, raw) ?? {};
      const path = prefix ? `${prefix}.${name}` : name;
      const t = primaryType(child);
      const items = t === 'array' && isObj(child.items) ? resolveRef(root, child.items as JsonSchema) : undefined;
      const hasChildren = (t === 'object' && Object.keys(props(child)).length > 0) || (!!items && primaryType(items) === 'object');
      out.push({
        path,
        name,
        type: t,
        types: schemaTypes(child),
        nullable: isNullable(child),
        required: req && reqs.includes(name),
        pii: isPii(child),
        enum: Array.isArray(child.enum) ? (child.enum as unknown[]) : undefined,
        format: typeof child.format === 'string' ? child.format : undefined,
        description: typeof child.description === 'string' ? child.description : undefined,
        depth,
        inArray,
        hasChildren,
        maxItems: typeof child.maxItems === 'number' ? child.maxItems : undefined,
      });
      if (t === 'object') walk(child, path, depth + 1, req && reqs.includes(name), inArray);
      else if (items && primaryType(items) === 'object') walk(items, `${path}[]`, depth + 1, false, true);
    }
  };
  walk(root, '', 0, true, false);
  return out;
}

export function fieldAt(root: JsonSchema, dotted: string): FieldInfo | undefined {
  return listFields(root).find((f) => f.path === dotted);
}

// ---------------------------------------------------------------- validation

export interface DocError {
  path: string;
  message: string;
}

const FORMAT_RE: Record<string, RegExp> = {
  'date-time': /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/,
  date: /^\d{4}-\d{2}-\d{2}$/,
  email: /^[^@\s]+@[^@\s]+\.[^@\s]+$/,
  uuid: /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
};

function ptr(base: string, seg: string | number): string {
  return `${base}/${String(seg).replace(/~/g, '~0').replace(/\//g, '~1')}`;
}

function typeMatches(t: SType, v: unknown): boolean {
  const jt = jsonType(v);
  if (t === 'number') return jt === 'number' || jt === 'integer';
  return jt === t;
}

/** Validate a document against a schema (the subset the registry accepts). Paths are JSON pointers. */
export function validateDocument(schema: JsonSchema, doc: unknown): DocError[] {
  const errors: DocError[] = [];
  const check = (s0: JsonSchema | undefined, v: unknown, path: string) => {
    const s = resolveRef(schema, s0);
    if (!s) return;
    const types = Array.isArray(s.type) || typeof s.type === 'string' ? schemaTypes(s) : [];
    if (types.length && !types.some((t) => typeMatches(t, v))) {
      errors.push({ path, message: `expected ${types.join(' or ')}, got ${jsonType(v)}` });
      return;
    }
    if (Array.isArray(s.enum) && !s.enum.some((e) => JSON.stringify(e) === JSON.stringify(v))) {
      errors.push({ path, message: `must be one of ${s.enum.map((e) => JSON.stringify(e)).join(', ')}` });
    }
    if ('const' in s && JSON.stringify(s.const) !== JSON.stringify(v)) errors.push({ path, message: `must be ${JSON.stringify(s.const)}` });
    if (typeof v === 'number') {
      if (typeof s.minimum === 'number' && v < s.minimum) errors.push({ path, message: `must be ≥ ${s.minimum}` });
      if (typeof s.maximum === 'number' && v > s.maximum) errors.push({ path, message: `must be ≤ ${s.maximum}` });
      if (typeof s.exclusiveMinimum === 'number' && v <= s.exclusiveMinimum) errors.push({ path, message: `must be > ${s.exclusiveMinimum}` });
      if (typeof s.exclusiveMaximum === 'number' && v >= s.exclusiveMaximum) errors.push({ path, message: `must be < ${s.exclusiveMaximum}` });
    }
    if (typeof v === 'string') {
      if (typeof s.minLength === 'number' && v.length < s.minLength) errors.push({ path, message: `must be at least ${s.minLength} characters` });
      if (typeof s.maxLength === 'number' && v.length > s.maxLength) errors.push({ path, message: `must be at most ${s.maxLength} characters` });
      if (typeof s.pattern === 'string') {
        try {
          if (!new RegExp(s.pattern).test(v)) errors.push({ path, message: `must match ${s.pattern}` });
        } catch {
          /* invalid pattern is a schema problem, not a document problem */
        }
      }
      if (typeof s.format === 'string' && FORMAT_RE[s.format] && !FORMAT_RE[s.format].test(v)) errors.push({ path, message: `must be a valid ${s.format}` });
    }
    if (Array.isArray(v)) {
      if (typeof s.minItems === 'number' && v.length < s.minItems) errors.push({ path, message: `must have at least ${s.minItems} items` });
      if (typeof s.maxItems === 'number' && v.length > s.maxItems) errors.push({ path, message: `must have at most ${s.maxItems} items` });
      const prefix = Array.isArray(s.prefixItems) ? (s.prefixItems as JsonSchema[]) : [];
      v.forEach((item, i) => {
        if (i < prefix.length) check(prefix[i], item, ptr(path, i));
        else if (isObj(s.items)) check(s.items as JsonSchema, item, ptr(path, i));
      });
    }
    if (isObj(v)) {
      const ps = props(s);
      for (const r of required(s)) if (!(r in v) || v[r] === undefined) errors.push({ path: ptr(path, r), message: 'required property missing' });
      for (const [k, val] of Object.entries(v)) {
        if (k in ps) check(ps[k], val, ptr(path, k));
        else if (s.additionalProperties === false) errors.push({ path: ptr(path, k), message: 'property not allowed' });
        else if (isObj(s.additionalProperties)) check(s.additionalProperties as JsonSchema, val, ptr(path, k));
      }
    }
  };
  check(schema, doc, '');
  return errors;
}

/** Problems with the schema itself (shown in the editor before saving). */
export function lintSchema(root: JsonSchema): DocError[] {
  const out: DocError[] = [];
  const walk = (s: unknown, path: string, depth: number) => {
    if (!isObj(s)) return;
    if (depth > MAX_DEPTH) {
      out.push({ path, message: `nesting deeper than ${MAX_DEPTH}` });
      return;
    }
    const ps = props(s);
    for (const r of required(s)) if (!(r in ps)) out.push({ path: ptr(path, 'required'), message: `required "${r}" is not a property` });
    for (const name of Object.keys(ps)) if (!isValidPropName(name)) out.push({ path: ptr(ptr(path, 'properties'), name), message: `"${name}" is not a valid property name` });
    if (typeof s.pattern === 'string') {
      try {
        new RegExp(s.pattern);
      } catch {
        out.push({ path: ptr(path, 'pattern'), message: 'pattern is not a valid regular expression' });
      }
    }
    if (typeof s.minimum === 'number' && typeof s.maximum === 'number' && s.minimum > s.maximum) out.push({ path, message: 'minimum is greater than maximum' });
    if (Array.isArray(s.enum) && s.enum.length === 0) out.push({ path: ptr(path, 'enum'), message: 'enum needs at least one value' });
    for (const [name, child] of Object.entries(ps)) walk(child, ptr(ptr(path, 'properties'), name), depth + 1);
    if (isObj(s.items)) walk(s.items, ptr(path, 'items'), depth + 1);
    if (isObj(s.$defs)) for (const [n, d] of Object.entries(s.$defs)) walk(d, ptr(ptr(path, '$defs'), n), depth + 1);
  };
  walk(root, '', 0);
  return out;
}

// ---------------------------------------------------------------- compatibility

export interface CompatChange {
  path: string;
  kind: string;
  breaking: boolean;
  detail?: string;
}

function widens(oldT: SType[], newT: SType[]): { narrowed: SType[]; added: SType[] } {
  const covers = (set: SType[], t: SType) => set.includes(t) || (t === 'integer' && set.includes('number'));
  return { narrowed: oldT.filter((t) => !covers(newT, t)), added: newT.filter((t) => !covers(oldT, t)) };
}

/** Compare a proposed schema with the previous version. Mirrors the server's rules for the editor preview. */
export function compatibility(oldS: JsonSchema, newS: JsonSchema): CompatChange[] {
  const out: CompatChange[] = [];
  const cmp = (a0: JsonSchema | undefined, b0: JsonSchema | undefined, path: string) => {
    const a = resolveRef(oldS, a0) ?? {};
    const b = resolveRef(newS, b0) ?? {};
    const at = schemaTypes(a), bt = schemaTypes(b);
    if (at.length && bt.length) {
      const { narrowed, added } = widens(at, bt);
      for (const t of narrowed) {
        if (t === 'null') out.push({ path, kind: 'null_removed', breaking: true, detail: 'no longer nullable' });
        else out.push({ path, kind: 'type_narrowed', breaking: true, detail: `${t} no longer accepted` });
      }
      for (const t of added) out.push({ path, kind: t === 'null' ? 'became_nullable' : 'type_widened', breaking: false, detail: `${t} now accepted` });
    } else if (!at.length && bt.length) {
      out.push({ path, kind: 'type_narrowed', breaking: true, detail: `now must be ${bt.join(' or ')}` });
    }
    if (Array.isArray(b.enum)) {
      const old = Array.isArray(a.enum) ? a.enum.map((x) => JSON.stringify(x)) : null;
      const now = b.enum.map((x) => JSON.stringify(x));
      if (!old) out.push({ path, kind: 'enum_added', breaking: true, detail: 'values now restricted' });
      else {
        const removed = old.filter((x) => !now.includes(x));
        const added = now.filter((x) => !old.includes(x));
        if (removed.length) out.push({ path, kind: 'enum_narrowed', breaking: true, detail: `removed ${removed.join(', ')}` });
        if (added.length) out.push({ path, kind: 'enum_widened', breaking: false, detail: `added ${added.join(', ')}` });
      }
    }
    const tighter = (k: string, lowerIsTighter: boolean) => {
      const x = a[k], y = b[k];
      if (typeof y !== 'number') return;
      if (typeof x !== 'number' || (lowerIsTighter ? y < x : y > x)) out.push({ path, kind: 'constraint_tightened', breaking: true, detail: `${k} ${x ?? 'none'} → ${y}` });
    };
    tighter('minimum', false);
    tighter('exclusiveMinimum', false);
    tighter('minLength', false);
    tighter('minItems', false);
    tighter('maximum', true);
    tighter('exclusiveMaximum', true);
    tighter('maxLength', true);
    tighter('maxItems', true);
    if (typeof b.pattern === 'string' && b.pattern !== a.pattern) out.push({ path, kind: 'pattern_changed', breaking: true, detail: b.pattern });
    if (typeof b.format === 'string' && b.format !== a.format) out.push({ path, kind: 'format_changed', breaking: true, detail: b.format });
    if (b.additionalProperties === false && a.additionalProperties !== false) out.push({ path, kind: 'additional_properties_closed', breaking: true });
    const ap = props(a), bp = props(b);
    const ar = required(a), br = required(b);
    for (const name of Object.keys(ap)) {
      const p = ptr(ptr(path, 'properties'), name);
      if (!(name in bp)) out.push({ path: p, kind: 'property_removed', breaking: true });
      else cmp(ap[name], bp[name], p);
    }
    for (const name of Object.keys(bp)) {
      if (!(name in ap)) out.push({ path: ptr(ptr(path, 'properties'), name), kind: 'property_added', breaking: br.includes(name), detail: br.includes(name) ? 'added as required' : undefined });
    }
    for (const r of br) if (r in ap && !ar.includes(r)) out.push({ path: ptr(ptr(path, 'properties'), r), kind: 'became_required', breaking: true });
    if (isObj(a.items) || isObj(b.items)) cmp(a.items as JsonSchema, b.items as JsonSchema, ptr(path, 'items'));
  };
  cmp(oldS, newS, '');
  return out;
}

// ---------------------------------------------------------------- examples

/** A plausible sample document for a schema (defaults, first enum value, required fields first). */
export function exampleFor(root: JsonSchema, s0: JsonSchema = root, depth = 0, onlyRequired = false): unknown {
  const s = resolveRef(root, s0) ?? {};
  if ('default' in s) return s.default;
  if ('const' in s) return s.const;
  if (Array.isArray(s.enum) && s.enum.length) return s.enum.find((x) => x !== null) ?? s.enum[0];
  if (depth > 8) return null;
  switch (primaryType(s)) {
    case 'object': {
      const out: Record<string, unknown> = {};
      const reqs = required(s);
      for (const [k, v] of Object.entries(props(s))) {
        if (onlyRequired && !reqs.includes(k)) continue;
        out[k] = exampleFor(root, v, depth + 1, onlyRequired);
      }
      return out;
    }
    case 'array':
      return isObj(s.items) && !onlyRequired ? [exampleFor(root, s.items as JsonSchema, depth + 1, onlyRequired)] : [];
    case 'string':
      if (s.format === 'date-time') return '2026-10-30T10:00:00Z';
      if (s.format === 'date') return '2026-10-30';
      if (s.format === 'email') return 'someone@example.com';
      if (s.format === 'uuid') return '00000000-0000-4000-8000-000000000000';
      return '';
    case 'integer':
    case 'number':
      return typeof s.minimum === 'number' ? s.minimum : 0;
    case 'boolean':
      return false;
    case 'null':
      return null;
    default:
      return null;
  }
}

/** Short human type label: `string?`, `integer`, `enum(3)`, `object`, `array<object>`. */
export function typeLabel(s: JsonSchema | undefined, root?: JsonSchema): string {
  const r = root ? resolveRef(root, s) : s;
  if (!r) return 'any';
  const t = primaryType(r);
  let label: string = t;
  if (Array.isArray(r.enum)) label = `enum(${r.enum.filter((v) => v !== null).length})`;
  else if (t === 'array') {
    const items = isObj(r.items) ? (root ? resolveRef(root, r.items as JsonSchema) : (r.items as JsonSchema)) : undefined;
    label = `array<${items ? primaryType(items) : 'any'}>`;
  }
  return isNullable(r) ? `${label}?` : label;
}
