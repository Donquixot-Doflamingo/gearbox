<script lang="ts">
  import { page } from '$app/state';
  import DecisionEditor from '$lib/editors/decision/DecisionEditor.svelte';

  let app = $derived(page.params.app ?? '');
  let name = $derived(page.params.name ?? '');
</script>

{#key `${app}/${name}`}
  <DecisionEditor {app} {name} />
{/key}
