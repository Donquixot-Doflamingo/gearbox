<script lang="ts">
  import { dismiss, toasts } from '$lib/stores/toast.svelte';
</script>

<div class="toasts" role="status" aria-live="polite">
  {#each toasts as t (t.id)}
    <div class="toast" class:crit={t.kind === 'crit'}>
      <span>{t.message}</span>
      {#if t.action}
        <button type="button" onclick={() => { t.action?.run(); dismiss(t.id); }}>{t.action.label}</button>
      {/if}
      <button type="button" aria-label="Dismiss" onclick={() => dismiss(t.id)}>×</button>
    </div>
  {/each}
</div>
