<script lang="ts">
  // One table cell per environment with the live version (and rollout) of a resource.
  import type { LiveState } from '$lib/api/types.gen';
  import { currentEnv } from '$lib/nav.svelte';
  import { shell } from '$lib/stores/shell.svelte';

  let { live, draftChanged = false, latest = 0 }: { live: Record<string, LiveState>; draftChanged?: boolean; latest?: number } = $props();
  let env = $derived(currentEnv());
</script>

{#each shell.envs as e (e)}
  {@const st = live[e]}
  <td class:cur={e === env} class="nowrap">
    {#if st}
      <span class="ver" class:live={e === 'prod'}>v{st.version}{st.rollout ? ` · ${st.rollout.percent}%` : ''}</span>
    {:else if e === shell.envs[0] && (draftChanged || latest === 0)}
      <span class="ver draft">draft</span>
    {:else}
      <span class="ver none">—</span>
    {/if}
  </td>
{/each}
