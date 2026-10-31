<script lang="ts">
  import type { Release } from '$lib/api/types.gen';

  let { release }: { release: Release } = $props();

  let changed = $derived(release.contents.filter((c) => c.changed));
  let text = $derived.by(() => {
    const r = release;
    const list = changed.map((c) => (c.rollout?.rollout ? `${c.name} v${c.version} at ${c.rollout.rollout.percent}% (v${c.rollout.rollout.fromVersion} for the rest)` : `${c.name} v${c.version}`)).join(', ');
    switch (r.type) {
      case 'rollback':
        return `Rolled back${r.reason ? `: ${r.reason}` : ''}${list ? ` (${list})` : ''}`;
      case 'kill_switch':
        return `Kill switch: ${changed.map((c) => c.name).join(', ') || 'gate'}${r.reason ? ` · ${r.reason}` : ''}`;
      case 'rollout':
        return list || r.reason || 'Rollout step';
      case 'data':
        return `Data: ${list || 'segments and lookups'}`;
      default:
        return list || (r.changeRequestId ? `Change request ${r.changeRequestId}` : 'Release');
    }
  });
</script>

{text}
