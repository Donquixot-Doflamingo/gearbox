<script lang="ts">
  import { api } from '$lib/api';
  import { ApiError } from '$lib/api/client';
  import type { Member, Role } from '$lib/api/types.gen';
  import Dialog from '$lib/components/Dialog.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Field from '$lib/components/Field.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import { query } from '$lib/query.svelte';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  const ROLES: Role[] = ['viewer', 'author', 'approver', 'admin'];
  const members = query(() => api.listMembers({}).then((r) => r.items));
  let admin = $derived(session.can('admin'));
  let open = $state(false);
  let email = $state('');
  let name = $state('');
  let role: Role = $state('author');
  let apps = $state('');
  let err: ApiError | null = $state(null);

  async function add(): Promise<void> {
    err = null;
    try {
      await api.addMember({ body: { email, name: name || undefined, role, apps: apps.split(',').map((s) => s.trim()).filter(Boolean) } });
      toast(`Added ${email} as ${role}`);
      open = false;
      email = name = apps = '';
      void members.reload();
    } catch (e) {
      if (e instanceof ApiError) err = e;
    }
  }
  async function setRole(m: Member, r: Role): Promise<void> {
    try {
      await api.updateMember({ memberId: m.id, body: { email: m.email, name: m.name, role: r, apps: m.apps } });
      toast(`${m.name ?? m.email} is now ${r}`);
      void members.reload();
    } catch (e) {
      toastError(e, 'Role not changed');
      void members.reload();
    }
  }
  async function remove(m: Member): Promise<void> {
    const r = await confirmAction({ title: `Remove ${m.name ?? m.email}?`, body: 'They lose access to every app at their next request.', ok: 'Remove', danger: true });
    if (!r) return;
    try {
      await api.removeMember({ memberId: m.id });
      toast(`Removed ${m.email}`);
      void members.reload();
    } catch (e) {
      toastError(e, 'Not removed');
    }
  }
</script>

<section class="stack">
  <div class="actions"><button type="button" class="btn btn-primary" onclick={() => (open = true)} disabled={!admin} title={session.why('admin')}>Add member</button></div>
  {#if members.error}<ErrorNote error={members.error} retry={members.reload} />
  {:else if !members.data}<Loading />
  {:else}
    <div class="tbl-wrap">
      <table>
        <thead><tr><th>Name</th><th>Email</th><th>Role</th><th>Apps</th><th></th></tr></thead>
        <tbody>
          {#each members.data as m (m.id)}
            <tr>
              <td>{m.name ?? '—'}{#if m.id === session.member?.id} <span class="pill pill-info">you</span>{/if}</td>
              <td class="mono">{m.email}</td>
              <td>
                <label class="sr" for="role-{m.id}">Role for {m.name ?? m.email}</label>
                <select id="role-{m.id}" value={m.role} disabled={!admin || m.id === session.member?.id} onchange={(e) => setRole(m, e.currentTarget.value as Role)}>{#each ROLES as r (r)}<option value={r}>{r}</option>{/each}</select>
              </td>
              <td class="mono">{m.apps?.length ? m.apps.join(', ') : 'all apps'}</td>
              <td class="num">{#if admin && m.id !== session.member?.id}<button type="button" class="btn btn-sm" onclick={() => remove(m)}>Remove</button>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
  <p class="hint">viewer reads · author drafts, tests and opens change requests · approver approves, rolls out and rolls back · admin manages apps, credentials, members and webhooks. Sign-in is through your OIDC provider.</p>
</section>

<Dialog bind:open title="Add member">
  <form id="add-member" class="stack" onsubmit={(e) => { e.preventDefault(); void add(); }}>
    <Field label="Email" id="m-email" required error={err?.fieldError('/email')}><input id="m-email" type="email" bind:value={email} required data-autofocus /></Field>
    <Field label="Name" id="m-name"><input id="m-name" type="text" bind:value={name} /></Field>
    <Field label="Role" id="m-role"><select id="m-role" bind:value={role}>{#each ROLES as r (r)}<option value={r}>{r}</option>{/each}</select></Field>
    <Field label="Apps" id="m-apps" hint="Comma-separated; empty means all apps"><input id="m-apps" type="text" bind:value={apps} /></Field>
    {#if err && !err.errors.length}<ErrorNote error={err} />{/if}
  </form>
  {#snippet actions()}
    <button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
    <button type="submit" form="add-member" class="btn btn-primary" disabled={!email}>Add</button>
  {/snippet}
</Dialog>
