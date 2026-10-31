import { describe, expect, it } from 'vitest';
import {
  addProperty,
  compatibility,
  exampleFor,
  getAt,
  isNullable,
  lintSchema,
  listFields,
  moveProperty,
  primaryType,
  removeProperty,
  renameProperty,
  setRequired,
  typeLabel,
  validateDocument,
  withNullable,
  withType,
  type JsonSchema,
} from './schemaTree';

const lead: JsonSchema = {
  type: 'object',
  required: ['lead', 'car'],
  properties: {
    lead: { type: 'object', required: ['id', 'city'], properties: { id: { type: 'string' }, first_name: { type: ['string', 'null'] }, phone: { type: 'string', 'x-gearbox-pii': true }, city: { type: 'string' }, source: { enum: ['app', 'web', 'partner'] } } },
    car: { type: 'object', required: ['year', 'km', 'price'], properties: { year: { type: 'integer', minimum: 1980 }, km: { type: 'integer', minimum: 0 }, price: { type: 'number', minimum: 0 } } },
    inspections: { type: 'array', maxItems: 50, items: { type: 'object', required: ['part', 'score'], properties: { part: { type: 'string' }, score: { type: 'number' } } } },
  },
};

describe('types and nullability', () => {
  it('reads types', () => {
    expect(primaryType(getAt(lead, ['properties', 'lead', 'properties', 'first_name']))).toBe('string');
    expect(isNullable(getAt(lead, ['properties', 'lead', 'properties', 'first_name']))).toBe(true);
    expect(primaryType({ enum: ['a'] })).toBe('string');
    expect(typeLabel({ type: ['integer', 'null'] })).toBe('integer?');
    expect(typeLabel((lead.properties as Record<string, JsonSchema>).inspections)).toBe('array<object>');
  });
  it('toggles null and changes type', () => {
    expect(withNullable({ type: 'string' }, true)).toEqual({ type: ['string', 'null'] });
    expect(withNullable({ type: ['string', 'null'] }, false)).toEqual({ type: 'string' });
    expect(withNullable({ enum: ['a', 'b'] }, true)).toEqual({ enum: ['a', 'b', null] });
    const t = withType({ type: ['string', 'null'], maxLength: 4, description: 'd' }, 'integer');
    expect(t).toEqual({ type: ['integer', 'null'], description: 'd' });
    expect(withType({ type: 'string' }, 'object')).toEqual({ type: 'object', properties: {} });
  });
});

describe('editing properties', () => {
  it('adds with a unique name, renames keeping order and required, removes', () => {
    let { root, name } = addProperty(lead, ['properties', 'car'], 'make');
    expect(name).toBe('make');
    ({ root, name } = addProperty(root, ['properties', 'car'], 'make'));
    expect(name).toBe('make_2');
    root = renameProperty(root, ['properties', 'car'], 'year', 'model_year');
    const car = getAt(root, ['properties', 'car'])!;
    expect(Object.keys(car.properties as object)).toEqual(['model_year', 'km', 'price', 'make', 'make_2']);
    expect(car.required).toEqual(['model_year', 'km', 'price']);
    root = removeProperty(root, ['properties', 'car'], 'km');
    expect(getAt(root, ['properties', 'car'])!.required).toEqual(['model_year', 'price']);
    expect(() => renameProperty(root, ['properties', 'car'], 'price', 'make')).toThrow('already exists');
    expect(() => renameProperty(root, ['properties', 'car'], 'price', '1x')).toThrow('not a valid');
  });
  it('sets required in property order and moves properties', () => {
    let root = setRequired(lead, ['properties', 'lead'], 'phone', true);
    expect(getAt(root, ['properties', 'lead'])!.required).toEqual(['id', 'phone', 'city']);
    root = setRequired(root, ['properties', 'lead'], 'id', false);
    expect(getAt(root, ['properties', 'lead'])!.required).toEqual(['phone', 'city']);
    root = moveProperty(root, ['properties', 'lead'], 'city', -1);
    expect(Object.keys(getAt(root, ['properties', 'lead'])!.properties as object).slice(2, 4)).toEqual(['city', 'phone']);
  });
});

describe('fields, validation, compatibility, examples', () => {
  it('lists fields with array items marked', () => {
    const f = listFields(lead);
    expect(f.map((x) => x.path)).toEqual(['lead', 'lead.id', 'lead.first_name', 'lead.phone', 'lead.city', 'lead.source', 'car', 'car.year', 'car.km', 'car.price', 'inspections', 'inspections[].part', 'inspections[].score']);
    expect(f.find((x) => x.path === 'lead.phone')!.pii).toBe(true);
    expect(f.find((x) => x.path === 'lead.id')!.required).toBe(true);
    expect(f.find((x) => x.path === 'inspections[].part')!.inArray).toBe(true);
  });
  it('validates documents', () => {
    const errs = validateDocument(lead, { lead: { id: 'L-1', city: 'Pune', source: 'fax' }, car: { make: 'Maruti', year: 2019 }, inspections: [{ part: 'x', score: 'high' }] });
    expect(errs.map((e) => e.path).sort()).toEqual(['/car/km', '/car/price', '/inspections/0/score', '/lead/source'].sort());
    expect(validateDocument(lead, exampleFor(lead))).toEqual([]);
    expect(validateDocument({ type: 'integer' }, 2.5)).toHaveLength(1);
  });
  it('detects breaking and compatible changes', () => {
    const next = JSON.parse(JSON.stringify(lead));
    delete next.properties.lead.properties.phone;
    next.properties.lead.properties.source.enum = ['app', 'web'];
    next.properties.lead.properties.first_name.type = 'string';
    next.properties.car.required.push('make');
    next.properties.car.properties.make = { type: 'string' };
    next.properties.car.properties.fuel = { type: 'string' };
    const c = compatibility(lead, next);
    const kinds = c.map((x) => `${x.kind}:${x.breaking}`);
    expect(kinds).toContain('property_removed:true');
    expect(kinds).toContain('enum_narrowed:true');
    expect(kinds).toContain('null_removed:true');
    expect(kinds).toContain('property_added:false');
    expect(c.find((x) => x.path === '/properties/car/properties/make')!.breaking).toBe(true);
    expect(compatibility(lead, lead)).toEqual([]);
  });
  it('lints bad schemas', () => {
    expect(lintSchema({ type: 'object', required: ['x'], properties: {} }).map((e) => e.message)).toEqual(['required "x" is not a property']);
    expect(lintSchema({ type: 'string', pattern: '(' })).toHaveLength(1);
  });
});
