<script lang="ts">
  // Top-level shortcuts (/changes, /fleet, /logs, /audit, /settings) open that section of the last app.
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import { DEFAULT_ENV, href, lastApp } from '$lib/nav.svelte';
  import ErrorNote from './ErrorNote.svelte';
  import Loading from './Loading.svelte';

  let { path }: { path: string } = $props();
  let error: unknown = $state(null);

  async function go(): Promise<void> {
    try {
      let app = lastApp();
      if (!app) app = (await api.listApps({ query: { limit: 1 } })).items[0]?.name ?? null;
      if (!app) return void goto(href('/apps'), { replaceState: true });
      const q = new URLSearchParams(page.url.search);
      if (!q.has('env')) q.set('env', DEFAULT_ENV);
      const rest = page.url.pathname.replace(/^.*?\/(changes|fleet|logs|audit|settings)/, '');
      void goto(href(`/apps/${encodeURIComponent(app)}${path}${rest}?${q.toString()}`), { replaceState: true });
    } catch (e) {
      error = e;
    }
  }
  $effect(() => {
    void go();
  });
</script>

<main class="layout" style="grid-template-columns:minmax(0,1fr)">
  {#if error}<ErrorNote {error} />{:else}<Loading label="Opening…" />{/if}
</main>
