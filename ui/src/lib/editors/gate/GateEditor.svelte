<script lang="ts">
  import { untrack } from 'svelte';
  import { api } from '$lib/api';
  import type { GateSpec } from '$lib/api/types.gen';
  import Crumbs from '$lib/components/Crumbs.svelte';
  import DraftBar from '$lib/components/DraftBar.svelte';
  import ErrorNote from '$lib/components/ErrorNote.svelte';
  import Loading from '$lib/components/Loading.svelte';
  import PageHeader from '$lib/components/PageHeader.svelte';
  import Problems from '$lib/components/Problems.svelte';
  import SchemaRefPicker from '$lib/components/SchemaRefPicker.svelte';
  import UsagesPanel from '$lib/components/UsagesPanel.svelte';
  import VersionsPanel from '$lib/components/VersionsPanel.svelte';
  import ConditionBuilder from '$lib/editors/condition/ConditionBuilder.svelte';
  import { DraftEditor } from '$lib/draft.svelte';
  import { killStates } from '$lib/killswitch';
  import { newGroup, newLeaf } from '$lib/model/condition';
  import { relTime } from '$lib/model/format';
  import { listFields } from '$lib/model/schemaTree';
  import { appHref, qp } from '$lib/nav.svelte';
  import { query } from '$lib/query.svelte';
  import { confirmAction } from '$lib/stores/confirm.svelte';
  import { registry } from '$lib/stores/registry.svelte';
  import { session } from '$lib/stores/session.svelte';
  import { shell } from '$lib/stores/shell.svelte';
  import { toast, toastError } from '$lib/stores/toast.svelte';

  let { app, name }: { app: string; name: string } = $props();
  const ed = new DraftEditor<GateSpec>(app, 'gates', name);
  void ed.load();
  $effect(() => {
    JSON.stringify(ed.spec);
    if (ed.spec) untrack(() => ed.scheduleValidate());
  });

  let tab = $derived(qp('tab') ?? 'rules');
  let canEdit = $derived(session.can('edit'));
  let g = $derived(ed.spec);
  let attrSchema = $derived(registry.schema(app, g?.attributesSchema?.name, g?.attributesSchema?.version));
  let fields = $derived(attrSchema ? listFields(attrSchema) : null);

  const kills = query(async () => {
    const envs = shell.envs;
    const all = await Promise.all(envs.map((e) => api.listReleases({ app, env: e, query: { limit: 200 } }).then((r) => [e, killStates(r.items).get(name)] as const)));
    return new Map(all);
  });

  function addRule(): void {
    let i = (g.rules?.length ?? 0) + 1;
    while (g.rules.some((r) => r.id === `rule_${i}`)) i++;
    g.rules = [...(g.rules ?? []), { id: `rule_${i}`, when: newGroup('all', [newLeaf('field')]), value: true }];
  }
  function move(i: number, d: number): void {
    const j = i + d;
    if (j < 0 || j >= g.rules.length) return;
    const [x] = g.rules.splice(i, 1);
    g.rules.splice(j, 0, x);
  }

  async function kill(env: string, killed: boolean): Promise<void> {
    const r = await confirmAction({
      title: killed ? `Turn on the kill switch for ${name}?` : `Turn off the kill switch for ${name}?`,
      env,
      envDetail: 'new generation',
      body: killed ? [`${name} returns false for every unit in ${env} within seconds, whatever its rules say.`, 'This publishes a new bundle generation without review and is recorded in the audit trail.'] : [`${name} goes back to its rules in ${env}.`],
      ok: killed ? 'Turn off for everyone' : 'Restore the rules',
      danger: killed || env === 'prod',
      reason: { label: 'Reason', required: true, placeholder: killed ? 'e.g. auction service down' : 'e.g. service recovered' },
    });
    if (!r) return;
    try {
      const rel = await api.publishRelease({ app, env, body: { type: 'kill_switch', gate: name, killed, reason: r.reason } });
      toast(`Published generation ${rel.generation} to ${env}: kill switch ${killed ? 'on' : 'off'}`);
      void kills.reload();
    } catch (e) {
      toastError(e, 'Kill switch not changed');
    }
  }
  const TABS = [['rules', 'Rules'], ['kill', 'Kill switch'], ['versions', 'Versions'], ['usages', 'Used by']] as const;
</script>

<PageHeader title={name}>
  {#snippet eyebrow()}<Crumbs parts={[{ label: 'gates', href: appHref('/gates') }]} />{/snippet}
  {#snippet sub()}{ed.description || 'Gate'}{/snippet}
</PageHeader>

{#if ed.loadError}
  <ErrorNote error={ed.loadError} retry={() => ed.load()} />
{:else if ed.loading && !ed.resource}
  <Loading />
{:else if g}
  <DraftBar {ed} />
  <nav class="tabs" aria-label="Gate sections">
    {#each TABS as [id, label] (id)}<a href={appHref(`/gates/${name}`, { tab: id === 'rules' ? undefined : id })} aria-current={tab === id ? 'page' : undefined}>{label}</a>{/each}
  </nav>
  {#if tab === 'rules'}
    <section class="panel">
      <fieldset disabled={!canEdit} class="stack">
        <div class="form-grid">
          <div class="field"><label for="gt-desc">Description</label><input id="gt-desc" type="text" bind:value={ed.description} /></div>
          <label class="check" style="align-self:end;padding-bottom:8px"><input type="checkbox" checked={g.enabled !== false} onchange={(e) => (g.enabled = e.currentTarget.checked)} /> Enabled (when off, the gate returns false)</label>
          <div class="field">
            <label for="gt-default">Default (no rule matched)</label>
            <select id="gt-default" value={String(!!g.default)} onchange={(e) => (g.default = e.currentTarget.value === 'true')}><option value="false">false</option><option value="true">true</option></select>
          </div>
        </div>
        <SchemaRefPicker {app} value={g.attributesSchema} onchange={(v) => (g.attributesSchema = v)} label="Attributes schema (optional)" hint="Type-checks the rule conditions; without it, type field paths by hand." />
        <h3 class="sub-h">Rules (top to bottom; first match wins)</h3>
        {#each g.rules ?? [] as r, i (i)}
          <div class="panel tight" style="background:var(--bg)">
            <div class="form-row">
              <div class="field" style="width:160px"><label for="gt-rid-{i}">ID</label><input id="gt-rid-{i}" type="text" class="code" bind:value={r.id} /></div>
              <div class="field" style="width:120px"><label for="gt-rv-{i}">Returns</label><select id="gt-rv-{i}" value={String(r.value)} onchange={(e) => (r.value = e.currentTarget.value === 'true')}><option value="true">true</option><option value="false">false</option></select></div>
              <label class="check" style="padding-bottom:8px"><input type="checkbox" checked={!!r.rollout} onchange={(e) => (r.rollout = e.currentTarget.checked ? { percent: 10 } : undefined)} /> Rollout</label>
              {#if r.rollout}
                <div class="field" style="width:110px"><label for="gt-rp-{i}">Percent</label><input id="gt-rp-{i}" type="number" min="0" max="100" step="0.5" value={r.rollout.percent ?? 0} oninput={(e) => (r.rollout = { ...r.rollout, percent: Number(e.currentTarget.value) })} /></div>
                <div class="field grow"><label for="gt-rs-{i}">Salt</label><input id="gt-rs-{i}" type="text" class="code" value={r.rollout.salt ?? ''} placeholder="{name}.{r.id}" oninput={(e) => (r.rollout = { ...r.rollout, salt: e.currentTarget.value || undefined })} /></div>
              {/if}
              <span class="spacer"></span>
              <div class="row-sm">
                <button type="button" class="btn btn-xs btn-ghost" onclick={() => move(i, -1)} disabled={i === 0} aria-label="Move {r.id} up">↑</button>
                <button type="button" class="btn btn-xs btn-ghost" onclick={() => move(i, 1)} disabled={i === g.rules.length - 1} aria-label="Move {r.id} down">↓</button>
                <button type="button" class="btn btn-xs" onclick={() => g.rules.splice(i, 1)}>Delete</button>
              </div>
            </div>
            <ConditionBuilder bind:value={r.when} {app} {fields} subject={g.attributesSchema ? g.attributesSchema.name : 'the attributes'} pointer="/rules/{i}/when" problems={ed.problems} exprBase="attributes" />
          </div>
        {:else}
          <p class="hint">No rules: every unit gets the default ({String(!!g.default)}).</p>
        {/each}
        <div><button type="button" class="btn btn-sm" onclick={addRule}>Add rule</button></div>
      </fieldset>
      <Problems problems={ed.problems.filter((p) => !p.path.includes('/when'))} showPath />
    </section>
  {:else if tab === 'kill'}
    <section class="panel">
      <header><h2>Kill switch</h2><span class="hint">publishes a new generation immediately, without review</span></header>
      {#if kills.error}<ErrorNote error={kills.error} retry={kills.reload} />{/if}
      <div class="tbl-wrap"><table>
        <thead><tr><th>Environment</th><th>State</th><th>Last change</th><th></th></tr></thead>
        <tbody>
          {#each shell.envs as e (e)}
            {@const k = kills.data?.get(e)}
            <tr>
              <td class="mono">{e}</td>
              <td>{#if k?.killed === true}<span class="pill pill-crit">killed: false for everyone</span>{:else if k?.killed === null}<span class="pill pill-warn">changed (direction not reported)</span>{:else}<span class="pill pill-ok">following its rules</span>{/if}</td>
              <td class="muted">{k ? `generation ${k.generation} · ${relTime(k.at)}${k.reason ? ` · ${k.reason}` : ''}` : '—'}</td>
              <td class="nowrap">
                {#if k?.killed !== true}<button type="button" class="btn btn-sm btn-danger" onclick={() => kill(e, true)} disabled={!session.can('release')} title={session.why('release')}>Kill in {e}</button>{/if}
                {#if k && k.killed !== false}<button type="button" class="btn btn-sm" onclick={() => kill(e, false)} disabled={!session.can('release')} title={session.why('release')}>Restore in {e}</button>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table></div>
      <p class="hint">Sidecars swap within seconds. Approver role needed. Every use is in the audit trail.</p>
    </section>
  {:else if tab === 'versions'}
    <VersionsPanel {ed} kind="gates" />
  {:else if tab === 'usages'}
    <UsagesPanel {app} kind="gates" {name} />
  {/if}
{/if}
