<script lang="ts">
  // Pick a registered schema and version. Offers a link to register one when none fits.
  import type { SchemaRef } from '$lib/api/types.gen';
  import { appHref } from '$lib/nav.svelte';
  import { registry } from '$lib/stores/registry.svelte';

  let {
    app,
    value = $bindable(),
    label,
    id,
    required = false,
    disabled = false,
    error,
    hint,
    onchange,
  }: { app: string; value: SchemaRef | undefined; label: string; id?: string; required?: boolean; disabled?: boolean; error?: string | null; hint?: string; onchange?: (v: SchemaRef | undefined) => void } = $props();

  const fallbackId = `schema-${Math.random().toString(36).slice(2, 8)}`;
  let uid = $derived(id ?? fallbackId);
  let list = $derived(registry.list(app, 'schemas'));
  let current = $derived(list?.find((s) => s.name === value?.name));
  let versions = $derived(current ? Array.from({ length: current.latestVersion }, (_, i) => current!.latestVersion - i) : []);

  function setName(name: string): void {
    if (!name) {
      value = undefined;
    } else {
      const s = list?.find((x) => x.name === name);
      value = s && s.latestVersion > 0 ? { name, version: s.latestVersion } : { name, version: 1 };
    }
    onchange?.(value);
  }
  function setVersion(v: number): void {
    if (!value) return;
    value = { name: value.name, version: v };
    onchange?.(value);
  }
</script>

<div class="field">
  <label for={uid}>{label}{required ? ' *' : ''}</label>
  <div class="row-sm">
    <select id={uid} class="grow" value={value?.name ?? ''} {disabled} onchange={(e) => setName(e.currentTarget.value)} aria-invalid={error ? 'true' : undefined}>
      <option value="">{list ? '— choose a schema —' : 'Loading schemas…'}</option>
      {#each list ?? [] as s (s.name)}
        <option value={s.name} disabled={s.latestVersion === 0}>{s.name}{s.latestVersion === 0 ? ' (no version yet)' : ''}</option>
      {/each}
      {#if value?.name && list && !current}<option value={value.name}>{value.name} (not registered)</option>{/if}
    </select>
    {#if value?.name}
      <label class="sr" for="{uid}-v">Version of {label}</label>
      <select id="{uid}-v" value={value.version} {disabled} onchange={(e) => setVersion(Number(e.currentTarget.value))} style="width:auto">
        {#each versions.length ? versions : [value.version] as v (v)}<option value={v}>v{v}{current && v === current.latestVersion ? ' (latest)' : ''}</option>{/each}
      </select>
      <a class="btn btn-sm btn-ghost" href={appHref(`/schemas/${value.name}`)} title="Open {value.name}">Open</a>
    {/if}
  </div>
  {#if error}<span class="error-text" role="alert">{error}</span>{:else if hint}<span class="hint">{hint}</span>{/if}
  {#if list && list.length === 0}<span class="hint">No schemas yet. <a href={appHref('/schemas', { new: 1 })}>Register a schema</a> first.</span>{/if}
</div>
