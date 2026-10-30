import { describe, expect, it } from 'vitest';
import type { DecisionSpec } from '../api/types.gen';
import { addNode, autoLayout, connect, cycleNodes, disconnect, edgesOf, graphProblems, positions, problemsByNode, reachable, removeNode, renameNode } from './graph';

function spec(): DecisionSpec {
  return {
    entry: 'a',
    nodes: [
      { id: 'a', type: 'transform', fields: [], next: 'sw' },
      { id: 'sw', type: 'switch', cases: [{ id: 'yes', when: 'true', next: 'b' }], default: 'c' },
      { id: 'b', type: 'output', fields: [], onError: 'c' },
      { id: 'c', type: 'transform', fields: [], next: 'end' },
    ],
  };
}

describe('graph model', () => {
  it('derives edges', () => {
    const e = edgesOf(spec()).map((x) => `${x.from}>${x.to}:${x.kind}`);
    expect(e).toEqual(['a>sw:next', 'sw>b:case', 'sw>c:default', 'b>c:error', 'c>end:next']);
  });
  it('lays out by depth from the entry', () => {
    const p = autoLayout(spec());
    expect(p.a.x).toBeLessThan(p.sw.x);
    expect(p.sw.x).toBeLessThan(p.b.x);
    expect(p.b.x).toBe(p.c.x);
    expect(positions(spec()).a).toEqual(p.a);
  });
  it('connects, disconnects, adds, removes and renames', () => {
    const s = spec();
    expect(connect(s, 'sw', 'a', 'next')).toBe('case');
    expect((s.nodes[1] as { cases: unknown[] }).cases).toHaveLength(2);
    expect(cycleNodes(s).has('a')).toBe(true);
    const back = edgesOf(s).find((e) => e.from === 'sw' && e.to === 'a')!;
    disconnect(s, back);
    expect(cycleNodes(s).size).toBe(0);
    const id = addNode(s, 'rules');
    expect(id).toBe('rules');
    expect(reachable(s).has('rules')).toBe(false);
    renameNode(s, 'c', 'reject');
    expect(edgesOf(s).filter((e) => e.to === 'reject')).toHaveLength(2);
    expect(() => renameNode(s, 'b', 'a')).toThrow('already used');
    removeNode(s, 'reject');
    expect(edgesOf(s).some((e) => e.to === 'reject')).toBe(false);
    expect((s.nodes[1] as { default: string }).default).toBe('');
  });
  it('reports shape problems by node', () => {
    const s = spec();
    s.nodes.push({ id: 'lonely', type: 'output', fields: [] });
    s.nodes[0].next = 'ghost';
    const p = graphProblems(s);
    expect(p.map((x) => x.code)).toContain('UNKNOWN_NODE');
    expect(p.map((x) => x.code)).toContain('UNREACHABLE_NODE');
    const by = problemsByNode(s, p);
    expect(by.get('a')![0].code).toBe('UNKNOWN_NODE');
    expect(by.get('lonely')!.length).toBeGreaterThan(0);
  });
});
