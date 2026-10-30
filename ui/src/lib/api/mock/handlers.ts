// In-browser implementation of every operation in the contract, over the fixture database.

import type { OperationId, Ops } from '../operations.gen';
import type {
  App,
  AuditEvent,
  ChangeRequest,
  DecisionSpec,
  ExperimentSpec,
  ExprCatalogue,
  GateSpec,
  LookupRow,
  LookupSpec,
  Member,
  Release,
  Resource,
  ResourceKind,
  ResourceSummary,
  Role,
  SegmentSpec,
  Sidecar,
  Usage,
  ValidationProblem,
  VersionInfo,
} from '../types.gen';
import { murmur3 } from '../../model/buckets';
import { OPERATORS } from '../../model/condition';
import { parseCsv, parseMemberList } from '../../model/csv';
import { unifiedDiff } from '../../model/diff';
import { compatibility, isObj, validateDocument, type JsonSchema } from '../../model/schemaTree';
import { toYaml } from '../../model/yaml';
import { Engine, runTests, type World } from './engine';
import { DAY, HOUR, SHADOW, type AppData, type Db, type StoredResource, type StoredVersion } from './fixtures';
import { collectRefs, schemaRuleErrors, validateSpec, type Registry } from './validate';

export class MockError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly title: string,
    readonly detail?: string,
    readonly errors?: { path: string; code?: string; message: string }[],
  ) {
    super(title);
  }
}

export interface Ctx {
  db: Db;
  now: number;
  changed: () => void;
}

type Handler<K extends OperationId> = (args: Ops[K]['args'], ctx: Ctx) => Ops[K]['response'];

const fail = (status: number, code: string, title: string, detail?: string, errors?: MockError['errors']): never => {
  throw new MockError(status, code, title, detail, errors);
};

const ROLE_RANK: Record<Role, number> = { viewer: 0, author: 1, approver: 2, admin: 3 };

function me(c: Ctx): Member {
  return c.db.members.find((m) => m.id === c.db.meId) ?? c.db.members[0];
}
function need(c: Ctx, role: Role, what: string): Member {
  const m = me(c);
  if (ROLE_RANK[m.role] < ROLE_RANK[role]) fail(403, 'FORBIDDEN', 'Not allowed', `${what} needs the ${role} role; you are ${m.role}.`);
  return m;
}
const iso = (c: Ctx, offset = 0) => new Date(c.now - offset).toISOString();
const nextId = (c: Ctx, prefix: string) => `${prefix}_${++c.db.seq}`;
const NAME = /^[a-z][a-z0-9_]{1,62}$/;

function audit(c: Ctx, action: string, resource: string, app?: string, environment?: string, details?: Record<string, unknown>, breakGlass?: boolean): void {
  const e: AuditEvent = { id: nextId(c, 'aud'), at: iso(c), actor: me(c).email, action, app, environment, resource, details, breakGlass };
  c.db.audit.unshift(e);
}

function appData(c: Ctx, app: string): AppData {
  const d = c.db.data[app];
  if (!d) fail(404, 'NOT_FOUND', 'App not found', `There is no app called ${app}.`);
  return d;
}
function envOf(c: Ctx, app: string, env: string) {
  const e = appData(c, app).environments.find((x) => x.name === env);
  if (!e) fail(404, 'NOT_FOUND', 'Environment not found', `${app} has no environment ${env}.`);
  return e!;
}
function findRes(c: Ctx, app: string, kind: ResourceKind, name: string): StoredResource | undefined {
  return appData(c, app).resources.find((r) => r.kind === kind && r.name === name && !r.archived);
}
function getRes(c: Ctx, app: string, kind: ResourceKind, name: string): StoredResource {
  const r = findRes(c, app, kind, name);
  if (!r) fail(404, 'NOT_FOUND', `${kind.replace(/s$/, '')} not found`, `There is no ${kind.replace(/s$/, '')} called ${name} in ${app}.`);
  return r!;
}
const latest = (r: StoredResource): StoredVersion | undefined => r.versions.at(-1);
const versionOf = (r: StoredResource, v: number): StoredVersion | undefined => r.versions.find((x) => x.version === v);

function page<T>(items: T[], cursor?: string, limit = 50): { items: T[]; nextCursor?: string } {
  const start = cursor ? Math.max(0, parseInt(cursor, 10) || 0) : 0;
  const n = Math.min(200, Math.max(1, limit));
  const slice = items.slice(start, start + n);
  return { items: slice, nextCursor: start + n < items.length ? String(start + n) : undefined };
}

function contentHash(v: unknown): string {
  const s = JSON.stringify(v);
  return `sha256:${murmur3(s).toString(16).padStart(8, '0')}${murmur3(s, 7).toString(16).padStart(8, '0')}`;
}

function versionInfo(v: StoredVersion): VersionInfo {
  return { version: v.version, message: v.message, contentHash: contentHash([v.spec, v.members, v.rows]), createdAt: v.createdAt, createdBy: v.createdBy, breaking: v.breaking };
}

function decorate(r: StoredResource, spec: unknown, members?: string[], rows?: LookupRow[]): unknown {
  if (r.kind === 'segments' && isObj(spec) && spec.type === 'static') return { ...spec, static: { ...(isObj(spec.static) ? spec.static : {}), memberCount: (members ?? []).length } };
  if (r.kind === 'lookups' && isObj(spec)) return { ...spec, rowCount: (rows ?? []).length };
  return spec;
}
function undecorate(kind: ResourceKind, spec: unknown): unknown {
  if (!isObj(spec)) return spec;
  if (kind === 'segments' && isObj(spec.static)) {
    const st = { ...spec.static };
    delete st.memberCount;
    return { ...spec, static: st };
  }
  if (kind === 'lookups') {
    const s = { ...spec };
    delete s.rowCount;
    return s;
  }
  return spec;
}

function draftChanged(r: StoredResource): boolean {
  const l = latest(r);
  if (!l) return true;
  return JSON.stringify(r.draft.spec) !== JSON.stringify(l.spec) || (r.members !== undefined && JSON.stringify(r.members) !== JSON.stringify(l.members ?? [])) || (r.rows !== undefined && JSON.stringify(r.rows) !== JSON.stringify(l.rows ?? []));
}

function subtype(r: StoredResource): string | undefined {
  const s = r.draft.spec as Record<string, unknown>;
  if (r.kind === 'decisions') {
    const d = s as unknown as DecisionSpec;
    return d.nodes?.length === 1 && ['rules', 'table', 'transform'].includes(d.nodes[0].type) ? d.nodes[0].type : 'graph';
  }
  if (r.kind === 'segments') return String(s.type);
  if (r.kind === 'experiments') return String(s.status);
  if (r.kind === 'schemas' && isObj(s.jsonSchema)) {
    const t = (s.jsonSchema as JsonSchema).type;
    return Array.isArray(t) ? t.join('|') : typeof t === 'string' ? t : 'any';
  }
  return undefined;
}

function summary(r: StoredResource): ResourceSummary {
  return {
    kind: r.kind,
    name: r.name,
    description: r.description,
    subtype: subtype(r),
    latestVersion: latest(r)?.version ?? 0,
    draftChanged: draftChanged(r),
    live: r.live,
    updatedAt: r.draft.updatedAt,
    updatedBy: r.draft.updatedBy,
    stats: r.stats,
  };
}

function registry(c: Ctx, app: string): Registry {
  const data = appData(c, app);
  const find = (kind: ResourceKind, name: string) => data.resources.find((r) => r.kind === kind && r.name === name && !r.archived);
  const schema = (name: string, version: number) => {
    const v = find('schemas', name)?.versions.find((x) => x.version === version);
    return v && isObj(v.spec) && isObj(v.spec.jsonSchema) ? (v.spec.jsonSchema as JsonSchema) : undefined;
  };
  return {
    schema,
    schemaExists: (n) => !!find('schemas', n),
    exists: (k, n) => !!find(k, n),
    segmentType: (n) => (find('segments', n)?.draft.spec as SegmentSpec | undefined)?.type,
    segmentAttributes: (n) => {
      const s = find('segments', n)?.draft.spec as SegmentSpec | undefined;
      if (s?.type !== 'dynamic' || !s.dynamic) return undefined;
      const sch = schema(s.dynamic.attributesSchema.name, s.dynamic.attributesSchema.version);
      return sch && isObj(sch.properties) ? Object.keys(sch.properties) : undefined;
    },
    experimentVariants: (n) => (find('experiments', n)?.draft.spec as ExperimentSpec | undefined)?.variants.map((v) => v.name),
  };
}

/** The version of a resource an environment runs (live, else latest, else draft). */
function effective(r: StoredResource, env: string): { spec: unknown; members?: string[]; rows?: LookupRow[] } {
  const live = r.live[env];
  const v = (live && versionOf(r, live.version)) || latest(r);
  if (v) return { spec: v.spec, members: v.members, rows: v.rows };
  return { spec: r.draft.spec, members: r.members, rows: r.rows };
}

function world(c: Ctx, app: string, env: string, decisionName: string): World {
  const data = appData(c, app);
  const reg = registry(c, app);
  const find = (kind: ResourceKind, name: string) => data.resources.find((r) => r.kind === kind && r.name === name && !r.archived);
  return {
    decisionName,
    env,
    schema: reg.schema,
    segment: (n) => {
      const r = find('segments', n);
      if (!r) return undefined;
      const e = effective(r, env);
      return { spec: e.spec as SegmentSpec, members: e.members ?? r.members ?? [] };
    },
    lookup: (n) => {
      const r = find('lookups', n);
      return r ? (effective(r, env).rows ?? r.rows ?? []) : undefined;
    },
    gate: (n) => {
      const r = find('gates', n);
      return r ? { spec: effective(r, env).spec as GateSpec, killed: !!data.killed[env]?.[n] } : undefined;
    },
    experiment: (n) => {
      const r = find('experiments', n);
      return r ? (effective(r, env).spec as ExperimentSpec) : undefined;
    },
    decision: (n) => {
      const r = find('decisions', n);
      return r ? (effective(r, env).spec as DecisionSpec) : undefined;
    },
  };
}

function toResource(c: Ctx, app: string, r: StoredResource): Resource {
  return {
    kind: r.kind,
    name: r.name,
    description: r.description,
    draft: {
      spec: decorate(r, r.draft.spec, r.members, r.rows) as Resource['draft']['spec'],
      revision: r.draft.revision,
      updatedAt: r.draft.updatedAt,
      updatedBy: r.draft.updatedBy,
      validation: validationResult(c, app, r.kind, r.draft.spec),
    },
    latestVersion: latest(r)?.version ?? 0,
    live: r.live,
  };
}

function validationResult(c: Ctx, app: string, kind: ResourceKind, spec: unknown) {
  const problems = validateSpec(kind, spec, registry(c, app));
  const valid = !problems.some((p) => p.severity === 'error');
  return { valid, problems, dependencies: valid ? collectRefs(kind, spec).map(({ kind: k, name, version }) => ({ kind: k, name, version })) : undefined };
}

function specFor(r: StoredResource, source: string | undefined, version: number | undefined): unknown {
  if (source === 'version' || version !== undefined) {
    const v = version !== undefined ? versionOf(r, version) : latest(r);
    if (!v) fail(404, 'NOT_FOUND', 'Version not found', `${r.name} has no version ${version ?? '(none cut yet)'}.`);
    return v!.spec;
  }
  return r.draft.spec;
}

function jsonChanges(a: unknown, b: unknown, path = ''): { op: 'add' | 'remove' | 'replace'; path: string; from?: unknown; to?: unknown }[] {
  if (JSON.stringify(a) === JSON.stringify(b)) return [];
  if (isObj(a) && isObj(b)) {
    const out: ReturnType<typeof jsonChanges> = [];
    for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) {
      const p = `${path}/${k}`;
      if (!(k in b)) out.push({ op: 'remove', path: p, from: a[k] });
      else if (!(k in a)) out.push({ op: 'add', path: p, to: b[k] });
      else out.push(...jsonChanges(a[k], b[k], p));
    }
    return out;
  }
  if (Array.isArray(a) && Array.isArray(b) && a.length === b.length) return a.flatMap((x, i) => jsonChanges(x, b[i], `${path}/${i}`));
  return [{ op: 'replace', path: path || '/', from: a, to: b }];
}

function hmac(v: string): string {
  return `hmac:${murmur3('gbx' + v).toString(16).padStart(8, '0')}${murmur3(v, 11).toString(16).padStart(8, '0')}`;
}

function liveContents(data: AppData, env: string, changed: string[]): Release['contents'] {
  return data.resources
    .filter((r) => r.live[env] && !r.archived)
    .map((r) => ({ kind: r.kind, name: r.name, version: r.live[env].version, rollout: r.live[env].rollout ? { ...r.live[env] } : undefined, changed: changed.includes(`${r.kind}/${r.name}`) }));
}

function newGeneration(c: Ctx, app: string, env: string, rel: Omit<Release, 'generation' | 'environment' | 'publishedAt' | 'publishedBy' | 'manifestHash' | 'contents'>, changed: string[]): Release {
  const data = appData(c, app);
  const e = envOf(c, app, env);
  e.generation += 1;
  e.publishedAt = iso(c);
  const contents = liveContents(data, env, changed);
  const r: Release = { generation: e.generation, environment: env, publishedAt: iso(c), publishedBy: me(c).email, manifestHash: contentHash(contents), bundleBytes: 3_400_000 + contents.length * 9000, minEngineVersion: '1.0.0', contents, ...rel };
  (data.releases[env] ??= []).unshift(r);
  // healthy sidecars swap; ones already behind stay behind
  for (const s of data.sidecars[env] ?? []) {
    if (s.status === 'in_sync') {
      s.generation = e.generation;
      s.lastSyncAt = iso(c, -1500);
    }
  }
  return r;
}

function changeRequest(c: Ctx, app: string, id: string): ChangeRequest {
  const cr = appData(c, app).changeRequests.find((x) => x.id === id);
  if (!cr) fail(404, 'NOT_FOUND', 'Change request not found', `There is no change request ${id}.`);
  return cr!;
}

function runDecisionTestsFor(c: Ctx, app: string, env: string, r: StoredResource, spec: DecisionSpec) {
  return runTests(world(c, app, env, r.name), spec);
}

function liveFilter<T extends { ts: string }>(items: T[], from?: string, to?: string): T[] {
  return items.filter((x) => (!from || x.ts >= from) && (!to || x.ts <= to));
}

const EXPR_FUNCTIONS: ExprCatalogue['functions'] = [
  ['len', 'len(v)', 'Length of a string, list or map', 'len(input.items)'],
  ['upper', 'upper(s)', 'Upper-case text', 'upper(input.lead.city)'],
  ['lower', 'lower(s)', 'Lower-case text', 'lower(input.lead.source)'],
  ['trim', 'trim(s[, chars])', 'Trim whitespace or characters', 'trim(input.name)'],
  ['trimPrefix', 'trimPrefix(s, prefix)', 'Remove a prefix', 'trimPrefix(input.lead.phone, "+91-")'],
  ['split', 'split(s, sep)', 'Split text into a list', 'split(input.tags, ",")'],
  ['join', 'join(list, sep)', 'Join a list into text', 'join(input.tags, ", ")'],
  ['replace', 'replace(s, old, new)', 'Replace text', 'replace(input.city, "Gurugram", "Gurgaon")'],
  ['abs', 'abs(n)', 'Absolute value', 'abs(input.delta)'],
  ['round', 'round(n)', 'Round to the nearest integer', 'round(input.car.price / 1000)'],
  ['floor', 'floor(n)', 'Round down', 'floor(input.score)'],
  ['ceil', 'ceil(n)', 'Round up', 'ceil(input.score)'],
  ['int', 'int(v)', 'Integer from a number or numeric text', 'int(input.vehicle.mfgYear)'],
  ['float', 'float(v)', 'Number from numeric text', 'float(input.vehicle.askingPrice)'],
  ['string', 'string(v)', 'Text from any value', 'string(input.car.year)'],
  ['min', 'min(a, b, …)', 'Smallest value', 'min(input.a, input.b)'],
  ['max', 'max(a, b, …)', 'Largest value', 'max(input.a, 0)'],
  ['mean', 'mean(list)', 'Average of numbers', 'mean(map(input.inspections, .score))'],
  ['median', 'median(list)', 'Median of numbers', 'median(input.scores)'],
  ['sum', 'sum(list[, pred])', 'Sum of numbers', 'sum(input.items, .price)'],
  ['map', 'map(list, pred)', 'Transform each element (# or .field)', 'map(input.inspections, .part)'],
  ['filter', 'filter(list, pred)', 'Keep elements where pred is true', 'filter(input.inspections, .score < 6)'],
  ['all', 'all(list, pred)', 'True when every element matches', 'all(input.inspections, .score >= 5)'],
  ['any', 'any(list, pred)', 'True when one element matches', 'any(input.tags, # == "certified")'],
  ['none', 'none(list, pred)', 'True when no element matches', 'none(input.flags, # == "flood")'],
  ['count', 'count(list, pred)', 'Number of matching elements', 'count(input.inspections, .score < 6)'],
  ['find', 'find(list, pred)', 'First matching element', 'find(input.parts, .name == "engine")'],
  ['first', 'first(list)', 'First element', 'first(input.items)'],
  ['last', 'last(list)', 'Last element', 'last(input.items)'],
  ['keys', 'keys(map)', 'Keys sorted', 'keys(input.meta)'],
  ['values', 'values(map)', 'Values sorted by key', 'values(input.meta)'],
  ['date', 'date(s)', 'Parse a date (UTC unless the text names a zone)', 'date(input.lead.created_at)'],
  ['duration', 'duration(s)', 'Parse 30s, 5m, 2h', 'duration("2h")'],
  ['toJSON', 'toJSON(v)', 'JSON text with sorted keys', 'toJSON(input)'],
  ['fromJSON', 'fromJSON(s)', 'Parse JSON text', 'fromJSON(input.payload)'],
].map(([name, signature, description, example]) => ({ name, signature, description, example }));

const GEARBOX_FUNCTIONS: ExprCatalogue['functions'] = [
  { name: 'lookup', signature: 'lookup(table, key[, default])', description: 'Value from a bundle lookup table', example: 'lookup("city_zone", input.lead.city, "other")', gearbox: true },
  { name: 'inSegment', signature: 'inSegment(segment, id)', description: 'Is the id a member of the segment', example: 'inSegment("vip_dealers", input.dealer.id)', gearbox: true },
  { name: 'gate', signature: 'gate(name)', description: 'Gate result for ctx.unit', example: 'gate("new_pricing")', gearbox: true },
  { name: 'variant', signature: 'variant(experiment)', description: 'Assigned variant for ctx.unit', example: 'variant("checkout_v2") == "treatment"', gearbox: true },
  { name: 'bucket', signature: 'bucket(salt)', description: 'Stable 0–9999 bucket for ctx.unit (nil without a unit)', example: 'bucket("fast-track-2026-10") < 2000', gearbox: true },
  { name: 'daysBetween', signature: 'daysBetween(a, b)', description: 'Whole days between two times', example: 'daysBetween(date(input.lead.created_at), ctx.now) > 30', gearbox: true },
  { name: 'coalesce', signature: 'coalesce(a, b, …)', description: 'First value that is not nil', example: 'coalesce(input.lead.alt_phone, input.lead.phone)', gearbox: true },
];

export const handlers: { [K in OperationId]: Handler<K> } = {
  // ---------------------------------------------------------------- session
  login: () => undefined,
  logout: (_a, c) => {
    audit(c, 'session.signed_out', `members/${me(c).id}`);
  },
  getSession: (_a, c) => ({ member: me(c), csrfToken: 'mock-csrf-token' }),

  // ---------------------------------------------------------------- apps
  listApps: (a, c) => {
    const m = me(c);
    const apps: App[] = c.db.apps.filter((x) => !m.apps?.length || m.apps.includes(x.name)).map((x) => ({ ...x }));
    return page(apps, a.query?.cursor, a.query?.limit);
  },
  createApp: (a, c) => {
    need(c, 'admin', 'Creating an app');
    const b = a.body;
    if (!NAME.test(b.name)) fail(422, 'INVALID_ARGUMENT', 'Invalid name', undefined, [{ path: '/name', message: 'Use a–z, 0–9 and _; start with a letter; 2–63 characters.' }]);
    if (c.db.apps.some((x) => x.name === b.name)) fail(409, 'NAME_TAKEN', 'Name taken', `An app called ${b.name} already exists.`, [{ path: '/name', message: 'already taken' }]);
    const envs = b.environments?.length ? b.environments : ['dev', 'stage', 'prod'];
    const app = { name: b.name, description: b.description, owners: b.owners ?? [], environments: envs, bundleLimitMB: 64, createdAt: iso(c) };
    c.db.apps.push(app);
    c.db.data[b.name] = {
      resources: [],
      environments: envs.map((e) => ({ name: e, generation: 0, policy: e === 'prod' ? { requiredApprovals: 1, authorCanApprove: false, allowDataReleases: false, logSampleRate: 0.01 } : e === 'dev' ? { requiredApprovals: 0, authorCanApprove: true, allowDataReleases: true, logSampleRate: 1 } : { requiredApprovals: 1, authorCanApprove: true, allowDataReleases: true, logSampleRate: 0.1 } })),
      credentials: [],
      changeRequests: [],
      releases: Object.fromEntries(envs.map((e) => [e, []])),
      sidecars: Object.fromEntries(envs.map((e) => [e, []])),
      decisionLogs: Object.fromEntries(envs.map((e) => [e, []])),
      updateLogs: Object.fromEntries(envs.map((e) => [e, []])),
      exposureLogs: Object.fromEntries(envs.map((e) => [e, []])),
      killed: Object.fromEntries(envs.map((e) => [e, {}])),
    };
    audit(c, 'app.created', `apps/${b.name}`, b.name);
    return { ...app };
  },
  getApp: (a, c) => {
    const app = c.db.apps.find((x) => x.name === a.app);
    if (!app) fail(404, 'NOT_FOUND', 'App not found', `There is no app called ${a.app}.`);
    return { ...app! };
  },
  updateApp: (a, c) => {
    need(c, 'admin', 'Changing app settings');
    const app = c.db.apps.find((x) => x.name === a.app);
    if (!app) return fail(404, 'NOT_FOUND', 'App not found');
    if (a.body.bundleLimitMB !== undefined && (a.body.bundleLimitMB < 1 || a.body.bundleLimitMB > 256)) fail(422, 'INVALID_ARGUMENT', 'Invalid bundle limit', undefined, [{ path: '/bundleLimitMB', message: 'Between 1 and 256 MB.' }]);
    Object.assign(app, a.body);
    audit(c, 'app.updated', `apps/${a.app}`, a.app);
    return { ...app };
  },
  listEnvironments: (a, c) => ({ items: appData(c, a.app).environments }),
  updateEnvironment: (a, c) => {
    need(c, 'admin', 'Changing an environment policy');
    const e = envOf(c, a.app, a.env);
    const p = a.body;
    if (p.requiredApprovals !== undefined && (p.requiredApprovals < 0 || p.requiredApprovals > 5)) fail(422, 'INVALID_ARGUMENT', 'Invalid policy', undefined, [{ path: '/requiredApprovals', message: 'Between 0 and 5.' }]);
    if (p.logSampleRate !== undefined && (p.logSampleRate < 0 || p.logSampleRate > 1)) fail(422, 'INVALID_ARGUMENT', 'Invalid policy', undefined, [{ path: '/logSampleRate', message: 'Between 0 and 1.' }]);
    e.policy = { ...e.policy, ...p };
    audit(c, 'environment.policy_changed', `environments/${a.env}`, a.app, a.env, { ...p });
    return e;
  },
  listCredentials: (a, c) => ({ items: appData(c, a.app).credentials.filter((x) => x.environment === a.env) }),
  createCredential: (a, c) => {
    need(c, 'admin', 'Creating a credential');
    envOf(c, a.app, a.env);
    const suffix = murmur3(String(c.now) + c.db.seq).toString(36).slice(0, 4);
    const cred = { id: `cred_${a.env}_${a.app.slice(0, 4)}_${suffix}`, environment: a.env, label: a.body?.label, state: 'active' as const, createdAt: iso(c), createdBy: me(c).email };
    appData(c, a.app).credentials.unshift(cred);
    audit(c, 'credential.created', `credentials/${cred.id}`, a.app, a.env);
    return { credential: cred, secret: `gbx_cred_${a.env}_${suffix}_${murmur3(cred.id).toString(36)}${murmur3(cred.id, 3).toString(36)}` };
  },
  revokeCredential: (a, c) => {
    need(c, 'admin', 'Revoking a credential');
    const cred = appData(c, a.app).credentials.find((x) => x.id === a.credentialId && x.environment === a.env);
    if (!cred) return fail(404, 'NOT_FOUND', 'Credential not found');
    cred.state = 'revoked';
    audit(c, 'credential.revoked', `credentials/${cred.id}`, a.app, a.env);
    return cred;
  },

  // ---------------------------------------------------------------- members
  listMembers: (_a, c) => ({ items: c.db.members }),
  addMember: (a, c) => {
    need(c, 'admin', 'Adding a member');
    if (!/^[^@\s]+@[^@\s]+$/.test(a.body.email)) fail(422, 'INVALID_ARGUMENT', 'Invalid email', undefined, [{ path: '/email', message: 'Enter an email address.' }]);
    if (c.db.members.some((m) => m.email === a.body.email)) fail(409, 'NAME_TAKEN', 'Already a member', undefined, [{ path: '/email', message: 'already a member' }]);
    const m: Member = { id: nextId(c, 'mem'), email: a.body.email, name: a.body.name, role: a.body.role, apps: a.body.apps ?? [] };
    c.db.members.push(m);
    audit(c, 'member.added', `members/${m.id}`, undefined, undefined, { role: m.role });
    return m;
  },
  updateMember: (a, c) => {
    need(c, 'admin', 'Changing a role');
    const m = c.db.members.find((x) => x.id === a.memberId);
    if (!m) return fail(404, 'NOT_FOUND', 'Member not found');
    const from = m.role;
    Object.assign(m, { name: a.body.name ?? m.name, role: a.body.role, apps: a.body.apps ?? m.apps });
    audit(c, 'member.role_changed', `members/${m.id}`, undefined, undefined, { from, to: m.role });
    return m;
  },
  removeMember: (a, c) => {
    need(c, 'admin', 'Removing a member');
    if (a.memberId === c.db.meId) fail(409, 'IN_USE', 'You cannot remove yourself');
    c.db.members = c.db.members.filter((x) => x.id !== a.memberId);
    audit(c, 'member.removed', `members/${a.memberId}`);
  },
  createToken: (a, c) => {
    if (!a.body.label?.trim()) fail(422, 'INVALID_ARGUMENT', 'Label required', undefined, [{ path: '/label', message: 'Give the token a label.' }]);
    const days = a.body.expiresInDays ?? 90;
    if (days < 1 || days > 365) fail(422, 'INVALID_ARGUMENT', 'Invalid expiry', undefined, [{ path: '/expiresInDays', message: 'Between 1 and 365 days.' }]);
    const id = nextId(c, 'pat');
    audit(c, 'token.created', `tokens/${id}`, undefined, undefined, { label: a.body.label });
    return { id, token: `gbx_pat_${murmur3(id + c.now).toString(36)}${murmur3(id, 5).toString(36)}${murmur3(id, 9).toString(36)}`, expiresAt: iso(c, -days * DAY) };
  },

  // ---------------------------------------------------------------- generic resources
  listResources: (a, c) => {
    const q = a.query?.q?.toLowerCase() ?? '';
    const items = appData(c, a.app)
      .resources.filter((r) => r.kind === a.kind && !r.archived)
      .filter((r) => !q || r.name.includes(q) || (r.description ?? '').toLowerCase().includes(q))
      .sort((x, y) => x.name.localeCompare(y.name))
      .map(summary);
    return page(items, a.query?.cursor, a.query?.limit);
  },
  createResource: (a, c) => {
    need(c, 'author', 'Creating a resource');
    const b = a.body;
    if (!NAME.test(b.name ?? '')) fail(422, 'INVALID_ARGUMENT', 'Invalid name', 'Names use a–z, 0–9 and _, start with a letter and are 2–63 characters long.', [{ path: '/name', message: 'Use a–z, 0–9 and _; start with a letter.' }]);
    if (findRes(c, a.app, a.kind, b.name)) fail(409, 'NAME_TAKEN', 'Name taken', `A ${a.kind.replace(/s$/, '')} called ${b.name} already exists.`, [{ path: '/name', message: 'already taken' }]);
    const spec = undecorate(a.kind, b.spec);
    if (a.kind === 'decisions') {
      const rule = schemaRuleErrors(spec as DecisionSpec, registry(c, a.app));
      if (rule) fail(422, rule.code, 'Rule node needs schemas', 'Every rules and table node needs a registered inputSchema and outputSchema. Register them under Schemas first.', rule.errors.map((e) => ({ ...e, path: '/spec' + e.path })));
    }
    const r: StoredResource = { kind: a.kind, name: b.name, description: b.description, draft: { spec, revision: 1, updatedAt: iso(c), updatedBy: me(c).email }, versions: [], live: {} };
    if (a.kind === 'segments' && (spec as SegmentSpec).type === 'static') r.members = [];
    if (a.kind === 'lookups') r.rows = [];
    appData(c, a.app).resources.push(r);
    audit(c, 'resource.created', `${a.kind}/${b.name}`, a.app);
    return toResource(c, a.app, r);
  },
  getResource: (a, c) => toResource(c, a.app, getRes(c, a.app, a.kind, a.name)),
  archiveResource: (a, c) => {
    need(c, 'author', 'Archiving');
    const r = getRes(c, a.app, a.kind, a.name);
    const envs = Object.keys(r.live);
    if (envs.length) fail(409, 'IN_USE', 'Still live', `${a.name} is live in ${envs.join(', ')}. Release a bundle without it first.`);
    const users = appData(c, a.app).resources.filter((x) => !x.archived && x !== r && collectRefs(x.kind, x.draft.spec).some((ref) => ref.kind === a.kind && ref.name === a.name));
    if (users.length) fail(409, 'IN_USE', 'In use', `Used by ${users.map((u) => `${u.kind}/${u.name}`).join(', ')}.`);
    r.archived = true;
    audit(c, 'resource.archived', `${a.kind}/${a.name}`, a.app);
  },
  saveDraft: (a, c) => {
    need(c, 'author', 'Saving a draft');
    const r = getRes(c, a.app, a.kind, a.name);
    if (a.body.revision !== r.draft.revision) fail(409, 'REVISION_CONFLICT', 'Draft changed', `Draft is at revision ${r.draft.revision}; you sent ${a.body.revision}. Reload and apply your change again.`);
    const spec = undecorate(a.kind, a.body.spec);
    if (a.kind === 'decisions') {
      const rule = schemaRuleErrors(spec as DecisionSpec, registry(c, a.app));
      if (rule) fail(422, rule.code, rule.code === 'SCHEMA_REQUIRED' ? 'Rule node needs schemas' : 'Schema not registered', 'Every rules and table node needs a registered inputSchema and outputSchema. Register them under Schemas first.', rule.errors.map((e) => ({ ...e, path: '/spec' + e.path })));
    }
    if (a.kind === 'schemas' && !isObj((spec as { jsonSchema?: unknown }).jsonSchema)) fail(422, 'INVALID_ARGUMENT', 'jsonSchema required', undefined, [{ path: '/spec/jsonSchema', message: 'required' }]);
    r.draft = { spec, revision: r.draft.revision + 1, updatedAt: iso(c), updatedBy: me(c).email };
    if (a.body.description !== undefined) r.description = a.body.description;
    if (a.kind === 'segments' && (spec as SegmentSpec).type === 'static' && !r.members) r.members = [];
    audit(c, 'draft.saved', `${a.kind}/${a.name}`, a.app, undefined, { revision: r.draft.revision });
    return toResource(c, a.app, r);
  },
  validateResource: (a, c) => {
    const r = getRes(c, a.app, a.kind, a.name);
    return validationResult(c, a.app, a.kind, a.body?.spec !== undefined ? undecorate(a.kind, a.body.spec) : r.draft.spec);
  },
  listVersions: (a, c) => {
    const r = getRes(c, a.app, a.kind, a.name);
    return page([...r.versions].reverse().map(versionInfo), a.query?.cursor, a.query?.limit);
  },
  cutVersion: (a, c) => {
    need(c, 'author', 'Cutting a version');
    const r = getRes(c, a.app, a.kind, a.name);
    if (a.body.revision !== r.draft.revision) fail(409, 'REVISION_CONFLICT', 'Draft changed', `Draft is at revision ${r.draft.revision}; you cut from ${a.body.revision}. Reload first.`);
    if (!draftChanged(r)) fail(422, 'NO_CHANGES', 'Nothing to cut', `The draft is the same as v${latest(r)!.version}.`);
    const problems = validateSpec(a.kind, r.draft.spec, registry(c, a.app)).filter((p) => p.severity === 'error');
    if (problems.length) fail(422, 'VALIDATION_FAILED', 'Draft does not validate', `${problems.length} problem${problems.length === 1 ? '' : 's'} must be fixed before cutting a version.`, problems.map((p) => ({ path: p.path, code: p.code, message: p.message })));
    let breaking = false;
    if (a.kind === 'decisions') {
      const t = runDecisionTestsFor(c, a.app, 'dev', r, r.draft.spec as DecisionSpec);
      if (t.failed) fail(422, 'TESTS_FAILED', 'Tests failed', `${t.failed} of ${t.passed + t.failed} test cases fail on the draft.`, t.results.map((x, i) => ({ x, i })).filter(({ x }) => !x.ok).map(({ x, i }) => ({ path: `/tests/${i}`, code: 'TEST_FAILED', message: `${x.name}: ${x.error ?? (x.mismatches ?? []).map((m) => `${m.path} expected ${JSON.stringify(m.expected)} got ${JSON.stringify(m.actual)}`).join('; ')}` })));
    }
    if (a.kind === 'schemas' && latest(r)) {
      const prev = (latest(r)!.spec as { jsonSchema: JsonSchema }).jsonSchema;
      const changes = compatibility(prev, (r.draft.spec as { jsonSchema: JsonSchema }).jsonSchema);
      const br = changes.filter((x) => x.breaking);
      if (br.length && !a.body.allowBreaking) fail(422, 'BREAKING_CHANGE', 'Breaking change', `${br.length} breaking change${br.length === 1 ? '' : 's'} since v${latest(r)!.version}. Tick "allow breaking" to cut anyway; decisions on the old version keep using it.`, br.map((x) => ({ path: '/jsonSchema' + x.path, code: x.kind, message: `${x.kind.replace(/_/g, ' ')}${x.detail ? ': ' + x.detail : ''}` })));
      breaking = br.length > 0;
    }
    const v: StoredVersion = { version: (latest(r)?.version ?? 0) + 1, message: a.body.message, createdAt: iso(c), createdBy: me(c).email, spec: JSON.parse(JSON.stringify(r.draft.spec)), members: r.members ? [...r.members] : undefined, rows: r.rows ? r.rows.map((x) => ({ ...x })) : undefined, breaking: a.kind === 'schemas' ? breaking : undefined };
    r.versions.push(v);
    audit(c, 'version.cut', `${a.kind}/${a.name}@${v.version}`, a.app, undefined, { message: a.body.message });
    return versionInfo(v);
  },
  getVersion: (a, c) => {
    const r = getRes(c, a.app, a.kind, a.name);
    const v = versionOf(r, a.version);
    if (!v) return fail(404, 'NOT_FOUND', 'Version not found', `${a.name} has no version ${a.version}.`);
    return { kind: r.kind, name: r.name, info: versionInfo(v), spec: decorate(r, v.spec, v.members, v.rows) as Resource['draft']['spec'] };
  },
  diffVersions: (a, c) => {
    const r = getRes(c, a.app, a.kind, a.name);
    const pick = (s: string) => {
      if (s === 'draft') return { spec: r.draft.spec, members: r.members, rows: r.rows, label: `${r.name}@draft` };
      const v = versionOf(r, Number(s));
      if (!v) return fail(404, 'NOT_FOUND', 'Version not found', `${r.name} has no version ${s}.`);
      return { spec: v.spec, members: v.members, rows: v.rows, label: `${r.name}@${v.version}` };
    };
    if (!a.query?.from || !a.query?.to) return fail(422, 'INVALID_ARGUMENT', 'from and to are required');
    const x = pick(a.query.from), y = pick(a.query.to);
    const doc = (p: { spec: unknown; members?: string[]; rows?: LookupRow[] }) => ({ spec: p.spec, ...(p.members ? { members: `${p.members.length} members, hash ${contentHash(p.members).slice(7, 15)}` } : {}), ...(p.rows ? { rows: `${p.rows.length} rows, hash ${contentHash(p.rows).slice(7, 15)}` } : {}) });
    return { unified: unifiedDiff(toYaml(doc(x)), toYaml(doc(y)), x.label, y.label), changes: jsonChanges(x.spec, y.spec) };
  },
  listUsages: (a, c) => {
    getRes(c, a.app, a.kind, a.name);
    const out: Usage[] = [];
    for (const r of appData(c, a.app).resources) {
      if (r.archived) continue;
      const scan = (spec: unknown, version?: number) => {
        for (const ref of collectRefs(r.kind, spec)) {
          if (ref.kind === a.kind && ref.name === a.name && !(r.kind === a.kind && r.name === a.name)) out.push({ kind: r.kind, name: r.name, version, usesVersion: ref.version, where: ref.where });
        }
      };
      scan(r.draft.spec);
      const l = latest(r);
      if (l) scan(l.spec, l.version);
    }
    return { items: out };
  },

  // ---------------------------------------------------------------- schemas
  validateDocument: (a, c) => {
    const r = getRes(c, a.app, 'schemas', a.name);
    const v = versionOf(r, a.version);
    if (!v) return fail(404, 'NOT_FOUND', 'Version not found', `${a.name} has no version ${a.version}.`);
    const errors = validateDocument((v.spec as { jsonSchema: JsonSchema }).jsonSchema, a.body.document).map((e) => ({ path: e.path || '/', message: e.message }));
    return { valid: errors.length === 0, errors };
  },
  checkSchemaCompatibility: (a, c) => {
    const r = getRes(c, a.app, 'schemas', a.name);
    const l = latest(r);
    if (!l) return { compatible: true, changes: [] };
    const changes = compatibility((l.spec as { jsonSchema: JsonSchema }).jsonSchema, a.body.spec.jsonSchema).map(({ path, kind, breaking }) => ({ path: path || '/', kind, breaking }));
    return { compatible: !changes.some((x) => x.breaking), changes };
  },

  // ---------------------------------------------------------------- decisions
  evaluateDecision: (a, c) => {
    const r = getRes(c, a.app, 'decisions', a.name);
    const env = a.body.environment ?? 'dev';
    envOf(c, a.app, env);
    const spec = specFor(r, a.body.source, a.body.version) as DecisionSpec;
    const engine = new Engine(world(c, a.app, env, a.name), { now: a.body.ctx?.now, unit: a.body.ctx?.unit, requestId: 'req_console' });
    const res = engine.run(spec, a.body.input, { skipAI: a.body.skipAI ?? true });
    res.decisionVersion = `${a.name}@${a.body.source === 'version' || a.body.version ? (a.body.version ?? latest(r)?.version) : 'draft'}`;
    res.generation = envOf(c, a.app, env).generation;
    return res;
  },
  runDecisionTests: (a, c) => {
    const r = getRes(c, a.app, 'decisions', a.name);
    const env = a.body?.environment ?? 'dev';
    return runDecisionTestsFor(c, a.app, env, r, specFor(r, a.body?.source, a.body?.version) as DecisionSpec);
  },
  getShadowReport: (a, c) => {
    const r = getRes(c, a.app, 'decisions', a.name);
    const node = (r.draft.spec as DecisionSpec).nodes.find((n) => n.id === a.nodeId);
    if (!node || node.type !== 'ai') return fail(404, 'NOT_FOUND', 'Not an AI node', `${a.name} has no AI node ${a.nodeId}.`);
    const days = a.query?.days ?? 7;
    const scale = days / 7;
    const rows = SHADOW.rows.map((row) => row.map((v) => Math.round(v * scale)));
    const total = rows.flat().reduce((x, y) => x + y, 0);
    const agree = rows.reduce((x, row, i) => x + row[i], 0);
    const labels = (isObj(node.responseSchema) && isObj((node.responseSchema as JsonSchema).properties) && Array.isArray(((node.responseSchema as JsonSchema).properties as Record<string, JsonSchema>).route?.enum) ? (((node.responseSchema as JsonSchema).properties as Record<string, JsonSchema>).route.enum as string[]) : SHADOW.labels).slice(0, 3);
    return { calls: total, agreement: total ? agree / total : 0, matrix: { labels, rows }, latency: { p50Ms: 380, p95Ms: 920 }, timeoutRate: 0.008, schemaFailureRate: 0.002, disagreements: SHADOW.disagreements };
  },

  // ---------------------------------------------------------------- segments
  listSegmentMembers: (a, c) => {
    const r = getRes(c, a.app, 'segments', a.name);
    const all = r.members ?? [];
    const p = a.query?.prefix ?? '';
    const filtered = p ? all.filter((m) => m.toLowerCase().startsWith(p.toLowerCase())) : all;
    return { ...page(filtered, a.query?.cursor, a.query?.limit), total: all.length };
  },
  replaceSegmentMembers: (a, c) => {
    need(c, 'author', 'Uploading members');
    const r = getRes(c, a.app, 'segments', a.name);
    const spec = r.draft.spec as SegmentSpec;
    if (spec.type !== 'static') fail(422, 'INVALID_ARGUMENT', 'Not a static segment', `${a.name} is a ${spec.type} segment; only static segments have members.`);
    let members: string[];
    try {
      members = typeof a.body === 'string' ? parseMemberList(a.body) : a.body.members.map(String);
    } catch (e) {
      return fail(422, 'INVALID_ARGUMENT', 'Could not read the upload', (e as Error).message);
    }
    if (spec.static?.keyType === 'number') {
      const bad = members.map((m, i) => ({ m, i })).filter(({ m }) => isNaN(Number(m)));
      if (bad.length) fail(422, 'INVALID_ARGUMENT', 'Members must be numbers', `${bad.length} members are not numbers (key type is number).`, bad.slice(0, 20).map(({ m, i }) => ({ path: `/members/${i}`, message: `${m} is not a number` })));
    }
    if (members.length > 1_000_000) fail(422, 'INVALID_ARGUMENT', 'Too many members', 'Up to 1,000,000 members.');
    const uniq = [...new Set(members)];
    r.members = spec.static?.pii ? uniq.map(hmac) : uniq;
    r.draft.revision += 1;
    r.draft.updatedAt = iso(c);
    r.draft.updatedBy = me(c).email;
    audit(c, 'segment.members_replaced', `segments/${a.name}`, a.app, undefined, { total: uniq.length });
    return { total: uniq.length, revision: r.draft.revision };
  },
  checkSegmentMembership: (a, c) => {
    const r = getRes(c, a.app, 'segments', a.name);
    const b = a.body;
    let spec: SegmentSpec, members: string[];
    if (b.source === 'version' || b.version !== undefined) {
      const v = b.version !== undefined ? versionOf(r, b.version) : latest(r);
      if (!v) return fail(404, 'NOT_FOUND', 'Version not found');
      spec = v.spec as SegmentSpec;
      members = v.members ?? [];
    } else {
      spec = r.draft.spec as SegmentSpec;
      members = r.members ?? [];
    }
    const eng = new Engine({ ...world(c, a.app, 'dev', '(check)'), segment: (n) => (n === a.name ? { spec, members } : undefined) }, { unit: b.unit });
    if (spec.type === 'static') {
      if (b.key === undefined || b.key === '') fail(422, 'INVALID_ARGUMENT', 'Key required', 'Send the member key to check a static segment.', [{ path: '/key', message: 'required' }]);
      const k = spec.static?.pii ? hmac(String(b.key)) : String(b.key);
      const m = members.includes(k);
      return { member: m, reason: m ? 'listed' : 'not_listed' };
    }
    if (spec.type === 'dynamic') {
      if (!isObj(b.attributes)) fail(422, 'INVALID_ARGUMENT', 'Attributes required', 'Send attributes matching the segment attributesSchema.', [{ path: '/attributes', message: 'required' }]);
      const sch = spec.dynamic ? registry(c, a.app).schema(spec.dynamic.attributesSchema.name, spec.dynamic.attributesSchema.version) : undefined;
      if (sch) {
        const errs = validateDocument(sch, b.attributes);
        if (errs.length) fail(422, 'INVALID_ARGUMENT', 'Attributes do not match the schema', undefined, errs.map((e) => ({ path: '/attributes' + e.path, message: e.message })));
      }
      const m = eng.inSegment(a.name, undefined, b.attributes as Record<string, unknown>);
      return { member: m, reason: m ? 'conditions_matched' : 'conditions_failed' };
    }
    const unit = b.unit ?? b.key;
    if (!unit) return fail(422, 'INVALID_ARGUMENT', 'Unit required', 'Send the unit to check a rollout segment.', [{ path: '/unit', message: 'required' }]);
    const ro = spec.rollout ?? { percent: 0 };
    const bk = murmur3((ro.salt || a.name) + unit) % 10000;
    const limit = Math.round((ro.percent ?? 0) * 100);
    return { member: bk < limit, reason: `bucket ${bk} ${bk < limit ? '<' : '≥'} ${limit}` };
  },

  // ---------------------------------------------------------------- lookups
  listLookupRows: (a, c) => {
    const r = getRes(c, a.app, 'lookups', a.name);
    const rows = r.rows ?? [];
    if (a.query?.key !== undefined && a.query.key !== '') return { items: rows.filter((x) => x.key === a.query!.key), total: rows.length };
    return { ...page(rows, a.query?.cursor, a.query?.limit), total: rows.length };
  },
  replaceLookupRows: (a, c) => {
    need(c, 'author', 'Uploading rows');
    const r = getRes(c, a.app, 'lookups', a.name);
    const spec = r.draft.spec as LookupSpec;
    let rows: LookupRow[];
    if (typeof a.body === 'string') {
      const csv = parseCsv(a.body);
      if (csv[0]?.[0]?.trim().toLowerCase() === 'key') csv.shift();
      rows = csv.map((cols) => {
        const raw = cols.slice(1).join(',');
        let value: unknown = raw;
        try {
          value = JSON.parse(raw);
        } catch {
          /* plain string */
        }
        return { key: cols[0].trim(), value };
      });
    } else rows = a.body.rows;
    const sch = registry(c, a.app).schema(spec.valueSchema.name, spec.valueSchema.version);
    const errors: { path: string; message: string }[] = [];
    const seen = new Set<string>();
    rows.forEach((row, i) => {
      if (!row.key) errors.push({ path: `/rows/${i}/key`, message: 'empty key' });
      if (seen.has(row.key)) errors.push({ path: `/rows/${i}/key`, message: `duplicate key ${row.key}` });
      seen.add(row.key);
      if (spec.keyType === 'number' && isNaN(Number(row.key))) errors.push({ path: `/rows/${i}/key`, message: `${row.key} is not a number` });
      if (sch) for (const e of validateDocument(sch, row.value)) errors.push({ path: `/rows/${i}/value${e.path}`, message: `${row.key}: ${e.message}` });
    });
    if (errors.length) fail(422, 'VALIDATION_FAILED', 'Rows do not match', `${errors.length} problem${errors.length === 1 ? '' : 's'}; every value must match ${spec.valueSchema.name}@${spec.valueSchema.version}.`, errors.slice(0, 50));
    r.rows = rows;
    r.draft.revision += 1;
    r.draft.updatedAt = iso(c);
    r.draft.updatedBy = me(c).email;
    audit(c, 'lookup.rows_replaced', `lookups/${a.name}`, a.app, undefined, { total: rows.length });
    return { total: rows.length, revision: r.draft.revision };
  },

  // ---------------------------------------------------------------- change requests
  listChangeRequests: (a, c) => {
    const items = appData(c, a.app).changeRequests.filter((x) => (!a.query?.status || x.status === a.query.status) && (!a.query?.environment || x.environment === a.query.environment)).sort((x, y) => y.createdAt.localeCompare(x.createdAt));
    return page(items, a.query?.cursor, a.query?.limit);
  },
  createChangeRequest: (a, c) => {
    need(c, 'author', 'Opening a change request');
    const b = a.body;
    const env = envOf(c, a.app, b.environment);
    const errors: { path: string; code?: string; message: string }[] = [];
    if (!b.title?.trim()) errors.push({ path: '/title', message: 'Give the change a title.' });
    if (!b.items?.length) errors.push({ path: '/items', message: 'Add at least one version.' });
    let passed = 0, failed = 0;
    const items = (b.items ?? []).map((it, i) => {
      const r = findRes(c, a.app, it.kind, it.name);
      if (!r) {
        errors.push({ path: `/items/${i}/name`, message: `${it.kind}/${it.name} does not exist` });
        return { ...it };
      }
      const v = versionOf(r, it.version);
      if (!v) errors.push({ path: `/items/${i}/version`, message: `${it.name} has no version ${it.version}` });
      if (it.rollout && it.kind !== 'decisions') errors.push({ path: `/items/${i}/rollout`, message: 'Only decisions roll out gradually.' });
      const live = r.live[b.environment];
      if (it.rollout && !live) errors.push({ path: `/items/${i}/rollout`, message: `${it.name} is not live in ${b.environment} yet; release it at 100% first.` });
      if (live?.version === it.version && !live.rollout) errors.push({ path: `/items/${i}/version`, code: 'NO_CHANGES', message: `${it.name} v${it.version} is already live in ${b.environment}.` });
      if (v && it.kind === 'decisions') {
        const t = runDecisionTestsFor(c, a.app, b.environment, r, v.spec as DecisionSpec);
        passed += t.passed;
        failed += t.failed;
        if (t.failed) errors.push({ path: `/items/${i}`, code: 'TESTS_FAILED', message: `${t.failed} test${t.failed === 1 ? '' : 's'} of ${it.name} v${it.version} fail against ${b.environment}` });
      }
      const fromV = live ? versionOf(r, live.version) : undefined;
      const diff = v ? unifiedDiff(fromV ? toYaml(fromV.spec) : '', toYaml(v.spec), fromV ? `${it.name}@${fromV.version}` : `${it.name} (not live)`, `${it.name}@${it.version}`) : '';
      return { ...it, liveVersion: live?.version, diff };
    });
    if (errors.length) fail(422, errors.some((e) => e.code === 'TESTS_FAILED') ? 'TESTS_FAILED' : 'VALIDATION_FAILED', 'Cannot open the change request', 'Fix the items below.', errors);
    const required = env.policy.requiredApprovals ?? 0;
    const cr: ChangeRequest = { id: `cr_${++c.db.seq}`, environment: b.environment, title: b.title, description: b.description, items, status: required === 0 ? 'approved' : 'open', author: me(c).email, createdAt: iso(c), approvals: [], requiredApprovals: required, comments: [], tests: { passed, failed } };
    appData(c, a.app).changeRequests.unshift(cr);
    audit(c, 'change_request.opened', `change-requests/${cr.id}`, a.app, b.environment);
    return cr;
  },
  getChangeRequest: (a, c) => changeRequest(c, a.app, a.id),
  approveChangeRequest: (a, c) => {
    const m = need(c, 'approver', 'Approving');
    const cr = changeRequest(c, a.app, a.id);
    if (cr.status !== 'open') fail(409, 'INVALID_STATE', 'Not open', `${cr.id} is ${cr.status}.`);
    const env = envOf(c, a.app, cr.environment);
    if (cr.author === m.email && !env.policy.authorCanApprove) fail(403, 'SELF_APPROVAL', 'You cannot approve your own change', `${cr.environment} needs an approver who is not the author.`);
    if ((cr.approvals ?? []).some((x) => x.by === m.email)) fail(409, 'ALREADY_APPROVED', 'Already approved', 'You already approved this change request.');
    cr.approvals = [...(cr.approvals ?? []), { by: m.email, at: iso(c), comment: a.body?.comment }];
    if (cr.approvals.length >= (cr.requiredApprovals ?? 1)) cr.status = 'approved';
    audit(c, 'change_request.approved', `change-requests/${cr.id}`, a.app, cr.environment);
    return cr;
  },
  rejectChangeRequest: (a, c) => {
    need(c, 'approver', 'Requesting changes');
    const cr = changeRequest(c, a.app, a.id);
    if (cr.status !== 'open' && cr.status !== 'approved') fail(409, 'INVALID_STATE', 'Not open', `${cr.id} is ${cr.status}.`);
    if (!a.body.comment?.trim()) fail(422, 'INVALID_ARGUMENT', 'Comment required', undefined, [{ path: '/comment', message: 'Say what needs to change.' }]);
    cr.status = 'rejected';
    cr.comments = [...(cr.comments ?? []), { by: me(c).email, at: iso(c), body: a.body.comment }];
    audit(c, 'change_request.rejected', `change-requests/${cr.id}`, a.app, cr.environment);
    return cr;
  },
  commentChangeRequest: (a, c) => {
    const cr = changeRequest(c, a.app, a.id);
    if (!a.body.body?.trim()) fail(422, 'INVALID_ARGUMENT', 'Empty comment', undefined, [{ path: '/body', message: 'Write a comment.' }]);
    cr.comments = [...(cr.comments ?? []), { by: me(c).email, at: iso(c), body: a.body.body }];
    return cr;
  },
  withdrawChangeRequest: (a, c) => {
    const cr = changeRequest(c, a.app, a.id);
    if (cr.author !== me(c).email) fail(403, 'FORBIDDEN', 'Only the author can withdraw', `${cr.author} opened ${cr.id}.`);
    if (cr.status === 'published' || cr.status === 'withdrawn') fail(409, 'INVALID_STATE', 'Cannot withdraw', `${cr.id} is ${cr.status}.`);
    cr.status = 'withdrawn';
    audit(c, 'change_request.withdrawn', `change-requests/${cr.id}`, a.app, cr.environment);
    return cr;
  },

  // ---------------------------------------------------------------- releases
  listReleases: (a, c) => {
    envOf(c, a.app, a.env);
    return page(appData(c, a.app).releases[a.env] ?? [], a.query?.cursor, a.query?.limit);
  },
  getRelease: (a, c) => {
    const r = (appData(c, a.app).releases[a.env] ?? []).find((x) => x.generation === a.generation);
    if (!r) return fail(404, 'NOT_FOUND', 'Release not found', `${a.env} has no generation ${a.generation}.`);
    return r;
  },
  publishRelease: (a, c) => {
    const data = appData(c, a.app);
    const env = envOf(c, a.app, a.env);
    const b = a.body;
    switch (b.type) {
      case 'change_request': {
        need(c, 'approver', 'Publishing');
        const cr = changeRequest(c, a.app, b.changeRequestId ?? '');
        if (cr.environment !== a.env) fail(422, 'INVALID_ARGUMENT', 'Wrong environment', `${cr.id} targets ${cr.environment}, not ${a.env}.`);
        if (cr.status !== 'approved') fail(409, 'NOT_APPROVED', 'Not approved', `${cr.id} is ${cr.status}; it needs ${cr.requiredApprovals} approval${cr.requiredApprovals === 1 ? '' : 's'} first.`);
        const changed: string[] = [];
        for (const it of cr.items) {
          const r = findRes(c, a.app, it.kind, it.name);
          if (!r) continue;
          const cur = r.live[a.env];
          const pct = it.rollout?.percent;
          r.live[a.env] = pct !== undefined && cur && pct < 100 ? { version: it.version, rollout: { fromVersion: cur.version, percent: pct } } : { version: it.version };
          changed.push(`${it.kind}/${it.name}`);
        }
        cr.status = 'published';
        const rel = newGeneration(c, a.app, a.env, { type: 'change_request', changeRequestId: cr.id }, changed);
        cr.publishedGeneration = rel.generation;
        audit(c, 'release.published', `change-requests/${cr.id}`, a.app, a.env, { generation: rel.generation });
        return rel;
      }
      case 'data': {
        need(c, 'author', 'Publishing data');
        if (!env.policy.allowDataReleases) fail(403, 'FORBIDDEN', 'Data releases are off', `${a.env} does not allow data releases; open a change request instead.`);
        const changed: string[] = [];
        const errors: { path: string; message: string }[] = [];
        (b.items ?? []).forEach((it, i) => {
          if (it.kind !== 'segments' && it.kind !== 'lookups') errors.push({ path: `/items/${i}/kind`, message: 'Data releases carry segments and lookups only.' });
          const r = findRes(c, a.app, it.kind, it.name);
          if (!r || !versionOf(r, it.version)) errors.push({ path: `/items/${i}`, message: `${it.kind}/${it.name} v${it.version} does not exist` });
        });
        if (!b.items?.length) errors.push({ path: '/items', message: 'Pick at least one segment or lookup version.' });
        if (errors.length) fail(422, 'VALIDATION_FAILED', 'Cannot publish', undefined, errors);
        for (const it of b.items ?? []) {
          findRes(c, a.app, it.kind, it.name)!.live[a.env] = { version: it.version };
          changed.push(`${it.kind}/${it.name}`);
        }
        const rel = newGeneration(c, a.app, a.env, { type: 'data', reason: b.reason }, changed);
        audit(c, 'release.published', changed.join(', '), a.app, a.env, { generation: rel.generation, type: 'data' });
        return rel;
      }
      case 'rollout': {
        need(c, 'approver', 'Changing a rollout');
        const r = getRes(c, a.app, 'decisions', b.decision ?? '');
        const live = r.live[a.env];
        if (!live?.rollout) return fail(409, 'NOT_ROLLING_OUT', 'No rollout in progress', `${r.name} is not rolling out in ${a.env}.`);
        const p = b.percent ?? 0;
        if (p < 0 || p > 100) fail(422, 'INVALID_ARGUMENT', 'Invalid percent', undefined, [{ path: '/percent', message: '0–100' }]);
        r.live[a.env] = p >= 100 ? { version: live.version } : p <= 0 ? { version: live.rollout.fromVersion } : { version: live.version, rollout: { fromVersion: live.rollout.fromVersion, percent: p } };
        const rel = newGeneration(c, a.app, a.env, { type: 'rollout', reason: b.reason ?? (p >= 100 ? 'Rollout complete' : p <= 0 ? 'Rollout stopped' : `Step to ${p}%`) }, [`decisions/${r.name}`]);
        audit(c, 'release.published', `decisions/${r.name}@${live.version}`, a.app, a.env, { generation: rel.generation, rollout: p });
        return rel;
      }
      case 'rollback': {
        need(c, 'approver', 'Rolling back');
        const target = (data.releases[a.env] ?? []).find((x) => x.generation === b.toGeneration);
        if (!target) return fail(422, 'INVALID_ARGUMENT', 'Unknown generation', `${a.env} has no generation ${b.toGeneration}.`, [{ path: '/toGeneration', message: 'pick an earlier generation' }]);
        if (target.generation >= env.generation) fail(422, 'INVALID_ARGUMENT', 'Not an earlier generation', `Generation ${env.generation} is current.`);
        if (!b.reason?.trim()) fail(422, 'INVALID_ARGUMENT', 'Reason required', undefined, [{ path: '/reason', message: 'Say why you are rolling back.' }]);
        const changed: string[] = [];
        for (const r of data.resources) {
          const want = target.contents.find((x) => x.kind === r.kind && x.name === r.name);
          const before = JSON.stringify(r.live[a.env] ?? null);
          if (want) r.live[a.env] = want.rollout?.rollout ? { version: want.version, rollout: { ...want.rollout.rollout } } : { version: want.version };
          else delete r.live[a.env];
          if (JSON.stringify(r.live[a.env] ?? null) !== before) changed.push(`${r.kind}/${r.name}`);
        }
        const rel = newGeneration(c, a.app, a.env, { type: 'rollback', reason: b.reason, breakGlass: true }, changed);
        audit(c, 'release.published', `generations/${target.generation}`, a.app, a.env, { generation: rel.generation, type: 'rollback', reason: b.reason }, true);
        return rel;
      }
      case 'kill_switch': {
        need(c, 'approver', 'Using the kill switch');
        getRes(c, a.app, 'gates', b.gate ?? '');
        (data.killed[a.env] ??= {})[b.gate!] = b.killed ?? true;
        const rel = newGeneration(c, a.app, a.env, { type: 'kill_switch', reason: b.reason ?? (b.killed === false ? 'Kill switch off' : 'Kill switch on') }, [`gates/${b.gate}`]);
        audit(c, 'release.published', `gates/${b.gate}`, a.app, a.env, { generation: rel.generation, type: 'kill_switch', killed: b.killed ?? true });
        return rel;
      }
    }
    return fail(422, 'INVALID_ARGUMENT', 'Unknown release type');
  },

  // ---------------------------------------------------------------- operations
  getOverview: (a, c) => {
    const data = appData(c, a.app);
    const env = envOf(c, a.app, a.env);
    const scs = data.sidecars[a.env] ?? [];
    const decisions = data.resources.filter((r) => r.kind === 'decisions' && !r.archived && r.live[a.env]);
    const cpm = decisions.reduce((s, r) => s + (r.stats?.callsPerMinute ?? 0), 0) * (a.env === 'prod' ? 1 : a.env === 'stage' ? 0.02 : 0.004);
    let seed = a.env.length * 7 + 3;
    const series = Array.from({ length: 48 }, (_, i) => {
      seed = (seed * 9301 + 49297) % 233280;
      return { at: new Date(c.now - (47 - i) * 30 * 60_000).toISOString(), p99Micros: Math.round(360 + (seed / 233280) * 90 + (i > 30 && i < 35 ? 60 : 0)) };
    });
    series[47].p99Micros = 410;
    const fleet = fleetSummary(scs, env.generation, a.env === 'prod' ? 214 : undefined);
    return {
      generation: env.generation,
      p99Micros: scs.length ? 410 : undefined,
      callsPerMinute: Math.round(cpm),
      fallbackRate: decisions.length ? 0.0001 : 0,
      sidecars: fleet,
      decisions: data.resources.filter((r) => r.kind === 'decisions' && !r.archived).length,
      pendingChangeRequests: data.changeRequests.filter((x) => x.status === 'open' || x.status === 'approved').length,
      latencySeries: scs.length ? series : [],
      recentReleases: (data.releases[a.env] ?? []).slice(0, 5),
    };
  },
  listSidecars: (a, c) => {
    const env = envOf(c, a.app, a.env);
    const scs = [...(appData(c, a.app).sidecars[a.env] ?? [])].sort((x, y) => (x.status === 'in_sync' ? 1 : 0) - (y.status === 'in_sync' ? 1 : 0) || x.id.localeCompare(y.id));
    return { summary: fleetSummary(scs, env.generation, a.env === 'prod' ? 214 : undefined), ...page(scs, a.query?.cursor, a.query?.limit) };
  },
  searchDecisionLogs: (a, c) => {
    envOf(c, a.app, a.env);
    const q = a.query ?? {};
    const items = liveFilter(appData(c, a.app).decisionLogs[a.env] ?? [], q.from, q.to).filter(
      (x) => (!q.decision || x.decision === q.decision) && (!q.version || x.decisionVersion.endsWith(`@${q.version}`)) && (!q.requestId || x.requestId === q.requestId || x.requestId.startsWith(q.requestId)) && (!q.unitId || x.unitId === q.unitId) && (q.fallback === undefined || x.fallbackUsed === q.fallback),
    );
    return page(items, q.cursor, q.limit);
  },
  listUpdateLogs: (a, c) => {
    envOf(c, a.app, a.env);
    const items = (appData(c, a.app).updateLogs[a.env] ?? []).filter((x) => !a.query?.result || x.result === a.query.result);
    return page(items, a.query?.cursor, a.query?.limit);
  },
  listExposureLogs: (a, c) => {
    envOf(c, a.app, a.env);
    const items = (appData(c, a.app).exposureLogs[a.env] ?? []).filter((x) => (!a.query?.experiment || x.experiment === a.query.experiment) && (!a.query?.unitId || x.unitId === a.query.unitId));
    return page(items, a.query?.cursor, a.query?.limit);
  },
  listAudit: (a, c) => {
    const q = a.query ?? {};
    const items = c.db.audit.filter((x) => (!q.app || !x.app || x.app === q.app) && (!q.actor || x.actor.includes(q.actor)));
    return page(items, q.cursor, q.limit);
  },
  listWebhooks: (_a, c) => ({ items: c.db.webhooks }),
  createWebhook: (a, c) => {
    need(c, 'admin', 'Adding a webhook');
    if (!/^https:\/\/[^\s]+$/.test(a.body.url)) fail(422, 'INVALID_ARGUMENT', 'Invalid URL', undefined, [{ path: '/url', message: 'Use an https:// URL.' }]);
    if (!a.body.events?.length) fail(422, 'INVALID_ARGUMENT', 'Pick events', undefined, [{ path: '/events', message: 'Pick at least one event.' }]);
    const w = { id: nextId(c, 'wh'), url: a.body.url, events: a.body.events as never[] };
    c.db.webhooks.push(w);
    audit(c, 'webhook.created', `webhooks/${w.id}`);
    return { ...w, secret: `whsec_${murmur3(w.id + c.now).toString(36)}${murmur3(w.url).toString(36)}` };
  },
  deleteWebhook: (a, c) => {
    need(c, 'admin', 'Removing a webhook');
    c.db.webhooks = c.db.webhooks.filter((w) => w.id !== a.webhookId);
    audit(c, 'webhook.deleted', `webhooks/${a.webhookId}`);
  },

  // ---------------------------------------------------------------- runtime
  decide: (a, c) => {
    const r = getRes(c, a.app, 'decisions', a.body.decision);
    const live = r.live[a.env];
    if (!live) return fail(404, 'NOT_FOUND', 'Not live', `${r.name} is not live in ${a.env}.`);
    const v = versionOf(r, a.body.options?.pinVersion ?? live.version)!;
    const res = new Engine(world(c, a.app, a.env, r.name), { now: a.body.options?.now, unit: a.body.unitId, requestId: a.body.requestId }).run(v.spec as DecisionSpec, a.body.input, { skipAI: true });
    if (res.error) fail(400, res.error.code, res.error.title, res.error.detail, res.error.errors);
    return { ...res, decisionVersion: `${r.name}@${v.version}`, generation: envOf(c, a.app, a.env).generation, requestId: a.body.requestId, path: a.body.options?.trace ? res.path : [] };
  },
  checkGate: (a, c) => {
    const r = getRes(c, a.app, 'gates', a.name);
    const g = new Engine(world(c, a.app, a.env, '(gate)'), { unit: a.body.unitId }).gate(a.name, (a.body.attributes ?? {}) as Record<string, unknown>);
    return { value: g.value, ruleId: g.rule, gateVersion: `${a.name}@${r.live[a.env]?.version ?? 'draft'}`, generation: envOf(c, a.app, a.env).generation };
  },
  assignVariant: (a, c) => {
    const r = getRes(c, a.app, 'experiments', a.name);
    const x = new Engine(world(c, a.app, a.env, '(experiment)'), { unit: a.body.unitId }).assign(a.name, (a.body.attributes ?? {}) as Record<string, unknown>);
    return { inExperiment: x.in_experiment, variant: x.variant, parameters: x.parameters, reason: x.reason as 'assigned', experimentVersion: `${a.name}@${r.live[a.env]?.version ?? 'draft'}`, generation: envOf(c, a.app, a.env).generation };
  },

  // ---------------------------------------------------------------- sidecar (not used by the UI)
  getBundleHead: () => fail(401, 'UNAUTHENTICATED', 'Sidecar endpoints need an app credential'),
  watchBundles: () => fail(401, 'UNAUTHENTICATED', 'Sidecar endpoints need an app credential'),
  getBlob: () => fail(401, 'UNAUTHENTICATED', 'Sidecar endpoints need an app credential'),
  sidecarHeartbeat: () => fail(401, 'UNAUTHENTICATED', 'Sidecar endpoints need an app credential'),
  ingestLogs: () => fail(401, 'UNAUTHENTICATED', 'Sidecar endpoints need an app credential'),

  // ---------------------------------------------------------------- meta
  getExprCatalogue: () => ({
    variables: [
      { name: 'input', type: 'map', description: "The node's input, validated against its input schema" },
      { name: 'request', type: 'map', description: 'The original request input' },
      { name: 'nodes', type: 'map', description: 'Outputs of nodes that already ran: nodes.<id>' },
      { name: 'out', type: 'map', description: 'Transform only: fields assigned so far' },
      { name: 'ctx.now', type: 'time', description: 'Request time (ctx.now.Year(), ctx.now.Hour())' },
      { name: 'ctx.unit', type: 'string', description: 'Unit ID from the request' },
      { name: 'ctx.requestId', type: 'string', description: 'Request ID' },
      { name: 'ctx.env', type: 'string', description: 'Environment' },
    ],
    functions: [...EXPR_FUNCTIONS, ...GEARBOX_FUNCTIONS],
    operators: [
      { symbol: '== != < <= > >=', description: 'Comparison (ordering with nil is false)' },
      { symbol: '&& || !', description: 'Logic (also and, or, not)' },
      { symbol: '+ - * / % ^', description: 'Arithmetic (nil is an error; use ??)' },
      { symbol: 'a ?? b', description: 'b when a is nil' },
      { symbol: 'c ? a : b', description: 'Conditional' },
      { symbol: 'in, not in', description: 'List membership or map key' },
      { symbol: 'contains, startsWith, endsWith, matches', description: 'Text operators (matches uses RE2)' },
      { symbol: 'x | f()', description: 'Pipe: f(x)' },
      { symbol: 'let x = …; expr', description: 'Name a value' },
    ],
    conditionOperators: [
      ...OPERATORS.map((o) => ({ op: o.op, description: o.label, valueKind: o.valueKind })),
      { op: 'IN_SEGMENT', description: 'member of a segment', valueKind: 'segment' as const },
      { op: 'NOT_IN_SEGMENT', description: 'not a member of a segment', valueKind: 'segment' as const },
      { op: 'GATE_PASSES', description: 'gate passes for this unit', valueKind: 'gate' as const },
      { op: 'GATE_FAILS', description: 'gate fails for this unit', valueKind: 'gate' as const },
    ],
  }),
  getServerVersion: () => ({ server: '1.0.0 (sample data)', engine: '1.0.0', minSidecar: '1.0.0-rc.2' }),
};

function fleetSummary(scs: Sidecar[], gen: number, claimedTotal?: number) {
  const total = claimedTotal ?? scs.length;
  const behind = scs.filter((s) => s.generation !== gen).length;
  const generations: Record<string, number> = {};
  const versions: Record<string, number> = {};
  for (const s of scs) {
    if (s.generation !== gen) generations[String(s.generation)] = (generations[String(s.generation)] ?? 0) + 1;
    if (s.sidecarVersion && s.status !== 'in_sync') versions[s.sidecarVersion] = (versions[s.sidecarVersion] ?? 0) + 1;
  }
  generations[String(gen)] = total - behind;
  const rcCount = scs.filter((s) => s.sidecarVersion === '1.0.0-rc.3').length;
  if (scs.length) versions['1.0.0-rc.3'] = total - behind - (scs.length - behind - rcCount);
  return { total, onLatest: total - behind, generations, versions, medianSyncLagSeconds: scs.length ? 1.8 : undefined };
}

export { HOUR };
export type { ValidationProblem };
