// App and environment live in the URL: /apps/<app>/…?env=prod. These helpers read and build such URLs.
import { goto } from '$app/navigation';
import { base } from '$app/paths';
import { page } from '$app/state';

const LAST_APP = 'gearbox-last-app';
export const DEFAULT_ENV = 'prod';

export function rememberApp(app: string): void {
  try {
    globalThis.localStorage?.setItem(LAST_APP, app);
  } catch {
    /* ignore */
  }
}

export function lastApp(): string | null {
  try {
    return globalThis.localStorage?.getItem(LAST_APP) ?? null;
  } catch {
    return null;
  }
}

export function currentApp(): string {
  return page.params.app ?? lastApp() ?? '';
}

export function currentEnv(): string {
  return page.url.searchParams.get('env') || DEFAULT_ENV;
}

type Q = Record<string, string | number | boolean | undefined | null>;

/** Link inside the current app, keeping the environment: appHref('/decisions/lead_routing'). */
export function appHref(path = '', query: Q = {}, opts: { app?: string; env?: string } = {}): string {
  const app = opts.app ?? currentApp();
  const qs = new URLSearchParams();
  qs.set('env', opts.env ?? currentEnv());
  for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== null && v !== '' && v !== false) qs.set(k, String(v));
  return `${base}/apps/${encodeURIComponent(app)}${path}?${qs.toString()}`;
}

/** Link outside app scope (keeps nothing). */
export function href(path: string): string {
  return `${base}${path}`;
}

export function setEnv(env: string): void {
  const u = new URL(page.url);
  u.searchParams.set('env', env);
  void goto(u.pathname + u.search + u.hash, { keepFocus: true, noScroll: true });
}

/** Replace query parameters on the current URL (filters, selected tab). */
export function setQuery(query: Q, opts: { replace?: boolean } = {}): void {
  const u = new URL(page.url);
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === '' || v === false) u.searchParams.delete(k);
    else u.searchParams.set(k, String(v));
  }
  void goto(u.pathname + u.search + u.hash, { keepFocus: true, noScroll: true, replaceState: opts.replace ?? true });
}

export function qp(name: string): string | null {
  return page.url.searchParams.get(name);
}
