<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    title,
    eyebrow,
    sub,
    actions,
    children,
  }: { title: string; eyebrow?: Snippet; sub?: Snippet | string; actions?: Snippet; children?: Snippet } = $props();
</script>

<div class="page-head">
  <div>
    {#if eyebrow}<p class="eyebrow">{@render eyebrow()}</p>{/if}
    <h1>{title}</h1>
    {#if typeof sub === 'string'}<p class="sub">{sub}</p>{:else if sub}<div class="sub">{@render sub()}</div>{/if}
    {@render children?.()}
  </div>
  {#if actions}<div class="actions">{@render actions()}</div>{/if}
</div>
