<script lang="ts">
  // Entry: open the last app (or the first one), else the app list.
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import { DEFAULT_ENV, href, lastApp } from '$lib/nav.svelte';

  let error: unknown = $state(null);

  async function go(): Promise<void> {
    try {
      const { items } = await api.listApps({ query: { limit: 200 } });
      const last = lastApp();
      const target = items.find((a) => a.name === last) ?? items[0];
      if (target) void goto(href(`/apps/${encodeURIComponent(target.name)}?env=${target.environments.includes(DEFAULT_ENV) ? DEFAULT_ENV : (target.environments[0] ?? DEFAULT_ENV)}`), { replaceState: true });
      else void goto(href('/apps'), { replaceState: true });
    } catch (e) {
      error = e;
    }
  }
  $effect(() => {
    void go();
  });
</script>

<main class="layout" style="grid-template-columns:minmax(0,1fr)">
  {#if error}<ErrorNote {error} retry={() => { error = null; void go(); }} title="Cannot reach the Gearbox API" />{:else}<Loading label="Opening Gearbox…" />{/if}
</main>
