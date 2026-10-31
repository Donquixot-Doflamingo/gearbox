<script lang="ts">
  import { page } from '$app/state';
  import LayerEditor from '$lib/editors/layer/LayerEditor.svelte';

  let app = $derived(page.params.app ?? '');
  let name = $derived(page.params.name ?? '');
</script>

{#key `${app}/${name}`}
  <LayerEditor {app} {name} />
{/key}
