<script lang="ts">
  import { page } from '$app/state';
  import type { Snippet } from 'svelte';
  import { api } from '$lib/api';
  import type { App } from '$lib/api/types.gen';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import NavRail from '$lib/components/NavRail.svelte';
  import TopBar from '$lib/components/TopBar.svelte';
  import { appHref, currentEnv, rememberApp } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { shell } from '$lib/stores/shell.svelte';

  let { children }: { children: Snippet } = $props();

  let app = $derived(page.params.app ?? '');
  let env = $derived(currentEnv());

  $effect(() => {
    if (app) rememberApp(app);
  });
  $effect(() => {
    void session.load();
  });

  const apps = query(() => api.listApps({ query: { limit: 200 } }).then((r) => r.items));
  const envs = query(() => api.listEnvironments({ app }).then((r) => r.items));
  const badges = query(async () => {
    const a = app, e = env;
    const [crs, fleet] = await Promise.all([
      api.listChangeRequests({ app: a, query: { status: 'open', limit: 200 } }).catch(() => ({ items: [] })),
      api.listSidecars({ app: a, env: e, query: { limit: 1 } }).catch(() => null),
    ]);
    return { pending: crs.items.length, lagging: fleet ? fleet.summary.total - fleet.summary.onLatest : 0 };
  });

  let appList: App[] = $derived(apps.data ?? []);
  let envNames = $derived(envs.data?.map((e) => e.name) ?? appList.find((a) => a.name === app)?.environments ?? ['dev', 'stage', 'prod']);
  let envUnknown = $derived(!!envs.data && !envNames.includes(env));
  $effect(() => {
    shell.envs = envNames;
  });
</script>

<a class="skip" href="#main">Skip to content</a>
<TopBar apps={appList} envs={envNames} />
<div class="layout">
  <NavRail pending={badges.data?.pending ?? 0} lagging={badges.data?.lagging ?? 0} />
  <main id="main" tabindex="-1">
    {#if envs.error}
      <ErrorNote error={envs.error} retry={envs.reload} title="Cannot open {app}" />
    {:else if session.error && !session.member}
      <ErrorNote error={session.error} retry={() => session.load()} title="Cannot load your session" />
    {:else}
      {#if envUnknown}
        <div class="callout warn" role="alert">
          <strong>{app} has no environment “{env}”.</strong>
          <span>Pick one: {#each envNames as e, i (e)}{i ? ', ' : ''}<a href={appHref(page.url.pathname.replace(/^.*?\/apps\/[^/]+/, ''), {}, { env: e })}>{e}</a>{/each}.</span>
        </div>
      {/if}
      {@render children()}
    {/if}
  </main>
</div>
