import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, buildUrl, configureApi, isMock, request, resetApi, type ApiRequest, type Transport } from './client';

function res(status: number, body: unknown, ct = 'application/json'): Response {
  const text = body === undefined ? '' : typeof body === 'string' ? body : JSON.stringify(body);
  return {
    status,
    ok: status >= 200 && status < 300,
    headers: { get: (h: string) => (h.toLowerCase() === 'content-type' ? ct : h.toLowerCase() === 'content-length' ? String(text.length) : null) },
    json: async () => JSON.parse(text),
    text: async () => text,
  } as unknown as Response;
}

const req = (over: Partial<ApiRequest> = {}): ApiRequest => ({ op: 'listApps', method: 'GET', path: '/apps', params: {}, ...over });

describe('buildUrl', () => {
  it('fills params and skips empty query values', () => {
    expect(buildUrl('/api/v1/', '/apps/{app}/{kind}', { app: 'lead service', kind: 'schemas' }, { q: 'x', cursor: undefined, limit: 50, fallback: false, empty: '' })).toBe(
      '/api/v1/apps/lead%20service/schemas?q=x&limit=50&fallback=false',
    );
    expect(() => buildUrl('', '/apps/{app}', {})).toThrow('missing path parameter');
  });
});

describe('request', () => {
  beforeEach(() => resetApi());

  it('sends credentials and parses JSON', async () => {
    const f = vi.fn(async () => res(200, { items: [] }));
    configureApi({ baseUrl: '/api/v1', fetch: f as unknown as typeof fetch });
    await expect(request(req())).resolves.toEqual({ items: [] });
    const [url, init] = f.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/apps');
    expect(init.credentials).toBe('include');
  });

  it('fetches the CSRF token before the first mutating request', async () => {
    const calls: [string, RequestInit][] = [];
    const f = vi.fn(async (url: string, init: RequestInit) => {
      calls.push([url, init]);
      if (url.endsWith('/session')) return res(200, { member: {}, csrfToken: 'tok' });
      return res(201, { name: 'x' });
    });
    configureApi({ fetch: f as unknown as typeof fetch });
    await request(req({ op: 'createApp', method: 'POST', body: { name: 'x' } }));
    expect(calls[0][0]).toBe('/api/v1/session');
    const h = calls[1][1].headers as Record<string, string>;
    expect(h['x-csrf-token']).toBe('tok');
    expect(h['content-type']).toBe('application/json');
    expect(calls[1][1].body).toBe('{"name":"x"}');
  });

  it('turns problem+json into ApiError with field errors', async () => {
    const problem = { title: 'Rule node needs schemas', status: 422, code: 'SCHEMA_REQUIRED', detail: 'd', errors: [{ path: '/nodes/1/inputSchema', message: 'inputSchema is required' }] };
    configureApi({ fetch: (async () => res(422, problem, 'application/problem+json')) as unknown as typeof fetch });
    try {
      await request(req());
      throw new Error('no throw');
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError);
      const a = e as ApiError;
      expect(a.code).toBe('SCHEMA_REQUIRED');
      expect(a.status).toBe(422);
      expect(a.fieldError('/nodes/1/inputSchema')).toBe('inputSchema is required');
      expect(a.errorsUnder('/nodes/1')).toHaveLength(1);
      expect(a.errorsUnder('/nodes/10')).toHaveLength(0);
    }
  });

  it('calls onUnauthorized on 401', async () => {
    const onUnauthorized = vi.fn(() => undefined);
    configureApi({ fetch: (async () => res(401, { title: 'Sign in', status: 401, code: 'UNAUTHENTICATED' }, 'application/problem+json')) as unknown as typeof fetch, onUnauthorized });
    await expect(request(req())).rejects.toThrow('Sign in');
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('returns undefined for 204', async () => {
    configureApi({ fetch: (async () => res(204, undefined, '')) as unknown as typeof fetch });
    await expect(request(req({ op: 'removeMember', method: 'DELETE', path: '/members/{memberId}', params: { memberId: 'm' } }))).resolves.toEqual(undefined);
  });

  it('switches to the mock when the API is unreachable and auto mode is on', async () => {
    const mock: Transport = async () => ({ status: 200, body: { items: ['mock'] } });
    configureApi({ mockAuto: true, loadMock: async () => mock, fetch: (async () => res(200, '<!doctype html>', 'text/html')) as unknown as typeof fetch });
    await expect(request(req())).resolves.toEqual({ items: ['mock'] });
    expect(isMock()).toBe(true);
  });

  it('reports network failures as ApiError NETWORK without auto mock', async () => {
    configureApi({ fetch: (async () => { throw new TypeError('Failed to fetch'); }) as unknown as typeof fetch });
    await expect(request(req())).rejects.toMatchObject({ code: 'NETWORK', status: 0 });
  });

  it('uses the mock transport when mock mode is forced', async () => {
    const mock: Transport = async (r) => ({ status: 404, body: { title: 'Not found', status: 404, code: 'NOT_FOUND', detail: r.op } });
    configureApi({ mock: true, loadMock: async () => mock });
    await expect(request(req())).rejects.toMatchObject({ code: 'NOT_FOUND', detail: 'listApps' });
  });
});
