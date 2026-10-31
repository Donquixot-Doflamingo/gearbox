import { describe, expect, it } from 'vitest';
import { cellIssue, cellToExpr, matchCell, outputCellText, parseCell, parseOutputCell } from './tableCell';

describe('parseCell', () => {
  it('parses every syntax', () => {
    expect(parseCell('-')).toEqual({ kind: 'any' });
    expect(parseCell('')).toEqual({ kind: 'any' });
    expect(parseCell('< 300000')).toEqual({ kind: 'compare', op: '<', value: 300000 });
    expect(parseCell('>=5')).toEqual({ kind: 'compare', op: '>=', value: 5 });
    expect(parseCell('= "Pune"')).toEqual({ kind: 'compare', op: '==', value: 'Pune' });
    expect(parseCell('[300000..800000]')).toEqual({ kind: 'range', lo: 300000, hi: 800000 });
    expect(parseCell('[-1.5 .. 2]')).toEqual({ kind: 'range', lo: -1.5, hi: 2 });
    expect(parseCell('["Gurgaon","Delhi"]')).toEqual({ kind: 'list', values: ['Gurgaon', 'Delhi'] });
    expect(parseCell('"text"')).toEqual({ kind: 'literal', value: 'text' });
    expect(parseCell('42')).toEqual({ kind: 'literal', value: 42 });
    expect(parseCell('true')).toEqual({ kind: 'literal', value: true });
    expect(parseCell('value % 2 == 0')).toEqual({ kind: 'expr', expr: 'value % 2 == 0' });
    expect(parseCell('[5..1]').kind).toBe('invalid');
    expect(parseCell('[1, 2').kind).toBe('invalid');
  });

  it('flags likely mistakes', () => {
    expect(cellIssue('Gurgaon')?.level).toBe('warning');
    expect(cellIssue('(value > 1')?.level).toBe('error');
    expect(cellIssue('[5..1]')?.level).toBe('error');
    expect(cellIssue('"Gurgaon"')).toBeNull();
  });
});

describe('matchCell', () => {
  it('matches values', () => {
    expect(matchCell(parseCell('< 300000'), 250000)).toBe(true);
    expect(matchCell(parseCell('< 300000'), 300000)).toBe(false);
    expect(matchCell(parseCell('[300000..800000]'), 800000)).toBe(true);
    expect(matchCell(parseCell('[300000..800000]'), 850000)).toBe(false);
    expect(matchCell(parseCell('["Gurgaon","Delhi"]'), 'Delhi')).toBe(true);
    expect(matchCell(parseCell('-'), undefined)).toBe(true);
    expect(matchCell(parseCell('<= 5'), null)).toBe(false);
    expect(matchCell(parseCell('"5"'), 5)).toBe(true);
    expect(matchCell(parseCell('value > 3'), 4)).toBeUndefined();
    expect(matchCell(parseCell('value > 3'), 4, (_e, v) => (v as number) > 3)).toBe(true);
  });

  it('builds expressions and round-trips output cells', () => {
    expect(cellToExpr(parseCell('[1..2]'), 'price')).toBe('price >= 1 && price <= 2');
    expect(cellToExpr(parseCell('value % 2 == 0'), 'age')).toBe('age % 2 == 0');
    expect(parseOutputCell('15000')).toBe(15000);
    expect(parseOutputCell('A')).toBe('A');
    expect(parseOutputCell('"15000"')).toBe('15000');
    expect(parseOutputCell('=input.x')).toBe('=input.x');
    expect(outputCellText('15000')).toBe('"15000"');
    expect(outputCellText('A')).toBe('A');
    expect(outputCellText(15000)).toBe('15000');
  });
});
