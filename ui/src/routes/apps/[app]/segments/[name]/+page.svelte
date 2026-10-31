<script lang="ts">
  import { page } from '$app/state';
  import SegmentEditor from '$lib/editors/segment/SegmentEditor.svelte';

  let app = $derived(page.params.app ?? '');
  let name = $derived(page.params.name ?? '');
</script>

{#key `${app}/${name}`}
  <SegmentEditor {app} {name} />
{/key}
