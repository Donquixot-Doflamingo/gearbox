<script lang="ts">
  // Create a resource as a draft, then open its editor.
  import { goto } from '$app/navigation';
  import type { Snippet } from 'svelte';
  import { api } from '$lib/api';
  import { ApiError } from '$lib/api/client';
  import type { ResourceKind, ResourceSpec } from '$lib/api/types.gen';
  import { appHref } from '$lib/nav.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import { toast } from '$lib/stores/toast.svelte';
  import Dialog from './Dialog.svelte';
  import ErrorNote from './ErrorNote.svelte';
  import Field from './Field.svelte';

  let {
    app,
    kind,
    open = $bindable(false),
    title,
    buildSpec,
    extra,
    valid = true,
  }: { app: string; kind: ResourceKind; open?: boolean; title: string; buildSpec: () => unknown; extra?: Snippet; valid?: boolean } = $props();

  let name = $state('');
  let description = $state('');
  let busy = $state(false);
  let err = $state<ApiError | null>(null);
  const NAME = /^[a-z][a-z0-9_]{1,62}$/;
  let nameProblem = $derived(name && !NAME.test(name) ? 'Use a–z, 0–9 and _, start with a letter, 2–63 characters.' : null);

  $effect(() => {
    if (open) {
      err = null;
    }
  });

  async function create(): Promise<void> {
    busy = true;
    err = null;
    try {
      const r = await api.createResource({ app, kind, body: { name, description: description || undefined, spec: buildSpec() as ResourceSpec } });
      registry.invalidate(app, kind);
      toast(`Created ${r.name} as a draft`);
      open = false;
      name = '';
      description = '';
      void goto(appHref(`/${kind}/${r.name}`));
    } catch (e) {
      if (e instanceof ApiError) err = e;
      else throw e;
    } finally {
      busy = false;
    }
  }
</script>

<Dialog bind:open {title}>
  <form id="new-{kind}" class="stack" onsubmit={(e) => { e.preventDefault(); void create(); }}>
    <Field label="Name" id="new-{kind}-name" required hint="a–z, 0–9 and _. Names cannot change later." error={nameProblem ?? err?.fieldError('/name')}>
      <input id="new-{kind}-name" type="text" bind:value={name} autocomplete="off" spellcheck="false" data-autofocus aria-invalid={nameProblem ? 'true' : undefined} />
    </Field>
    <Field label="Description" id="new-{kind}-desc"><input id="new-{kind}-desc" type="text" bind:value={description} maxlength="500" /></Field>
    {@render extra?.()}
    {#if err}<ErrorNote error={err} />{/if}
  </form>
  {#snippet actions()}
    <button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
    <button type="submit" form="new-{kind}" class="btn btn-primary" disabled={busy || !name || !!nameProblem || !valid}>{busy ? 'Creating…' : 'Create draft'}</button>
  {/snippet}
</Dialog>
