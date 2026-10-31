<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { Environment, EnvironmentPolicy } from '$lib/api/types.gen';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import { relTime } from '$lib/model/format';
  import { query } from '$lib/query.svelte';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  let app = $derived(page.params.app ?? '');
  const envs = query(() => api.listEnvironments({ app }).then((r) => r.items));
  let drafts = $state<Record<string, EnvironmentPolicy>>({});
  $effect(() => {
    for (const e of envs.data ?? []) if (!drafts[e.name]) drafts[e.name] = { ...e.policy };
  });
  let admin = $derived(session.can('admin'));

  function changed(e: Environment): boolean {
    return JSON.stringify(drafts[e.name]) !== JSON.stringify({ ...e.policy });
  }
  async function save(e: Environment): Promise<void> {
    const p = drafts[e.name];
    const r = await confirmAction({
      title: `Change the ${e.name} policy?`,
      env: e.name,
      body: [`Approvals: ${p.requiredApprovals ?? 0}${p.authorCanApprove ? ', the author may approve' : ', not the author'}. Data releases: ${p.allowDataReleases ? 'allowed when tests pass' : 'need a change request'}. Log sampling: ${((p.logSampleRate ?? 0) * 100).toFixed(2)}%.`],
      ok: 'Save policy',
      danger: e.name === 'prod',
    });
    if (!r) return;
    try {
      const out = await api.updateEnvironment({ app, env: e.name, body: p });
      drafts[e.name] = { ...out.policy };
      void envs.reload();
      toast(`Saved the ${e.name} policy`);
    } catch (err) {
      toastError(err, 'Policy not saved');
    }
  }
</script>

{#if envs.error}<ErrorNote error={envs.error} retry={envs.reload} />
{:else if !envs.data}<Loading />
{:else}
  <div class="grid-3" style="grid-template-columns:repeat(auto-fit,minmax(280px,1fr))">
    {#each envs.data as e (e.name)}
      {@const p = drafts[e.name]}
      {#if p}
        <section class="panel">
          <header><h2 class="mono">{e.name}</h2><span class="hint">generation {e.generation}{e.publishedAt ? ` · ${relTime(e.publishedAt)}` : ''}</span></header>
          <fieldset disabled={!admin} class="stack">
            <div class="field"><label for="ep-a-{e.name}">Required approvals</label><input id="ep-a-{e.name}" type="number" min="0" max="5" value={p.requiredApprovals ?? 0} oninput={(ev) => (p.requiredApprovals = Number(ev.currentTarget.value))} /></div>
            <label class="check"><input type="checkbox" checked={!!p.authorCanApprove} onchange={(ev) => (p.authorCanApprove = ev.currentTarget.checked)} /> Authors may approve their own requests</label>
            <label class="check"><input type="checkbox" checked={!!p.allowDataReleases} onchange={(ev) => (p.allowDataReleases = ev.currentTarget.checked)} /> Allow data releases (segments, lookups) without review when tests pass</label>
            <div class="field"><label for="ep-s-{e.name}">Decision log sample rate (0–1)</label><input id="ep-s-{e.name}" type="number" min="0" max="1" step="0.001" value={p.logSampleRate ?? 0} oninput={(ev) => (p.logSampleRate = Number(ev.currentTarget.value))} /></div>
          </fieldset>
          <div class="actions"><button type="button" class="btn {e.name === 'prod' ? 'btn-danger' : 'btn-primary'}" onclick={() => save(e)} disabled={!admin || !changed(e)}>Save {e.name} policy</button></div>
        </section>
      {/if}
    {/each}
  </div>
  {#if !admin}<p class="hint">{session.why('admin')}</p>{/if}
{/if}
