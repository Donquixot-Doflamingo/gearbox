<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    label,
    id,
    hint,
    error,
    required = false,
    class: cls = '',
    children,
  }: { label: string; id?: string; hint?: string; error?: string | null; required?: boolean; class?: string; children: Snippet } = $props();
</script>

<div class="field {cls}">
  {#if id}<label for={id}>{label}{#if required}<span aria-hidden="true"> *</span>{/if}</label>{:else}<span class="label">{label}{#if required}<span aria-hidden="true"> *</span>{/if}</span>{/if}
  {@render children()}
  {#if error}<span class="error-text" id={id ? `${id}-err` : undefined} role="alert">{error}</span>{:else if hint}<span class="hint" id={id ? `${id}-hint` : undefined}>{hint}</span>{/if}
</div>
