// Mock validation: approximates the server's checks so editors show realistic problems in mock mode.

import type { Condition, DecisionSpec, ExperimentSpec, GateSpec, LayerSpec, LookupSpec, ResourceKind, ResourceRef, SchemaRef, SchemaSpec, SegmentSpec, ValidationProblem } from '../types.gen';
import { checkCondition, condPointer, operatorInfo, type CondPath } from '../../model/condition';
import { isExprSlot } from '../../model/dataSlot';
import { syntaxError } from '../../model/expr';
import { graphProblems } from '../../model/graph';
import { getAt, isObj, lintSchema, listFields, primaryType, validateDocument, type FieldInfo, type JsonSchema } from '../../model/schemaTree';

export interface Registry {
  schema(name: string, version: number): JsonSchema | undefined;
  schemaExists(name: string): boolean;
  exists(kind: ResourceKind, name: string): boolean;
  segmentType(name: string): string | undefined;
  /** Attribute names of a dynamic segment's attributesSchema. */
  segmentAttributes(name: string): string[] | undefined;
  experimentVariants(name: string): string[] | undefined;
}

export interface Ref extends ResourceRef {
  where: string;
}

/** Every resource a spec reads, with the JSON pointer where. */
export function collectRefs(kind: ResourceKind, spec: unknown): Ref[] {
  const out: Ref[] = [];
  const walk = (v: unknown, path: string, parentKey: string) => {
    if (Array.isArray(v)) return v.forEach((x, i) => walk(x, `${path}/${i}`, parentKey));
    if (!isObj(v)) return;
    for (const [k, x] of Object.entries(v)) {
      const p = `${path}/${k}`;
      if (['inputSchema', 'outputSchema', 'attributesSchema', 'valueSchema', 'schema'].includes(k) && isObj(x) && typeof x.name === 'string' && typeof x.version === 'number') {
        out.push({ kind: 'schemas', name: x.name, version: x.version, where: p });
        continue;
      }
      if (k === 'responseSchema' || k === 'jsonSchema') continue;
      if (typeof x === 'string' && x) {
        if (k === 'segment') out.push({ kind: 'segments', name: x, where: p });
        if (k === 'gate') out.push({ kind: 'gates', name: x, where: p });
        if (k === 'experiment') out.push({ kind: 'experiments', name: x, where: p });
        if (k === 'lookup' && v.type === 'lookup') out.push({ kind: 'lookups', name: x, where: p });
        if (k === 'decision' && v.type === 'subdecision') out.push({ kind: 'decisions', name: x, where: p });
        if (k === 'layer' && kind === 'experiments') out.push({ kind: 'layers', name: x, where: p });
      }
      walk(x, p, k);
    }
  };
  walk(spec, '', '');
  // expression calls: lookup("x"), inSegment("x"), gate("x"), variant("x")
  const text = JSON.stringify(spec ?? {});
  for (const m of text.matchAll(/\b(lookup|inSegment|gate|variant)\(\\"([a-z][a-z0-9_]*)\\"/g)) {
    const k: ResourceKind = m[1] === 'lookup' ? 'lookups' : m[1] === 'inSegment' ? 'segments' : m[1] === 'gate' ? 'gates' : 'experiments';
    if (!out.some((r) => r.kind === k && r.name === m[2])) out.push({ kind: k, name: m[2], where: '(expression)' });
  }
  return out;
}

function exprProblems(expr: unknown, path: string, out: ValidationProblem[]): void {
  if (typeof expr !== 'string') return;
  if (!expr.trim()) {
    out.push({ code: 'PARSE_ERROR', severity: 'error', path, message: 'Expression is empty.' });
    return;
  }
  const err = syntaxError(expr);
  if (err) out.push({ code: 'PARSE_ERROR', severity: 'error', path, message: err });
}

/** Problems in a data slot checked against a schema; `=expr` values are checked for syntax only. */
function dataProblems(value: unknown, schema: JsonSchema | undefined, path: string, out: ValidationProblem[]): void {
  const walk = (v: unknown, p: string) => {
    if (isExprSlot(v)) exprProblems(v.slice(1), p, out);
    else if (Array.isArray(v)) v.forEach((x, i) => walk(x, `${p}/${i}`));
    else if (isObj(v)) for (const [k, x] of Object.entries(v)) walk(x, `${p}/${k}`);
  };
  walk(value, path);
  if (!schema || value === undefined) return;
  for (const e of validateDocument(schema, value)) {
    // skip errors on values that are expressions (checked at runtime)
    const ptr = e.path.split('/').slice(1);
    let cur: unknown = value;
    for (const s of ptr) cur = isObj(cur) ? cur[s] : Array.isArray(cur) ? cur[Number(s)] : undefined;
    if (isExprSlot(cur)) continue;
    out.push({ code: 'TYPE_MISMATCH', severity: 'error', path: path + e.path, message: `${e.path || 'value'} ${e.message}` });
  }
}

function conditionProblems(cond: Condition | undefined, base: string, fields: FieldInfo[] | null, reg: Registry, out: ValidationProblem[], schemaLabel: string): void {
  if (!cond) {
    out.push({ code: 'PARSE_ERROR', severity: 'error', path: base, message: 'Condition is missing.' });
    return;
  }
  const segs = (() => {
    const names = new Set<string>();
    const walk = (c: Condition) => {
      if ('all' in c) c.all.forEach(walk);
      else if ('any' in c) c.any.forEach(walk);
      else if ('not' in c) walk(c.not);
      else if ('segment' in c && c.segment) names.add(c.segment);
    };
    walk(cond);
    return [...names].filter((n) => reg.exists('segments', n)).map((n) => ({ name: n, type: reg.segmentType(n) }));
  })();
  for (const p of checkCondition(cond, { fields, segments: segs })) {
    const code = /not in the input schema/.test(p.message) ? 'UNKNOWN_FIELD' : /does not apply/.test(p.message) ? 'TYPE_MISMATCH' : /does not exist/.test(p.message) ? 'SEGMENT_NOT_FOUND' : 'INVALID_CONDITION';
    out.push({ code, severity: 'error', path: condPointer(cond, base, p.path), message: code === 'UNKNOWN_FIELD' ? `${p.message.replace('the input schema', schemaLabel)}` : p.message });
  }
  // type of literal values vs field type; expressions; gates; dynamic segment attributes
  const walk = (c: Condition, path: CondPath) => {
    const ptr = condPointer(cond, base, path);
    if ('all' in c) return c.all.forEach((x, i) => walk(x, [...path, i]));
    if ('any' in c) return c.any.forEach((x, i) => walk(x, [...path, i]));
    if ('not' in c) return walk(c.not, [...path, 0]);
    if ('field' in c && fields) {
      const f = fields.find((x) => x.path === c.field);
      const info = operatorInfo(c.op);
      if (f && info && info.valueKind !== 'none') {
        const vals = Array.isArray(c.value) ? c.value : [c.value];
        const wantNum = f.type === 'integer' || f.type === 'number';
        for (const v of vals) {
          if (wantNum && typeof v === 'string') out.push({ code: 'TYPE_MISMATCH', severity: 'error', path: ptr, message: `${c.field} is ${f.type} in ${schemaLabel}; cannot compare with string ${JSON.stringify(v)}` });
          if (f.type === 'string' && typeof v === 'number' && !info.valueType) out.push({ code: 'TYPE_MISMATCH', severity: 'warning', path: ptr, message: `${c.field} is string in ${schemaLabel}; ${v} is compared as text` });
          if (f.enum && ['EQ', 'IN'].includes(c.op) && !f.enum.includes(v)) out.push({ code: 'TYPE_MISMATCH', severity: 'warning', path: ptr, message: `${JSON.stringify(v)} is not one of ${c.field}'s values (${f.enum.map((x) => JSON.stringify(x)).join(', ')})` });
        }
      }
    }
    if ('expr' in c) exprProblems(c.expr, `${ptr}/expr`, out);
    if ('segment' in c) {
      if (c.key) exprProblems(c.key, `${ptr}/key`, out);
      if (c.attributes) {
        for (const [k, e] of Object.entries(c.attributes)) exprProblems(e, `${ptr}/attributes/${k}`, out);
        const want = reg.segmentAttributes(c.segment);
        if (want) for (const a of want) if (!(a in c.attributes)) out.push({ code: 'TYPE_MISMATCH', severity: 'error', path: `${ptr}/attributes`, message: `segment ${c.segment} needs attribute ${a}` });
      }
    }
    if ('gate' in c && c.gate && !reg.exists('gates', c.gate)) out.push({ code: 'UNKNOWN_GATE', severity: 'error', path: ptr, message: `gate ${c.gate} does not exist` });
    if ('experiment' in c && c.experiment) {
      const vs = reg.experimentVariants(c.experiment);
      if (!vs) out.push({ code: 'UNKNOWN_EXPERIMENT', severity: 'error', path: ptr, message: `experiment ${c.experiment} does not exist` });
      else for (const v of c.variants) if (!vs.includes(v)) out.push({ code: 'UNKNOWN_VARIANT', severity: 'error', path: ptr, message: `${c.experiment} has no variant ${v}` });
    }
  };
  walk(cond, []);
}

function schemaRefProblems(ref: SchemaRef | undefined, path: string, reg: Registry, required: string | null, out: ValidationProblem[]): JsonSchema | undefined {
  if (!ref || !ref.name) {
    if (required) out.push({ code: 'SCHEMA_REQUIRED', severity: 'error', path, message: required });
    return undefined;
  }
  const s = reg.schema(ref.name, ref.version);
  if (!s) out.push({ code: 'SCHEMA_NOT_FOUND', severity: 'error', path, message: reg.schemaExists(ref.name) ? `${ref.name} has no version ${ref.version}; cut one in the schema registry` : `schema ${ref.name} is not registered` });
  return s;
}

/** The 422 SCHEMA_REQUIRED / SCHEMA_NOT_FOUND rule applied on create and save. */
export function schemaRuleErrors(spec: DecisionSpec, reg: Registry): { code: string; errors: { path: string; code: string; message: string }[] } | null {
  const errors: { path: string; code: string; message: string }[] = [];
  (spec.nodes ?? []).forEach((n, i) => {
    if (n.type !== 'rules' && n.type !== 'table') return;
    for (const k of ['inputSchema', 'outputSchema'] as const) {
      const ref = (n as { inputSchema?: SchemaRef; outputSchema?: SchemaRef })[k];
      if (!ref?.name) errors.push({ path: `/nodes/${i}/${k}`, code: 'SCHEMA_REQUIRED', message: `${k} is required for a ${n.type} node` });
      else if (!reg.schema(ref.name, ref.version)) errors.push({ path: `/nodes/${i}/${k}`, code: 'SCHEMA_NOT_FOUND', message: `${ref.name}@${ref.version} is not registered` });
    }
  });
  if (!errors.length) return null;
  return { code: errors.some((e) => e.code === 'SCHEMA_REQUIRED') ? 'SCHEMA_REQUIRED' : 'SCHEMA_NOT_FOUND', errors };
}

export function validateSpec(kind: ResourceKind, spec: unknown, reg: Registry): ValidationProblem[] {
  const out: ValidationProblem[] = [];
  if (!isObj(spec)) return [{ code: 'INVALID_ARGUMENT', severity: 'error', path: '', message: 'Spec must be an object.' }];
  switch (kind) {
    case 'schemas': {
      const s = spec as unknown as SchemaSpec;
      if (!isObj(s.jsonSchema)) return [{ code: 'INVALID_ARGUMENT', severity: 'error', path: '/jsonSchema', message: 'jsonSchema is required.' }];
      for (const e of lintSchema(s.jsonSchema)) out.push({ code: 'INVALID_SCHEMA', severity: 'error', path: '/jsonSchema' + e.path, message: e.message });
      (s.examples ?? []).forEach((ex, i) => {
        for (const e of validateDocument(s.jsonSchema, ex)) out.push({ code: 'EXAMPLE_INVALID', severity: 'error', path: `/examples/${i}${e.path}`, message: `example ${i + 1}: ${e.path || 'document'} ${e.message}` });
      });
      return out;
    }
    case 'decisions':
      return validateDecision(spec as unknown as DecisionSpec, reg);
    case 'segments': {
      const s = spec as unknown as SegmentSpec;
      if (s.type === 'dynamic') {
        const sch = schemaRefProblems(s.dynamic?.attributesSchema, '/dynamic/attributesSchema', reg, 'A dynamic segment needs an attributesSchema.', out);
        conditionProblems(s.dynamic?.when, '/dynamic/when', sch ? listFields(sch) : null, reg, out, s.dynamic?.attributesSchema ? `${s.dynamic.attributesSchema.name}@${s.dynamic.attributesSchema.version}` : 'the attributes schema');
      }
      if (s.type === 'rollout') {
        const p = s.rollout?.percent;
        if (typeof p !== 'number' || p < 0 || p > 100) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: '/rollout/percent', message: 'Percent must be between 0 and 100.' });
        if (s.rollout?.include) conditionProblems(s.rollout.include, '/rollout/include', null, reg, out, 'attributes');
      }
      return out;
    }
    case 'lookups':
      schemaRefProblems((spec as unknown as LookupSpec).valueSchema, '/valueSchema', reg, 'A lookup needs a valueSchema.', out);
      return out;
    case 'gates': {
      const g = spec as unknown as GateSpec;
      const sch = g.attributesSchema ? schemaRefProblems(g.attributesSchema, '/attributesSchema', reg, null, out) : undefined;
      (g.rules ?? []).forEach((r, i) => {
        if (!r.id) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `/rules/${i}/id`, message: 'Rule needs an id.' });
        conditionProblems(r.when, `/rules/${i}/when`, sch ? listFields(sch) : null, reg, out, g.attributesSchema?.name ?? 'attributes');
      });
      return out;
    }
    case 'experiments': {
      const e = spec as unknown as ExperimentSpec;
      if ((e.variants ?? []).length < 2) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: '/variants', message: 'An experiment needs at least two variants.' });
      const names = new Set<string>();
      (e.variants ?? []).forEach((v, i) => {
        if (!v.name) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `/variants/${i}/name`, message: 'Variant needs a name.' });
        if (names.has(v.name)) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `/variants/${i}/name`, message: `Two variants are called ${v.name}.` });
        names.add(v.name);
        if (!(v.weight >= 0)) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `/variants/${i}/weight`, message: 'Weight must be 0 or more.' });
      });
      if ((e.variants ?? []).every((v) => !v.weight)) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: '/variants', message: 'At least one variant needs a weight above 0.' });
      if (e.layer && !reg.exists('layers', e.layer)) out.push({ code: 'UNKNOWN_LAYER', severity: 'error', path: '/layer', message: `layer ${e.layer} does not exist` });
      if (e.attributesSchema) schemaRefProblems(e.attributesSchema, '/attributesSchema', reg, null, out);
      if (e.targeting) conditionProblems(e.targeting, '/targeting', null, reg, out, 'attributes');
      return out;
    }
    case 'layers': {
      const l = spec as unknown as LayerSpec;
      const sum = (l.allocations ?? []).reduce((a, x) => a + (x.percent || 0), 0);
      if (sum > 100) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: '/allocations', message: `Shares add up to ${sum}%; they must be 100% or less.` });
      (l.allocations ?? []).forEach((a, i) => {
        if (!reg.exists('experiments', a.experiment)) out.push({ code: 'UNKNOWN_EXPERIMENT', severity: 'error', path: `/allocations/${i}/experiment`, message: `experiment ${a.experiment || '?'} does not exist` });
      });
      return out;
    }
  }
}

function validateDecision(spec: DecisionSpec, reg: Registry): ValidationProblem[] {
  const out: ValidationProblem[] = [...graphProblems(spec)];
  const inputFieldsOf = (ref: SchemaRef | undefined) => {
    const s = ref ? reg.schema(ref.name, ref.version) : undefined;
    return s ? listFields(s) : null;
  };
  const piiNames = new Set<string>();
  if (spec.inputSchema) {
    const s = reg.schema(spec.inputSchema.name, spec.inputSchema.version);
    if (s) listFields(s).filter((f) => f.pii).forEach((f) => piiNames.add(f.path));
  }
  // PII fields of the entry rules/transform input as well
  spec.nodes.forEach((n, i) => {
    const base = `/nodes/${i}`;
    if (n.from) exprProblems(n.from, `${base}/from`, out);
    switch (n.type) {
      case 'rules': {
        const inS = schemaRefProblems(n.inputSchema, `${base}/inputSchema`, reg, 'inputSchema is required for a rules node. Register it under Schemas first.', out);
        const outS = schemaRefProblems(n.outputSchema, `${base}/outputSchema`, reg, 'outputSchema is required for a rules node. Register it under Schemas first.', out);
        if (inS) listFields(inS).filter((f) => f.pii).forEach((f) => piiNames.add(f.path));
        const fields = inputFieldsOf(n.inputSchema);
        const label = n.inputSchema ? `${n.inputSchema.name}@${n.inputSchema.version}` : 'the input schema';
        const ids = new Set<string>();
        (n.rules ?? []).forEach((r, j) => {
          if (ids.has(r.id)) out.push({ code: 'DUPLICATE_ID', severity: 'error', path: `${base}/rules/${j}/id`, message: `Two rules are called ${r.id}.` });
          ids.add(r.id);
          conditionProblems(r.when, `${base}/rules/${j}/when`, fields, reg, out, label);
          if (r.then === undefined) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `${base}/rules/${j}/then`, message: 'Rule needs a then value.' });
          else dataProblems(r.then, outS && n.mode !== 'all_matches' ? outS : outS, `${base}/rules/${j}/then`, out);
          if (r.rollout && (r.rollout.percent < 0 || r.rollout.percent > 100)) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `${base}/rules/${j}/rollout/percent`, message: 'Rollout percent must be between 0 and 100.' });
        });
        if (n.default !== undefined) dataProblems(n.default, outS, `${base}/default`, out);
        else out.push({ code: 'NO_DEFAULT', severity: 'warning', path: `${base}/default`, message: 'No default: when no rule matches the node returns null.' });
        break;
      }
      case 'table': {
        schemaRefProblems(n.inputSchema, `${base}/inputSchema`, reg, 'inputSchema is required for a table node. Register it under Schemas first.', out);
        const outS = schemaRefProblems(n.outputSchema, `${base}/outputSchema`, reg, 'outputSchema is required for a table node. Register it under Schemas first.', out);
        (n.inputs ?? []).forEach((c, j) => exprProblems(c.expr, `${base}/inputs/${j}/expr`, out));
        const props = outS && isObj(outS.properties) ? Object.keys(outS.properties) : null;
        (n.outputs ?? []).forEach((o, j) => {
          if (props && !props.includes(o)) out.push({ code: 'UNKNOWN_FIELD', severity: 'error', path: `${base}/outputs/${j}`, message: `${o} is not a property of ${n.outputSchema?.name}` });
        });
        (n.rows ?? []).forEach((r, j) => {
          for (const [col, cell] of Object.entries(r.when ?? {})) {
            if (!(n.inputs ?? []).some((c) => c.name === col)) out.push({ code: 'UNKNOWN_FIELD', severity: 'warning', path: `${base}/rows/${j}/when/${col}`, message: `column ${col} is not an input` });
            const t = String(cell).trim();
            if (/^\[\s*-?\d/.test(t) && !/^\[\s*-?\d+(\.\d+)?\s*\.\.\s*-?\d+(\.\d+)?\s*\]$/.test(t)) {
              try {
                JSON.parse(t);
              } catch {
                out.push({ code: 'PARSE_ERROR', severity: 'error', path: `${base}/rows/${j}/when/${col}`, message: `cell ${t} is not a list or a range` });
              }
            }
          }
          if (outS) dataProblems(r.then, { ...outS, required: [] }, `${base}/rows/${j}/then`, out);
        });
        if (n.hitPolicy === 'priority' && (n.rows ?? []).some((r) => r.priority === undefined)) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `${base}/rows`, message: 'Hit policy priority needs a priority on every row.' });
        break;
      }
      case 'transform':
      case 'output': {
        if (n.expression) exprProblems(n.expression, `${base}/expression`, out);
        (n.fields ?? []).forEach((f, j) => {
          if (!f.target) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `${base}/fields/${j}/target`, message: 'Field needs a target path.' });
          exprProblems(f.expr, `${base}/fields/${j}/expr`, out);
        });
        if (n.type === 'transform' && n.outputSchema) schemaRefProblems(n.outputSchema, `${base}/outputSchema`, reg, null, out);
        break;
      }
      case 'switch':
        (n.cases ?? []).forEach((c, j) => exprProblems(c.when, `${base}/cases/${j}/when`, out));
        break;
      case 'lookup':
        if (!n.lookup) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `${base}/lookup`, message: 'Choose a lookup.' });
        else if (!reg.exists('lookups', n.lookup)) out.push({ code: 'UNKNOWN_LOOKUP', severity: 'error', path: `${base}/lookup`, message: `lookup ${n.lookup} does not exist` });
        exprProblems(n.key, `${base}/key`, out);
        break;
      case 'gate':
        if (!n.gate || !reg.exists('gates', n.gate)) out.push({ code: 'UNKNOWN_GATE', severity: 'error', path: `${base}/gate`, message: n.gate ? `gate ${n.gate} does not exist` : 'Choose a gate.' });
        break;
      case 'experiment':
        if (!n.experiment || !reg.exists('experiments', n.experiment)) out.push({ code: 'UNKNOWN_EXPERIMENT', severity: 'error', path: `${base}/experiment`, message: n.experiment ? `experiment ${n.experiment} does not exist` : 'Choose an experiment.' });
        break;
      case 'subdecision':
        if (!n.decision || !reg.exists('decisions', n.decision)) out.push({ code: 'UNKNOWN_DECISION', severity: 'error', path: `${base}/decision`, message: n.decision ? `decision ${n.decision} does not exist` : 'Choose a decision.' });
        if (n.input) exprProblems(n.input, `${base}/input`, out);
        break;
      case 'ai': {
        for (const [k, e] of Object.entries(n.vars ?? {})) {
          exprProblems(e, `${base}/vars/${k}`, out);
          for (const pii of piiNames) if (new RegExp(`\\b${pii.replace(/\./g, '\\.')}\\b`).test(e) || new RegExp(`\\.${pii.split('.').at(-1)}\\b`).test(e)) out.push({ code: 'PII_IN_PROMPT', severity: 'error', path: `${base}/vars/${k}`, message: `var ${k} reads ${pii}, which is marked PII; PII never reaches a prompt` });
        }
        const used = [...(n.prompt?.user ?? '').matchAll(/\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g), ...(n.prompt?.system ?? '').matchAll(/\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g)].map((m) => m[1]);
        for (const u of used) if (!(u in (n.vars ?? {}))) out.push({ code: 'UNKNOWN_VAR', severity: 'error', path: `${base}/prompt/user`, message: `{{${u}}} is not a var` });
        if (!n.prompt?.user?.trim()) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `${base}/prompt/user`, message: 'The user prompt is empty.' });
        if (!isObj(n.responseSchema) || primaryType(n.responseSchema as JsonSchema) === 'any') out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `${base}/responseSchema`, message: 'responseSchema needs a type.' });
        else for (const e of lintSchema(n.responseSchema as JsonSchema)) out.push({ code: 'INVALID_SCHEMA', severity: 'error', path: `${base}/responseSchema${e.path}`, message: e.message });
        if (n.timeoutMs < 50 || n.timeoutMs > 10000) out.push({ code: 'INVALID_ARGUMENT', severity: 'error', path: `${base}/timeoutMs`, message: 'Timeout must be 50–10,000 ms.' });
        break;
      }
    }
  });
  if (spec.output?.schema) schemaRefProblems(spec.output.schema, '/output/schema', reg, null, out);
  if (spec.inputSchema) schemaRefProblems(spec.inputSchema, '/inputSchema', reg, null, out);
  if (spec.fallback !== undefined && spec.output?.schema) dataProblems(spec.fallback, reg.schema(spec.output.schema.name, spec.output.schema.version), '/fallback', out);
  if (spec.fallback === undefined) out.push({ code: 'NO_FALLBACK', severity: 'warning', path: '/fallback', message: 'No fallback: a failing node without onError returns an error to the caller.' });
  return out;
}

/** Resolve a schema JSON from a registry-style spec (helper for handlers). */
export function schemaOf(spec: unknown): JsonSchema | undefined {
  return isObj(spec) && isObj(spec.jsonSchema) ? (spec.jsonSchema as JsonSchema) : undefined;
}

export { getAt };
