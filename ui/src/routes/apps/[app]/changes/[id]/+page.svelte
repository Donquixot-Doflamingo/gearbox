<script lang="ts">
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { ChangeRequest } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import CrStatus from '$lib/components/CrStatus.svelte';
  import Dialog from '$lib/components/Dialog.svelte';
  import DiffView from '$lib/components/DiffView.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Kind from '$lib/components/Kind.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import { fmtDateTime, relTime } from '$lib/model/format';
  import { appHref } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  let app = $derived(page.params.app ?? '');
  let id = $derived(page.params.id ?? '');
  const cr = query(() => api.getChangeRequest({ app, id }));
  const env = query(() => api.listEnvironments({ app }));
  let c = $derived(cr.data);
  let nextGen = $derived((env.data?.items.find((e) => e.name === c?.environment)?.generation ?? 0) + 1);
  let mine = $derived(!!c && c.author === session.member?.email);
  let comment = $state('');
  let rejecting = $state(false);
  let rejectText = $state('');
  let busy = $state(false);

  async function act(fn: () => Promise<ChangeRequest>, ok: string): Promise<void> {
    busy = true;
    try {
      cr.data = await fn();
      toast(ok);
    } catch (e) {
      toastError(e);
    } finally {
      busy = false;
    }
  }

  async function approve(): Promise<void> {
    if (!c) return;
    const r = await confirmAction({ title: `Approve ${c.id}?`, env: c.environment, body: [`${c.items.map((i) => `${i.name} v${i.version}`).join(', ')} become publishable${(c.approvals?.length ?? 0) + 1 < (c.requiredApprovals ?? 1) ? ' after more approvals' : ''}. Nothing changes for callers until someone publishes.`], ok: 'Approve', danger: false, reason: { label: 'Comment (optional)' } });
    if (r) await act(() => api.approveChangeRequest({ app, id, body: { comment: r.reason || undefined } }), `Approved ${id}`);
  }
  async function reject(): Promise<void> {
    rejecting = false;
    await act(() => api.rejectChangeRequest({ app, id, body: { comment: rejectText } }), `Requested changes on ${id}`);
    rejectText = '';
  }
  async function withdraw(): Promise<void> {
    const r = await confirmAction({ title: `Withdraw ${id}?`, body: 'The request closes; the versions stay available for a new request.', ok: 'Withdraw', danger: false });
    if (r) await act(() => api.withdrawChangeRequest({ app, id }), `Withdrew ${id}`);
  }
  async function addComment(): Promise<void> {
    await act(() => api.commentChangeRequest({ app, id, body: { body: comment } }), 'Comment added');
    comment = '';
  }
  async function publish(): Promise<void> {
    if (!c) return;
    const r = await confirmAction({
      title: `Publish ${c.id} to ${c.environment}?`,
      env: c.environment,
      envDetail: `generation ${nextGen}`,
      body: [`Every ${app} sidecar in ${c.environment} swaps to the new bundle within seconds.`, ...c.items.filter((i) => i.rollout).map((i) => `${i.name} v${i.version} starts at ${i.rollout!.percent}% of units; the rest stay on v${i.liveVersion}.`)],
      ok: `Publish to ${c.environment}`,
      danger: true,
    });
    if (!r) return;
    busy = true;
    try {
      const rel = await api.publishRelease({ app, env: c.environment, body: { type: 'change_request', changeRequestId: c.id } });
      toast(`Published generation ${rel.generation} to ${c.environment}`);
      void goto(appHref('/releases', {}, { env: c.environment }));
    } catch (e) {
      toastError(e, 'Not published');
    } finally {
      busy = false;
    }
  }
</script>

{#if cr.error}
  <ErrorNote error={cr.error} retry={cr.reload} />
{:else if !c}
  <Loading />
{:else}
  <PageHeader title="{c.id} · {c.title}">
    {#snippet eyebrow()}<Crumbs parts={[{ label: 'change requests', href: appHref('/changes') }]} />{/snippet}
    {#snippet sub()}<span class="row-sm"><CrStatus cr={c!} /><span class="pill {c!.environment === 'prod' ? 'pill-crit' : 'pill-muted'}">to {c!.environment}</span></span>{/snippet}
    {#snippet actions()}
      {#if c!.status === 'open'}
        <button type="button" class="btn btn-primary" onclick={approve} disabled={busy || !session.can('approve')} title={session.why('approve') ?? (mine ? 'You opened this request; the environment may not allow self-approval' : undefined)}>Approve</button>
        <button type="button" class="btn" onclick={() => (rejecting = true)} disabled={busy || !session.can('approve')}>Request changes</button>
      {/if}
      {#if c!.status === 'approved'}
        <button type="button" class="btn btn-danger" onclick={publish} disabled={busy || !session.can('release')} title={session.why('release')}>Publish to {c!.environment}</button>
      {/if}
      {#if mine && (c!.status === 'open' || c!.status === 'approved')}<button type="button" class="btn" onclick={withdraw} disabled={busy}>Withdraw</button>{/if}
    {/snippet}
  </PageHeader>

  <div class="grid-2">
    <section class="panel">
      <dl class="kv">
        <dt>Opened by</dt><dd>{c.author} · {fmtDateTime(c.createdAt)}</dd>
        <dt>Needs</dt><dd>{c.requiredApprovals ?? 0} approval{c.requiredApprovals === 1 ? '' : 's'} · {(c.approvals ?? []).length} so far</dd>
        <dt>Tests</dt><dd>{#if c.tests}<span class="pill {c.tests.failed ? 'pill-crit' : 'pill-ok'}">{c.tests.passed} of {(c.tests.passed ?? 0) + (c.tests.failed ?? 0)} passed</span> <span class="hint">against {c.environment}</span>{:else}—{/if}</dd>
        {#if c.publishedGeneration}<dt>Published</dt><dd>generation {c.publishedGeneration}</dd>{/if}
        {#if c.description}<dt>Note</dt><dd>{c.description}</dd>{/if}
      </dl>
      {#if c.status === 'open' && mine}<p class="hint">You opened this request. {c.environment === 'prod' ? 'prod usually needs an approver who is not the author.' : ''}</p>{/if}
    </section>
    <section class="panel">
      <header><h2>Approvals</h2></header>
      {#if (c.approvals ?? []).length}
        <ul class="list">{#each c.approvals ?? [] as a (a.by + a.at)}<li><div class="main"><span>{a.by}</span><span class="meta">{relTime(a.at)}{a.comment ? ` · “${a.comment}”` : ''}</span></div><span class="pill pill-ok">approved</span></li>{/each}</ul>
      {:else}<p class="hint">No approvals yet.</p>{/if}
    </section>
  </div>

  <section class="panel">
    <header><h2>Changes</h2><span class="hint">diff from the version live in {c.environment} when the request was opened</span></header>
    {#each c.items as it (it.kind + it.name)}
      <details open={c.items.length <= 2}>
        <summary class="row-sm" style="cursor:pointer"><Kind type={it.kind} /> <a class="mono" href={appHref(`/${it.kind}/${it.name}`)}>{it.name}</a> <span class="mono">{it.liveVersion ? `v${it.liveVersion} → ` : ''}v{it.version}</span>{#if it.rollout}<span class="pill pill-info">rollout from {it.rollout.percent}%</span>{/if}</summary>
        <div style="margin-top:8px"><DiffView diff={it.diff ?? ''} empty={it.liveVersion === it.version ? 'Same version as live (rollout change only).' : 'No diff available.'} /></div>
      </details>
    {/each}
  </section>

  <section class="panel">
    <header><h2>Comments</h2></header>
    <ul class="list">
      {#each c.comments ?? [] as m (m.by + m.at)}<li><div class="main"><span class="meta">{m.by} · {relTime(m.at)}</span><span style="white-space:pre-wrap">{m.body}</span></div></li>{:else}<li class="hint">No comments.</li>{/each}
    </ul>
    <div class="field"><label for="cr-comment">Add a comment</label><textarea id="cr-comment" rows="2" bind:value={comment} maxlength="5000"></textarea></div>
    <div><button type="button" class="btn btn-sm" onclick={addComment} disabled={busy || !comment.trim()}>Comment</button></div>
  </section>

  <Dialog bind:open={rejecting} title="Request changes on {c.id}">
    <p>The request closes as “changes requested”. Say what needs to change; {c.author} sees your comment.</p>
    <div class="field"><label for="rej">Comment *</label><textarea id="rej" rows="3" bind:value={rejectText} data-autofocus></textarea></div>
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => (rejecting = false)}>Cancel</button>
      <button type="button" class="btn btn-primary" onclick={reject} disabled={!rejectText.trim()}>Request changes</button>
    {/snippet}
  </Dialog>
{/if}
