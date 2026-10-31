<script lang="ts">
  // Input for a code slot (an Expr expression). Shows a syntax hint from the local parser;
  // the server's validation is authoritative.
  import { syntaxError } from '$lib/model/expr';

  let {
    value = $bindable(''),
    id,
    placeholder = 'Expr expression',
    disabled = false,
    multiline = false,
    error,
    label,
    onchange,
  }: { value: string; id?: string; placeholder?: string; disabled?: boolean; multiline?: boolean; error?: string | null; label?: string; onchange?: (v: string) => void } = $props();

  const uid = id ?? `expr-${Math.random().toString(36).slice(2, 8)}`;
  let touched = $state(false);
  let syntax = $derived(touched && value?.trim() ? syntaxError(value) : null);
  let msg = $derived(error ?? (syntax ? `Check the syntax: ${syntax}` : null));
</script>

<div class="stack-sm grow">
  {#if label}<label class="label" for={uid}>{label}</label>{/if}
  {#if multiline}
    <textarea id={uid} class="code" rows="3" bind:value {placeholder} {disabled} spellcheck="false" oninput={() => { touched = true; onchange?.(value); }} aria-invalid={error ? 'true' : undefined} aria-describedby={msg ? `${uid}-m` : undefined}></textarea>
  {:else}
    <input id={uid} type="text" class="code" bind:value {placeholder} {disabled} spellcheck="false" autocomplete="off" oninput={() => { touched = true; onchange?.(value); }} aria-invalid={error ? 'true' : undefined} aria-describedby={msg ? `${uid}-m` : undefined} />
  {/if}
  {#if msg}<span id="{uid}-m" class={error ? 'error-text' : 'hint warn'}>{msg}</span>{/if}
</div>
