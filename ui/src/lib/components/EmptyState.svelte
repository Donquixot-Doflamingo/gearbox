<script lang="ts">
  import type { Snippet } from 'svelte';

  let { title, children, actions }: { title: string; children?: Snippet; actions?: Snippet } = $props();
</script>

<section class="empty">
  <h2>{title}</h2>
  {#if children}<p>{@render children()}</p>{/if}
  {#if actions}<div class="actions">{@render actions()}</div>{/if}
</section>
