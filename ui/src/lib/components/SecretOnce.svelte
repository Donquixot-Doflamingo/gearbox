<script lang="ts">
  import CopyButton from './CopyButton.svelte';

  let { secret, what = 'secret', note }: { secret: string; what?: string; note?: string } = $props();
</script>

<div class="callout warn">
  <strong>Copy the {what} now. It is shown only once.</strong>
  <div class="secret"><code>{secret}</code><CopyButton text={secret} /></div>
  {#if note}<span class="hint">{note}</span>{/if}
</div>
