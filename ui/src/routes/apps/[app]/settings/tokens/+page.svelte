<script lang="ts">
  import { api } from '$lib/api';
  import { ApiError } from '$lib/api/client';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Field from '$lib/components/Field.svelte';
  import SecretOnce from '$lib/components/SecretOnce.svelte';
  import { fmtDate } from '$lib/model/format';

  let label = $state('');
  let days = $state(90);
  let created: { id: string; token: string; expiresAt: string } | null = $state(null);
  let err: ApiError | null = $state(null);
  let busy = $state(false);

  async function create(): Promise<void> {
    busy = true;
    err = null;
    try {
      created = await api.createToken({ body: { label, expiresInDays: days } });
      label = '';
    } catch (e) {
      if (e instanceof ApiError) err = e;
    } finally {
      busy = false;
    }
  }
</script>

<div class="grid-2">
  <section class="panel">
    <header><h2>New personal access token</h2></header>
    <p class="hint">For gearboxctl and CI: <code class="inl">Authorization: Bearer gbx_pat_…</code>. The token acts with your role.</p>
    <form class="stack" onsubmit={(e) => { e.preventDefault(); void create(); }}>
      <Field label="Label" id="tk-label" required error={err?.fieldError('/label')}><input id="tk-label" type="text" bind:value={label} placeholder="e.g. CI publish job" /></Field>
      <Field label="Expires in (days)" id="tk-days" error={err?.fieldError('/expiresInDays')}><input id="tk-days" type="number" min="1" max="365" bind:value={days} /></Field>
      {#if err && !err.errors.length}<ErrorNote error={err} />{/if}
      <div><button type="submit" class="btn btn-primary" disabled={busy || !label.trim()}>Create token</button></div>
    </form>
  </section>
  <section class="panel">
    {#if created}
      <SecretOnce secret={created.token} what="token" note="Expires {fmtDate(created.expiresAt)}. Store it in your CI secret store." />
    {:else}
      <h2>Your tokens</h2>
      <p class="hint">The API does not list existing tokens yet (requested in contracts/CHANGES-REQUESTED.md), so tokens cannot be reviewed or revoked here. Create short-lived tokens.</p>
    {/if}
  </section>
</div>
