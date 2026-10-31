// Fetch wrapper used by every generated operation (operations.gen.ts).
//
// - Base URL from PUBLIC_GEARBOX_API (default /api/v1), cookies included.
// - Mutating requests carry X-CSRF-Token from GET /session.
// - application/problem+json errors become a typed ApiError (code + field errors).
// - 401 calls onUnauthorized (the app redirects to /auth/login?returnTo=…).
// - Mock mode: requests go to an in-browser implementation of the API instead of the network.
//   It is on when configured, and switches on by itself in dev when the API is unreachable.

import type { OperationId } from './operations.gen';
import type { FieldError, Problem } from './types.gen';

export interface CallOptions {
  signal?: AbortSignal;
  headers?: Record<string, string>;
}

export interface ApiRequest extends CallOptions {
  op: OperationId;
  method: string;
  /** Path template from the contract, e.g. `/apps/{app}/{kind}`. */
  path: string;
  params: Record<string, string | number>;
  query?: object;
  body?: unknown;
  contentType?: string;
}

export interface TransportResponse {
  status: number;
  body: unknown;
  contentType?: string;
}

/** A transport answers a request without the network (used by the mock). */
export type Transport = (req: ApiRequest) => Promise<TransportResponse>;

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly title: string;
  readonly detail?: string;
  readonly errors: FieldError[];
  readonly problem: Problem | null;
  readonly op?: string;

  constructor(status: number, problem: Partial<Problem> | null, op?: string) {
    const title = problem?.title ?? `Request failed (${status})`;
    super(problem?.detail ? `${title}: ${problem.detail}` : title);
    this.name = 'ApiError';
    this.status = status;
    this.code = problem?.code ?? (status === 0 ? 'NETWORK' : `HTTP_${status}`);
    this.title = title;
    this.detail = problem?.detail;
    this.errors = Array.isArray(problem?.errors) ? problem!.errors! : [];
    this.problem = problem && problem.code ? (problem as Problem) : null;
    this.op = op;
  }

  /** Message for one JSON pointer (exact match). */
  fieldError(path: string): string | undefined {
    return this.errors.find((e) => e.path === path)?.message;
  }

  /** Field errors at or below a JSON pointer prefix. */
  errorsUnder(prefix: string): FieldError[] {
    const p = prefix.endsWith('/') ? prefix : prefix + '/';
    return this.errors.filter((e) => e.path === prefix || e.path.startsWith(p));
  }

  static is(e: unknown): e is ApiError {
    return e instanceof ApiError;
  }
}

/** Short human text for any thrown value. */
export function errorText(e: unknown): string {
  if (e instanceof ApiError) return e.detail ? `${e.title}. ${e.detail}` : e.title;
  if (e instanceof Error) return e.message;
  return String(e);
}

export interface ApiConfig {
  baseUrl: string;
  /** Force mock mode. */
  mock: boolean;
  /** Switch to mock mode when the API is unreachable (dev only). */
  mockAuto: boolean;
  onUnauthorized: (returnTo: string) => void;
  fetch: typeof fetch;
  loadMock: () => Promise<Transport>;
}

const defaultConfig: ApiConfig = {
  baseUrl: '/api/v1',
  mock: false,
  mockAuto: false,
  onUnauthorized: () => {},
  fetch: (...a) => globalThis.fetch(...a),
  loadMock: () => import('./mock/index').then((m) => m.mockTransport),
};

let config: ApiConfig = { ...defaultConfig };
let csrfToken: string | null = null;
let mockOn = false;
let mockTransport: Transport | null = null;
const mockListeners = new Set<(on: boolean) => void>();

export function configureApi(partial: Partial<ApiConfig>): void {
  config = { ...config, ...partial };
  if (partial.mock !== undefined) setMock(partial.mock);
}

/** Reset to defaults (tests). */
export function resetApi(): void {
  config = { ...defaultConfig };
  csrfToken = null;
  mockTransport = null;
  setMock(false);
}

export function apiBaseUrl(): string {
  return config.baseUrl;
}

export function isMock(): boolean {
  return mockOn;
}

function setMock(on: boolean): void {
  if (mockOn === on) return;
  mockOn = on;
  for (const l of mockListeners) l(on);
}

export function onMockChange(listener: (on: boolean) => void): () => void {
  mockListeners.add(listener);
  return () => mockListeners.delete(listener);
}

export function setCsrfToken(token: string | null): void {
  csrfToken = token;
}

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

/** Fill `{name}` placeholders and append the query string. */
export function buildUrl(base: string, path: string, params: Record<string, string | number>, query?: object): string {
  const filled = path.replace(/\{([^}]+)\}/g, (_, k: string) => {
    const v = params[k];
    if (v === undefined || v === null || v === '') throw new Error(`missing path parameter "${k}" for ${path}`);
    return encodeURIComponent(String(v));
  });
  const qs = new URLSearchParams();
  if (query) {
    for (const [k, v] of Object.entries(query as Record<string, unknown>)) {
      if (v === undefined || v === null || v === '') continue;
      if (Array.isArray(v)) v.forEach((x) => qs.append(k, String(x)));
      else qs.append(k, String(v));
    }
  }
  const s = qs.toString();
  return base.replace(/\/$/, '') + filled + (s ? `?${s}` : '');
}

function isJson(ct: string | null | undefined): boolean {
  return !!ct && /[/+]json\b/i.test(ct);
}

function currentLocation(): string {
  const loc = (globalThis as { location?: Location }).location;
  return loc ? loc.pathname + loc.search + loc.hash : '/';
}

async function viaMock(req: ApiRequest): Promise<TransportResponse> {
  if (!mockTransport) mockTransport = await config.loadMock();
  return mockTransport(req);
}

async function ensureCsrf(req: ApiRequest): Promise<void> {
  if (csrfToken || !MUTATING.has(req.method) || mockOn) return;
  try {
    const res = await config.fetch(buildUrl(config.baseUrl, '/session', {}), {
      credentials: 'include',
      headers: { accept: 'application/json' },
    });
    if (res.ok && isJson(res.headers.get('content-type'))) {
      const s = (await res.json()) as { csrfToken?: string };
      if (s.csrfToken) csrfToken = s.csrfToken;
    }
  } catch {
    /* the request itself will report the problem */
  }
}

async function viaNetwork(req: ApiRequest): Promise<TransportResponse> {
  await ensureCsrf(req);
  const headers: Record<string, string> = { accept: 'application/json, application/problem+json', ...(req.headers ?? {}) };
  let body: BodyInit | undefined;
  if (req.body !== undefined) {
    const ct = req.contentType ?? 'application/json';
    headers['content-type'] = ct;
    body = ct === 'application/json' ? JSON.stringify(req.body) : String(req.body);
  }
  if (MUTATING.has(req.method) && csrfToken) headers['x-csrf-token'] = csrfToken;
  const res = await config.fetch(buildUrl(config.baseUrl, req.path, req.params, req.query), {
    method: req.method,
    credentials: 'include',
    headers,
    body,
    signal: req.signal,
  });
  const ct = res.headers.get('content-type');
  if (res.status === 204 || res.status === 202 || res.headers.get('content-length') === '0') {
    return { status: res.status, body: undefined, contentType: ct ?? undefined };
  }
  if (isJson(ct)) return { status: res.status, body: await res.json(), contentType: ct ?? undefined };
  return { status: res.status, body: await res.text(), contentType: ct ?? undefined };
}

/** True when a response shows that no Gearbox API answered (dev server HTML, gateway errors). */
function looksUnreachable(r: TransportResponse): boolean {
  if ([502, 503, 504].includes(r.status) && !isJson(r.contentType)) return true;
  return !!r.contentType && /text\/html/i.test(r.contentType);
}

export async function request<R>(req: ApiRequest): Promise<R> {
  let res: TransportResponse;
  if (mockOn) {
    res = await viaMock(req);
  } else {
    try {
      res = await viaNetwork(req);
      if (config.mockAuto && looksUnreachable(res)) {
        setMock(true);
        res = await viaMock(req);
      }
    } catch (e) {
      if ((e as Error)?.name === 'AbortError') throw e;
      if (config.mockAuto) {
        setMock(true);
        res = await viaMock(req);
      } else {
        throw new ApiError(0, { title: 'Cannot reach the Gearbox API', detail: `${config.baseUrl}: ${(e as Error)?.message ?? e}`, code: 'NETWORK' }, req.op);
      }
    }
  }
  if (res.status >= 200 && res.status < 300) {
    if (req.op === 'getSession' && res.body && typeof res.body === 'object' && 'csrfToken' in res.body) {
      csrfToken = String((res.body as { csrfToken: unknown }).csrfToken);
    }
    return res.body as R;
  }
  const problem = res.body && typeof res.body === 'object' ? (res.body as Partial<Problem>) : { title: typeof res.body === 'string' && res.body ? res.body.slice(0, 200) : undefined };
  const err = new ApiError(res.status, { ...problem, status: res.status }, req.op);
  if (res.status === 401) config.onUnauthorized(currentLocation());
  if (res.status === 403 && err.code === 'CSRF') csrfToken = null;
  throw err;
}
