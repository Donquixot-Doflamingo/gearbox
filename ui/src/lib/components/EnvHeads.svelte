<script lang="ts">
  import { currentEnv } from '$lib/nav.svelte';
  import { shell } from '$lib/stores/shell.svelte';
  let env = $derived(currentEnv());
</script>

{#each shell.envs as e (e)}<th class:cur={e === env}>{e}</th>{/each}
