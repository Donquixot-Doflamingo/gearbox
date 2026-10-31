<script lang="ts">
  import { api } from '$lib/api';
  import { ApiError } from '$lib/api/client';
  import type { Webhook } from '$lib/api/types.gen';
  import Dialog from '$lib/components/Dialog.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Field from '$lib/components/Field.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import SecretOnce from '$lib/components/SecretOnce.svelte';
  import { fmtDateTime } from '$lib/model/format';
  import { query } from '$lib/query.svelte';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  const EVENTS = ['release.published', 'release.rolled_back', 'change_request.opened', 'change_request.approved', 'sidecar.sync_failed'];
  const hooks = query(() => api.listWebhooks({}).then((r) => r.items));
  let admin = $derived(session.can('admin'));
  let open = $state(false);
  let url = $state('https://');
  let events = $state<string[]>(['release.published']);
  let secret = $state<string | null>(null);
  let err = $state<ApiError | null>(null);

  async function add(): Promise<void> {
    err = null;
    try {
      const r = await api.createWebhook({ body: { url, events } });
      secret = (r as Webhook & { secret?: string }).secret ?? null;
      void hooks.reload();
      if (!secret) {
        open = false;
        toast('Webhook added');
      }
    } catch (e) {
      if (e instanceof ApiError) err = e;
    }
  }
  function close(): void {
    open = false;
    secret = null;
    url = 'https://';
    events = ['release.published'];
  }
  async function remove(w: Webhook): Promise<void> {
    const r = await confirmAction({ title: 'Remove this webhook?', body: w.url, ok: 'Remove', danger: true });
    if (!r) return;
    try {
      await api.deleteWebhook({ webhookId: w.id });
      void hooks.reload();
    } catch (e) {
      toastError(e);
    }
  }
</script>

<section class="stack">
  <div class="actions">
    <button type="button" class="btn btn-primary" onclick={() => (open = true)} disabled={!admin} title={session.why('admin')}>Add webhook</button>
    <span class="hint">Gearbox POSTs JSON events signed with <code class="inl">X-Gearbox-Signature: sha256=&lt;hmac&gt;</code> and retries with backoff for 24 h.</span>
  </div>
  {#if hooks.error}<ErrorNote error={hooks.error} retry={hooks.reload} />
  {:else if !hooks.data}<Loading />
  {:else if !hooks.data.length}<p class="hint">No webhooks. Add one to post releases to chat or trigger a pipeline.</p>
  {:else}
    <div class="tbl-wrap">
      <table>
        <thead><tr><th>URL</th><th>Events</th><th>Last delivery</th><th></th></tr></thead>
        <tbody>
          {#each hooks.data as w (w.id)}
            <tr>
              <td class="mono wrap-any">{w.url}</td>
              <td class="mono">{w.events.join(', ')}</td>
              <td>{#if w.lastDelivery}<span class="pill {w.lastDelivery.status && w.lastDelivery.status < 300 ? 'pill-ok' : 'pill-crit'}">{w.lastDelivery.status} · {fmtDateTime(w.lastDelivery.at)}</span>{:else}—{/if}</td>
              <td class="num">{#if admin}<button type="button" class="btn btn-sm" onclick={() => remove(w)}>Remove</button>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<Dialog bind:open title="Add webhook" onclose={close}>
  {#if secret}
    <SecretOnce {secret} what="signing secret" note="Verify X-Gearbox-Signature with it on your side." />
  {:else}
    <form id="add-hook" class="stack" onsubmit={(e) => { e.preventDefault(); void add(); }}>
      <Field label="URL" id="wh-url" required error={err?.fieldError('/url')}><input id="wh-url" type="url" bind:value={url} data-autofocus /></Field>
      <fieldset class="stack-sm">
        <legend class="label">Events</legend>
        {#each EVENTS as ev (ev)}<label class="check"><input type="checkbox" bind:group={events} value={ev} /> <span class="mono">{ev}</span></label>{/each}
        {#if err?.fieldError('/events')}<span class="error-text">{err.fieldError('/events')}</span>{/if}
      </fieldset>
      {#if err && !err.errors.length}<ErrorNote error={err} />{/if}
    </form>
  {/if}
  {#snippet actions()}
    {#if secret}<button type="button" class="btn btn-primary" onclick={close}>I stored it</button>
    {:else}
      <button type="button" class="btn" onclick={close}>Cancel</button>
      <button type="submit" form="add-hook" class="btn btn-primary" disabled={!events.length}>Add webhook</button>
    {/if}
  {/snippet}
</Dialog>
