<script lang="ts">
  // Modal dialog: traps focus, closes on Escape and on the scrim, returns focus to the opener.
  import type { Snippet } from 'svelte';

  let {
    open = $bindable(false),
    title,
    wide = false,
    onclose,
    children,
    actions,
  }: { open?: boolean; title: string; wide?: boolean; onclose?: () => void; children: Snippet; actions?: Snippet } = $props();

  let box: HTMLDivElement | undefined = $state();
  let opener: HTMLElement | null = null;
  const id = `dlg-${Math.random().toString(36).slice(2, 8)}`;

  const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

  function close(): void {
    open = false;
    onclose?.();
  }

  $effect(() => {
    if (!open) return;
    opener = document.activeElement as HTMLElement | null;
    const t = setTimeout(() => {
      const first = box?.querySelector<HTMLElement>('[data-autofocus]') ?? box?.querySelector<HTMLElement>(FOCUSABLE);
      (first ?? box)?.focus();
    }, 0);
    return () => {
      clearTimeout(t);
      if (opener && document.contains(opener)) opener.focus();
    };
  });

  function onkeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    if (e.key !== 'Tab' || !box) return;
    const items = [...box.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent !== null);
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="scrim" onclick={(e) => { if (e.target === e.currentTarget) close(); }}>
    <div class="dialog" class:wide bind:this={box} role="dialog" aria-modal="true" aria-labelledby={id} tabindex="-1" {onkeydown}>
      <h2 id={id}>{title}</h2>
      {@render children()}
      {#if actions}<div class="dlg-actions">{@render actions()}</div>{/if}
    </div>
  </div>
{/if}
