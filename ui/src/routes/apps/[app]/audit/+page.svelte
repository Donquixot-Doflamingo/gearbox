<script lang="ts">
  import { page } from '$app/state';
  import { api } from '$lib/api';
  import type { AuditEvent } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Pager from '$lib/components/Pager.svelte';
  import { fmtDateTime } from '$lib/model/format';
  import { qp, setQuery } from '$lib/nav.svelte';
  import { Paged } from '$lib/query.svelte';

  let app = $derived(page.params.app ?? '');
  let actor = $derived(qp('actor') ?? '');
  let all = $derived(qp('scope') === 'all');
  const list = new Paged<AuditEvent>((cursor) => api.listAudit({ query: { app: all ? undefined : app, actor: actor || undefined, cursor, limit: 50 } }));
  let open: string | null = $state(null);
  const LABEL: Record<string, string> = {
    'resource.created': 'Created', 'draft.saved': 'Saved draft', 'version.cut': 'Cut version', 'change_request.opened': 'Opened change request',
    'change_request.approved': 'Approved change request', 'change_request.rejected': 'Requested changes', 'change_request.withdrawn': 'Withdrew change request',
    'release.published': 'Published', 'credential.created': 'Created credential', 'credential.revoked': 'Revoked credential', 'member.role_changed': 'Changed role',
  };
</script>

<PageHeader title="Audit" sub="Every change to definitions, releases, credentials and members. Append-only.">
  {#snippet eyebrow()}<Crumbs />{/snippet}
</PageHeader>

<div class="filters">
  <label class="sr" for="au-actor">Actor</label>
  <input id="au-actor" type="search" placeholder="Filter by actor (email)" value={actor} onchange={(e) => setQuery({ actor: e.currentTarget.value.trim() || undefined })} />
  <div class="chips" role="group" aria-label="Scope">
    <button type="button" class="chip" aria-pressed={!all} onclick={() => setQuery({ scope: undefined })}>this app</button>
    <button type="button" class="chip" aria-pressed={all} onclick={() => setQuery({ scope: 'all' })}>everything</button>
  </div>
</div>

{#if list.error}<ErrorNote error={list.error} retry={list.reset} />
{:else if list.loading && !list.items.length}<Loading />
{:else if !list.items.length}<p class="hint">No audit events match.</p>
{:else}
  <div class="tbl-wrap">
    <table>
      <thead><tr><th>When</th><th>Who</th><th>Action</th><th>Resource</th><th>Env</th></tr></thead>
      <tbody>
        {#each list.items as a (a.id)}
          <tr class="clickable" tabindex="0" onclick={() => (open = open === a.id ? null : a.id)} onkeydown={(e) => { if (e.key === 'Enter') open = open === a.id ? null : a.id; }} aria-expanded={open === a.id}>
            <td class="nowrap">{fmtDateTime(a.at)}</td>
            <td>{a.actor.split('@')[0]}</td>
            <td>{LABEL[a.action] ?? a.action}{#if a.breakGlass} <span class="pill pill-warn">break-glass</span>{/if}</td>
            <td class="mono">{a.resource}</td>
            <td class="mono">{a.environment ?? '—'}</td>
          </tr>
          {#if open === a.id && a.details}
            <tr><td colspan="5"><pre class="code">{JSON.stringify(a.details, null, 2)}</pre></td></tr>
          {/if}
        {/each}
      </tbody>
    </table>
  </div>
  <Pager next={list.next} loading={list.loading} onmore={list.more} />
{/if}
