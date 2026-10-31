<script lang="ts">
  import { goto } from '$app/navigation';
  import { api } from '$lib/api';
  import { initials } from '$lib/model/format';
  import { appHref, href } from '$lib/nav.svelte';
  import { mode } from '$lib/stores/mode.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  let open = $state(false);
  let root = $state<HTMLDivElement | undefined>();
  let mockMembers: { id: string; name?: string; email: string; role: string }[] = $state([]);

  $effect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (root && !root.contains(e.target as Node)) open = false;
    };
    document.addEventListener('mousedown', onDoc);
    if (mode.mock) void import('$lib/api/mock/index').then((m) => (mockMembers = m.mockMembers()));
    return () => document.removeEventListener('mousedown', onDoc);
  });

  function onkeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape' && open) {
      open = false;
      root?.querySelector<HTMLElement>('.avatar')?.focus();
    }
  }

  async function signOut(): Promise<void> {
    open = false;
    try {
      await api.logout({});
    } catch (e) {
      toastError(e);
    }
    void goto(href(`/auth/login?signedOut=1`));
  }

  async function actAs(id: string): Promise<void> {
    const m = await import('$lib/api/mock/index');
    m.setMockUser(id);
    await session.load();
    open = false;
    toast(`Acting as ${session.member?.name ?? session.member?.email} (${session.member?.role})`);
  }

  async function resetSample(): Promise<void> {
    const m = await import('$lib/api/mock/index');
    m.resetMockDb();
    open = false;
    location.reload();
  }

  let me = $derived(session.member);
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="picker" bind:this={root} {onkeydown}>
  <button type="button" class="avatar" aria-haspopup="menu" aria-expanded={open} onclick={() => (open = !open)} title={me ? `${me.name ?? me.email} · ${me.role}` : 'Account'}>
    {me ? initials(me.name, me.email) : '?'}
  </button>
  {#if open}
    <div class="menu" role="menu">
      {#if me}
        <div class="mh"><strong style="color:var(--fg)">{me.name ?? me.email}</strong><br />{me.email} · {me.role}</div>
      {/if}
      <hr />
      <a class="mi" role="menuitem" href={appHref('/settings/tokens')} onclick={() => (open = false)}>Personal access tokens</a>
      <a class="mi" role="menuitem" href={appHref('/settings/apps')} onclick={() => (open = false)}>Settings</a>
      {#if mode.mock}
        <hr />
        <div class="mh">Sample data: act as</div>
        {#each mockMembers as m (m.id)}
          <button type="button" class="mi" role="menuitemradio" aria-checked={me?.id === m.id} onclick={() => actAs(m.id)}>
            <span>{m.name ?? m.email}</span><span class="hint" style="margin-left:auto">{m.role}</span>
          </button>
        {/each}
        <button type="button" class="mi" role="menuitem" onclick={resetSample}>Reset sample data</button>
      {/if}
      <hr />
      <button type="button" class="mi" role="menuitem" onclick={signOut}>Sign out</button>
    </div>
  {/if}
</div>
