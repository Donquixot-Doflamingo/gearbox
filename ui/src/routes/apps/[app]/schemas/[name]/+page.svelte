<script lang="ts">
  import { page } from '$app/state';
  import SchemaEditor from '$lib/editors/schema/SchemaEditor.svelte';

  let app = $derived(page.params.app ?? '');
  let name = $derived(page.params.name ?? '');
</script>

{#key `${app}/${name}`}
  <SchemaEditor {app} {name} />
{/key}
