<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { Credential } from '$lib/api/types.gen';
  import Dialog from '$lib/components/Dialog.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import SecretOnce from '$lib/components/SecretOnce.svelte';
  import { fmtDate, relTime } from '$lib/model/format';
  import { query } from '$lib/query.svelte';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { shell } from '$lib/stores/shell.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  let app = $derived(page.params.app ?? '');
  const creds = query(async () => {
    const lists = await Promise.all(shell.envs.map((e) => api.listCredentials({ app, env: e }).then((r) => r.items)));
    return lists.flat();
  });
  let admin = $derived(session.can('admin'));
  let open = $state(false);
  let env = $state('prod');
  let label = $state('');
  let secret = $state<string | null>(null);
  let busy = $state(false);

  async function create(): Promise<void> {
    busy = true;
    try {
      const r = await api.createCredential({ app, env, body: { label: label || undefined } });
      secret = r.secret;
      void creds.reload();
    } catch (e) {
      toastError(e, 'Not created');
    } finally {
      busy = false;
    }
  }
  function closeDialog(): void {
    open = false;
    secret = null;
    label = '';
  }
  async function revoke(c: Credential): Promise<void> {
    const r = await confirmAction({ title: `Revoke ${c.id}?`, env: c.environment, body: ['Sidecars using this credential keep serving their current bundle but can no longer fetch updates. Create and roll out a new credential first.'], ok: 'Revoke', danger: true, typeToConfirm: c.environment === 'prod' ? c.id : undefined });
    if (!r) return;
    try {
      await api.revokeCredential({ app, env: c.environment, credentialId: c.id });
      toast(`Revoked ${c.id}`);
      void creds.reload();
    } catch (e) {
      toastError(e, 'Not revoked');
    }
  }
</script>

<section class="stack">
  <div class="actions">
    <button type="button" class="btn btn-primary" onclick={() => (open = true)} disabled={!admin} title={session.why('admin')}>New credential</button>
    <span class="hint">A credential lets a sidecar read one app in one environment. Secrets are stored hashed and shown once.</span>
  </div>
  {#if creds.error}<ErrorNote error={creds.error} retry={creds.reload} />
  {:else if !creds.data}<Loading />
  {:else if !creds.data.length}<p class="hint">No credentials yet. Create one per environment and mount it into the sidecar as GEARBOX_CREDENTIAL_FILE.</p>
  {:else}
    <div class="tbl-wrap">
      <table>
        <thead><tr><th>Credential</th><th>Env</th><th>Created</th><th>Last used</th><th>State</th><th></th></tr></thead>
        <tbody>
          {#each creds.data as c (c.id)}
            <tr>
              <td class="mono">{c.id}<span class="desc">{c.label ?? ''}</span></td>
              <td class="mono">{c.environment}</td>
              <td class="nowrap">{fmtDate(c.createdAt)} · {c.createdBy?.split('@')[0] ?? ''}</td>
              <td class="nowrap">{relTime(c.lastUsedAt)}</td>
              <td><span class="pill {c.state === 'active' ? 'pill-ok' : 'pill-muted'}">{c.state}</span></td>
              <td class="num">{#if c.state === 'active'}<button type="button" class="btn btn-sm" onclick={() => revoke(c)} disabled={!admin}>Revoke</button>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<Dialog bind:open title="New sidecar credential" onclose={closeDialog}>
  {#if secret}
    <SecretOnce {secret} what="credential secret" note="Store it as a Kubernetes secret and mount it into the sidecar (GEARBOX_CREDENTIAL_FILE)." />
  {:else}
    <div class="field"><label for="cred-env">Environment</label><select id="cred-env" bind:value={env}>{#each shell.envs as e (e)}<option value={e}>{e}</option>{/each}</select></div>
    <div class="field"><label for="cred-label">Label</label><input id="cred-label" type="text" bind:value={label} maxlength="100" placeholder="e.g. k8s prod" data-autofocus /></div>
    {#if env === 'prod'}<p class="env-callout">Target: prod</p>{/if}
  {/if}
  {#snippet actions()}
    {#if secret}
      <button type="button" class="btn btn-primary" onclick={closeDialog}>I stored it</button>
    {:else}
      <button type="button" class="btn" onclick={closeDialog}>Cancel</button>
      <button type="button" class="btn btn-primary" onclick={create} disabled={busy}>Create credential</button>
    {/if}
  {/snippet}
</Dialog>
