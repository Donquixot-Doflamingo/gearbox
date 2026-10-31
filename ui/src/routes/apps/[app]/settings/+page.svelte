<script lang="ts">
  import { goto } from '$app/navigation';
  import { appHref } from '$lib/nav.svelte';

  $effect(() => {
    void goto(appHref('/settings/apps'), { replaceState: true });
  });
</script>

<p class="loading">Opening settings…</p>
