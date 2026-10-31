import { describe, expect, it } from 'vitest';
import type { Condition } from '../api/types.gen';
import {
  appendChild,
  checkCondition,
  condKind,
  condPointer,
  describe as describeCond,
  getCond,
  moveChild,
  newLeaf,
  operatorsFor,
  parseValue,
  removeCond,
  replaceCond,
  setGroupKind,
  toExpr,
  unwrapCond,
  withOperator,
  wrapCond,
} from './condition';
import type { FieldInfo } from './schemaTree';

const tree: Condition = {
  all: [
    { field: 'price', op: 'BETWEEN', value: 300000, valueTo: 2500000 },
    { any: [{ field: 'source', op: 'IN', value: ['app', 'web'] }, { segment: 'metro_premium', op: 'NOT_IN_SEGMENT', attributes: { city: 'input.city' } }] },
  ],
};

describe('condition tree edits', () => {
  it('identifies kinds', () => {
    expect(condKind(tree)).toBe('all');
    expect(condKind(getCond(tree, [1])!)).toBe('any');
    expect(condKind(getCond(tree, [1, 1])!)).toBe('segment');
    expect(condKind({ experiment: 'x', variants: ['a'] })).toBe('variant');
  });

  it('replaces immutably', () => {
    const next = replaceCond(tree, [1, 0], { field: 'source', op: 'EQ', value: 'app' });
    expect(getCond(next, [1, 0])).toEqual({ field: 'source', op: 'EQ', value: 'app' });
    expect(getCond(tree, [1, 0])).toEqual({ field: 'source', op: 'IN', value: ['app', 'web'] });
  });

  it('removes and collapses empty groups', () => {
    const one = removeCond(tree, [1, 0])!;
    expect((getCond(one, [1]) as { any: Condition[] }).any).toHaveLength(1);
    const two = removeCond(one, [1, 0])!;
    expect((two as { all: Condition[] }).all).toHaveLength(1);
    expect(removeCond(tree, [])).toBeNull();
  });

  it('removing the child of a not removes the not', () => {
    const t: Condition = { all: [{ not: { expr: 'true' } }, { expr: 'false' }] };
    expect(removeCond(t, [0, 0])).toEqual({ all: [{ expr: 'false' }] });
  });

  it('appends, moves, wraps, unwraps and switches group kind', () => {
    let t = appendChild(tree, [], newLeaf('gate'));
    expect((t as { all: Condition[] }).all).toHaveLength(3);
    t = moveChild(t, [], 2, 0);
    expect(condKind(getCond(t, [0])!)).toBe('gate');
    t = wrapCond(t, [0], 'not');
    expect(condKind(getCond(t, [0])!)).toBe('not');
    t = unwrapCond(t, [0]);
    expect(condKind(getCond(t, [0])!)).toBe('gate');
    t = setGroupKind(t, [], 'any');
    expect(condKind(t)).toBe('any');
  });

  it('builds JSON pointers for server problems', () => {
    expect(condPointer(tree, '/nodes/1/rules/0/when', [1, 1])).toBe('/nodes/1/rules/0/when/all/1/any/1');
    expect(condPointer({ not: { expr: 'x' } }, '/w', [0])).toBe('/w/not');
  });
});

describe('operators and values', () => {
  it('filters operators by field type', () => {
    const num = operatorsFor('integer').map((o) => o.op);
    expect(num).toContain('BETWEEN');
    expect(num).not.toContain('STARTS_WITH');
    const str = operatorsFor('string').map((o) => o.op);
    expect(str).toContain('MATCHES');
    expect(str).not.toContain('GT');
    expect(operatorsFor('array').map((o) => o.op)).toContain('CONTAINS_ANY');
    expect(operatorsFor('any').length).toBe(35);
  });

  it('parses values per operator', () => {
    expect(parseValue('GT', 'integer', '12')).toEqual({ value: 12 });
    expect(parseValue('GT', 'integer', 'twelve').error).toMatch('not a number');
    expect(parseValue('IN', 'string', 'app, web, "a,b"')).toEqual({ value: ['app', 'web', 'a,b'] });
    expect(parseValue('IN_NUMERIC', 'string', '[1, 2]')).toEqual({ value: [1, 2] });
    expect(parseValue('BETWEEN', 'number', '10', '5').error).toMatch('greater');
    expect(parseValue('EXISTS', 'string', '')).toEqual({});
    expect(parseValue('MATCHES', 'string', '(').error).toMatch('regular expression');
    expect(parseValue('EQ', 'string', '"Pune"')).toEqual({ value: 'Pune' });
  });

  it('reshapes value when the operator changes', () => {
    expect(withOperator({ field: 'a', op: 'EQ', value: 'x' }, 'IN')).toEqual({ field: 'a', op: 'IN', value: ['x'] });
    expect(withOperator({ field: 'a', op: 'IN', value: ['x', 'y'] }, 'EQ')).toEqual({ field: 'a', op: 'EQ', value: 'x' });
    expect(withOperator({ field: 'a', op: 'EQ', value: 'x' }, 'EXISTS')).toEqual({ field: 'a', op: 'EXISTS' });
  });
});

describe('describe, toExpr and checks', () => {
  it('describes and compiles', () => {
    expect(describeCond(tree)).toMatch('price between 300000 and 2500000');
    const e = toExpr(tree);
    expect(e).toMatch('input.price >= 300000 && input.price <= 2500000');
    expect(e).toMatch('!inSegment("metro_premium", {"city": input.city})');
  });

  it('finds unknown fields, bad operators and empty values', () => {
    const fields: FieldInfo[] = [
      { path: 'price', name: 'price', type: 'number', types: ['number'], nullable: false, required: true, pii: false, depth: 0, inArray: false, hasChildren: false },
      { path: 'source', name: 'source', type: 'string', types: ['string'], nullable: false, required: false, pii: false, depth: 0, inArray: false, hasChildren: false },
    ];
    const c: Condition = { all: [{ field: 'nope', op: 'EQ', value: 1 }, { field: 'source', op: 'GT', value: 3 }, { field: 'price', op: 'IN', value: [] }] };
    const problems = checkCondition(c, { fields, segments: [] });
    expect(problems.map((p) => p.message)).toEqual(['nope is not in the input schema', '> does not apply to string', 'enter a value']);
    expect(problems[1].path).toEqual([1]);
  });
});
