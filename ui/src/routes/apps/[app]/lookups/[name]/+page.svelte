<script lang="ts">
  import { page } from '$app/state';
  import LookupEditor from '$lib/editors/lookup/LookupEditor.svelte';

  let app = $derived(page.params.app ?? '');
  let name = $derived(page.params.name ?? '');
</script>

{#key `${app}/${name}`}
  <LookupEditor {app} {name} />
{/key}
