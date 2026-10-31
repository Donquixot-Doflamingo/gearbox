<script lang="ts">
  // Header for every resource editor: live versions, unsaved state, validate / discard / save,
  // conflict handling, and a guard against leaving with unsaved edits.
  import { beforeNavigate, goto } from '$app/navigation';
  import type { Snippet } from 'svelte';
  import type { DraftEditor } from '$lib/draft.svelte';
  import { relTime } from '$lib/model/format';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { session } from '$lib/stores/session.svelte';
  import ErrorNote from './ErrorNote.svelte';

  let { ed, children, savingDisabled = false, saveHint }: { ed: DraftEditor; children?: Snippet; savingDisabled?: boolean; saveHint?: string } = $props();

  let leaving = false;
  beforeNavigate((nav) => {
    if (!ed.dirty || leaving || !nav.to) return;
    const to = nav.to.url;
    if (to.pathname === location.pathname) return; // tab or query change on the same editor
    nav.cancel();
    void confirmAction({ title: 'Leave without saving?', body: `Your changes to ${ed.name} are not saved.`, ok: 'Leave', danger: true }).then((r) => {
      if (r) {
        leaving = true;
        void goto(to);
      }
    });
  });

  $effect(() => {
    const onUnload = (e: BeforeUnloadEvent) => {
      if (ed.dirty) e.preventDefault();
    };
    window.addEventListener('beforeunload', onUnload);
    return () => window.removeEventListener('beforeunload', onUnload);
  });

  function onkey(e: KeyboardEvent): void {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      if (ed.dirty && session.can('edit')) void ed.save();
    }
  }

  let live = $derived(Object.entries(ed.resource?.live ?? {}));
  let latest = $derived(ed.resource?.latestVersion ?? 0);
</script>

<svelte:window onkeydown={onkey} />

<div class="stack-sm">
  <div class="row-sm" aria-label="Versions">
    <span class="ver draft" title="Draft revision {ed.revision}">{ed.dirty ? 'unsaved changes' : `draft r${ed.revision}`}{latest ? ` · based on v${latest}` : ' · no version yet'}</span>
    {#each live as [env, st] (env)}
      <span class="ver" class:live={env === 'prod'} title="Live in {env}">{env} v{st.version}{st.rollout ? ` at ${st.rollout.percent}% · v${st.rollout.fromVersion} for the rest` : ''}</span>
    {/each}
    {#if !live.length}<span class="ver none">not live anywhere</span>{/if}
    {#if ed.resource?.draft.updatedAt}<span class="hint">saved {relTime(ed.resource.draft.updatedAt)} by {ed.resource.draft.updatedBy ?? 'someone'}</span>{/if}
  </div>
  <div class="actions">
    {@render children?.()}
    <button type="button" class="btn" onclick={() => ed.validateNow()} disabled={ed.validating}>{ed.validating ? 'Validating…' : 'Validate'}</button>
    <button type="button" class="btn" onclick={() => ed.discard()} disabled={!ed.dirty || ed.saving}>Discard</button>
    <button type="button" class="btn btn-primary" onclick={() => ed.save()} disabled={!ed.dirty || ed.saving || savingDisabled || !session.can('edit')} title={session.why('edit') ?? saveHint ?? 'Save the draft (Ctrl/Cmd+S)'}>{ed.saving ? 'Saving…' : 'Save draft'}</button>
    {#if ed.validation}
      <span class="hint" class:warn={ed.errorCount > 0} aria-live="polite">{ed.errorCount ? `${ed.errorCount} problem${ed.errorCount === 1 ? '' : 's'}` : ed.problems.length ? `valid · ${ed.problems.length} warning${ed.problems.length === 1 ? '' : 's'}` : 'valid'}</span>
    {/if}
  </div>
  {#if ed.conflict}
    <div class="callout warn" role="alert">
      <strong>Someone else saved this draft while you were editing.</strong>
      <span>Reload to see their version (your edits are lost), or overwrite it with yours.</span>
      <div class="actions">
        <button type="button" class="btn btn-sm" onclick={() => ed.discard()}>Reload theirs</button>
        <button type="button" class="btn btn-sm btn-danger" onclick={() => ed.overwrite()}>Overwrite with mine</button>
      </div>
    </div>
  {:else if ed.saveError && ed.saveError.code !== 'REVISION_CONFLICT'}
    <ErrorNote error={ed.saveError} title="Not saved: {ed.saveError.title}" />
  {/if}
</div>
