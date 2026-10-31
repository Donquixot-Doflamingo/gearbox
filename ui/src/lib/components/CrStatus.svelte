<script lang="ts">
  import type { ChangeRequest } from '$lib/api/types.gen';
  let { cr }: { cr: ChangeRequest } = $props();
  const MAP: Record<string, [string, string]> = {
    open: ['pill-warn', 'waiting for approval'],
    approved: ['pill-info', 'approved, not published'],
    published: ['pill-ok', 'published'],
    rejected: ['pill-crit', 'changes requested'],
    withdrawn: ['pill-muted', 'withdrawn'],
  };
  let m = $derived(MAP[cr.status] ?? ['pill-muted', cr.status]);
</script>

<span class="pill {m[0]}">{m[1]}{cr.status === 'published' && cr.publishedGeneration ? ` · gen ${cr.publishedGeneration}` : ''}</span>
