<script lang="ts">
  import { page } from '$app/state';
  import { appHref } from '$lib/nav.svelte';

  let { pending = 0, lagging = 0 }: { pending?: number; lagging?: number } = $props();

  const NAV: { group?: string; items: { path: string; label: string; badge?: 'pending' | 'lagging' }[] }[] = [
    { items: [{ path: '', label: 'Overview' }] },
    { group: 'Build', items: [
      { path: '/schemas', label: 'Schemas' },
      { path: '/decisions', label: 'Decisions' },
      { path: '/segments', label: 'Segments' },
      { path: '/lookups', label: 'Lookups' },
      { path: '/gates', label: 'Gates' },
      { path: '/experiments', label: 'Experiments' },
      { path: '/layers', label: 'Layers' },
      { path: '/ai', label: 'AI nodes' },
    ] },
    { group: 'Ship', items: [
      { path: '/changes', label: 'Change requests', badge: 'pending' },
      { path: '/releases', label: 'Releases' },
      { path: '/fleet', label: 'Sidecar fleet', badge: 'lagging' },
    ] },
    { group: 'Observe', items: [
      { path: '/logs', label: 'Decision logs' },
      { path: '/update-logs', label: 'Update logs' },
      { path: '/exposures', label: 'Exposure logs' },
      { path: '/audit', label: 'Audit' },
    ] },
    { group: 'Admin', items: [{ path: '/settings', label: 'Settings' }] },
  ];

  /** Section of the current URL: /apps/x/decisions/y → /decisions */
  let section = $derived.by(() => {
    const m = /^.*?\/apps\/[^/]+(\/[^/?#]*)?/.exec(page.url.pathname);
    return m?.[1] ?? '';
  });
</script>

<nav class="rail" aria-label="Sections">
  {#each NAV as g, gi (gi)}
    <div class="grp">
      {#if g.group}<h3>{g.group}</h3>{/if}
      {#each g.items as it (it.path)}
        {@const n = it.badge === 'pending' ? pending : it.badge === 'lagging' ? lagging : 0}
        <a href={appHref(it.path)} aria-current={section === it.path ? 'page' : undefined}>
          <span>{it.label}</span>
          {#if n > 0}<span class="count" class:warn={it.badge === 'lagging'} aria-label="{n} {it.badge === 'lagging' ? 'lagging' : 'pending'}">{n}</span>{/if}
        </a>
      {/each}
    </div>
  {/each}
</nav>
