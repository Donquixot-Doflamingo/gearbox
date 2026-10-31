<script lang="ts">
  // Pick a field from the input schema tree (searchable, keyboard operable). Without a schema, type a path.
  import { untrack } from 'svelte';
  import type { FieldInfo } from '$lib/model/schemaTree';

  let {
    value = '',
    fields,
    id,
    disabled = false,
    onpick,
  }: { value: string; fields: FieldInfo[] | null; id?: string; disabled?: boolean; onpick: (path: string) => void } = $props();

  const uid = id ?? `fp-${Math.random().toString(36).slice(2, 8)}`;
  let open = $state(false);
  let q = $state('');
  let active = $state(0);
  let root: HTMLDivElement | undefined = $state();
  let current = $derived(fields?.find((f) => f.path === value));
  let shown = $derived((fields ?? []).filter((f) => !q || f.path.toLowerCase().includes(q.toLowerCase())));
  let selectable = (f: FieldInfo) => !f.inArray;

  $effect(() => {
    if (!open) return;
    untrack(() => {
      q = '';
      active = Math.max(0, (fields ?? []).findIndex((f) => f.path === value));
    });
    const onDoc = (e: MouseEvent) => {
      if (root && !root.contains(e.target as Node)) open = false;
    };
    document.addEventListener('mousedown', onDoc);
    queueMicrotask(() => root?.querySelector<HTMLInputElement>('input[type=search]')?.focus());
    return () => document.removeEventListener('mousedown', onDoc);
  });

  function pick(f: FieldInfo): void {
    if (!selectable(f)) return;
    onpick(f.path);
    open = false;
    queueMicrotask(() => root?.querySelector<HTMLButtonElement>('.picker-btn')?.focus());
  }

  function onkeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault();
      open = false;
      root?.querySelector<HTMLButtonElement>('.picker-btn')?.focus();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      active = Math.min(shown.length - 1, active + 1);
      root?.querySelector(`[data-i="${active}"]`)?.scrollIntoView({ block: 'nearest' });
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      active = Math.max(0, active - 1);
      root?.querySelector(`[data-i="${active}"]`)?.scrollIntoView({ block: 'nearest' });
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const f = shown[active];
      if (f) pick(f);
    }
  }
</script>

{#if fields === null}
  <input id={uid} type="text" class="code" placeholder="field.path" value={value} {disabled} oninput={(e) => onpick(e.currentTarget.value)} aria-label="Field path" />
{:else}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="picker" bind:this={root} {onkeydown}>
    <button type="button" id={uid} class="picker-btn" aria-haspopup="listbox" aria-expanded={open} {disabled} onclick={() => (open = !open)}>
      {#if value}<span>{value}</span>{:else}<span class="ph">Choose a field</span>{/if}
      {#if current}<span class="t">{current.type}{current.nullable ? '?' : ''}</span>{:else if value}<span class="t" style="color:var(--crit)">not in schema</span>{/if}
    </button>
    {#if open}
      <div class="picker-pop">
        <label class="sr" for="{uid}-q">Search fields</label>
        <input id="{uid}-q" type="search" placeholder="Search fields" bind:value={q} oninput={() => (active = 0)} autocomplete="off" />
        <div role="listbox" aria-label="Fields">
          {#each shown as f, i (f.path)}
            <button
              type="button"
              class="picker-opt"
              class:active={i === active}
              role="option"
              aria-selected={f.path === value}
              aria-disabled={!selectable(f)}
              data-i={i}
              style:padding-left="{8 + f.depth * 14}px"
              title={f.inArray ? 'Inside an array: use an expression such as any(input.list, .field == x)' : f.description}
              onclick={() => pick(f)}
              onmouseenter={() => (active = i)}
            >
              <span>{f.name}</span>
              {#if f.pii}<span class="pii">PII</span>{/if}
              <span class="t">{f.type}{f.nullable ? '?' : ''}{f.required ? '' : ' · optional'}</span>
            </button>
          {:else}
            <p class="hint" style="padding:6px">No field matches “{q}”.</p>
          {/each}
        </div>
      </div>
    {/if}
  </div>
{/if}
