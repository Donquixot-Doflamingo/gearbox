<script lang="ts">
  import type { ValidationProblem } from '$lib/api/types.gen';

  let { problems, showPath = false, max = 50, onpick }: { problems: ValidationProblem[]; showPath?: boolean; max?: number; onpick?: (p: ValidationProblem) => void } = $props();
</script>

{#if problems.length}
  <ul class="problems">
    {#each problems.slice(0, max) as p, i (i + p.path + p.message)}
      <li class={p.severity}>
        <span class="pm" aria-hidden="true">{p.severity === 'error' ? '✕' : '!'}</span>
        <span>
          <span class="sr">{p.severity}: </span>
          {#if onpick}<button type="button" class="link-btn" onclick={() => onpick(p)}>{p.message}</button>{:else}{p.message}{/if}
          {#if showPath && p.path}<span class="pp"> {p.path}</span>{/if}
        </span>
      </li>
    {/each}
    {#if problems.length > max}<li class="warning"><span class="pm">…</span><span>{problems.length - max} more</span></li>{/if}
  </ul>
{/if}
