<script lang="ts">
  import { parseUnified } from '$lib/model/diff';

  let { diff, empty = 'No differences.' }: { diff: string; empty?: string } = $props();
  let lines = $derived(parseUnified(diff ?? ''));
</script>

{#if lines.length}
  <pre class="code scroll" aria-label="Diff">{#each lines as l, i (i)}<span class="diff-{l.kind}">{l.text || ' '}</span>{/each}</pre>
{:else}
  <p class="hint">{empty}</p>
{/if}
