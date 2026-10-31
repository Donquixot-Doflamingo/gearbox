<script lang="ts">
  import { page } from '$app/state';
  import ExperimentEditor from '$lib/editors/experiment/ExperimentEditor.svelte';

  let app = $derived(page.params.app ?? '');
  let name = $derived(page.params.name ?? '');
</script>

{#key `${app}/${name}`}
  <ExperimentEditor {app} {name} />
{/key}
