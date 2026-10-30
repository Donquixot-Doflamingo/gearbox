// See https://svelte.dev/docs/kit/types#app.d.ts
declare global {
  namespace App {
    // interface Error {}
    // interface Locals {}
    // interface PageData {}
    // interface PageState {}
    // interface Platform {}
  }
  /** PUBLIC_GEARBOX_API at build time (vite.config.ts `define`). */
  const __GEARBOX_API__: string;
  /** PUBLIC_GEARBOX_MOCK at build time. */
  const __GEARBOX_MOCK__: string;
}

export {};
