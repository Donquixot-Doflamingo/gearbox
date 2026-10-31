<script lang="ts">
  import '../app.css';
  import type { Snippet } from 'svelte';
  import ConfirmHost from '$lib/components/ConfirmHost.svelte';
  import Toasts from '$lib/components/Toasts.svelte';
  import { setupApi } from '$lib/setup';

  let { children }: { children: Snippet } = $props();
  setupApi();
</script>

{@render children()}
<Toasts />
<ConfirmHost />
