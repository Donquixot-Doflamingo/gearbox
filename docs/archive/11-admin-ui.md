# 11 · Admin UI

The admin UI is a SvelteKit app. [`ui-prototype/index.html`](ui-prototype/index.html) shows every panel with sample data (internal domain; swap for neutral data before publication); each panel maps to one route and a small set of components below, so it can be converted screen by screen.

## 1. Stack

- SvelteKit 2 + Svelte 5 (runes: `$state`, `$derived`, `$props`), TypeScript strict, `adapter-static` (served by `gearbox-server` or any CDN) — the API does the auth, so no server-side rendering is needed.
- Generated Connect TypeScript client (`@connectrpc/connect-web` + `buf generate`); one `api.ts` that sets credentials and error handling.
- Editors: CodeMirror 6 for Expr with autocomplete from the input schema and the function list; graph canvas with Svelte Flow (`@xyflow/svelte`).
- Styling: plain CSS with design tokens (CSS custom properties) from the prototype; light and dark.
- Tests: Vitest + Testing Library for components, Playwright for flows, `svelte-check`, ESLint, Prettier; axe checks in Playwright.

## 2. Route map

| Route | Prototype panel | Main components |
|---|---|---|
| `/` | Overview | `StatTile`, `ReleaseFeed`, `FleetHealth` |
| `/apps/[app]/decisions` | Decisions | `DataTable`, `KindBadge`, `EnvVersionChips` |
| `/apps/[app]/decisions/[name]` | Graph editor | `GraphCanvas`, `NodeInspector`, `TestPanel`, `VersionBar` |
| `…/[name]/nodes/[node]` (rules) | Rule set editor | `RuleList`, `ConditionBuilder`, `ExprEditor`, `ThenEditor` |
| `…/[name]/nodes/[node]` (table) | Decision table | `TableGrid`, `CellEditor`, `HitPolicySelect`, CSV import |
| `…/[name]/nodes/[node]` (transform) | Transform mapper | `FieldMapList`, `ExprEditor`, `JsonPreview` (input ↔ output) |
| `/apps/[app]/lookups` | Lookups | `DataTable`, `CsvUpload`, `KeyPreview` |
| `/apps/[app]/gates`, `/apps/[app]/experiments` | Gates & experiments | `GateRules`, `RolloutSlider`, `VariantWeights`, `LayerMap` |
| `/apps/[app]/segments` | Segments | `DataTable`, `CsvUpload`, `MembershipCheck` |
| `/apps/[app]/ai` | AI nodes | `PromptEditor`, `SchemaEditor`, `ShadowReport` |
| `/changes`, `/changes/[id]` | Releases & approvals | `DiffView`, `TestResults`, `ApproveBar`, `RolloutStepper` |
| `/fleet` | Sidecar fleet | `FleetTable`, `GenerationSpread` |
| `/logs`, `/logs/[requestId]` | Decision log | `LogQuery`, `LogTable`, `PathTrace` |
| `/audit` | Audit | `AuditTable` |
| `/settings/*` | Settings | apps, credentials, members, environments, webhooks |

`/apps/[app]` and the environment (`?env=prod`) live in the top bar and are kept in the URL so every link is shareable.

## 3. Rules for the conversion

- One Svelte component per prototype block marked `data-component="…"` in the HTML.
- Sample data in the prototype's `DATA` object becomes typed fixtures used by Vitest and a mock Connect transport, so the UI can be built before the API.
- Tokens in the prototype's `:root` become `src/lib/styles/tokens.css` unchanged.
- No direct `fetch`; every call goes through the generated client.
- Forms: the server's validation errors (field path + message) are shown next to the field; save is disabled while invalid or unchanged.
- Destructive or production-affecting actions (publish to prod, rollback, revoke credential) require a confirm dialog that repeats the target environment.
- Accessibility: keyboard reachable graph editor (arrow keys move selection, Enter opens inspector), visible focus, labels on every control.
