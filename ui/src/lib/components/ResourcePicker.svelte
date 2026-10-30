<script lang="ts">
  import type { ResourceKind } from '$lib/api/types.gen';
  import { appHref } from '$lib/nav.svelte';
  import { registry } from '$lib/stores/registry.svelte';

  let {
    app,
    kind,
    value = $bindable(''),
    label,
    id,
    disabled = false,
    error,
    hint,
    filter,
    onchange,
  }: { app: string; kind: ResourceKind; value: string; label: string; id?: string; disabled?: boolean; error?: string | null; hint?: string; filter?: (s: { name: string; subtype?: string }) => boolean; onchange?: (v: string) => void } = $props();

  const uid = id ?? `res-${Math.random().toString(36).slice(2, 8)}`;
  let list = $derived((registry.list(app, kind) ?? []).filter((s) => !filter || filter(s)));
  let loaded = $derived(registry.list(app, kind) !== undefined);
</script>

<div class="field">
  <label for={uid}>{label}</label>
  <div class="row-sm">
    <select id={uid} class="grow" bind:value {disabled} onchange={() => onchange?.(value)} aria-invalid={error ? 'true' : undefined}>
      <option value="">{loaded ? `— choose —` : 'Loading…'}</option>
      {#each list as s (s.name)}<option value={s.name}>{s.name}{s.subtype ? ` · ${s.subtype}` : ''}</option>{/each}
      {#if value && loaded && !list.some((s) => s.name === value)}<option value={value}>{value} (missing)</option>{/if}
    </select>
    {#if value}<a class="btn btn-sm btn-ghost" href={appHref(`/${kind}/${value}`)}>Open</a>{/if}
  </div>
  {#if error}<span class="error-text" role="alert">{error}</span>{:else if hint}<span class="hint">{hint}</span>{/if}
</div>
