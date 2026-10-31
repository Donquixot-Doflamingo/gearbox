import { describe, expect, it } from 'vitest';
import { evaluate, rootNames, syntaxError } from './expr';

const env = {
  input: { lead: { city: 'Gurgaon', source: 'app', first_name: 'Rahul', last_name: 'Sharma' }, car: { year: 2019, price: 525000 }, inspections: [{ part: 'engine', score: 8 }, { part: 'body', score: 5 }] },
  ctx: { now: new Date('2026-10-30T10:00:00Z'), unit: 'L-1' },
  nodes: { eligibility: { eligible: true, lane: 'fast' } },
};

describe('expr evaluator', () => {
  it('evaluates paths, arithmetic and methods', () => {
    expect(evaluate('ctx.now.Year() - input.car.year', env)).toBe(7);
    expect(evaluate('input.car.price / 100000', env)).toBe(5.25);
    expect(evaluate('input.lead.first_name + " " + input.lead.last_name', env)).toBe('Rahul Sharma');
    expect(evaluate('input.missing.deep.path', env)).toBeNull();
  });
  it('handles predicates and builtins', () => {
    expect(evaluate('mean(map(input.inspections ?? [], .score)) ?? 0', env)).toBe(6.5);
    expect(evaluate('map(filter(input.inspections, .score < 6), .part)', env)).toEqual(['body']);
    expect(evaluate('all(input.inspections, #.score > 4)', env)).toBe(true);
    expect(evaluate('upper(input.lead.city)', env)).toBe('GURGAON');
    expect(evaluate('input.lead.city | lower()', env)).toBe('gurgaon');
    expect(evaluate('trimPrefix("+91-98", "+91-")', env)).toBe('98');
    expect(evaluate('len([1, 2, 3])', env)).toBe(3);
  });
  it('handles logic, ternary, in, nil comparisons', () => {
    expect(evaluate('input.lead.source in ["app", "web"] && input.car.price >= 300000', env)).toBe(true);
    expect(evaluate('input.lead.source not in ["app"]', env)).toBe(false);
    expect(evaluate('nodes.eligibility.eligible ? (nodes.eligibility.lane == "fast" ? "fast_track" : "standard") : "reject"', env)).toBe('fast_track');
    expect(evaluate('input.nothing > 3', env)).toBe(false);
    expect(evaluate('input.nothing == nil', env)).toBe(true);
    expect(evaluate('let a = 2; a * 3', env)).toBe(6);
    expect(evaluate('{"part": "x", ok: true}', env)).toEqual({ part: 'x', ok: true });
    expect(evaluate('1..3', env)).toEqual([1, 2, 3]);
    expect(evaluate('"abc" startsWith "a"', env)).toBe(true);
    expect(evaluate('if input.car.year > 2020 { "new" } else { "old" }', env)).toBe('old');
  });
  it('reports errors', () => {
    expect(() => evaluate('input.nothing + 1', env)).toThrow('nil');
    expect(() => evaluate('now()', env)).toThrow('disabled');
    expect(() => evaluate('unknownFn(1)', env)).toThrow('unknown function');
    expect(syntaxError('input.a +')).toMatch('ends too early');
    expect(syntaxError('input.a == "x"')).toBeNull();
  });
  it('uses extra functions and lists root names', () => {
    expect(evaluate('lookup("t", input.lead.city)', env, { fns: { lookup: (_t, k) => (k === 'Gurgaon' ? 'NCR-2' : null) } })).toBe('NCR-2');
    expect(rootNames('nodes.x.y + input.a + len(out.z)').sort()).toEqual(['input', 'nodes', 'out']);
  });
});
