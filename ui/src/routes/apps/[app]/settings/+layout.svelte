<script lang="ts">
  import { page } from '$app/state';
  import type { Snippet } from 'svelte';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { appHref } from '$lib/nav.svelte';

  let { children }: { children: Snippet } = $props();
  const TABS = [
    ['apps', 'Apps'],
    ['environments', 'Environments'],
    ['credentials', 'Sidecar credentials'],
    ['members', 'Members'],
    ['tokens', 'Access tokens'],
    ['webhooks', 'Webhooks'],
  ] as const;
  let current = $derived(/\/settings\/([^/?]+)/.exec(page.url.pathname)?.[1] ?? 'apps');
</script>

<PageHeader title="Settings" sub="App, access and delivery settings.">
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'settings' }]} />{/snippet}
</PageHeader>
<nav class="tabs" aria-label="Settings sections">
  {#each TABS as [id, label] (id)}<a href={appHref(`/settings/${id}`)} aria-current={current === id ? 'page' : undefined}>{label}</a>{/each}
</nav>
{@render children()}
