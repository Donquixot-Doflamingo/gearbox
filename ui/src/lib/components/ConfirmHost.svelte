<script lang="ts">
  import { confirmState, settleConfirm } from '$lib/stores/confirm.svelte';
  import Dialog from './Dialog.svelte';

  let open = $state(false);
  let reason = $state('');
  let typed = $state('');
  let req = $derived(confirmState.current);

  $effect(() => {
    if (req) {
      reason = '';
      typed = '';
      open = true;
    } else open = false;
  });

  let paragraphs = $derived(req?.body === undefined ? [] : Array.isArray(req.body) ? req.body : [req.body]);
  let blocked = $derived(!!req && ((!!req.reason?.required && !reason.trim()) || (!!req.typeToConfirm && typed.trim() !== req.typeToConfirm)));
</script>

{#if req}
  <Dialog bind:open title={req.title} onclose={() => settleConfirm(null)}>
    {#if req.env}
      <p class="env-callout" class:calm={req.env !== 'prod' && !req.danger}>Target: {req.env}{req.envDetail ? ` · ${req.envDetail}` : ''}</p>
    {/if}
    {#each paragraphs as p, i (i)}<p>{p}</p>{/each}
    {#if req.reason}
      <div class="field">
        <label for="confirm-reason">{req.reason.label}{req.reason.required ? ' *' : ''}</label>
        <textarea id="confirm-reason" bind:value={reason} placeholder={req.reason.placeholder} rows="2" data-autofocus></textarea>
      </div>
    {/if}
    {#if req.typeToConfirm}
      <div class="field">
        <label for="confirm-type">Type <code class="inl">{req.typeToConfirm}</code> to confirm</label>
        <input id="confirm-type" type="text" bind:value={typed} autocomplete="off" />
      </div>
    {/if}
    {#snippet actions()}
      <button type="button" class="btn" onclick={() => settleConfirm(null)} data-autofocus={req?.reason || req?.typeToConfirm ? undefined : true}>Cancel</button>
      <button type="button" class="btn {req?.danger === false ? 'btn-primary' : 'btn-danger'}" disabled={blocked} onclick={() => settleConfirm({ reason: reason.trim() })}>{req?.ok ?? 'Confirm'}</button>
    {/snippet}
  </Dialog>
{/if}
