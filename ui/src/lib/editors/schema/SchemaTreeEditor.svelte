<script lang="ts">
  // Tree editor for nested objects and arrays, with a property panel. Edits are immutable updates of `value`.
  import { addProperty, getAt, lintSchema, moveProperty, primaryType, removeProperty, type JsonSchema, type SchemaPath } from '$lib/model/schemaTree';
  import PropertyEditor from './PropertyEditor.svelte';
  import SchemaTreeNode from './SchemaTreeNode.svelte';

  let { value = $bindable(), disabled = false, rootLabel = 'root' }: { value: JsonSchema; disabled?: boolean; rootLabel?: string } = $props();

  let selected = $state<SchemaPath>([]);
  let closed = $state<Record<string, boolean>>({});

  // keep the selection valid after deletes/renames
  $effect(() => {
    if (!getAt(value, selected)) selected = [];
  });

  const ops = {
    select: (p: SchemaPath) => (selected = p),
    add: (objPath: SchemaPath) => {
      const r = addProperty(value, objPath, 'new_field');
      value = r.root;
      closed[objPath.join('/') || '#'] = false;
      selected = [...objPath, 'properties', r.name];
      queueMicrotask(() => document.getElementById('pe-name')?.focus());
    },
    remove: (objPath: SchemaPath, name: string) => {
      value = removeProperty(value, objPath, name);
      if (selected.join('/').startsWith([...objPath, 'properties', name].join('/'))) selected = objPath;
    },
    move: (objPath: SchemaPath, name: string, delta: number) => (value = moveProperty(value, objPath, name, delta)),
    toggle: (key: string) => (closed[key] = !(closed[key] ?? false)),
    isOpen: (key: string) => !closed[key],
  };

  let problems = $derived(lintSchema(value));
  let rootType = $derived(primaryType(value));
</script>

<div class="grid-side">
  <section class="panel tight" aria-label="Schema tree">
    <header><h2>Structure</h2>{#if rootType === 'object' && !disabled}<button type="button" class="btn btn-xs" onclick={() => ops.add([])}>Add property</button>{/if}</header>
    <div class="tree">
      <SchemaTreeNode root={value} path={[]} name={rootLabel} {selected} {ops} {disabled} />
    </div>
    {#if problems.length}
      <ul class="problems">
        {#each problems as p (p.path + p.message)}<li class="error"><span class="pm" aria-hidden="true">✕</span><span>{p.message} <span class="pp">{p.path || '/'}</span></span></li>{/each}
      </ul>
    {/if}
  </section>
  <section class="panel">
    <PropertyEditor bind:root={value} bind:path={selected} {disabled} />
  </section>
</div>
