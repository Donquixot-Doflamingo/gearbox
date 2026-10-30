<script lang="ts">
  import type { Release } from '$lib/api/types.gen';

  let { release, latest = false }: { release: Release; latest?: boolean } = $props();
  let rolling = $derived(release.contents.some((c) => c.changed && c.rollout?.rollout));
</script>

{#if release.breakGlass}<span class="pill pill-warn">no review</span>
{:else if release.type === 'kill_switch'}<span class="pill pill-crit">kill switch</span>
{:else if rolling && latest}<span class="pill pill-info">rolling out</span>
{:else if release.type === 'data'}<span class="pill pill-muted">data</span>
{:else}<span class="pill pill-ok">{latest ? 'live' : 'done'}</span>{/if}
