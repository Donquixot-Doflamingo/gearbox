# 01 · Features

Every feature below follows the same layout: **what it is**, **how it works** (numbered steps), **limits**, and a **case study** from the lead service (`app: lead_service`). Snippets are YAML renderings of the JSON the API takes ([OpenAPI](../contracts/openapi/gearbox-admin.v1.yaml)); paths are relative to `/api/v1`.

| § | Feature | § | Feature |
|---|---|---|---|
| 1 | [Schema registry and evolution](#1-schema-registry-and-schema-evolution) | 10 | [AI node](#10-ai-node-shadow--inline) |
| 2 | [Rule sets](#2-rule-sets-structured-and-expr-conditions) | 11 | [Versions, change requests, releases](#11-versions-change-requests-approvals-releases-rollout-rollback) |
| 3 | [Segment conditions](#3-segment-conditions-static-dynamic-rollout) | 12 | [Sidecar sync and last-known-good](#12-sidecar-sync-and-last-known-good) |
| 4 | [Decision tables](#4-decision-tables) | 13 | [Logs and replay](#13-decision-update-and-exposure-logs-replaying-a-call) |
| 5 | [Transforms before a rule node](#5-transforms-before-a-rule-node) | 14 | [Remote Decision API](#14-remote-decision-api) |
| 6 | [Decision graphs](#6-decision-graphs) | 15 | [gearboxctl and offline bundles](#15-gearboxctl-and-offline-bundles) |
| 7 | [Lookups](#7-lookups) | 16 | [Data and code slots](#16-data-slots-and-code-slots-applies-everywhere) |
| 8 | [Gates and kill switch](#8-gates-and-kill-switch) | | |
| 9 | [Experiments, layers, holdouts, exposure log](#9-experiments-layers-holdouts-and-the-exposure-log) | | |

The running example input (`lead_input@1`):

```json
{ "lead": { "id": "L-1029384", "first_name": "Rahul", "last_name": "Sharma", "phone": "+91-9876543210", "city": "Gurgaon", "source": "app" },
  "car":  { "make": "Maruti", "model": "Swift VXI", "year": 2019, "km": 48200, "price": 525000 },
  "inspections": [ { "part": "engine", "score": 8 }, { "part": "body", "score": 5 }, { "part": "ac", "score": 5.5 } ] }
```

---

## 1. Schema registry and schema evolution

**What.** A per-app registry of named, versioned JSON Schemas. Every `rules` and `table` node must point at a registered `inputSchema` and `outputSchema` (`{name, version}`) **before it can be saved**. Schemas drive type checking at save, input validation at runtime, editor autocomplete and PII tracking.

**Supported JSON Schema (2020-12 subset).**

| Feature | How |
|---|---|
| Nested objects | `properties` to any depth (max 32); nothing is flattened |
| Arrays | `items`, `prefixItems`, `minItems`, `maxItems` (arrays a rule or transform loops over **must** declare `maxItems`) |
| Nullable | `type: [string, "null"]` |
| Enums / constants | `enum`, `const` |
| Bounds and formats | `minimum`, `maximum`, `exclusiveMinimum/Maximum`, `minLength`, `maxLength`, `pattern` (RE2), `format` (`date-time`, `date`, `email`, `uuid`) |
| Reuse | `$defs` and local `$ref` |
| PII | `x-gearbox-pii: true` on a field |
| Root type | Any: object, string, number, array… (an output schema can be a plain string) |

**How it works.**

1. `POST /apps/lead_service/schemas` with `{name, description, spec: {jsonSchema, examples}}` creates a draft (every example must validate).
2. `POST /apps/lead_service/schemas/lead_input/versions` with `{revision, message}` cuts immutable `v1`.
3. A decision references `{name: lead_input, version: 1}`. Saving a `rules`/`table` node without schemas returns `422 SCHEMA_REQUIRED`; pointing at an unregistered version returns `422 SCHEMA_NOT_FOUND`.
4. At runtime the node's input is validated against `inputSchema` before any rule runs. Failure on the entry node → gRPC `INVALID_ARGUMENT`/`INPUT_SCHEMA`; on an inner node → `onError` or the decision fallback.
5. Every literal `then`/`default` is checked against `outputSchema` at save; `=` expressions are checked at runtime.

**Evolution.** A new version must be backward compatible with the previous one unless you pass `allowBreaking: true`.

| Change | Breaking? |
|---|---|
| Add an optional property | no |
| Add `null` to a type, widen an enum | no |
| Remove a property | **yes** (`property_removed`) |
| Make a property required | **yes** (`became_required`) |
| Narrow a type or enum | **yes** (`type_narrowed`) |
| Remove `null` from a type | **yes** |

Check first with `POST /apps/lead_service/schemas/lead_input/compatibility {spec}`. Decisions keep using the version they pin until they are updated; `GET …/schemas/lead_input/usages` lists who uses which version.

**Limits.** Nesting depth 32; names `^[a-z][a-z0-9_]{1,62}$`; AI nodes do not use the registry (they carry an inline `responseSchema`).

### Case study: adding `fuel` to leads

```yaml
# POST /apps/lead_service/schemas   (draft of lead_input, abbreviated)
name: lead_input
spec:
  jsonSchema:
    type: object
    required: [lead, car]
    properties:
      lead:
        type: object
        required: [id, city]
        properties:
          id: { type: string }
          first_name: { type: [string, "null"] }
          phone: { type: string, x-gearbox-pii: true }
          city: { type: string }
          source: { enum: [app, web, partner] }
      car:
        type: object
        required: [year, km, price]
        properties:
          year: { type: integer, minimum: 1980 }
          km: { type: integer, minimum: 0 }
          price: { type: number, minimum: 0 }
      inspections:
        type: array
        maxItems: 50
        items: { type: object, required: [part, score], properties: { part: { type: string }, score: { type: number } } }
```

1. The lead service starts sending `car.fuel`. The author adds `fuel: { enum: [petrol, diesel, cng, electric] }` (optional) to the draft.
2. `POST …/lead_input/compatibility` → `{compatible: true, changes: [{path: /properties/car/properties/fuel, kind: property_added, breaking: false}]}`.
3. Cut `lead_input@2`. `lead_routing` still pins `@1` and keeps working; the author bumps the transform's input when it needs `fuel`.
4. Later someone tries to make `fuel` required → `compatible: false` (`became_required`). They either keep it optional or cut `@3` with `allowBreaking: true` after checking `usages`.

---

## 2. Rule sets (structured and Expr conditions)

**What.** A `rules` node: an ordered list of rules, each `when` (condition, code) → `then` (output, data), plus a `default`.

**How it works.**

1. The node input is validated against `inputSchema`.
2. Rules are ordered by `priority` (higher first; equal keeps list order). `enabled: false` rules are skipped.
3. `mode: first_match` (default) returns the first match's `then`; `all_matches` returns `{"matches": [then, …]}` (`outputSchema` then describes one match).
4. No match → `default`.
5. A rule with `rollout: {percent, salt}` matches only for a stable share of units (`bucket < percent × 100`, bucket 0–9999); calls without a unit never match it. Default salt `"<decision>.<node>.<rule id>"`.

**Condition tree.** Combine with `all` (AND), `any` (OR), `not`. Leaves:

| Leaf | Shape | Notes |
|---|---|---|
| field | `{field, op, value, valueTo}` | `field` is a dotted path into the node input, type-checked against the schema. 35 operators ([reference](04-reference.md#4-condition-operators)) |
| segment | `{segment, op: IN_SEGMENT\|NOT_IN_SEGMENT, key?, attributes?}` | See §3 |
| gate | `{gate, op: GATE_PASSES\|GATE_FAILS}` | See §8 |
| variant | `{experiment, variants: [..]}` | True when the unit is in one of the variants |
| expr | `{expr}` | Any boolean Expr expression |

**Missing fields.** A missing or `null` field makes every positive **and** negative operator false (`NEQ`, `NOT_IN` included); only `NOT_EXISTS` is true. The same applies inside Expr: paths never fail on a missing parent, comparisons with `nil` are false, arithmetic with `nil` is an error (use `?? 0`).

**Limits.** 500 rules per node; expression ≤ 4 KB; RE2 regex only; the decision must stay inside its `budgetMicros` (default 200 µs).

### Case study: eligibility

```yaml
- id: eligibility
  type: rules
  inputSchema:  { name: eligibility_input,  version: 1 }
  outputSchema: { name: eligibility_output, version: 1 }
  mode: first_match
  rules:
    - id: r1
      name: Blocked city
      when: { segment: blocked_cities, op: IN_SEGMENT, key: input.city }
      then: { eligible: false, reason: city_not_served }
    - id: r2
      name: Too old
      when: { field: ageYears, op: GT, value: 12 }
      then: { eligible: false, reason: car_too_old }
    - id: r5
      name: Weak inspection (Expr)
      when: { expr: 'input.inspectionAvg < 4 && input.km > 150000' }
      then: { eligible: false, reason: poor_condition }
  default: { eligible: true, lane: standard, reason: default }
```

| Input (after the transform, §5) | Matched | Output |
|---|---|---|
| `city: Leh` (member of `blocked_cities`) | `r1` | `{eligible: false, reason: city_not_served}` |
| `ageYears: 14` | `r2` | `{eligible: false, reason: car_too_old}` |
| `ageYears: 7, inspectionAvg: 6.2` | none | `default` |
| `ageYears` missing | `r2` is **false** (missing ≠ "> 12") | `default` (unless a later rule matches) |

Writing `{field: ageYears, op: GT, value: "12"}` fails validation: `TYPE_MISMATCH … ageYears is integer in eligibility_input@1`.

---

## 3. Segment conditions (static, dynamic, rollout)

**What.** A segment is a named set of units used inside conditions. Three types:

| Type | Membership | Condition needs | Where membership lives |
|---|---|---|---|
| `static` | Uploaded list (CSV/JSON, up to 1,000,000; HMACs when `pii: true`) | `key` (Expr, e.g. `input.dealer.id`) | Bundle (`segment_members`) |
| `dynamic` | A condition tree over attributes, **evaluated at decision time** | `attributes`: map from the segment's attribute names to Expr over the node input | Nothing stored; evaluated per call |
| `rollout` | Stable `percent` of units: `murmur3(salt + key) mod 10000 < percent × 100`, optional `include` targeting first | `key` (default `ctx.unit`) | Nothing stored |

**How it works.**

1. Create the segment (`POST /apps/{app}/segments`); for static, `PUT …/segments/{name}/members` then cut a version; for dynamic, register an `attributesSchema` first.
2. Reference it from any condition: `{segment, op, key | attributes}`. Validation checks that the segment exists (`SEGMENT_NOT_FOUND`) and that `attributes` match its `attributesSchema`.
3. Combine with field conditions through `all`/`any`/`not` like any other leaf.
4. Test membership without a decision: `POST …/segments/{name}/check` with `key`, `attributes` or `unit` → `{member, reason}` (`listed`, `conditions_matched`, `bucket 731 < 1000`…).
5. Segment versions ship like any resource. With `allowDataReleases` on, a new member list can be published without a change request if every dependent test passes (§11).

**Limits.** 1,000,000 static members; members count toward the bundle limit (64 MB default). Changing a rollout salt reshuffles units. A rollout segment without a key/unit is never a member.

### Case study: a field condition AND a dynamic segment

Goal: premium handling for leads that came from the app **and** are in the `metro_premium` dynamic segment (metro city, price above 8 lakh), but only for a 10% beta.

```yaml
# segment_attrs_city_price@1:  { city: string, price: number }
# POST /apps/lead_service/segments
name: metro_premium
spec:
  type: dynamic
  dynamic:
    attributesSchema: { name: segment_attrs_city_price, version: 1 }
    when: { all: [ { field: city, op: IN, value: [Delhi, Gurgaon, Mumbai, Bengaluru] },
                   { field: price, op: GT, value: 800000 } ] }
---
name: beta_10pct
spec: { type: rollout, rollout: { percent: 10, salt: beta_10pct } }
```

```yaml
# rule inside the eligibility node
- id: r3
  name: Metro premium beta
  when:
    all:
      - { field: source, op: EQ, value: app }                                   # field condition
      - { segment: metro_premium, op: IN_SEGMENT,                                # dynamic segment
          attributes: { city: input.city, price: input.price } }
      - { segment: beta_10pct, op: IN_SEGMENT }                                 # rollout segment, key = ctx.unit
  then: { eligible: true, lane: fast, reason: metro_premium_beta }
```

| Lead | source | city / price | unit bucket (beta_10pct) | r3 |
|---|---|---|---|---|
| A | app | Gurgaon / 950000 | 731 (< 1000) | **match** |
| B | app | Gurgaon / 525000 | 731 | no (price) |
| C | web | Mumbai / 1200000 | 120 | no (source) |
| D | app | Mumbai / 1200000 | 4410 | no (not in 10%) |
| E | app | Mumbai / 1200000 | *(no unit)* | no (rollout needs a unit) |

Check a single case in the console: `POST /apps/lead_service/segments/metro_premium/check {source: draft, attributes: {city: Gurgaon, price: 950000}}` → `{member: true, reason: conditions_matched}`.

---

## 4. Decision tables

**What.** A `table` node: input columns (each an Expr), output columns, rows of cell conditions. Same schema rule as rules nodes (registered `inputSchema` and `outputSchema`; `outputs` must be properties of the output schema).

**How it works.**

1. Each `inputs[].expr` is evaluated once per call (e.g. `input.price`).
2. Each row's `when` holds one cell per input; a missing cell or `-` means "any".
3. The hit policy picks the result: `first` (first matching row), `unique` (validation fails if rows can overlap), `collect` (all matches), `priority` (highest `priority` wins).
4. The matched row's `then` (data) is the output; no match → `default`.

Cell syntax: `-` any · `< 300000`, `>= 5` · `[a..b]` inclusive range · JSON list `["Delhi","Gurgaon"]` = one of · anything else is an Expr using `value` (`value % 2 == 0`).

**Limits.** 5,000 rows per table; the path records the matched row ID.

### Case study: price band

```yaml
- id: price_band
  type: table
  inputSchema:  { name: eligibility_input, version: 1 }
  outputSchema: { name: price_band_output, version: 1 }   # { band: string, max_discount: integer }
  hitPolicy: first
  inputs:
    - { name: price, expr: input.price }
    - { name: age,   expr: input.ageYears }
    - { name: city,  expr: input.city }
  outputs: [band, max_discount]
  rows:
    - { id: row_1, when: { price: "< 300000", age: "<= 5" },                                then: { band: C, max_discount: 5000 } }
    - { id: row_2, when: { price: "[300000..800000]", age: "<= 8", city: '["Gurgaon","Delhi"]' }, then: { band: A, max_discount: 15000 } }
    - { id: row_3, when: { price: "[300000..800000]" },                                    then: { band: B, max_discount: 10000 } }
  default: { band: D, max_discount: 0 }
```

The running example (Gurgaon, 525000, age 7) → `row_2` → `{band: A, max_discount: 15000}`; path step `{nodeId: price_band, nodeType: table, outcome: row_2}`.

---

## 5. Transforms before a rule node

**What.** A `transform` node reshapes JSON with one Expr per target field. Put one before a rules or table node so the rules see a flat, typed shape (their `inputSchema`) instead of the raw request. A transform's `outputSchema` is optional; when set, the output is checked against it and the next rules node's `inputSchema` must accept it (`SCHEMA_EDGE_MISMATCH` otherwise).

**How it works.**

1. `mode: build` starts from `{}`; `mode: patch` starts from a copy of the input.
2. `fields` are assigned in order; a later field may read `out` (fields so far). Targets are dotted paths; `items[0].x` and `items[].x` (append) are allowed.
3. `defaults` (data) fill paths still missing; `drop` removes paths (last); `omitNil: true` (default) leaves out nil results.
4. Alternatively one `expression` builds the whole output.
5. Values derived from `x-gearbox-pii` fields stay marked as PII in the output.

**Limits.** 500 fields per node.

### Case study: `normalize` → `eligibility`

```yaml
- id: normalize
  type: transform
  outputSchema: { name: eligibility_input, version: 1 }
  fields:
    - { target: city,          expr: input.lead.city }
    - { target: source,        expr: input.lead.source }
    - { target: ageYears,      expr: ctx.now.Year() - input.car.year }
    - { target: km,            expr: input.car.km }
    - { target: price,         expr: input.car.price }
    - { target: inspectionAvg, expr: 'mean(map(input.inspections ?? [], .score)) ?? 0' }
  next: eligibility
```

Output for the running example at `ctx.now = 2026-10-30`: `{city: Gurgaon, source: app, ageYears: 7, km: 48200, price: 525000, inspectionAvg: 6.17}`. Without the transform, `eligibility` would need `lead_input` as its input schema and conditions on `car.price`, `lead.city`; with it, rules stay short and the raw shape can evolve (`lead_input@2`) without touching rules.

---

## 6. Decision graphs

**What.** A decision is a graph of up to 200 nodes: `rules`, `table`, `transform`, `switch`, `lookup`, `gate`, `experiment`, `subdecision`, `ai`, `output`. A single rule set is a one-node decision.

**How it works.**

1. Evaluation starts at `entry`. Each node receives `input` = its `from` expression, or by default the output of the node that ran just before (the request for the entry node).
2. `next` names the following node; `next: end` returns this node's output; no `next` ends the decision here.
3. `switch`: first `cases[].when` (Expr) that is true picks `next`; otherwise `default`.
4. Errors (schema failure, expression error, AI timeout) go to the node's `onError`; with no `onError` the decision returns its `fallback` with `fallbackUsed: true`.
5. `subdecision` runs another decision of the same app (`input` Expr optional); cycles are rejected.
6. Expressions see `input`, `request`, `nodes.<id>` (outputs of nodes that already ran), `out` (transform only), `ctx` (`now`, `unit`, `requestId`, `env`).
7. Validation rejects cycles, unreachable nodes, references to nodes that may not have run on some path (`NODE_MAY_NOT_HAVE_RUN`), schema mismatches on edges, cost over budget.

| Node | Output (`nodes.<id>`) |
|---|---|
| `rules` / `table` | matched `then` / row outputs (or default) |
| `transform` / `output` | built object |
| `switch` | case taken |
| `lookup` | looked-up value or `default` |
| `gate` | gate result |
| `experiment` | assignment (variant, parameters) |
| `subdecision` | the other decision's output |
| `ai` | the model's validated answer |

### Case study: `lead_routing`

```
 normalize ─▶ eligibility ─▶ route(switch) ──eligible──▶ price_band ─▶ output
                                  │
                                  └──default──▶ reject(transform, next: end)
```

(§10 later inserts an AI node `priority` between `price_band` and `output`, with `onError: default_priority`.)

```yaml
spec:
  entry: normalize
  nodes:
    - { id: normalize, type: transform, outputSchema: { name: eligibility_input, version: 1 }, fields: [ … ], next: eligibility }
    - { id: eligibility, type: rules, inputSchema: …, outputSchema: …, rules: [ … ], default: …, next: route }
    - id: route
      type: switch
      cases: [ { id: eligible, when: nodes.eligibility.eligible, next: price_band } ]
      default: reject
    - { id: price_band, type: table, from: nodes.normalize, …, next: output }
    - id: reject
      type: transform
      fields: [ { target: route, expr: '"reject"' }, { target: reason, expr: nodes.eligibility.reason } ]
      next: end
    - id: output
      type: output
      fields:
        - { target: route,  expr: 'nodes.eligibility.lane == "fast" ? "fast_track" : "standard"' }
        - { target: reason, expr: nodes.eligibility.reason }
        - { target: band,   expr: nodes.price_band.band }
  output: { schema: { name: routing_output, version: 1 } }
  fallback: { route: manual_review, reason: fallback }
```

Response path for the running example: `normalize:ok → eligibility:<default> → route:eligible → price_band:row_2 → output:ok`. If `price_band` hit an expression error and had no `onError`, the caller would get `{route: manual_review, reason: fallback}` and `fallback_used: true`.

---

## 7. Lookups

**What.** A versioned key → value table shipped in the bundle (city → zone, pincode → serviceability). Values must match the lookup's `valueSchema` (a registered schema).

**How it works.**

1. Create `{keyType: string|number, valueSchema: {name, version}}`.
2. `PUT /apps/{app}/lookups/{name}/rows` with CSV (`key,value`, value as JSON or plain string) or JSON `{rows: [{key, value}]}`; cut a version.
3. Read it with a `lookup` node (`{lookup, key, default}`) or `lookup(name, key, default)` in any expression.
4. Data releases (§11) can publish new rows without a change request when the environment allows it.

**Limits.** Rows count toward the bundle limit; very large tables belong in the caller's own service.

### Case study: city zone

```yaml
- id: zone
  type: lookup
  lookup: city_zone            # valueSchema zone_value@1: { zone: string, slaHours: integer }
  key: input.lead.city
  default: { zone: other, slaHours: 48 }
```

Rows `Gurgaon → {zone: ncr, slaHours: 4}`. The output node adds `{target: slaHours, expr: nodes.zone.slaHours}`. A new city is a CSV upload plus `POST /apps/lead_service/environments/prod/releases {type: data, items: [{kind: lookups, name: city_zone, version: 8}]}`.

---

## 8. Gates and kill switch

**What.** A gate is a boolean feature flag: ordered rules (`when` → `value`, optional per-rule rollout) and a `default`. Used directly (`CheckGate`) or inside decisions (`gate` node, `{gate, op: GATE_PASSES}` condition).

**How it works.**

1. Evaluate `enabled`; a disabled gate returns `default` (`rule_id: disabled`).
2. Rules run in order; the first whose `when` matches (and whose rollout includes the unit) returns its `value`; else `default`.
3. `attributesSchema` (optional) type-checks the rule conditions; attributes come from the caller (`attributes_json`).
4. **Kill switch:** `POST /apps/{app}/environments/{env}/releases {type: kill_switch, gate, killed: true, reason}` publishes a new generation immediately (approver role); every sidecar returns `false` with `rule_id: killed` within seconds. `killed: false` restores it.

**Limits.** Rollouts without a unit never match (`rule_id: no_unit` when only rollout rules could apply).

### Case study: dealer auction v2

```yaml
name: dealer_auction_v2
spec:
  rules:
    - { id: internal, when: { field: city, op: IN, value: [Gurgaon] }, value: true }
    - { id: ramp, when: { field: source, op: IN, value: [app, web] }, value: true, rollout: { percent: 20 } }
  default: false
```

The auction service goes down at 14:02. The on-call approver publishes `{type: kill_switch, gate: dealer_auction_v2, killed: true, reason: auction service down}` → generation 43; `CheckGate` returns `{value: false, rule_id: killed, bundle_generation: 43}` on every pod by 14:02:05.

---

## 9. Experiments, layers, holdouts and the exposure log

**What.** An experiment assigns a unit to one of ≥ 2 weighted variants, each with `parameters` (data). Layers make experiments mutually exclusive; `holdoutPercent` (0–50) keeps a share of units out; `targeting` limits who enters. Lifecycle `draft → running → stopped` (after stop everyone gets `stoppedVariant`).

**How it works.**

1. No unit → `in_experiment: false`, reason `no_unit`.
2. Not running → `stopped` (gets `stoppedVariant`).
3. `targeting` false → `not_targeted`.
4. Layer: the unit's layer bucket must fall in this experiment's allocation, else `layer_conflict`.
5. Holdout bucket → `holdout`.
6. Variant by weighted bucket (salt = experiment salt) → `assigned`, with `parameters`.
7. Every assignment (`GetVariant`, an `experiment` node, a variant condition) writes an **exposure record** that is **never sampled** (`GET /apps/{app}/environments/{env}/exposure-logs`). Analysis happens outside Gearbox from this log.

**Limits.** Layer allocations sum to ≤ 100; holdout ≤ 50%.

### Case study: callback script test

```yaml
# layers/lead_contact: allocations [{experiment: callback_script, percent: 50}, {experiment: sms_first, percent: 50}]
name: callback_script
spec:
  status: running
  layer: lead_contact
  holdoutPercent: 10
  targeting: { field: source, op: EQ, value: app }
  variants:
    - { name: control,   weight: 50, parameters: { script: v1 } }
    - { name: treatment, weight: 50, parameters: { script: v2, callWithinMin: 5 } }
```

`GetVariant(callback_script, unit L-1029384)` → `{in_experiment: true, variant: treatment, parameters: {script: v2, callWithinMin: 5}, reason: assigned}`; exposure record `{type: exposure, unitId: L-1029384, experiment: callback_script, experimentVersion: callback_script@3, variant: treatment, reason: assigned}`. A `sms_first` call for the same unit returns `layer_conflict`.

---

## 10. AI node (shadow → inline)

**What.** One call to the **Integration Hub** per AI node invocation. The Hub contract is a **placeholder** ([`integration-hub-ai.yaml`](../contracts/openapi/integration-hub-ai.yaml)); the owner fills it in and only the sidecar's Hub client changes. AI nodes use an inline `responseSchema` (no registry schemas).

**How it works.**

1. Only `vars` (Expr) reach the prompt; `{{ var }}` placeholders in `prompt.user`/`prompt.system`. A var derived from a PII field fails validation (`PII_IN_PROMPT`).
2. The sidecar renders the prompt and calls `POST {GEARBOX_HUB_URL}/v1/ai/completions` (placeholder) with model alias, messages, `response_schema`, `max_output_tokens`, timeout, and an `Idempotency-Key` of `request_id:node_id`.
3. Timeout, Hub error, rate limit or an answer that fails `responseSchema` → `onError` (required).
4. Modes:

| Mode | Who decides | Latency added |
|---|---|---|
| `shadow` | The `onError` path always decides; the AI call runs in the background and its answer is recorded next to the deciding answer (path outcome `shadow`) | none |
| `inline` | The AI answer if it arrives within `timeoutMs` and validates; else `onError` | up to `timeoutMs` |
| `precompute` | Answers computed ahead of time into a lookup (not built yet: ticket GBX-095) | none |

5. Cache (`cache.ttlSeconds`, `maxEntries`) keyed by rendered prompt + model + schema; `budget.maxCallsPerMinute` is for the whole app and environment.
6. `GET /apps/{app}/decisions/{name}/nodes/{nodeId}/shadow-report` compares AI with the deciding path: agreement, confusion matrix, latency p50/p95, timeout and schema-failure rates, disagreements (PII-free vars).

**Limits.** `timeoutMs` 50–10,000. AI answers are excluded from the replay guarantee (the model behind an alias can change). The test console skips AI by default (`skipAI: true`).

### Case study: follow-up priority

```yaml
- id: priority
  type: ai
  model: reasoning-default
  mode: shadow
  timeoutMs: 1500
  vars:
    car:  'input.car.make + " " + input.car.model + ", " + string(input.car.year)'
    avg:  nodes.normalize.inspectionAvg
    city: input.lead.city
  prompt:
    system: You rate follow-up priority for used-car leads. Answer only with the JSON schema.
    user: 'Car: {{ car }}. Inspection average: {{ avg }}. City: {{ city }}.'
  responseSchema:
    type: object
    required: [route, reason]
    properties:
      route:  { enum: [fast_track, standard, manual_review] }
      reason: { type: string, maxLength: 200 }
  cache: { ttlSeconds: 21600, maxEntries: 50000 }
  budget: { maxCallsPerMinute: 600 }
  onError: default_priority
  next: output
```

1. Ship in `shadow` (v18). Callers see no added latency; `default_priority` decides.
2. After 7 days the shadow report shows `agreement: 0.91`, p95 900 ms, timeout rate 0.4%.
3. Adding `lead.phone` to `vars` fails validation with `PII_IN_PROMPT`.
4. The author cuts v19 with `mode: inline`, opens a change request with `rollout: {percent: 5}`, then steps 25 → 100.

---

## 11. Versions, change requests, approvals, releases, rollout, rollback

**What.** Every resource has one editable draft and immutable numbered versions. Change requests ask to make versions live in one environment; releases publish signed bundles with an ever-increasing **generation**.

**How it works.**

1. **Draft**: `PUT /apps/{app}/{kind}/{name}/draft {revision, spec}`; a stale `revision` → `409 REVISION_CONFLICT`. `POST …/validate` gives live feedback.
2. **Version**: `POST …/versions {revision, message}`; the draft must validate and, for decisions, every test must pass (`422 TESTS_FAILED`). `GET …/diff?from=17&to=draft` shows a YAML diff.
3. **Change request**: `POST /apps/{app}/change-requests {environment, title, items: [{kind, name, version, rollout?}]}`. The whole would-be bundle is validated and tested before it opens.
4. **Approval** per environment policy: `requiredApprovals` (0–5), `authorCanApprove` (`403 SELF_APPROVAL` otherwise). `…/approve`, `…/reject {comment}`, `…/comments`, `…/withdraw`.
5. **Release**: `POST /apps/{app}/environments/{env}/releases` builds the full bundle, re-runs every test, signs, uploads and notifies sidecars.

| `type` | Needs | Who |
|---|---|---|
| `change_request` | an approved change request | author or approver |
| `data` | segment/lookup versions; no review when `allowDataReleases` and dependent tests pass | author |
| `rollout` | a decision currently rolling out; new `percent` (100 completes, 0 reverts) | approver |
| `rollback` | `toGeneration`; break-glass when no change request (flagged in audit) | approver |
| `kill_switch` | `gate`, `killed` | approver |

6. **Gradual rollout**: both versions are in the bundle; `bucket(salt, unit) < percent × 100` → new version, so every sidecar gives the same unit the same version. Calls without a unit get the previous version.
7. **Promotion**: the same version (same `contentHash`) goes to the next environment through a new change request.

### Case study: widening band A

1. Asha edits `lead_routing`, runs tests, cuts v18 ("Widen band A to 8.5 lakh").
2. Opens `cr_214` for `prod`: `items: [{kind: decisions, name: lead_routing, version: 18, rollout: {percent: 5}}]`.
3. Ravi (approver) reviews the diff and test results, approves. Asha cannot self-approve in prod.
4. Release `{type: change_request, changeRequestId: cr_214}` → generation 42; 5% of units get v18.
5. Fallback rate stays flat; Ravi steps `{type: rollout, decision: lead_routing, percent: 50}` (gen 43) then `100` (gen 44).
6. Pune leads misroute on v18: Ravi publishes `{type: rollback, toGeneration: 41, reason: v17 misroutes Pune leads}` → gen 45 (content of 41, higher generation).

---

## 12. Sidecar sync and last-known-good

**What.** The sidecar keeps the app's bundle for its environment current, never serving an unverified or untested bundle, and survives control-plane outages.

**How it works.**

1. Start: load the last-known-good bundle from `GEARBOX_CACHE_DIR` (re-verified) or `GEARBOX_FALLBACK_BUNDLE`; ready once a verified bundle is active.
2. Listen to `GET /sidecar/v1/watch` (SSE). On `event: generation`, wait a random 0–2 s.
3. `GET /sidecar/v1/head` (`If-None-Match`, `have=<hashes>`) → generation, envelope URL, URLs of missing blobs (S3 presigned, or `/sidecar/v1/blobs/{sha256}` with the filesystem store). Poll every 30 s regardless; `304` = unchanged.
4. Download envelope and missing blobs **from the object store**.
5. Check in order: Ed25519 signature with a trusted `key_id`; generation greater than the active one; `min_engine_version` ≤ own engine version; every blob SHA-256; decompressed size limit.
6. Compile, run the bundle's test cases, atomically swap the snapshot pointer, write the bundle to disk (temp + rename + fsync), write an update record (`swapped`).
7. Any failure: keep the current generation, write `kept_previous` with the error, count `gearbox_sync_failures_total{reason}`; Fleet shows it.
8. Heartbeat `POST /sidecar/v1/heartbeat` every 30 s (generation, versions, p99, fallback rate, last error).

**Limits.** Bundle ≤ `bundleLimitMB` (64 MB compressed default); feed reconnect backoff 1 s → 60 s with jitter. New pods need the control-plane API for the head (or a cached/fallback bundle).

### Case study: control plane outage

1. 09:00 the control plane is down. Running sidecars keep answering with generation 42; watch reconnects with backoff; polls fail and are counted.
2. 09:05 HPA adds a pod. Its cache volume is empty and the head endpoint is unreachable → not ready; the old pods keep serving. A pod restart on the same node reloads 42 from its cache volume and is ready in seconds.
3. 09:20 the control plane returns. The feed reconnects, the next head poll returns 42 (`304` on pods already at 42), the new pod syncs and becomes ready.
4. A bad publish (gen 43 fails a test on load because of an engine bug) → every sidecar keeps 42 and reports `kept_previous`; nobody serves 43.

---

## 13. Decision, update and exposure logs; replaying a call

**What.** Three record types, written to a non-blocking ring buffer and shipped in batches (stdout, `POST /sidecar/v1/logs`, or OpenTelemetry):

| Log | When | Sampling |
|---|---|---|
| Decision | per `Decide` call | `logging.sample` per decision, else environment `logSampleRate`; `force_log` per call |
| Update | per sync that changes something or fails | never sampled |
| Exposure | per experiment assignment | **never sampled** |

Decision records hold `requestId`, `decisionVersion`, `dependencyHash`, `generation`, `unitId`, `evaluatedAt`, `path`, `fallbackUsed`, `evalMicros`, `status`, HMACs of input and output, and only the fields listed in `logging.fields` (PII never). AI calls add model, latency, cache hit and answer hash (payloads only with `logging.aiPayloads`).

**How replay works.**

1. Find the record: `GET /apps/{app}/environments/{env}/decision-logs?requestId=req_8c1f2a`.
2. Get the input (from the caller's own logs, or from `input` when every field was logged).
3. Call `Decide` with `options.pin_version` = the logged version and `options.now` = `evaluatedAt`, same `unit_id` (or `POST …/decisions/{name}/evaluate {source: version, version, ctx: {now, unit}}`).
4. Same `dependency_hash`, input, unit and `evaluated_at` → same output (AI nodes excepted). A different `dependency_hash` means a segment, lookup, gate or experiment changed since.

**Limits.** Server-side retention default 14 days; ≤ 1,000 records per upload batch; a full buffer drops records and counts `gearbox_log_dropped_total`.

### Case study: "why was L-1029384 rejected?"

Support has `requestId: req_8c1f2a`. The record shows `decisionVersion: lead_routing@17`, path `eligibility:r2 → route:default → reject:ok`, `evaluatedAt: 2026-10-31T09:15:22Z`. The analyst replays with `pin_version: 17, now: 2026-10-31T09:15:22Z` and gets the same reject: `ageYears` was 13 because the lead service sent `year: 2013`. Not a rules bug.

---

## 14. Remote Decision API

**What.** The same engine behind HTTP for callers that cannot run a sidecar (batch jobs, other clouds, browsers via a backend). Same request and response as `Decide`, as JSON; expect a network round trip.

| Endpoint | Same as |
|---|---|
| `POST /apps/{app}/environments/{env}/decide` | `Decide` |
| `POST /apps/{app}/environments/{env}/gates/{name}/check` | `CheckGate` |
| `POST /apps/{app}/environments/{env}/experiments/{name}/assign` | `GetVariant` |

Auth: an app credential (`gbx_cred_…`) or a personal token. Options: `trace`, `pinVersion`, `now`, `forceLog`.

### Case study: nightly re-scoring

A nightly job re-scores 20,000 stale leads from a data pipeline that has no sidecar:

```http
POST /api/v1/apps/lead_service/environments/prod/decide
Authorization: Bearer gbx_cred_prod_7Hq2_…
{"decision": "lead_routing", "unitId": "L-1029384", "requestId": "nightly-2026-10-31-0001", "input": { … }}
→ 200 {"output": {"route": "standard", "reason": "default"}, "decisionVersion": "lead_routing@18", "dependencyHash": "sha256:…", "generation": 44, "fallbackUsed": false, …}
```

---

## 15. gearboxctl and offline bundles

**What.** The CLI for CI and local work, using the same HTTP API, plus an **offline mode** for the sidecar.

| Command | Does |
|---|---|
| `gearboxctl login` | Store a personal access token (`gbx_pat_…`) |
| `gearboxctl push <dir>` | Create/update drafts from YAML files (schemas first, then segments, lookups, gates, experiments, layers, decisions) |
| `gearboxctl pull <dir>` | Write drafts or versions as YAML |
| `gearboxctl validate` / `test` | Validate and run tests locally with the engine package, or on the server |
| `gearboxctl version cut <kind>/<name> -m …` | Cut versions |
| `gearboxctl change open --env prod …` | Open a change request |
| `gearboxctl release …` | Publish (change request, data, rollout, rollback, kill switch) |
| `gearboxctl bundle export --env prod -o lead.bundle` | Write an offline bundle file |
| `gearboxctl keys generate --out <dir>` | Create an Ed25519 signing key and its trusted-keys line (development) |

Offline sidecar (no control plane): `gearbox-sidecar --bundle-file lead.bundle` (signed) or `gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned [--watch]`. `--allow-unsigned` is accepted only with one of those two flags; bundles from the control plane are always signature-checked.

### Case study: hermetic CI for the lead service

1. The lead service repo keeps `decisions/` YAML (same shapes as the API).
2. CI runs `gearboxctl test decisions/` (engine package, no server) and then integration tests against `gearbox-sidecar --definitions-dir decisions --allow-unsigned` in a container.
3. On merge, CI runs `gearboxctl push decisions/ && gearboxctl version cut decisions/lead_routing -m "$COMMIT_MSG" && gearboxctl change open --env stage …`. Release to prod still needs an approver in the UI.

---

## 16. Data slots and code slots (applies everywhere)

| Kind | Fields | Rule |
|---|---|---|
| **Code** | `when` (expr leaf), `expr`, `from`, transform/output `fields[].expr`, switch `cases[].when`, segment `key`/`attributes`, lookup `key`, table `inputs[].expr`, AI `vars` | Always Expr. A literal string needs quotes: `'"reject"'` |
| **Data** | rule `then`, `default`, decision `fallback`, transform `defaults[].value`, experiment `parameters`, test `expect` | Literal JSON. A string starting with `=` is an expression (`"=input.lead.id"`); `"=="` escapes a literal leading `=` |
