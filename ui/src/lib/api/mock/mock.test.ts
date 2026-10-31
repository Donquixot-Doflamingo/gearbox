import { beforeEach, describe, expect, it } from 'vitest';
import type { ApiRequest } from '../client';
import { operationMeta, type OperationId } from '../operations.gen';
import { handle, resetMockDb, setMockUser } from './index';

function call(op: OperationId, params: Record<string, string | number> = {}, body?: unknown, query?: object) {
  const m = operationMeta[op];
  const req: ApiRequest = { op, method: m.method, path: m.path, params, body, query };
  return handle(req);
}
const app = 'lead_service';

describe('mock API', () => {
  beforeEach(() => resetMockDb());

  it('serves session, apps and resource lists', () => {
    expect((call('getSession').body as { member: { role: string } }).member.role).toBe('admin');
    expect((call('listApps').body as { items: unknown[] }).items).toHaveLength(2);
    const schemas = call('listResources', { app, kind: 'schemas' }).body as { items: { name: string }[] };
    expect(schemas.items.map((s) => s.name)).toContain('lead_input');
    const decisions = call('listResources', { app, kind: 'decisions' }).body as { items: { name: string; subtype: string }[] };
    expect(decisions.items.find((d) => d.name === 'lead_routing')!.subtype).toBe('graph');
  });

  it('evaluates lead_routing and runs its tests', () => {
    const r = call('evaluateDecision', { app, name: 'lead_routing' }, {
      input: { lead: { id: 'L-1', city: 'Gurgaon', source: 'app' }, car: { make: 'Maruti', model: 'Swift', year: 2019, km: 48200, price: 525000 }, inspections: [{ part: 'engine', score: 8 }] },
      ctx: { now: '2026-10-30T10:00:00Z', unit: 'L-1' },
    });
    expect(r.status).toBe(200);
    const body = r.body as { output: { route: string }; path: { nodeId: string; outcome: string }[]; fallbackUsed: boolean };
    expect(body.fallbackUsed).toBe(false);
    expect(body.output.route).toBe('fast_track');
    expect(body.path.map((p) => `${p.nodeId}:${p.outcome}`)).toEqual(['normalize:ok', 'eligibility:r4', 'route:eligible', 'price_band:row_2', 'priority:error', 'default_priority:r1', 'output:ok']);
    const t = call('runDecisionTests', { app, name: 'lead_routing' }, {}).body as { passed: number; failed: number; results: { name: string; ok: boolean; error?: string }[] };
    expect(t.results.filter((x) => !x.ok).map((x) => x.name)).toEqual(['band A upper edge · ₹8,50,000']);
  });

  it('validates the draft without errors except known warnings', () => {
    const v = call('validateResource', { app, kind: 'decisions', name: 'lead_routing' }, {}).body as { valid: boolean; problems: { severity: string; message: string }[] };
    expect(v.problems.filter((p) => p.severity === 'error')).toEqual([]);
    expect(v.valid).toBe(true);
    for (const n of ['callback_eligibility', 'inspection_pricing_band', 'lead_payload_normalize', 'dealer_assignment']) {
      const x = call('validateResource', { app, kind: 'decisions', name: n }, {}).body as { problems: { severity: string; path: string; message: string }[] };
      expect(x.problems.filter((p) => p.severity === 'error')).toEqual([]);
    }
    for (const kind of ['segments', 'gates', 'experiments', 'layers', 'lookups', 'schemas'] as const) {
      const list = call('listResources', { app, kind }).body as { items: { name: string }[] };
      for (const it of list.items) {
        const x = call('validateResource', { app, kind, name: it.name }, {}).body as { problems: { severity: string; path: string; message: string }[] };
        expect(x.problems.filter((p) => p.severity === 'error').map((p) => `${kind}/${it.name}${p.path}: ${p.message}`)).toEqual([]);
      }
    }
  });

  it('reports a type mismatch on a string compared with an integer field', () => {
    const spec = (call('getResource', { app, kind: 'decisions', name: 'lead_routing' }).body as { draft: { spec: { nodes: { rules?: { when: unknown }[] }[] } } }).draft.spec;
    spec.nodes[1].rules![2].when = { all: [{ field: 'km', op: 'GT', value: '60000' }] };
    const v = call('validateResource', { app, kind: 'decisions', name: 'lead_routing' }, { spec }).body as { problems: { code: string; path: string }[] };
    expect(v.problems.find((p) => p.code === 'TYPE_MISMATCH')!.path).toBe('/nodes/1/rules/2/when/all/0');
  });

  it('enforces SCHEMA_REQUIRED and revisions', () => {
    const r = call('createResource', { app, kind: 'decisions' }, { name: 'no_schema', spec: { entry: 'r', nodes: [{ id: 'r', type: 'rules', rules: [] }] } });
    expect(r.status).toBe(422);
    expect((r.body as { code: string; errors: { path: string }[] }).code).toBe('SCHEMA_REQUIRED');
    expect((r.body as { errors: { path: string }[] }).errors[0].path).toBe('/spec/nodes/0/inputSchema');
    const ok = call('createResource', { app, kind: 'decisions' }, { name: 'with_schema', spec: { entry: 'r', nodes: [{ id: 'r', type: 'rules', inputSchema: { name: 'callback_input', version: 1 }, outputSchema: { name: 'callback_output', version: 1 }, rules: [] }] } });
    expect(ok.status).toBe(201);
    const stale = call('saveDraft', { app, kind: 'decisions', name: 'with_schema' }, { revision: 0, spec: { entry: 'r', nodes: [] } });
    expect(stale.status).toBe(409);
    expect((stale.body as { code: string }).code).toBe('REVISION_CONFLICT');
  });

  it('refuses to cut a version with failing tests, and cuts after a fix', () => {
    const res = call('getResource', { app, kind: 'decisions', name: 'lead_routing' }).body as { draft: { revision: number; spec: { nodes: { id: string; rows?: { when: Record<string, string> }[] }[] } } };
    const cut = call('cutVersion', { app, kind: 'decisions', name: 'lead_routing' }, { revision: res.draft.revision });
    expect((cut.body as { code: string }).code).toBe('TESTS_FAILED');
    const spec = res.draft.spec;
    spec.nodes.find((n) => n.id === 'price_band')!.rows![1].when.price = '[300000..850000]';
    const saved = call('saveDraft', { app, kind: 'decisions', name: 'lead_routing' }, { revision: res.draft.revision, spec }).body as { draft: { revision: number } };
    const ok = call('cutVersion', { app, kind: 'decisions', name: 'lead_routing' }, { revision: saved.draft.revision, message: 'band A to 8.5 lakh' });
    expect(ok.status).toBe(201);
    expect((ok.body as { version: number }).version).toBe(19);
  });

  it('runs the change request flow with self-approval rules and rollouts', () => {
    setMockUser('mem_01'); // asha, author of cr_214
    expect((call('approveChangeRequest', { app, id: 'cr_214' }, {}).body as { code: string }).code).toBe('FORBIDDEN');
    setMockUser('mem_03');
    const ap = call('approveChangeRequest', { app, id: 'cr_214' }, {}).body as { status: string };
    expect(ap.status).toBe('approved');
    const rel = call('publishRelease', { app, env: 'prod' }, { type: 'change_request', changeRequestId: 'cr_214' });
    expect(rel.status).toBe(201);
    expect((rel.body as { generation: number }).generation).toBe(43);
    const res = call('getResource', { app, kind: 'decisions', name: 'lead_routing' }).body as { live: Record<string, { version: number; rollout?: { fromVersion: number; percent: number } }> };
    expect(res.live.prod).toEqual({ version: 18, rollout: { fromVersion: 17, percent: 5 } });
    call('publishRelease', { app, env: 'prod' }, { type: 'rollout', decision: 'lead_routing', percent: 100 });
    const after = call('getResource', { app, kind: 'decisions', name: 'lead_routing' }).body as { live: Record<string, unknown> };
    expect(after.live.prod).toEqual({ version: 18 });
    const rb = call('publishRelease', { app, env: 'prod' }, { type: 'rollback', toGeneration: 42, reason: 'test' }).body as { breakGlass: boolean };
    expect(rb.breakGlass).toBe(true);
  });

  it('checks segment membership for every segment type', () => {
    expect((call('checkSegmentMembership', { app, name: 'blocked_cities' }, { key: 'Leh' }).body as { member: boolean }).member).toBe(true);
    expect((call('checkSegmentMembership', { app, name: 'metro_premium' }, { attributes: { city: 'Gurgaon', price: 950000 } }).body as { member: boolean }).member).toBe(true);
    expect((call('checkSegmentMembership', { app, name: 'metro_premium' }, { attributes: { city: 'Pune', price: 950000 } }).body as { member: boolean }).member).toBe(false);
    expect((call('checkSegmentMembership', { app, name: 'beta_10pct' }, { unit: 'L-1' }).body as { reason: string }).reason).toMatch('bucket');
    expect(call('checkSegmentMembership', { app, name: 'metro_premium' }, { attributes: { city: 'Pune' } }).status).toBe(422);
  });

  it('checks schema compatibility and documents', () => {
    const c = call('checkSchemaCompatibility', { app, name: 'eligibility_input' }, { spec: { jsonSchema: { type: 'object', properties: { city: { type: 'string' } } } } }).body as { compatible: boolean; changes: { kind: string }[] };
    expect(c.compatible).toBe(false);
    expect(c.changes.map((x) => x.kind)).toContain('property_removed');
    const d = call('validateDocument', { app, name: 'lead_input', version: 1 }, { document: { lead: { id: 'L-1', city: 'Pune' }, car: { make: 'Maruti', year: 2019 } } }).body as { valid: boolean; errors: { path: string }[] };
    expect(d.errors.map((e) => e.path)).toEqual(['/car/km', '/car/price']);
  });

  it('covers every operation in the contract', () => {
    for (const op of Object.keys(operationMeta) as OperationId[]) {
      const r = call(op, { app, env: 'prod', kind: 'decisions', name: 'lead_routing', version: 18, id: 'cr_214', generation: 42, nodeId: 'priority', memberId: 'mem_05', credentialId: 'x', webhookId: 'x', sha256: 'a'.repeat(64) }, op === 'createToken' ? { label: 'x' } : {});
      expect(r.status === 501 ? op : 'ok').toBe('ok');
      expect(r.status >= 500 ? `${op}: ${JSON.stringify(r.body)}` : 'ok').toBe('ok');
    }
  });
});
