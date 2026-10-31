import { describe, expect, it } from 'vitest';
import { allocate, bucket, murmur3 } from './buckets';
import { parseCsv, parseMemberList, toCsv } from './csv';
import { partialMismatches, resolveData } from './dataSlot';
import { diffLines, parseUnified, unifiedDiff } from './diff';
import { fmtMicros } from './format';
import { toYaml } from './yaml';

describe('buckets', () => {
  it('matches reference murmur3 values', () => {
    expect(murmur3('')).toBe(0);
    expect(murmur3('hello')).toBe(613153351);
    expect(murmur3('The quick brown fox jumps over the lazy dog')).toBe(0x2e4ff723);
    expect(bucket('salt', 'unit')).toBeLessThan(10000);
  });
  it('allocates holdout then variants', () => {
    const r = allocate({ holdoutPercent: 5, variants: [{ name: 'control', weight: 50 }, { name: 'a', weight: 25 }, { name: 'b', weight: 25 }] });
    expect(r.map((x) => [x.name, x.from, x.to])).toEqual([['holdout', 0, 499], ['control', 500, 5249], ['a', 5250, 7624], ['b', 7625, 9999]]);
  });
});

describe('csv, data slots, diff, yaml, format', () => {
  it('parses csv with quotes', () => {
    expect(parseCsv('key,value\n"a,b","{""x"":1}"\r\nc,2\n')).toEqual([['key', 'value'], ['a,b', '{"x":1}'], ['c', '2']]);
    expect(toCsv([['a,b', 1]])).toBe('"a,b",1\n');
    expect(parseMemberList('key\nLeh\nKargil\n')).toEqual(['Leh', 'Kargil']);
    expect(parseMemberList('["a", 1]')).toEqual(['a', '1']);
  });
  it('resolves data slots and partial matches', () => {
    expect(resolveData({ a: '=1 + 1', b: '==x', c: ['=2'] }, (e) => (e === '1 + 1' ? 2 : 2))).toEqual({ a: 2, b: '=x', c: [2] });
    expect(partialMismatches({ route: 'fast_track' }, { route: 'standard', reason: 'x' })).toEqual([{ path: '/route', expected: 'fast_track', actual: 'standard' }]);
    expect(partialMismatches({ route: 'x' }, { route: 'x', more: 1 })).toEqual([]);
  });
  it('diffs lines', () => {
    expect(diffLines('a\nb\nc', 'a\nx\nc').map((l) => l.op + l.text)).toEqual([' a', '-b', '+x', ' c']);
    const u = unifiedDiff('a\nb\nc\n', 'a\nx\nc\n', 'v1', 'v2');
    expect(u).toMatch('@@ -1,3 +1,3 @@');
    expect(parseUnified(u).map((l) => l.kind)).toEqual(['file', 'file', 'hunk', 'ctx', 'del', 'add', 'ctx']);
    expect(unifiedDiff('same', 'same')).toBe('');
  });
  it('writes yaml', () => {
    expect(toYaml({ a: 1, b: 'x y', c: [1, 2], d: { e: true }, f: [{ g: 'h' }], s: 'yes' })).toBe('a: 1\nb: x y\nc: [1, 2]\nd:\n  e: true\nf:\n  - g: h\ns: "yes"\n');
  });
  it('formats micros', () => {
    expect(fmtMicros(23)).toBe('23 µs');
    expect(fmtMicros(410)).toBe('410 µs');
    expect(fmtMicros(812000)).toBe('812 ms');
  });
});
