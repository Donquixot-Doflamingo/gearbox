// Kill-switch state per gate, derived from kill_switch releases. The contract's Release does not yet say
// which way a kill switch went (requested in contracts/CHANGES-REQUESTED.md); servers that add
// `killed` and `gate` to the release are read here, otherwise the state is "unknown".
import type { Release } from './api/types.gen';

export type KillState = { killed: boolean | null; generation: number; at: string; reason?: string };

export function killStates(releases: Release[]): Map<string, KillState> {
  const out = new Map<string, KillState>();
  for (const r of releases) {
    if (r.type !== 'kill_switch') continue;
    const ext = r as Release & { killed?: boolean; gate?: string };
    const gates = ext.gate ? [ext.gate] : r.contents.filter((c) => c.changed && c.kind === 'gates').map((c) => c.name);
    for (const g of gates) if (!out.has(g)) out.set(g, { killed: typeof ext.killed === 'boolean' ? ext.killed : null, generation: r.generation, at: r.publishedAt, reason: r.reason });
  }
  return out;
}
