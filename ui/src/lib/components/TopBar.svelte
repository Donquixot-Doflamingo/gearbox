<script lang="ts">
  import { goto } from '$app/navigation';
  import type { App } from '$lib/api/types.gen';
  import { appHref, currentApp, currentEnv, href, setEnv } from '$lib/nav.svelte';
  import { mode } from '$lib/stores/mode.svelte';
  import SessionMenu from './SessionMenu.svelte';

  let { apps, envs }: { apps: App[]; envs: string[] } = $props();

  let search = $state('');
  let app = $derived(currentApp());
  let env = $derived(currentEnv());

  function pickApp(name: string): void {
    if (name === '__all') void goto(href('/apps'));
    else void goto(appHref('', {}, { app: name }));
  }

  function onsearch(e: KeyboardEvent): void {
    if (e.key !== 'Enter') return;
    const v = search.trim();
    if (!v) return;
    if (/^req_/.test(v)) void goto(appHref(`/logs/${encodeURIComponent(v)}`));
    else if (/^cr_/.test(v)) void goto(appHref(`/changes/${encodeURIComponent(v)}`));
    else void goto(appHref('/decisions', { q: v }));
    search = '';
  }

  function onEnvKey(e: KeyboardEvent): void {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
    e.preventDefault();
    const i = envs.indexOf(env);
    const next = envs[(i + (e.key === 'ArrowRight' ? 1 : envs.length - 1)) % envs.length];
    setEnv(next);
    queueMicrotask(() => document.querySelector<HTMLElement>(`[data-env="${next}"]`)?.focus());
  }

  const THEME_LABEL = { system: 'Theme: system', light: 'Theme: light', dark: 'Theme: dark' } as const;
</script>

<header class="topbar">
  <a class="brand" href={appHref('')} aria-label="Gearbox overview">
    <svg width="22" height="22" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="4" stroke-dasharray="3.1 3.18" /><circle cx="12" cy="12" r="6.2" fill="none" stroke="currentColor" stroke-width="2.2" /><circle cx="12" cy="12" r="2" fill="currentColor" /></svg>
    <span>Gearbox</span>
  </a>
  <label for="app-select" class="sr">App</label>
  <select id="app-select" value={app} onchange={(e) => pickApp(e.currentTarget.value)}>
    {#each apps as a (a.name)}<option value={a.name}>{a.name}</option>{/each}
    {#if !apps.some((a) => a.name === app)}<option value={app}>{app}</option>{/if}
    <option value="__all">All apps…</option>
  </select>
  <div class="seg" role="radiogroup" aria-label="Environment">
    {#each envs as e (e)}
      <button type="button" role="radio" data-env={e} aria-checked={e === env} tabindex={e === env ? 0 : -1} onclick={() => setEnv(e)} onkeydown={onEnvKey}>{e}</button>
    {/each}
  </div>
  <div class="search">
    <label for="global-search" class="sr">Search decisions, a request ID or a change request</label>
    <input id="global-search" type="search" placeholder="Search decisions, req_… or cr_…" bind:value={search} onkeydown={onsearch} />
  </div>
  {#if mode.mock}
    <span class="sample-tag" title="No API answered, so this UI runs on built-in sample data. Edits stay in this browser tab.">Sample data</span>
  {/if}
  <button type="button" class="btn btn-icon btn-ghost" onclick={() => mode.cycleTheme()} aria-label={THEME_LABEL[mode.theme]} title={THEME_LABEL[mode.theme]}>
    {#if mode.theme === 'dark'}
      <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z" fill="currentColor" /></svg>
    {:else if mode.theme === 'light'}
      <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4.5" fill="currentColor" /><g stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8" /></g></svg>
    {:else}
      <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8.5" fill="none" stroke="currentColor" stroke-width="2" /><path d="M12 3.5a8.5 8.5 0 0 1 0 17z" fill="currentColor" /></svg>
    {/if}
  </button>
  <SessionMenu />
</header>
