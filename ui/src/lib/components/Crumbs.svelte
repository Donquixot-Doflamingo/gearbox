<script lang="ts">
  import { appHref, currentApp, currentEnv } from '$lib/nav.svelte';

  let { parts = [] }: { parts?: { label: string; href?: string }[] } = $props();
</script>

<a href={appHref('')}>{currentApp()}</a> · {currentEnv()}{#each parts as p (p.label)}
  {' / '}{#if p.href}<a href={p.href}>{p.label}</a>{:else}<span>{p.label}</span>{/if}{/each}
