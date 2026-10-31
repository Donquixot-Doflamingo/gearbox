<script lang="ts">
  import { fmtMicros } from '$lib/model/format';

  let { series, targetMicros = 1000 }: { series: { at: string; p99Micros: number }[]; targetMicros?: number } = $props();

  const W = 600, H = 140, L = 48, R = 592, B = 112, T = 14;
  let max = $derived(Math.max(targetMicros * 1.1, ...series.map((p) => p.p99Micros)) || 1);
  let x = $derived((i: number) => L + (series.length > 1 ? (i * (R - L)) / (series.length - 1) : 0));
  let y = $derived((v: number) => B - (v / max) * (B - T));
  let line = $derived(series.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)} ${y(p.p99Micros).toFixed(1)}`).join(' '));
  let area = $derived(series.length ? `${line} L${x(series.length - 1).toFixed(1)} ${B} L${L} ${B} Z` : '');
  let lastV = $derived(series.at(-1)?.p99Micros ?? 0);
  let lo = $derived(Math.min(...series.map((p) => p.p99Micros)));
  let hi = $derived(Math.max(...series.map((p) => p.p99Micros)));
  let summary = $derived(series.length ? `p99 latency ranged from ${fmtMicros(lo)} to ${fmtMicros(hi)}; now ${fmtMicros(lastV)}, target under ${fmtMicros(targetMicros)}` : 'No latency data');
</script>

{#if series.length}
  <svg class="chart" viewBox="0 0 {W} {H}" role="img" aria-label={summary}>
    <line class="target" x1={L} x2={R} y1={y(targetMicros)} y2={y(targetMicros)} />
    <text x={L - 6} y={y(targetMicros) + 4} text-anchor="end">{fmtMicros(targetMicros)}</text>
    <text class="targetlabel" x={R} y={y(targetMicros) - 6} text-anchor="end">target</text>
    <line class="grid" x1={L} x2={R} y1={y(targetMicros / 2)} y2={y(targetMicros / 2)} />
    <text x={L - 6} y={y(targetMicros / 2) + 4} text-anchor="end">{fmtMicros(targetMicros / 2)}</text>
    <line class="grid" x1={L} x2={R} y1={B} y2={B} />
    <text x={L - 6} y={B + 4} text-anchor="end">0</text>
    <path class="area" d={area} />
    <path class="line" d={line} />
    <circle class="end" cx={x(series.length - 1)} cy={y(lastV)} r="4" />
    <text class="endlabel" x={x(series.length - 1) - 8} y={y(lastV) - 10} text-anchor="end">{fmtMicros(lastV)}</text>
    <text x={L} y="132">24 h ago</text>
    <text x={R} y="132" text-anchor="end">now</text>
  </svg>
{:else}
  <p class="hint">No latency data yet. Sidecars report p99 with their heartbeat every 30 s.</p>
{/if}
