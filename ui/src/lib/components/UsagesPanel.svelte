<script lang="ts">
  import { api } from '$lib/api';
  import type { ResourceKind } from '$lib/api/types.gen';
  import { appHref } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import ErrorNote from './ErrorNote.svelte';
  import Kind from './Kind.svelte';
  import Loading from './Loading.svelte';

  let { app, kind, name }: { app: string; kind: ResourceKind; name: string } = $props();
  const usages = query(() => api.listUsages({ app, kind, name }).then((r) => r.items));
</script>

<section class="panel">
  <header><h2>Used by</h2><span class="hint">drafts and latest versions</span></header>
  {#if usages.error}<ErrorNote error={usages.error} retry={usages.reload} />
  {:else if !usages.data}<Loading />
  {:else if usages.data.length === 0}<p class="hint">Nothing uses {name} yet.</p>
  {:else}
    <div class="tbl-wrap">
      <table class="compact">
        <thead><tr><th>Resource</th><th>Kind</th><th>Which</th>{#if kind === 'schemas'}<th>Uses</th>{/if}<th>Where</th></tr></thead>
        <tbody>
          {#each usages.data as u, i (i)}
            <tr>
              <td><a class="name" href={appHref(`/${u.kind}/${u.name}`)}>{u.name}</a></td>
              <td><Kind type={u.kind} /></td>
              <td class="mono">{u.version ? `v${u.version}` : 'draft'}</td>
              {#if kind === 'schemas'}<td class="mono">{u.usesVersion ? `v${u.usesVersion}` : '—'}</td>{/if}
              <td class="mono hint">{u.where ?? ''}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>
