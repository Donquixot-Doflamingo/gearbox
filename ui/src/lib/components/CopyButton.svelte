<script lang="ts">
  let { text, label = 'Copy' }: { text: string; label?: string } = $props();
  let copied = $state(false);

  async function copy(): Promise<void> {
    try {
      await navigator.clipboard.writeText(text);
      copied = true;
      setTimeout(() => (copied = false), 1600);
    } catch {
      copied = false;
    }
  }
</script>

<button type="button" class="btn btn-sm" onclick={copy} aria-live="polite">{copied ? 'Copied' : label}</button>
