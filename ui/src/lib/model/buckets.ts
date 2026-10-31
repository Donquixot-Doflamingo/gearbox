// Bucketing used by rollouts, segments and experiments: murmur3_32(salt + key) mod 10000.

import type { ExperimentSpec } from '../api/types.gen';

export const BUCKETS = 10000;

/** MurmurHash3 x86 32-bit over the UTF-8 bytes of `s`. */
export function murmur3(s: string, seed = 0): number {
  const data = new TextEncoder().encode(s);
  let h = seed >>> 0;
  const c1 = 0xcc9e2d51, c2 = 0x1b873593;
  const n = data.length - (data.length % 4);
  for (let i = 0; i < n; i += 4) {
    let k = (data[i] | (data[i + 1] << 8) | (data[i + 2] << 16) | (data[i + 3] << 24)) >>> 0;
    k = Math.imul(k, c1);
    k = (k << 15) | (k >>> 17);
    k = Math.imul(k, c2);
    h ^= k;
    h = (h << 13) | (h >>> 19);
    h = (Math.imul(h, 5) + 0xe6546b64) >>> 0;
  }
  let k = 0;
  const tail = data.length & 3;
  if (tail === 3) k ^= data[n + 2] << 16;
  if (tail >= 2) k ^= data[n + 1] << 8;
  if (tail >= 1) {
    k ^= data[n];
    k = Math.imul(k, c1);
    k = (k << 15) | (k >>> 17);
    k = Math.imul(k, c2);
    h ^= k;
  }
  h ^= data.length;
  h ^= h >>> 16;
  h = Math.imul(h, 0x85ebca6b);
  h ^= h >>> 13;
  h = Math.imul(h, 0xc2b2ae35);
  h ^= h >>> 16;
  return h >>> 0;
}

export function bucket(salt: string, key: string): number {
  return murmur3(salt + key) % BUCKETS;
}

export interface BucketRange {
  name: string;
  kind: 'holdout' | 'variant';
  from: number;
  /** Inclusive. */
  to: number;
  share: number;
}

/** Holdout first, then variants by weight over the rest, as consecutive ranges of 0–9999. */
export function allocate(spec: Pick<ExperimentSpec, 'holdoutPercent' | 'variants'>): BucketRange[] {
  const out: BucketRange[] = [];
  const hold = Math.round(((spec.holdoutPercent ?? 0) / 100) * BUCKETS);
  let at = 0;
  if (hold > 0) {
    out.push({ name: 'holdout', kind: 'holdout', from: 0, to: hold - 1, share: hold / BUCKETS });
    at = hold;
  }
  const rest = BUCKETS - at;
  const total = spec.variants.reduce((s, v) => s + Math.max(0, v.weight || 0), 0);
  if (total <= 0) return out;
  let acc = 0;
  spec.variants.forEach((v, i) => {
    acc += Math.max(0, v.weight || 0);
    const end = i === spec.variants.length - 1 ? BUCKETS : at + Math.round((acc / total) * rest);
    const from = out.length && out.at(-1)!.kind === 'variant' ? out.at(-1)!.to + 1 : at;
    if (end - from > 0) out.push({ name: v.name, kind: 'variant', from, to: end - 1, share: (end - from) / BUCKETS });
    else out.push({ name: v.name, kind: 'variant', from, to: from - 1, share: 0 });
  });
  return out;
}
