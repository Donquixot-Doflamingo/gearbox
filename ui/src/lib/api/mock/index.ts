// Mock transport: answers every API operation in the browser from the fixture database.
// State lives in memory and in sessionStorage (so a reload keeps your edits); "Reset sample data" clears it.

import type { ApiRequest, Transport, TransportResponse } from '../client';
import type { OperationId } from '../operations.gen';
import { createDb, FIXTURE_VERSION, type Db } from './fixtures';
import { handlers, MockError, type Ctx } from './handlers';

const KEY = 'gearbox-mock-db';
let db: Db | null = null;

const CREATED = new Set<OperationId>(['createApp', 'createCredential', 'addMember', 'createToken', 'createResource', 'cutVersion', 'createChangeRequest', 'publishRelease', 'createWebhook']);

function storage(): Storage | null {
  try {
    return (globalThis as { sessionStorage?: Storage }).sessionStorage ?? null;
  } catch {
    return null;
  }
}

function load(): Db {
  if (db) return db;
  try {
    const raw = storage()?.getItem(KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Db;
      if (parsed.fixtureVersion === FIXTURE_VERSION) return (db = parsed);
    }
  } catch {
    /* fall back to fresh fixtures */
  }
  return (db = createDb());
}

let saveTimer: ReturnType<typeof setTimeout> | null = null;
function save(): void {
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    try {
      storage()?.setItem(KEY, JSON.stringify(db));
    } catch {
      /* quota or private mode: keep in memory */
    }
  }, 150);
}

/** Throw away edits and start from the sample data again. */
export function resetMockDb(): void {
  db = createDb();
  try {
    storage()?.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

/** Mock only: act as another member (to try roles and approvals). */
export function setMockUser(memberId: string): void {
  const d = load();
  if (d.members.some((m) => m.id === memberId)) {
    d.meId = memberId;
    save();
  }
}

export function mockMembers(): { id: string; name?: string; email: string; role: string }[] {
  return load().members.map(({ id, name, email, role }) => ({ id, name, email, role }));
}

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function handle(req: ApiRequest, now = Date.now()): TransportResponse {
  const d = load();
  const ctx: Ctx = { db: d, now, changed: save };
  const h = handlers[req.op] as ((args: unknown, c: Ctx) => unknown) | undefined;
  if (!h) return { status: 501, body: { title: 'Not implemented in mock mode', status: 501, code: 'NOT_IMPLEMENTED', detail: req.op }, contentType: 'application/problem+json' };
  const args = { ...req.params, query: req.query, body: req.body === undefined ? undefined : JSON.parse(JSON.stringify(req.body)) };
  try {
    const out = h(args, ctx);
    if (req.method !== 'GET') save();
    if (out === undefined) return { status: req.op === 'ingestLogs' ? 202 : 204, body: undefined };
    return { status: CREATED.has(req.op) ? 201 : 200, body: JSON.parse(JSON.stringify(out)), contentType: 'application/json' };
  } catch (e) {
    if (e instanceof MockError) {
      return { status: e.status, body: { type: 'about:blank', title: e.title, status: e.status, code: e.code, detail: e.detail, errors: e.errors }, contentType: 'application/problem+json' };
    }
    const msg = e instanceof Error ? e.message : String(e);
    return { status: 500, body: { title: 'Mock error', status: 500, code: 'INTERNAL', detail: msg }, contentType: 'application/problem+json' };
  }
}

export const mockTransport: Transport = async (req) => {
  await delay(req.method === 'GET' ? 60 + Math.random() * 120 : 140 + Math.random() * 160);
  return handle(req);
};
