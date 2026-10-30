<script lang="ts">
  let { type, label }: { type: string; label?: string } = $props();
  const VAR: Record<string, string> = {
    rules: '--t-rules', table: '--t-table', transform: '--t-transform', switch: '--t-switch', ai: '--t-ai', output: '--t-output',
    lookup: '--t-lookup', lookups: '--t-lookup', graph: '--t-graph', gate: '--t-gate', gates: '--t-gate', experiment: '--t-experiment',
    experiments: '--t-experiment', subdecision: '--t-subdecision', end: '--t-switch', static: '--t-table', dynamic: '--t-transform', rollout: '--t-ai',
    schemas: '--t-rules', decisions: '--t-graph', segments: '--t-table', layers: '--t-subdecision',
  };
  let color = $derived(`var(${VAR[type] ?? '--muted'})`);
</script>

<span class="kind" style:--k={color}>{label ?? type}</span>
