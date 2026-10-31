<script lang="ts">
  import { page } from '$app/state';
  import GateEditor from '$lib/editors/gate/GateEditor.svelte';

  let app = $derived(page.params.app ?? '');
  let name = $derived(page.params.name ?? '');
</script>

{#key `${app}/${name}`}
  <GateEditor {app} {name} />
{/key}
