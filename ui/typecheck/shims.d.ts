// Ambient declarations so `tsc -p tsconfig.check.json` can check plain .ts and .svelte.ts files
// without node_modules. On a machine with dependencies installed, svelte-check uses the real types.

declare function $state<T>(initial: T): T;
declare function $state<T>(): T | undefined;
declare namespace $state {
  function raw<T>(initial: T): T;
  function snapshot<T>(value: T): T;
}
declare function $derived<T>(expression: T): T;
declare namespace $derived {
  function by<T>(fn: () => T): T;
}
declare function $effect(fn: () => void | (() => void)): void;
declare namespace $effect {
  function pre(fn: () => void | (() => void)): void;
  function root(fn: () => void | (() => void)): () => void;
  function tracking(): boolean;
}

declare const __GEARBOX_API__: string;
declare const __GEARBOX_MOCK__: string;

declare module 'vitest' {
  type Fn = (...args: any[]) => any;
  export const describe: (name: string, fn: () => void) => void;
  export const it: (name: string, fn: () => void | Promise<void>) => void;
  export const test: typeof it;
  export const beforeEach: (fn: () => void | Promise<void>) => void;
  export const afterEach: (fn: () => void | Promise<void>) => void;
  export const expect: any;
  export const vi: { fn: <T extends Fn = Fn>(impl?: T) => T & { mock: { calls: any[][] } } };
}

declare module 'vitest/config' {
  export function defineConfig<T>(c: T): T;
}
declare module 'vite' {
  export function loadEnv(mode: string, dir: string, prefixes: string | string[]): Record<string, string>;
}
declare module '@sveltejs/kit/vite' {
  export function sveltekit(): unknown;
}
declare const process: { env: Record<string, string | undefined>; cwd(): string };

declare module '$app/state' {
  export const page: {
    url: URL;
    params: Record<string, string>;
    route: { id: string | null };
    status: number;
    error: { message: string } | null;
  };
  export const navigating: { to: { url: URL } | null } | null;
}
declare module '$app/navigation' {
  export function goto(url: string | URL, opts?: { replaceState?: boolean; noScroll?: boolean; keepFocus?: boolean; invalidateAll?: boolean }): Promise<void>;
  export function invalidateAll(): Promise<void>;
  export function beforeNavigate(fn: (nav: { cancel(): void; to: { url: URL } | null }) => void): void;
}
declare module '$app/paths' {
  export const base: string;
}
declare module '@sveltejs/kit' {
  export function redirect(status: number, location: string): never;
  export function error(status: number, message: string): never;
}

interface ImportMetaEnv {
  readonly DEV: boolean;
  readonly PROD: boolean;
  readonly MODE: string;
}
interface ImportMeta {
  readonly env: ImportMetaEnv;
}
