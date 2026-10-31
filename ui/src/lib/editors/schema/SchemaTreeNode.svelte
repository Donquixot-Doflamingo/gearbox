<script lang="ts">
  import SchemaTreeNode from './SchemaTreeNode.svelte';
  import { isObj, isPii, primaryType, resolveRef, typeLabel, type JsonSchema, type SchemaPath } from '$lib/model/schemaTree';

  interface Ops {
    select: (p: SchemaPath) => void;
    add: (objPath: SchemaPath) => void;
    remove: (objPath: SchemaPath, name: string) => void;
    move: (objPath: SchemaPath, name: string, delta: number) => void;
    toggle: (key: string) => void;
    isOpen: (key: string) => boolean;
  }

  let {
    root,
    path,
    name,
    required = false,
    selected,
    ops,
    disabled = false,
    index = 0,
    siblings = 1,
  }: { root: JsonSchema; path: SchemaPath; name: string; required?: boolean; selected: SchemaPath; ops: Ops; disabled?: boolean; index?: number; siblings?: number } = $props();

  let node = $derived.by(() => {
    let cur: unknown = root;
    for (const seg of path) cur = isObj(cur) ? cur[seg] : undefined;
    return (isObj(cur) ? resolveRef(root, cur as JsonSchema) : undefined) ?? {};
  });
  let t = $derived(primaryType(node));
  let props = $derived(isObj(node.properties) ? Object.entries(node.properties as Record<string, JsonSchema>) : []);
  let req = $derived(Array.isArray(node.required) ? (node.required as string[]) : []);
  let items = $derived(t === 'array' && isObj(node.items) ? (node.items as JsonSchema) : null);
  let expandable = $derived(t === 'object' || !!items);
  let key = $derived(path.join('/') || '#');
  let open = $derived(ops.isOpen(key));
  let isSel = $derived(selected.join('/') === path.join('/'));
  let parent = $derived(path.length >= 2 && path[path.length - 2] === 'properties' ? path.slice(0, -2) : null);
</script>

<div class="tree-row" class:sel={isSel}>
  <div class="tree-name">
    {#if expandable}
      <button type="button" class="tree-toggle" aria-label="{open ? 'Collapse' : 'Expand'} {name}" aria-expanded={open} onclick={() => ops.toggle(key)}>{open ? '▾' : '▸'}</button>
    {:else}
      <span class="tree-toggle" aria-hidden="true">·</span>
    {/if}
    <button type="button" class="pick" aria-current={isSel} onclick={() => ops.select(path)}>
      <span>{name}{#if required}<span class="req" title="required"> *</span>{/if}</span>
      <span class="t">{typeLabel(node, root)}</span>
      {#if isPii(node)}<span class="tag" title="Personal data; never logged or sent to AI">PII</span>{/if}
    </button>
  </div>
  {#if !disabled}
    <div class="row-sm">
      {#if t === 'object'}<button type="button" class="btn btn-xs btn-ghost" onclick={() => ops.add(path)} aria-label="Add property to {name}" title="Add property">+</button>{/if}
      {#if parent}
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => ops.move(parent!, name, -1)} disabled={index === 0} aria-label="Move {name} up">↑</button>
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => ops.move(parent!, name, 1)} disabled={index >= siblings - 1} aria-label="Move {name} down">↓</button>
        <button type="button" class="btn btn-xs btn-ghost" onclick={() => ops.remove(parent!, name)} aria-label="Delete {name}">✕</button>
      {/if}
    </div>
  {/if}
</div>
{#if expandable && open}
  <div class="tree-children">
    {#if t === 'object'}
      {#each props as [child], i (child)}
        <SchemaTreeNode {root} path={[...path, 'properties', child]} name={child} required={req.includes(child)} {selected} {ops} {disabled} index={i} siblings={props.length} />
      {/each}
      {#if props.length === 0}<p class="hint" style="padding:4px 8px">No properties. {#if !disabled}<button type="button" class="link-btn" onclick={() => ops.add(path)}>Add one</button>{/if}</p>{/if}
    {:else if items}
      <SchemaTreeNode {root} path={[...path, 'items']} name="[ ] items" {selected} {ops} {disabled} />
    {/if}
  </div>
{/if}
