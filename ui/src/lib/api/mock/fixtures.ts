// Sample data for mock mode: one fully populated app (lead_service) and a small second app.
// Adapted from the prototype's sample data and the OpenAPI examples.

import type {
  AuditEvent,
  ChangeRequest,
  Credential,
  DecisionLogRecord,
  DecisionSpec,
  Environment,
  ExperimentSpec,
  ExposureRecord,
  GateSpec,
  LayerSpec,
  LookupRow,
  LookupSpec,
  Member,
  PathStep,
  Release,
  ResourceKind,
  SchemaSpec,
  SegmentSpec,
  Sidecar,
  UpdateLogRecord,
  Webhook,
} from '../types.gen';
import type { JsonSchema } from '../../model/schemaTree';

export const MIN = 60_000;
export const HOUR = 60 * MIN;
export const DAY = 24 * HOUR;

export interface StoredVersion {
  version: number;
  message?: string;
  createdAt: string;
  createdBy: string;
  spec: unknown;
  members?: string[];
  rows?: LookupRow[];
  breaking?: boolean;
}

export interface StoredResource {
  kind: ResourceKind;
  name: string;
  description?: string;
  draft: { spec: unknown; revision: number; updatedAt: string; updatedBy: string };
  versions: StoredVersion[];
  /** env → live state */
  live: Record<string, { version: number; rollout?: { fromVersion: number; percent: number } }>;
  members?: string[];
  rows?: LookupRow[];
  stats?: { p99Micros?: number; callsPerMinute?: number; fallbackRate?: number };
  archived?: boolean;
}

export interface AppData {
  resources: StoredResource[];
  environments: Environment[];
  credentials: Credential[];
  changeRequests: ChangeRequest[];
  releases: Record<string, Release[]>;
  sidecars: Record<string, Sidecar[]>;
  decisionLogs: Record<string, DecisionLogRecord[]>;
  updateLogs: Record<string, UpdateLogRecord[]>;
  exposureLogs: Record<string, ExposureRecord[]>;
  killed: Record<string, Record<string, boolean>>;
}

export interface Db {
  fixtureVersion: number;
  meId: string;
  seq: number;
  members: Member[];
  apps: { name: string; description?: string; owners?: string[]; environments: string[]; bundleLimitMB?: number; createdAt: string }[];
  data: Record<string, AppData>;
  audit: AuditEvent[];
  webhooks: Webhook[];
}

export const FIXTURE_VERSION = 3;

const s = (v: JsonSchema): JsonSchema => v;

// ---------------------------------------------------------------- schemas

const leadInputV1 = s({
  type: 'object',
  required: ['lead', 'car'],
  properties: {
    lead: {
      type: 'object',
      required: ['id', 'city'],
      properties: {
        id: { type: 'string' },
        first_name: { type: ['string', 'null'] },
        last_name: { type: ['string', 'null'] },
        phone: { type: 'string', 'x-gearbox-pii': true },
        city: { type: 'string' },
        source: { enum: ['app', 'web', 'partner'] },
      },
    },
    car: {
      type: 'object',
      required: ['year', 'km', 'price'],
      properties: {
        make: { type: 'string' },
        model: { type: 'string' },
        year: { type: 'integer', minimum: 1980 },
        km: { type: 'integer', minimum: 0 },
        price: { type: 'number', minimum: 0 },
      },
    },
    inspections: {
      type: 'array',
      maxItems: 50,
      items: { type: 'object', required: ['part', 'score'], properties: { part: { type: 'string' }, score: { type: 'number' } } },
    },
  },
});

function leadInputV2(): JsonSchema {
  const v = JSON.parse(JSON.stringify(leadInputV1));
  v.properties.lead.properties.email = { type: ['string', 'null'], format: 'email', 'x-gearbox-pii': true, description: 'Customer email, when the lead form asked for it' };
  v.properties.car.properties.fuel = { enum: ['petrol', 'diesel', 'cng', 'electric', null], description: 'Fuel type from the listing' };
  return v;
}

const eligibilityInput = s({
  type: 'object',
  description: 'Normalized lead fields the eligibility rules read',
  required: ['city', 'ageYears', 'km', 'price'],
  properties: {
    city: { type: 'string' },
    source: { enum: ['app', 'web', 'partner'] },
    ageYears: { type: 'integer', minimum: 0 },
    km: { type: 'integer', minimum: 0 },
    price: { type: 'number', minimum: 0 },
    inspectionAvg: { type: 'number', minimum: 0, maximum: 10 },
  },
});

const eligibilityOutput = s({
  type: 'object',
  required: ['eligible', 'reason'],
  properties: {
    eligible: { type: 'boolean' },
    lane: { enum: ['fast', 'standard', null] },
    reason: { type: 'string', maxLength: 64 },
  },
});

const routingOutput = s({
  type: 'object',
  required: ['route'],
  properties: {
    route: { enum: ['fast_track', 'standard', 'reject', 'manual_review'] },
    reason: { type: 'string' },
    band: { type: ['string', 'null'] },
  },
});

const segmentAttrs = s({
  type: 'object',
  required: ['city', 'price'],
  properties: { city: { type: 'string' }, price: { type: 'number', minimum: 0 } },
});

const pricingInput = s({
  type: 'object',
  required: ['price', 'ageYears'],
  properties: { price: { type: 'number', minimum: 0 }, ageYears: { type: 'integer', minimum: 0 }, city: { type: 'string' } },
});
const pricingOutput = s({
  type: 'object',
  required: ['band'],
  properties: { band: { enum: ['A', 'B', 'C', 'D'] }, max_discount: { type: 'integer', minimum: 0 } },
});
const callbackInput = s({
  type: 'object',
  required: ['city', 'hour'],
  properties: { city: { type: 'string' }, hour: { type: 'integer', minimum: 0, maximum: 23 }, source: { enum: ['app', 'web', 'partner'] }, vip: { type: 'boolean' } },
});
const callbackOutput = s({
  type: 'object',
  required: ['eligible'],
  properties: { eligible: { type: 'boolean' }, slot: { type: ['string', 'null'], description: 'Callback window, e.g. 30m' } },
});
const cityZoneValue = s({ type: 'object', required: ['zone'], properties: { zone: { type: 'string' }, hub: { type: 'string' } } });
const slaHours = s({ type: 'integer', minimum: 1, maximum: 168, description: 'Hours until first contact' });
const serviceability = s({ type: 'object', required: ['serviceable'], properties: { serviceable: { type: 'boolean' }, hub: { type: ['string', 'null'] } } });
const auctionInput = s({
  type: 'object',
  required: ['auction'],
  properties: {
    auction: { type: 'object', required: ['id', 'city'], properties: { id: { type: 'string' }, city: { type: 'string' }, reserve: { type: 'number' } } },
    car: { type: 'object', properties: { make: { type: 'string' }, year: { type: 'integer' } } },
  },
});
const dealerPool = s({ type: 'object', required: ['pool'], properties: { pool: { enum: ['dealer_pool_a', 'dealer_pool_b', 'open_market'] }, reason: { type: 'string' } } });

// ---------------------------------------------------------------- lead_routing

type Variant = 'v16' | 'v17' | 'v18' | 'draft';

export function leadRouting(variant: Variant): DecisionSpec {
  const ref = (name: string, version = 1) => ({ name, version });
  const rules: NonNullable<Extract<DecisionSpec['nodes'][number], { type: 'rules' }>['rules']> = [
    { id: 'r1', name: 'Blocked city', when: { segment: 'blocked_cities', op: 'IN_SEGMENT', key: 'input.city' }, then: { eligible: false, reason: 'city_not_served' } },
    { id: 'r2', name: 'Car too old', when: { field: 'ageYears', op: 'GT', value: 12 }, then: { eligible: false, reason: 'car_too_old' } },
  ];
  if (variant !== 'v16') rules.push({ id: 'r3', name: 'Odometer over limit', when: { field: 'km', op: 'GT', value: 150000 }, then: { eligible: false, reason: 'km_over_limit' } });
  if (variant !== 'v16')
    rules.push({
      id: 'r4',
      name: 'Fast lane',
      rollout: { percent: 25, salt: 'eligibility.r4' },
      when: {
        all: [
          { field: 'price', op: 'BETWEEN', value: 300000, valueTo: 2500000 },
          { field: 'source', op: 'IN', value: ['app', 'web'] },
          { field: 'inspectionAvg', op: 'GTE', value: 7 },
          { segment: 'metro_premium', op: 'NOT_IN_SEGMENT', attributes: { city: 'input.city', price: 'input.price' } },
        ],
      },
      then: { eligible: true, lane: 'fast', reason: 'fast_lane' },
    });
  const bandAHi = variant === 'draft' ? 800000 : 800000;
  return {
    entry: 'normalize',
    inputSchema: ref('lead_input'),
    nodes: [
      {
        id: 'normalize',
        type: 'transform',
        description: 'Flatten the lead into the fields the rules read',
        outputSchema: ref('eligibility_input'),
        mode: 'build',
        fields: [
          { target: 'city', expr: 'input.lead.city' },
          { target: 'source', expr: 'input.lead.source' },
          { target: 'ageYears', expr: 'ctx.now.Year() - input.car.year' },
          { target: 'km', expr: 'input.car.km' },
          { target: 'price', expr: 'input.car.price' },
          { target: 'inspectionAvg', expr: 'mean(map(input.inspections ?? [], .score)) ?? 0' },
        ],
        next: 'eligibility',
        position: { x: 28, y: 150 },
      },
      {
        id: 'eligibility',
        type: 'rules',
        inputSchema: ref('eligibility_input'),
        outputSchema: ref('eligibility_output'),
        mode: 'first_match',
        rules,
        default: { eligible: true, lane: 'standard', reason: 'default' },
        next: 'route',
        position: { x: 252, y: 150 },
      },
      {
        id: 'route',
        type: 'switch',
        cases: [{ id: 'eligible', when: 'nodes.eligibility.eligible', next: 'price_band' }],
        default: 'reject',
        position: { x: 476, y: 150 },
      },
      {
        id: 'price_band',
        type: 'table',
        from: 'nodes.normalize',
        inputSchema: ref('pricing_input'),
        outputSchema: ref('pricing_output'),
        hitPolicy: 'first',
        inputs: [
          { name: 'price', expr: 'input.price' },
          { name: 'age', expr: 'input.ageYears' },
          { name: 'city', expr: 'input.city' },
        ],
        outputs: ['band', 'max_discount'],
        rows: [
          { id: 'row_1', when: { price: '< 300000', age: '<= 5', city: '-' }, then: { band: 'C', max_discount: 5000 } },
          { id: 'row_2', when: { price: `[300000..${bandAHi}]`, age: '<= 8', city: '["Gurgaon","Delhi"]' }, then: { band: 'A', max_discount: 15000 } },
          { id: 'row_3', when: { price: '[300000..800000]', age: '-', city: '-' }, then: { band: 'B', max_discount: 10000 } },
          { id: 'row_4', when: { price: '-', age: '-', city: '-' }, then: { band: 'D', max_discount: 0 } },
        ],
        default: { band: 'D', max_discount: 0 },
        next: 'priority',
        position: { x: 700, y: 40 },
      },
      {
        id: 'reject',
        type: 'transform',
        fields: [
          { target: 'route', expr: '"reject"' },
          { target: 'reason', expr: 'nodes.eligibility.reason' },
        ],
        next: 'end',
        position: { x: 700, y: 270 },
      },
      {
        id: 'priority',
        type: 'ai',
        description: 'Model opinion on routing; logged in shadow, not used yet',
        model: 'reasoning-default',
        mode: 'shadow',
        timeoutMs: variant === 'draft' ? 1200 : 1500,
        vars: {
          car: 'string(input.car.make) + " " + string(input.car.model)',
          year: 'input.car.year',
          avg: 'nodes.normalize.inspectionAvg',
          city: 'input.lead.city',
          band: 'nodes.price_band.band',
        },
        prompt: {
          system: 'You rate follow-up priority for used-car leads. Answer only with JSON matching the schema.',
          user: 'Car: {{car}}, {{year}}. Inspection average: {{avg}}. City: {{city}}. Price band: {{band}}.',
        },
        responseSchema: { type: 'object', required: ['route', 'reason'], properties: { route: { enum: ['fast_track', 'standard', 'manual_review'] }, reason: { type: 'string', maxLength: 200 } } },
        maxOutputTokens: 300,
        onError: 'default_priority',
        next: 'output',
        position: { x: 924, y: 40 },
      },
      {
        id: 'default_priority',
        type: 'rules',
        description: 'Decides while priority is in shadow',
        from: 'nodes.eligibility',
        inputSchema: ref('eligibility_output'),
        outputSchema: ref('routing_output'),
        mode: 'first_match',
        rules: [{ id: 'r1', name: 'Fast lane', when: { field: 'lane', op: 'EQ', value: 'fast' }, then: { route: 'fast_track', reason: '=input.reason' } }],
        default: { route: 'standard', reason: '=input.reason' },
        next: 'output',
        position: { x: 924, y: 170 },
      },
      {
        id: 'output',
        type: 'output',
        fields: [
          { target: 'route', expr: 'nodes.default_priority.route' },
          { target: 'reason', expr: 'nodes.default_priority.reason' },
          { target: 'band', expr: 'nodes.price_band.band' },
        ],
        position: { x: 1148, y: 105 },
      },
    ],
    output: { schema: ref('routing_output') },
    fallback: { route: 'manual_review', reason: 'fallback' },
    logging: { sample: 0.1, fields: ['lead.city', 'car.year', 'car.price'] },
    budgetMicros: 200,
    tests: [
      {
        name: 'fast track · Swift 2019, Gurgaon',
        ctx: { now: '2026-10-30T10:00:00Z', unit: 'L-1' },
        input: { lead: { id: 'L-1', city: 'Gurgaon', source: 'app' }, car: { make: 'Maruti', model: 'Swift VXI', year: 2019, km: 48200, price: 525000 }, inspections: [{ part: 'engine', score: 8 }, { part: 'body', score: 7 }] },
        expect: { route: 'fast_track' },
      },
      {
        name: 'too old · Alto 2011, Pune',
        ctx: { now: '2026-10-30T10:00:00Z', unit: 'L-2' },
        input: { lead: { id: 'L-2', city: 'Pune', source: 'web' }, car: { make: 'Maruti', model: 'Alto', year: 2011, km: 91000, price: 180000 } },
        expect: { route: 'reject', reason: 'car_too_old' },
        expectPath: ['normalize:ok', 'eligibility:r2', 'route:default', 'reject:ok'],
      },
      {
        name: 'blocked city · Leh',
        ctx: { now: '2026-10-30T10:00:00Z', unit: 'L-3' },
        input: { lead: { id: 'L-3', city: 'Leh', source: 'app' }, car: { year: 2020, km: 20000, price: 640000 } },
        expect: { route: 'reject', reason: 'city_not_served' },
      },
      {
        name: 'odometer · 1,82,000 km',
        ctx: { now: '2026-10-30T10:00:00Z', unit: 'L-4' },
        input: { lead: { id: 'L-4', city: 'Delhi', source: 'app' }, car: { year: 2018, km: 182000, price: 410000 } },
        expect: { route: variant === 'v16' ? 'standard' : 'reject' },
      },
      {
        name: 'band A upper edge · ₹8,50,000',
        ctx: { now: '2026-10-30T10:00:00Z', unit: 'L-5' },
        input: { lead: { id: 'L-5', city: 'Gurgaon', source: 'partner' }, car: { year: 2021, km: 30000, price: 850000 } },
        expect: { band: 'A' },
      },
      {
        name: 'missing car is rejected by the schema',
        input: { lead: { id: 'L-6', city: 'Pune' } },
        expectError: 'INVALID_ARGUMENT',
      },
    ],
  };
}

const callbackEligibility: DecisionSpec = {
  entry: 'rules',
  nodes: [
    {
      id: 'rules',
      type: 'rules',
      inputSchema: { name: 'callback_input', version: 1 },
      outputSchema: { name: 'callback_output', version: 1 },
      mode: 'first_match',
      rules: [
        { id: 'r1', name: 'Night hours', when: { any: [{ field: 'hour', op: 'LT', value: 9 }, { field: 'hour', op: 'GTE', value: 21 }] }, then: { eligible: false, slot: null } },
        { id: 'r2', name: 'VIP', when: { field: 'vip', op: 'IS_TRUE' }, then: { eligible: true, slot: '15m' } },
        { id: 'r3', name: 'Metro cities', when: { field: 'city', op: 'IN_CASE_INSENSITIVE', value: ['delhi', 'gurgaon', 'noida', 'mumbai', 'bengaluru'] }, then: { eligible: true, slot: '30m' } },
        { id: 'r4', name: 'Beta callback', when: { all: [{ segment: 'beta_10pct', op: 'IN_SEGMENT' }, { gate: 'new_inspection_flow', op: 'GATE_PASSES' }] }, then: { eligible: true, slot: '45m' } },
      ],
      default: { eligible: true, slot: '60m' },
      position: { x: 28, y: 60 },
    },
  ],
  output: { schema: { name: 'callback_output', version: 1 } },
  fallback: { eligible: false, slot: null },
  tests: [
    { name: 'night', input: { city: 'Delhi', hour: 23 }, expect: { eligible: false } },
    { name: 'metro', input: { city: 'Gurgaon', hour: 11 }, expect: { eligible: true, slot: '30m' } },
  ],
};

const inspectionPricing: DecisionSpec = {
  entry: 'band',
  nodes: [
    {
      id: 'band',
      type: 'table',
      inputSchema: { name: 'pricing_input', version: 1 },
      outputSchema: { name: 'pricing_output', version: 1 },
      hitPolicy: 'first',
      inputs: [
        { name: 'price', expr: 'input.price' },
        { name: 'age', expr: 'input.ageYears' },
      ],
      outputs: ['band', 'max_discount'],
      rows: [
        { id: 'row_1', when: { price: '< 300000', age: '<= 5' }, then: { band: 'C', max_discount: 5000 } },
        { id: 'row_2', when: { price: '[300000..850000]', age: '<= 8' }, then: { band: 'A', max_discount: 15000 } },
        { id: 'row_3', when: { price: '[300000..850000]', age: '-' }, then: { band: 'B', max_discount: 10000 } },
        { id: 'row_4', when: { price: '-', age: '-' }, then: { band: 'D', max_discount: 0 } },
      ],
      default: { band: 'D', max_discount: 0 },
      position: { x: 28, y: 60 },
    },
  ],
  fallback: { band: 'D', max_discount: 0 },
  tests: [{ name: 'band A edge', input: { price: 850000, ageYears: 5 }, expect: { band: 'A' } }],
};

const payloadNormalize: DecisionSpec = {
  entry: 'normalize',
  nodes: [
    {
      id: 'normalize',
      type: 'transform',
      mode: 'build',
      outputSchema: { name: 'lead_input', version: 1 },
      fields: [
        { target: 'lead.id', expr: 'input.ref' },
        { target: 'lead.first_name', expr: 'input.customer?.firstName' },
        { target: 'lead.last_name', expr: 'input.customer?.lastName' },
        { target: 'lead.phone', expr: 'trimPrefix(input.customer.mobile, "0")' },
        { target: 'lead.city', expr: 'input.location.city' },
        { target: 'lead.source', expr: '"partner"' },
        { target: 'car.make', expr: 'input.vehicle.brand' },
        { target: 'car.model', expr: 'input.vehicle.variant' },
        { target: 'car.year', expr: 'int(input.vehicle.mfgYear)' },
        { target: 'car.km', expr: 'int(input.vehicle.odometer)' },
        { target: 'car.price', expr: 'float(input.vehicle.askingPrice)' },
      ],
      defaults: [{ target: 'lead.source', value: 'partner' }],
      omitNil: true,
      position: { x: 28, y: 60 },
    },
  ],
  tests: [
    {
      name: 'partner payload',
      input: { ref: 'P-77', customer: { firstName: 'Neha', lastName: 'Rao', mobile: '09876500000' }, location: { city: 'Pune' }, vehicle: { brand: 'Honda', variant: 'City ZX', mfgYear: '2020', odometer: '30500', askingPrice: '780000' } },
      expect: { lead: { city: 'Pune', source: 'partner' }, car: { year: 2020 } },
    },
  ],
};

const dealerAssignment: DecisionSpec = {
  entry: 'shortlist',
  nodes: [
    {
      id: 'shortlist',
      type: 'transform',
      fields: [
        { target: 'city', expr: 'input.auction.city' },
        { target: 'price', expr: 'input.auction.reserve ?? 0' },
        { target: 'zone', expr: 'lookup("city_zone", input.auction.city, {"zone": "other"}).zone' },
      ],
      next: 'dealer_match',
      position: { x: 28, y: 100 },
    },
    {
      id: 'dealer_match',
      type: 'ai',
      model: 'fast-default',
      mode: 'inline',
      timeoutMs: 800,
      vars: { zone: 'nodes.shortlist.zone', reserve: 'nodes.shortlist.price', make: 'input.car.make' },
      prompt: { system: 'Pick the dealer pool most likely to win this auction.', user: 'Zone {{zone}}, reserve {{reserve}}, make {{make}}.' },
      responseSchema: { type: 'object', required: ['pool'], properties: { pool: { enum: ['dealer_pool_a', 'dealer_pool_b', 'open_market'] }, reason: { type: 'string' } } },
      onError: 'fallback_pool',
      next: 'output',
      position: { x: 252, y: 40 },
    },
    {
      id: 'fallback_pool',
      type: 'rules',
      from: 'nodes.shortlist',
      inputSchema: { name: 'segment_attrs_city_price', version: 1 },
      outputSchema: { name: 'dealer_pool_output', version: 1 },
      rules: [{ id: 'r1', name: 'Big reserve', when: { field: 'price', op: 'GT', value: 1000000 }, then: { pool: 'dealer_pool_a', reason: 'reserve' } }],
      default: { pool: 'dealer_pool_b', reason: 'default' },
      next: 'output',
      position: { x: 252, y: 180 },
    },
    {
      id: 'output',
      type: 'output',
      expression: 'nodes.dealer_match ?? nodes.fallback_pool',
      position: { x: 476, y: 100 },
    },
  ],
  fallback: { pool: 'open_market', reason: 'fallback' },
};

// ---------------------------------------------------------------- other resources

const segments: Record<string, SegmentSpec> = {
  blocked_cities: { type: 'static', static: { keyType: 'string', pii: false } },
  vip_dealers: { type: 'static', static: { keyType: 'string', pii: true } },
  metro_premium: {
    type: 'dynamic',
    dynamic: {
      attributesSchema: { name: 'segment_attrs_city_price', version: 1 },
      when: { all: [{ field: 'city', op: 'IN', value: ['Delhi', 'Gurgaon', 'Mumbai', 'Bengaluru'] }, { field: 'price', op: 'GT', value: 800000 }] },
    },
  },
  beta_10pct: { type: 'rollout', rollout: { percent: 10, salt: 'beta_10pct' } },
};

export const BLOCKED_CITIES = ['Leh', 'Kargil', 'Port Blair', 'Itanagar', 'Aizawl', 'Kohima', 'Imphal', 'Gangtok', 'Shillong', 'Agartala', 'Daman', 'Silvassa', 'Kavaratti', 'Diu'];

const lookups: Record<string, LookupSpec> = {
  city_zone: { keyType: 'string', valueSchema: { name: 'city_zone_value', version: 1 } },
  sla_hours_by_band: { keyType: 'string', valueSchema: { name: 'sla_hours_value', version: 1 } },
  pincode_serviceability: { keyType: 'number', valueSchema: { name: 'serviceability_value', version: 1 } },
};

const CITY_ZONE: [string, string, string][] = [
  ['Delhi', 'NCR-1', 'Okhla'],
  ['Gurgaon', 'NCR-2', 'Udyog Vihar'],
  ['Noida', 'NCR-3', 'Sector 63'],
  ['Mumbai', 'WEST-1', 'Andheri'],
  ['Pune', 'WEST-2', 'Hinjewadi'],
  ['Bengaluru', 'SOUTH-1', 'Whitefield'],
  ['Hyderabad', 'SOUTH-2', 'Gachibowli'],
  ['Chennai', 'SOUTH-3', 'Guindy'],
  ['Kolkata', 'EAST-1', 'Salt Lake'],
  ['Jaipur', 'NORTH-2', 'Sitapura'],
  ['Ahmedabad', 'WEST-3', 'Sanand'],
  ['Lucknow', 'NORTH-3', 'Gomti Nagar'],
];

const gates: Record<string, GateSpec> = {
  new_inspection_flow: {
    enabled: true,
    rules: [
      { id: 'beta', when: { segment: 'beta_10pct', op: 'IN_SEGMENT' }, value: true },
      { id: 'metro', when: { field: 'city', op: 'IN', value: ['Delhi', 'Gurgaon'] }, value: true, rollout: { percent: 25, salt: 'new_inspection_flow.metro' } },
    ],
    default: false,
    attributesSchema: { name: 'segment_attrs_city_price', version: 1 },
  },
  ai_priority_shadow: { enabled: true, rules: [], default: true },
  dealer_auction_v2: { enabled: true, rules: [{ id: 'all', when: { expr: 'true' }, value: true, rollout: { percent: 50 } }], default: false },
  whatsapp_followup: { enabled: false, rules: [], default: false },
};

const experiments: Record<string, ExperimentSpec> = {
  callback_timing: {
    status: 'running',
    hypothesis: 'Calling back sooner raises inspection bookings within 48 h.',
    salt: 'callback_timing',
    layer: 'lead_followup',
    holdoutPercent: 5,
    variants: [
      { name: 'control', weight: 50, parameters: { delay_min: 60 } },
      { name: 'wait_15m', weight: 25, parameters: { delay_min: 15 } },
      { name: 'wait_5m', weight: 25, parameters: { delay_min: 5 } },
    ],
  },
  banner_copy: {
    status: 'draft',
    hypothesis: 'A price-first banner gets more lead form starts.',
    salt: 'banner_copy',
    layer: 'lead_followup',
    variants: [
      { name: 'control', weight: 50, parameters: { headline: 'Sell your car in 30 minutes' } },
      { name: 'price_first', weight: 50, parameters: { headline: 'Get the best price today' } },
    ],
  },
};

const layers: Record<string, LayerSpec> = {
  lead_followup: { salt: 'lead_followup', allocations: [{ experiment: 'callback_timing', percent: 60 }, { experiment: 'banner_copy', percent: 30 }] },
};

// ---------------------------------------------------------------- builders

let clock = Date.now();
export function setClock(t: number): void {
  clock = t;
}
const ago = (ms: number) => new Date(clock - ms).toISOString();

function res(kind: ResourceKind, name: string, description: string, versions: { spec: unknown; by: string; at: number; message?: string; members?: string[]; rows?: LookupRow[] }[], draft: unknown, live: StoredResource['live'], extra: Partial<StoredResource> = {}): StoredResource {
  const vs: StoredVersion[] = versions.map((v, i) => ({
    version: (extra as { firstVersion?: number }).firstVersion ? (extra as { firstVersion?: number }).firstVersion! + i : i + 1,
    message: v.message,
    createdAt: ago(v.at),
    createdBy: v.by,
    spec: v.spec,
    members: v.members,
    rows: v.rows,
  }));
  const last = vs.at(-1);
  const r: StoredResource = {
    kind,
    name,
    description,
    draft: { spec: draft, revision: 3 + vs.length, updatedAt: last ? last.createdAt : ago(HOUR), updatedBy: last?.createdBy ?? 'asha@example.com' },
    versions: vs,
    live,
    ...extra,
  };
  delete (r as { firstVersion?: number }).firstVersion;
  return r;
}

const schemaRes = (name: string, description: string, specs: JsonSchema[], live: number, examples: unknown[] = []): StoredResource =>
  res(
    'schemas',
    name,
    description,
    specs.map((j, i) => ({ spec: { jsonSchema: j, examples } satisfies SchemaSpec, by: i % 2 ? 'ravi@example.com' : 'asha@example.com', at: (specs.length - i) * 5 * DAY + 3 * HOUR })),
    { jsonSchema: specs[specs.length - 1], examples } satisfies SchemaSpec,
    { dev: { version: specs.length }, stage: { version: live }, prod: { version: live } },
  );

function leadServiceResources(): StoredResource[] {
  const leadExample = {
    lead: { id: 'L-1029384', first_name: 'Rahul', last_name: 'Sharma', phone: '+91-9876543210', city: 'Gurgaon', source: 'app' },
    car: { make: 'Maruti', model: 'Swift VXI', year: 2019, km: 48200, price: 525000 },
    inspections: [{ part: 'engine', score: 8 }, { part: 'body', score: 5 }, { part: 'ac', score: 5.5 }],
  };
  const out: StoredResource[] = [
    schemaRes('lead_input', 'A used-car lead as sent by the lead service', [leadInputV1, leadInputV2()], 1, [leadExample]),
    schemaRes('eligibility_input', 'Normalized fields the eligibility rules read', [eligibilityInput], 1, [{ city: 'Gurgaon', source: 'app', ageYears: 7, km: 48200, price: 525000, inspectionAvg: 7.1 }]),
    schemaRes('eligibility_output', 'Eligibility verdict', [eligibilityOutput], 1, [{ eligible: true, lane: 'fast', reason: 'fast_lane' }]),
    schemaRes('routing_output', 'Final routing answer', [routingOutput], 1, [{ route: 'fast_track', reason: 'fast_lane', band: 'A' }]),
    schemaRes('segment_attrs_city_price', 'Attributes for city and price segments', [segmentAttrs], 1, [{ city: 'Gurgaon', price: 950000 }]),
    schemaRes('pricing_input', 'Price band inputs', [pricingInput], 1),
    schemaRes('pricing_output', 'Price band and maximum discount', [pricingOutput], 1),
    schemaRes('callback_input', 'Callback eligibility inputs', [callbackInput], 1),
    schemaRes('callback_output', 'Callback verdict and window', [callbackOutput], 1),
    schemaRes('city_zone_value', 'Zone and hub for a city', [cityZoneValue], 1),
    schemaRes('sla_hours_value', 'Hours until first contact (a plain integer)', [slaHours], 1),
    schemaRes('serviceability_value', 'Whether a pincode is serviceable', [serviceability], 1),
    schemaRes('auction_input', 'Auction for dealer assignment', [auctionInput], 1),
    schemaRes('dealer_pool_output', 'Chosen dealer pool', [dealerPool], 1),
  ];
  // decisions
  out.push(
    res(
      'decisions',
      'lead_routing',
      'Route a used-car lead to fast track, standard or review',
      [
        { spec: leadRouting('v16'), by: 'asha@example.com', at: 9 * DAY, message: 'Add the AI priority node in shadow' },
        { spec: leadRouting('v17'), by: 'asha@example.com', at: 4 * DAY, message: 'Fast lane for metro leads at 25%' },
        { spec: leadRouting('v18'), by: 'asha@example.com', at: 1 * DAY, message: 'Odometer limit; widen band A' },
      ],
      leadRouting('draft'),
      { dev: { version: 18 }, stage: { version: 18 }, prod: { version: 17, rollout: { fromVersion: 16, percent: 25 } } },
      { firstVersion: 16, stats: { p99Micros: 410, callsPerMinute: 8420, fallbackRate: 0 } } as Partial<StoredResource>,
    ),
    res('decisions', 'callback_eligibility', 'Can a lead get a callback within the hour', [{ spec: callbackEligibility, by: 'ravi@example.com', at: 1 * DAY }], callbackEligibility, { dev: { version: 23 }, stage: { version: 23 }, prod: { version: 23 } }, { firstVersion: 23, stats: { p99Micros: 90, callsPerMinute: 12960, fallbackRate: 0 } } as Partial<StoredResource>),
    res(
      'decisions',
      'inspection_pricing_band',
      'Price band and maximum discount from inspection data',
      [
        { spec: { ...inspectionPricing, nodes: [{ ...inspectionPricing.nodes[0], rows: (inspectionPricing.nodes[0] as { rows: unknown[] }).rows.map((r) => JSON.parse(JSON.stringify(r).replace(/850000/g, '800000'))) }] }, by: 'asha@example.com', at: 6 * DAY },
        { spec: inspectionPricing, by: 'asha@example.com', at: 1 * DAY, message: 'Widen band A to 8.5 lakh' },
      ],
      inspectionPricing,
      { dev: { version: 10 }, stage: { version: 10 }, prod: { version: 9 } },
      { firstVersion: 9, stats: { p99Micros: 120, callsPerMinute: 6105, fallbackRate: 0 } } as Partial<StoredResource>,
    ),
    res('decisions', 'lead_payload_normalize', 'Normalize partner lead payloads to the lead_input shape', [{ spec: payloadNormalize, by: 'karan@example.com', at: 6 * DAY }], payloadNormalize, { dev: { version: 6 }, stage: { version: 6 }, prod: { version: 6 } }, { firstVersion: 6, stats: { p99Micros: 210, callsPerMinute: 5730, fallbackRate: 0 } } as Partial<StoredResource>),
    res('decisions', 'dealer_assignment', 'Pick a dealer pool for an auction lead (AI node inline, 800 ms budget)', [{ spec: dealerAssignment, by: 'meera@example.com', at: 3 * DAY }], dealerAssignment, { dev: { version: 4 }, stage: { version: 4 }, prod: { version: 4 } }, { firstVersion: 4, stats: { p99Micros: 812000, callsPerMinute: 310, fallbackRate: 0.019 } } as Partial<StoredResource>),
  );
  // segments
  const vip = Array.from({ length: 1240 }, (_, i) => `D-${String(10001 + i * 7)}`);
  const beta = { by: 'karan@example.com', at: 3 * DAY };
  out.push(
    res('segments', 'blocked_cities', 'Cities we do not serve yet', [{ spec: segments.blocked_cities, by: 'karan@example.com', at: 8 * DAY, members: BLOCKED_CITIES }], segments.blocked_cities, { dev: { version: 5 }, stage: { version: 5 }, prod: { version: 5 } }, { firstVersion: 5, members: [...BLOCKED_CITIES] } as Partial<StoredResource>),
    res('segments', 'vip_dealers', 'Dealers with a VIP contract (stored as HMACs)', [{ spec: segments.vip_dealers, by: 'meera@example.com', at: 1 * DAY, members: vip }], segments.vip_dealers, { dev: { version: 31 }, stage: { version: 31 }, prod: { version: 31 } }, { firstVersion: 31, members: vip } as Partial<StoredResource>),
    res('segments', 'metro_premium', 'Leads in a metro city for cars above 8 lakh', [{ spec: segments.metro_premium, ...beta }], segments.metro_premium, { dev: { version: 2 }, stage: { version: 2 }, prod: { version: 2 } }, { firstVersion: 2 } as Partial<StoredResource>),
    res('segments', 'beta_10pct', 'A stable 10% of units for beta features', [{ spec: segments.beta_10pct, ...beta }], segments.beta_10pct, { dev: { version: 1 }, stage: { version: 1 }, prod: { version: 1 } }),
  );
  // lookups
  const cz: LookupRow[] = CITY_ZONE.map(([k, z, h]) => ({ key: k, value: { zone: z, hub: h } }));
  const sla: LookupRow[] = [
    { key: 'A', value: 2 },
    { key: 'B', value: 6 },
    { key: 'C', value: 12 },
    { key: 'D', value: 24 },
  ];
  const pins: LookupRow[] = Array.from({ length: 240 }, (_, i) => ({ key: String(110001 + i * 13), value: { serviceable: i % 9 !== 0, hub: i % 9 === 0 ? null : CITY_ZONE[i % CITY_ZONE.length][2] } }));
  out.push(
    res('lookups', 'city_zone', 'City to operations zone and hub', [{ spec: lookups.city_zone, by: 'karan@example.com', at: 4 * DAY, rows: cz }], lookups.city_zone, { dev: { version: 7 }, stage: { version: 7 }, prod: { version: 7 } }, { firstVersion: 7, rows: cz } as Partial<StoredResource>),
    res('lookups', 'sla_hours_by_band', 'First-contact SLA per price band', [{ spec: lookups.sla_hours_by_band, by: 'asha@example.com', at: 21 * DAY, rows: sla }], lookups.sla_hours_by_band, { dev: { version: 2 }, stage: { version: 2 }, prod: { version: 2 } }, { firstVersion: 2, rows: sla } as Partial<StoredResource>),
    res('lookups', 'pincode_serviceability', 'Is a pincode serviceable, and by which hub', [{ spec: lookups.pincode_serviceability, by: 'karan@example.com', at: 1 * DAY, rows: pins }], lookups.pincode_serviceability, { dev: { version: 12 }, stage: { version: 12 }, prod: { version: 12 } }, { firstVersion: 12, rows: pins } as Partial<StoredResource>),
  );
  // gates, experiments, layers
  for (const [name, spec] of Object.entries(gates)) {
    out.push(res('gates', name, { new_inspection_flow: 'New inspection booking flow', ai_priority_shadow: 'Run the priority AI node in shadow', dealer_auction_v2: 'Second-generation dealer auction', whatsapp_followup: 'Follow up on WhatsApp' }[name] ?? '', [{ spec, by: 'ravi@example.com', at: 2 * DAY }], spec, { dev: { version: 1 }, stage: { version: 1 }, prod: { version: 1 } }));
  }
  out.push(
    res('experiments', 'callback_timing', 'Does calling back sooner raise inspection bookings?', [{ spec: experiments.callback_timing, by: 'meera@example.com', at: 9 * DAY }], experiments.callback_timing, { dev: { version: 3 }, stage: { version: 3 }, prod: { version: 3 } }, { firstVersion: 3 } as Partial<StoredResource>),
    res('experiments', 'banner_copy', 'Price-first banner copy', [], experiments.banner_copy, {}),
    res('layers', 'lead_followup', 'Mutually exclusive lead follow-up experiments', [{ spec: layers.lead_followup, by: 'meera@example.com', at: 9 * DAY }], layers.lead_followup, { dev: { version: 1 }, stage: { version: 1 }, prod: { version: 1 } }),
  );
  return out;
}

function release(gen: number, env: string, type: Release['type'], at: number, by: string, contents: Release['contents'], extra: Partial<Release> = {}): Release {
  return { generation: gen, environment: env, type, publishedAt: ago(at), publishedBy: by, manifestHash: `sha256:${(gen * 2654435761).toString(16).padStart(8, '0')}9f2c${env.length}`, bundleBytes: 3_400_000 + gen * 1200, minEngineVersion: '1.0.0', contents, ...extra };
}

function prodContents(rollout: { v: number; from: number; pct: number } | null, changed: string[] = []): Release['contents'] {
  const base: Release['contents'] = [
    { kind: 'decisions', name: 'lead_routing', version: rollout?.v ?? 17, rollout: rollout ? { version: rollout.v, rollout: { fromVersion: rollout.from, percent: rollout.pct } } : undefined },
    { kind: 'decisions', name: 'callback_eligibility', version: 23 },
    { kind: 'decisions', name: 'inspection_pricing_band', version: 9 },
    { kind: 'decisions', name: 'lead_payload_normalize', version: 6 },
    { kind: 'decisions', name: 'dealer_assignment', version: 4 },
    { kind: 'segments', name: 'blocked_cities', version: 5 },
    { kind: 'segments', name: 'vip_dealers', version: 31 },
    { kind: 'lookups', name: 'city_zone', version: 7 },
    { kind: 'gates', name: 'dealer_auction_v2', version: 1 },
  ];
  return base.map((c) => ({ ...c, changed: changed.includes(c.name) }));
}

const PATHS: Record<string, { decision: string; v: string; out: unknown; input: Record<string, unknown>; path: PathStep[]; fb?: boolean; us: number }> = {
  fast: {
    decision: 'lead_routing',
    v: '17',
    out: { route: 'fast_track', band: 'A' },
    input: { 'lead.city': 'Gurgaon', 'car.year': 2019, 'car.price': 525000 },
    us: 21,
    path: [
      { nodeId: 'normalize', nodeType: 'transform', outcome: 'ok', micros: 9 },
      { nodeId: 'eligibility', nodeType: 'rules', outcome: 'r4', micros: 2 },
      { nodeId: 'route', nodeType: 'switch', outcome: 'eligible', micros: 0 },
      { nodeId: 'price_band', nodeType: 'table', outcome: 'row_2', micros: 3 },
      { nodeId: 'priority', nodeType: 'ai', outcome: 'shadow', micros: 0 },
      { nodeId: 'default_priority', nodeType: 'rules', outcome: 'r1', micros: 1 },
      { nodeId: 'output', nodeType: 'output', outcome: 'ok', micros: 3 },
    ],
  },
  standard: {
    decision: 'lead_routing',
    v: '16',
    out: { route: 'standard', band: 'B' },
    input: { 'lead.city': 'Pune', 'car.year': 2021, 'car.price': 690000 },
    us: 18,
    path: [
      { nodeId: 'normalize', nodeType: 'transform', outcome: 'ok', micros: 8 },
      { nodeId: 'eligibility', nodeType: 'rules', outcome: 'default', micros: 2 },
      { nodeId: 'route', nodeType: 'switch', outcome: 'eligible', micros: 0 },
      { nodeId: 'price_band', nodeType: 'table', outcome: 'row_3', micros: 3 },
      { nodeId: 'priority', nodeType: 'ai', outcome: 'shadow', micros: 0 },
      { nodeId: 'default_priority', nodeType: 'rules', outcome: 'default', micros: 1 },
      { nodeId: 'output', nodeType: 'output', outcome: 'ok', micros: 3 },
    ],
  },
  reject: {
    decision: 'lead_routing',
    v: '16',
    out: { route: 'reject', reason: 'car_too_old' },
    input: { 'lead.city': 'Pune', 'car.year': 2011, 'car.price': 180000 },
    us: 9,
    path: [
      { nodeId: 'normalize', nodeType: 'transform', outcome: 'ok', micros: 7 },
      { nodeId: 'eligibility', nodeType: 'rules', outcome: 'r2', micros: 1 },
      { nodeId: 'route', nodeType: 'switch', outcome: 'default', micros: 0 },
      { nodeId: 'reject', nodeType: 'transform', outcome: 'ok', micros: 1 },
    ],
  },
  callback: {
    decision: 'callback_eligibility',
    v: '23',
    out: { eligible: true, slot: '30m' },
    input: { city: 'Delhi', hour: 11 },
    us: 7,
    path: [{ nodeId: 'rules', nodeType: 'rules', outcome: 'r3', micros: 4 }],
  },
  dealer: {
    decision: 'dealer_assignment',
    v: '4',
    out: { pool: 'dealer_pool_b' },
    input: { 'auction.city': 'Pune' },
    us: 803112,
    fb: false,
    path: [
      { nodeId: 'shortlist', nodeType: 'transform', outcome: 'ok', micros: 3 },
      { nodeId: 'dealer_match', nodeType: 'ai', outcome: 'error', micros: 800021, error: 'timed out after 800 ms' },
      { nodeId: 'fallback_pool', nodeType: 'rules', outcome: 'default', micros: 2 },
      { nodeId: 'output', nodeType: 'output', outcome: 'ok', micros: 2 },
    ],
  },
  pricing: {
    decision: 'inspection_pricing_band',
    v: '9',
    out: { band: 'B', max_discount: 10000 },
    input: { price: 640000 },
    us: 5,
    path: [{ nodeId: 'band', nodeType: 'table', outcome: 'row_3', micros: 3 }],
  },
  fallback: {
    decision: 'lead_routing',
    v: '17',
    out: { route: 'manual_review', reason: 'fallback' },
    input: { 'lead.city': 'Noida' },
    us: 14,
    fb: true,
    path: [
      { nodeId: 'normalize', nodeType: 'transform', outcome: 'error', micros: 6, error: 'ageYears: value is nil (use ?? to give a default)' },
    ],
  },
};

function decisionLogs(env: string, gen: number, count: number): DecisionLogRecord[] {
  const keys = ['fast', 'standard', 'callback', 'fast', 'reject', 'pricing', 'standard', 'dealer', 'callback', 'fast', 'fallback', 'standard'];
  return Array.from({ length: count }, (_, i) => {
    const p = PATHS[keys[i % keys.length]];
    const id = (0x8c1f2a - i * 0x47).toString(16);
    return {
      type: 'decision' as const,
      ts: ago(i * 37_000 + 4000),
      requestId: `req_${id}`,
      traceId: `4bf92f3577b34da6a3ce929d0e0e${id.slice(-4)}`,
      sidecarId: `lead-service-7d9f8c-${['x2k4p', 'm81qz', 'c5wnd', 'hh0r2', '9qfal'][i % 5]}`,
      decision: p.decision,
      decisionVersion: `${p.decision}@${p.v}`,
      dependencyHash: `sha256:5c0e9a${(i % 7).toString(16)}…`,
      generation: i % 9 === 3 ? gen - 1 : gen,
      unitId: `L-${1029384 - i * 17}`,
      evaluatedAt: ago(i * 37_000 + 4000),
      path: p.path,
      fallbackUsed: !!p.fb,
      evalMicros: p.us + (i % 4),
      status: 'OK',
      inputHmac: `hmac:${id}a1`,
      outputHmac: `hmac:${id}b2`,
      input: p.input,
      output: p.out,
      ai: p.decision === 'dealer_assignment' ? [{ node: 'dealer_match', model: 'fast-default', latencyMs: 800, cacheHit: false, result: 'timeout' }] : p.decision === 'lead_routing' ? [{ node: 'priority', model: 'reasoning-default', latencyMs: 380 + (i % 5) * 40, cacheHit: i % 3 === 0, answerHash: `sha256:${id}`, result: 'shadow' }] : undefined,
    };
  });
}

function sidecars(env: string, gen: number, total: number, behind: number): Sidecar[] {
  const pods = ['x2k4p', 'm81qz', 'c5wnd', 'hh0r2', 't7vbe', '9qfal', 'p0dz3', 'w8rr1', 'k2m9a', 'v7nn2', 'b4tq8', 'z1xy5', 'q3ee7', 'r6uu0', 'y5ii9', 'n2oo4', 'l8pp6', 'j9ss2'];
  return Array.from({ length: Math.min(total, pods.length) }, (_, i) => {
    const lag = i >= 3 && i < 3 + behind;
    return {
      id: `lead-service-7d9f8c-${pods[i]}${env === 'prod' ? '' : '-' + env}`,
      node: lag ? 'node-b11' : `node-${['a12', 'a07', 'b03', 'c02', 'c09', 'a03'][i % 6]}`,
      sidecarVersion: lag ? '1.0.0-rc.2' : '1.0.0-rc.3',
      engineVersion: '1.0.0',
      generation: lag ? gen - 1 : gen,
      lastSyncAt: ago(lag ? 6 * MIN : (i % 4) * 1000 + 1000),
      lastSeenAt: ago((i % 5) * 1000 + 2000),
      p99Micros: 360 + ((i * 17) % 90),
      status: lag ? ('behind' as const) : ('in_sync' as const),
      lastError: lag ? 'watch stream reset; polling every 30 s, last poll timed out' : undefined,
    };
  });
}

function leadServiceData(): AppData {
  const prodReleases: Release[] = [
    release(42, 'prod', 'rollout', 3 * HOUR, 'ravi@example.com', prodContents({ v: 17, from: 16, pct: 25 }, ['lead_routing']), { reason: 'Step to 25%' }),
    release(41, 'prod', 'change_request', 17 * HOUR, 'ravi@example.com', prodContents({ v: 17, from: 16, pct: 5 }, ['callback_eligibility']), { changeRequestId: 'cr_212' }),
    release(40, 'prod', 'change_request', 19 * HOUR, 'ravi@example.com', prodContents({ v: 17, from: 16, pct: 5 }, ['lead_routing']), { changeRequestId: 'cr_210' }),
    release(39, 'prod', 'rollback', 2 * DAY + 2 * HOUR, 'meera@example.com', prodContents(null, ['dealer_assignment']).map((c) => (c.name === 'lead_routing' ? { ...c, version: 16 } : c)), { breakGlass: true, reason: 'dealer_assignment v5 timed out on every call' }),
    release(38, 'prod', 'kill_switch', 2 * DAY + 3 * HOUR, 'meera@example.com', prodContents(null, ['dealer_auction_v2']).map((c) => (c.name === 'lead_routing' ? { ...c, version: 16 } : c)), { reason: 'auction service down', killed: true, gate: 'dealer_auction_v2' } as Partial<Release>),
    release(37, 'prod', 'data', 3 * DAY, 'karan@example.com', prodContents(null, ['vip_dealers']).map((c) => (c.name === 'lead_routing' ? { ...c, version: 16 } : c))),
  ];
  const stageReleases: Release[] = [
    release(57, 'stage', 'change_request', 1 * DAY, 'ravi@example.com', prodContents(null, ['lead_routing']).map((c) => (c.name === 'lead_routing' ? { ...c, version: 18 } : c.name === 'inspection_pricing_band' ? { ...c, version: 10 } : c)), { changeRequestId: 'cr_211' }),
    release(56, 'stage', 'data', 2 * DAY, 'karan@example.com', prodContents(null, ['city_zone'])),
  ];
  const devReleases: Release[] = [release(63, 'dev', 'change_request', 20 * HOUR, 'asha@example.com', prodContents(null, ['lead_routing']).map((c) => (c.name === 'lead_routing' ? { ...c, version: 18 } : c)), { changeRequestId: 'cr_209' })];
  const crDiff = `--- lead_routing@17\n+++ lead_routing@18\n@@ -48,6 +48,12 @@\n       then: { eligible: false, reason: car_too_old }\n+    - id: r3\n+      name: Odometer over limit\n+      when: { field: km, op: GT, value: 150000 }\n+      then: { eligible: false, reason: km_over_limit }\n     - id: r4\n       name: Fast lane\n@@ -120,7 +126,7 @@\n     mode: shadow\n-    timeoutMs: 1500\n+    timeoutMs: 1200\n     vars:\n`;
  const changeRequests: ChangeRequest[] = [
    {
      id: 'cr_214',
      environment: 'prod',
      title: 'Odometer limit and band A to 8.5 lakh',
      description: 'Adds the odometer rule (r3) and widens band A to ₹8,50,000. The AI node stays in shadow.',
      items: [
        { kind: 'decisions', name: 'lead_routing', version: 18, rollout: { percent: 5 }, liveVersion: 17, diff: crDiff },
        { kind: 'decisions', name: 'inspection_pricing_band', version: 10, liveVersion: 9, diff: '--- inspection_pricing_band@9\n+++ inspection_pricing_band@10\n@@ -12,3 +12,3 @@\n       when:\n-        price: "[300000..800000]"\n+        price: "[300000..850000]"\n         age: "<= 8"\n' },
      ],
      status: 'open',
      author: 'asha@example.com',
      createdAt: ago(3 * HOUR),
      approvals: [],
      requiredApprovals: 1,
      comments: [{ by: 'ravi@example.com', at: ago(2 * HOUR), body: 'Looks fine. Can we start the rollout at 5% and watch fallbacks for an hour?' }],
      tests: { passed: 14, failed: 0 },
    },
    {
      id: 'cr_213',
      environment: 'stage',
      title: 'Partner payload: read askingPrice as a number',
      items: [{ kind: 'decisions', name: 'lead_payload_normalize', version: 6, liveVersion: 6, diff: '' }],
      status: 'open',
      author: 'karan@example.com',
      createdAt: ago(1 * DAY),
      approvals: [],
      requiredApprovals: 1,
      comments: [],
      tests: { passed: 6, failed: 0 },
    },
    {
      id: 'cr_212',
      environment: 'prod',
      title: 'callback_eligibility v23',
      items: [{ kind: 'decisions', name: 'callback_eligibility', version: 23, liveVersion: 22, diff: '' }],
      status: 'published',
      author: 'ravi@example.com',
      createdAt: ago(20 * HOUR),
      approvals: [{ by: 'meera@example.com', at: ago(18 * HOUR), comment: 'Ship it' }],
      requiredApprovals: 1,
      comments: [],
      tests: { passed: 8, failed: 0 },
      publishedGeneration: 41,
    },
    {
      id: 'cr_208',
      environment: 'prod',
      title: 'dealer_assignment v5: inline AI with 2 s timeout',
      items: [{ kind: 'decisions', name: 'dealer_assignment', version: 4, liveVersion: 4, diff: '' }],
      status: 'rejected',
      author: 'meera@example.com',
      createdAt: ago(5 * DAY),
      approvals: [],
      requiredApprovals: 1,
      comments: [{ by: 'ravi@example.com', at: ago(5 * DAY - HOUR), body: 'A 2 s inline call is too slow for the auction page. Keep 800 ms.' }],
      tests: { passed: 3, failed: 0 },
    },
  ];
  const updateLogs = (env: string, gen: number): UpdateLogRecord[] =>
    Array.from({ length: 16 }, (_, i) => {
      const fail = i === 2 || i === 5;
      return {
        type: 'update' as const,
        ts: ago(i * 11 * MIN + 2 * MIN),
        sidecarId: `lead-service-7d9f8c-${['x2k4p', 'm81qz', 'hh0r2', 'c5wnd', '9qfal', 't7vbe'][i % 6]}`,
        fromGeneration: gen - 1,
        toGeneration: gen,
        result: fail ? ('kept_previous' as const) : i % 7 === 6 ? ('up_to_date' as const) : ('swapped' as const),
        changed: [{ kind: 'decisions', name: 'lead_routing', from: 16, to: 17, sha256: '5c0e9a…' }],
        signature: 'valid' as const,
        downloadMs: 40 + i * 3,
        compileMs: 12 + (i % 5),
        tests: { passed: 14, failed: 0 },
        error: fail ? 'GET /sidecar/v1/head: context deadline exceeded' : undefined,
      };
    });
  const exposures = (env: string): ExposureRecord[] =>
    Array.from({ length: 30 }, (_, i) => ({
      type: 'exposure' as const,
      ts: ago(i * 41_000 + 3000),
      unitId: `L-${1029384 - i * 23}`,
      experiment: 'callback_timing',
      experimentVersion: 'callback_timing@3',
      variant: i % 20 === 0 ? undefined : ['control', 'control', 'wait_15m', 'wait_5m'][i % 4],
      reason: i % 20 === 0 ? 'holdout' : 'assigned',
      requestId: `req_${(0x8c1f2a - i * 0x47).toString(16)}`,
      sidecarId: `lead-service-7d9f8c-${['x2k4p', 'm81qz', 'c5wnd'][i % 3]}${env === 'prod' ? '' : '-' + env}`,
    }));
  return {
    resources: leadServiceResources(),
    environments: [
      { name: 'dev', policy: { requiredApprovals: 0, authorCanApprove: true, allowDataReleases: true, logSampleRate: 1 }, generation: 63, publishedAt: devReleases[0].publishedAt },
      { name: 'stage', policy: { requiredApprovals: 1, authorCanApprove: true, allowDataReleases: true, logSampleRate: 0.1 }, generation: 57, publishedAt: stageReleases[0].publishedAt },
      { name: 'prod', policy: { requiredApprovals: 1, authorCanApprove: false, allowDataReleases: true, logSampleRate: 0.01 }, generation: 42, publishedAt: prodReleases[0].publishedAt },
    ],
    credentials: [
      { id: 'cred_prod_lead_7Hq2', environment: 'prod', label: 'k8s prod', state: 'active', createdAt: ago(49 * DAY), createdBy: 'meera@example.com', lastUsedAt: ago(2000) },
      { id: 'cred_stage_lead_Pz80', environment: 'stage', label: 'k8s stage', state: 'active', createdAt: ago(1 * DAY), createdBy: 'meera@example.com', lastUsedAt: ago(5000) },
      { id: 'cred_dev_lead_k3Pw', environment: 'dev', label: 'dev cluster', state: 'active', createdAt: ago(90 * DAY), createdBy: 'karan@example.com', lastUsedAt: ago(11 * MIN) },
      { id: 'cred_stage_lead_x91', environment: 'stage', label: 'old stage', state: 'revoked', createdAt: ago(148 * DAY), createdBy: 'karan@example.com', lastUsedAt: ago(1 * DAY) },
    ],
    changeRequests,
    releases: { prod: prodReleases, stage: stageReleases, dev: devReleases },
    sidecars: { prod: sidecars('prod', 42, 214, 2), stage: sidecars('stage', 57, 12, 0), dev: sidecars('dev', 63, 4, 0) },
    decisionLogs: { prod: decisionLogs('prod', 42, 48), stage: decisionLogs('stage', 57, 14), dev: decisionLogs('dev', 63, 8) },
    updateLogs: { prod: updateLogs('prod', 42), stage: updateLogs('stage', 57).slice(0, 6), dev: updateLogs('dev', 63).slice(0, 4) },
    exposureLogs: { prod: exposures('prod'), stage: exposures('stage').slice(0, 8), dev: [] },
    killed: { prod: { dealer_auction_v2: true }, stage: {}, dev: {} },
  };
}

function pricingApiData(): AppData {
  const quoteIn = s({ type: 'object', required: ['price'], properties: { price: { type: 'number' }, city: { type: 'string' } } });
  const quoteOut = s({ type: 'object', required: ['quote'], properties: { quote: { type: 'number' } } });
  const spec: DecisionSpec = {
    entry: 'quote',
    nodes: [
      {
        id: 'quote',
        type: 'rules',
        inputSchema: { name: 'quote_input', version: 1 },
        outputSchema: { name: 'quote_output', version: 1 },
        rules: [{ id: 'r1', name: 'Metro premium', when: { field: 'city', op: 'IN', value: ['Delhi', 'Mumbai'] }, then: { quote: '=input.price * 1.02' } }],
        default: { quote: '=input.price' },
        position: { x: 28, y: 60 },
      },
    ],
  };
  return {
    resources: [schemaRes('quote_input', 'Quote request', [quoteIn], 1), schemaRes('quote_output', 'Quote', [quoteOut], 1), res('decisions', 'instant_quote', 'Instant price quote', [{ spec, by: 'ravi@example.com', at: 2 * DAY }], spec, { dev: { version: 1 } })],
    environments: [
      { name: 'dev', policy: { requiredApprovals: 0, authorCanApprove: true, allowDataReleases: true, logSampleRate: 1 }, generation: 3 },
      { name: 'stage', policy: { requiredApprovals: 1, authorCanApprove: true, allowDataReleases: false, logSampleRate: 0.1 }, generation: 0 },
      { name: 'prod', policy: { requiredApprovals: 2, authorCanApprove: false, allowDataReleases: false, logSampleRate: 0.01 }, generation: 0 },
    ],
    credentials: [],
    changeRequests: [],
    releases: { dev: [release(3, 'dev', 'change_request', 2 * DAY, 'ravi@example.com', [{ kind: 'decisions', name: 'instant_quote', version: 1, changed: true }])], stage: [], prod: [] },
    sidecars: { dev: [], stage: [], prod: [] },
    decisionLogs: { dev: [], stage: [], prod: [] },
    updateLogs: { dev: [], stage: [], prod: [] },
    exposureLogs: { dev: [], stage: [], prod: [] },
    killed: { dev: {}, stage: {}, prod: {} },
  };
}

export function createDb(now = Date.now()): Db {
  setClock(now);
  const members: Member[] = [
    { id: 'mem_01', name: 'Asha Verma', email: 'asha@example.com', role: 'author', apps: [] },
    { id: 'mem_02', name: 'Ravi Kumar', email: 'ravi@example.com', role: 'approver', apps: [] },
    { id: 'mem_03', name: 'Meera Shah', email: 'meera@example.com', role: 'admin', apps: [] },
    { id: 'mem_04', name: 'Karan Mehta', email: 'karan@example.com', role: 'author', apps: ['lead_service'] },
    { id: 'mem_05', name: 'Dev Patel', email: 'dev@example.com', role: 'viewer', apps: [] },
  ];
  const audit: AuditEvent[] = [
    { id: 'aud_120', at: ago(3 * HOUR), actor: 'ravi@example.com', action: 'release.published', app: 'lead_service', environment: 'prod', resource: 'decisions/lead_routing@17', details: { generation: 42, rollout: 25 } },
    { id: 'aud_119', at: ago(3 * HOUR + 7 * MIN), actor: 'asha@example.com', action: 'change_request.opened', app: 'lead_service', environment: 'prod', resource: 'change-requests/cr_214' },
    { id: 'aud_118', at: ago(17 * HOUR), actor: 'ravi@example.com', action: 'release.published', app: 'lead_service', environment: 'prod', resource: 'decisions/callback_eligibility@23', details: { generation: 41 } },
    { id: 'aud_117', at: ago(18 * HOUR), actor: 'meera@example.com', action: 'change_request.approved', app: 'lead_service', environment: 'prod', resource: 'change-requests/cr_212' },
    { id: 'aud_116', at: ago(20 * HOUR), actor: 'meera@example.com', action: 'credential.revoked', app: 'lead_service', environment: 'stage', resource: 'credentials/cred_stage_lead_x91' },
    { id: 'aud_115', at: ago(1 * DAY), actor: 'asha@example.com', action: 'version.cut', app: 'lead_service', resource: 'decisions/lead_routing@18', details: { message: 'Odometer limit; widen band A' } },
    { id: 'aud_114', at: ago(1 * DAY + HOUR), actor: 'karan@example.com', action: 'version.cut', app: 'lead_service', resource: 'lookups/pincode_serviceability@12' },
    { id: 'aud_113', at: ago(2 * DAY + 2 * HOUR), actor: 'meera@example.com', action: 'release.published', app: 'lead_service', environment: 'prod', resource: 'decisions/dealer_assignment@4', breakGlass: true, details: { type: 'rollback', reason: 'dealer_assignment v5 timed out on every call' } },
    { id: 'aud_112', at: ago(2 * DAY + 3 * HOUR), actor: 'meera@example.com', action: 'release.published', app: 'lead_service', environment: 'prod', resource: 'gates/dealer_auction_v2', details: { type: 'kill_switch', killed: true } },
    { id: 'aud_111', at: ago(4 * DAY), actor: 'meera@example.com', action: 'member.role_changed', resource: 'members/mem_04', details: { from: 'viewer', to: 'author' } },
  ];
  return {
    fixtureVersion: FIXTURE_VERSION,
    meId: 'mem_03',
    seq: 300,
    members,
    apps: [
      { name: 'lead_service', description: 'Lead routing and pricing decisions', owners: ['team-leads'], environments: ['dev', 'stage', 'prod'], bundleLimitMB: 64, createdAt: ago(120 * DAY) },
      { name: 'pricing_api', description: 'Instant quotes for the pricing API', owners: ['team-pricing'], environments: ['dev', 'stage', 'prod'], bundleLimitMB: 64, createdAt: ago(30 * DAY) },
    ],
    data: { lead_service: leadServiceData(), pricing_api: pricingApiData() },
    audit,
    webhooks: [
      { id: 'wh_1', url: 'https://hooks.example.com/gearbox', events: ['release.published', 'change_request.approved'], lastDelivery: { at: ago(3 * HOUR), status: 200 } },
      { id: 'wh_2', url: 'https://chat.example.com/hooks/T0/decisions', events: ['release.rolled_back', 'sidecar.sync_failed'], lastDelivery: { at: ago(2 * DAY), status: 200 } },
    ],
  };
}

/** AI shadow report for lead_routing/priority. */
export const SHADOW = {
  calls: 412330,
  labels: ['fast_track', 'standard', 'manual_review'],
  rows: [
    [61204, 3880, 410],
    [9915, 268402, 6120],
    [220, 14930, 47249],
  ],
  disagreements: [
    { requestId: 'req_7f2c19', decided: 'standard', ai: 'fast_track', vars: { car: 'Hyundai i20', year: 2021, avg: 7.8, city: 'Pune' } },
    { requestId: 'req_7f1a80', decided: 'standard', ai: 'manual_review', vars: { car: 'Honda City', year: 2016, avg: 6.1, city: 'Mumbai' } },
    { requestId: 'req_7ee042', decided: 'manual_review', ai: 'standard', vars: { car: 'Maruti Baleno', year: 2019, avg: 6.9, city: 'Delhi' } },
  ],
};
