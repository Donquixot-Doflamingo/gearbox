<script lang="ts">
  import { ApiError, errorText } from '$lib/api/client';

  let { error, retry, title }: { error: unknown; retry?: () => void; title?: string } = $props();
  let api = $derived(error instanceof ApiError ? error : null);
</script>

<div class="callout crit" role="alert">
  <strong>{title ?? api?.title ?? 'Something went wrong'}</strong>
  {#if api}
    {#if api.detail}<span>{api.detail}</span>{/if}
    <span class="hint">{api.code}{api.status ? ` · HTTP ${api.status}` : ''}</span>
    {#if api.errors.length}
      <ul class="problems">
        {#each api.errors as e (e.path + e.message)}
          <li class="error"><span class="pm" aria-hidden="true">✕</span><span>{e.message} <span class="pp">{e.path}</span></span></li>
        {/each}
      </ul>
    {/if}
  {:else}
    <span>{errorText(error)}</span>
  {/if}
  {#if retry}<div><button type="button" class="btn btn-sm" onclick={retry}>Try again</button></div>{/if}
</div>
