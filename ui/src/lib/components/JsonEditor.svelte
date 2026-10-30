<script lang="ts">
  // JSON text editor for data slots and documents. `value` updates only when the text parses.
  import { validateDocument, type JsonSchema } from '$lib/model/schemaTree';

  let {
    value = $bindable(),
    id,
    label,
    rows = 8,
    schema = null,
    disabled = false,
    allowUndefined = false,
    onchange,
  }: {
    value: unknown;
    id?: string;
    label?: string;
    rows?: number;
    schema?: JsonSchema | null;
    disabled?: boolean;
    allowUndefined?: boolean;
    onchange?: (v: unknown) => void;
  } = $props();

  const uid = id ?? `json-${Math.random().toString(36).slice(2, 8)}`;
  let text = $state(fmt(value));
  let parseError: string | null = $state(null);
  let lastEmitted: string = JSON.stringify(value ?? null);

  function fmt(v: unknown): string {
    return v === undefined ? '' : JSON.stringify(v, null, 2);
  }

  // follow outside changes (e.g. "Use example") without clobbering what is being typed
  $effect(() => {
    const s = JSON.stringify(value ?? null);
    if (s !== lastEmitted) {
      lastEmitted = s;
      text = fmt(value);
      parseError = null;
    }
  });

  function oninput(): void {
    if (text.trim() === '' && allowUndefined) {
      parseError = null;
      lastEmitted = 'null';
      value = undefined;
      onchange?.(undefined);
      return;
    }
    try {
      const v = JSON.parse(text);
      parseError = null;
      lastEmitted = JSON.stringify(v);
      value = v;
      onchange?.(v);
    } catch (e) {
      parseError = (e as Error).message.replace(/^JSON\.parse: /, '');
    }
  }

  function tidy(): void {
    if (!parseError) text = fmt(value);
  }

  let schemaErrors = $derived(schema && !parseError && value !== undefined ? validateDocument(schema, value) : []);
</script>

<div class="stack-sm">
  {#if label}<label class="label" for={uid}>{label}</label>{/if}
  <textarea
    id={uid}
    class="code"
    {rows}
    bind:value={text}
    {oninput}
    onblur={tidy}
    spellcheck="false"
    autocomplete="off"
    {disabled}
    aria-invalid={parseError ? 'true' : undefined}
    aria-describedby={parseError || schemaErrors.length ? `${uid}-msg` : undefined}
  ></textarea>
  <div id="{uid}-msg">
    {#if parseError}
      <span class="error-text">Not valid JSON: {parseError}</span>
    {:else if schemaErrors.length}
      <ul class="problems">
        {#each schemaErrors.slice(0, 6) as e (e.path + e.message)}
          <li class="warning"><span class="pm" aria-hidden="true">!</span><span>{e.path || 'document'} {e.message}</span></li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
