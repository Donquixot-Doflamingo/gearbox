# Contract changes requested

Requests from the admin UI (`agent/ui`). Each says what, why and who is affected. The UI works around every
item today; nothing here blocks it.

## UI-1 · Evaluate an unsaved spec

- **What:** add an optional `spec` (`DecisionSpec`) to `EvaluateRequest` (`POST /apps/{app}/decisions/{name}/evaluate`).
  When present, evaluate it instead of the saved draft or a version (same validation as `validate`).
- **Why:** the transform editor's live preview and the test console should reflect what the author is typing.
  Today the UI offers "Save and preview", which creates a draft revision per preview.
- **Affects:** control plane (evaluate handler), UI.

## UI-2 · Kill-switch state is not readable

- **What:** either add `killed: boolean` and `gate: Name` to `Release` when `type: kill_switch`, or expose the
  current state per environment (for example `killed: true` in `LiveState` of gates in `ResourceSummary.live`,
  or `GET /apps/{app}/environments/{env}/kill-switches`).
- **Why:** `ReleaseCreate` carries `killed`, but nothing returns it, so the UI cannot tell whether a gate is
  currently killed. The UI derives the state from `kill_switch` releases and reads `killed`/`gate` if the server
  adds them (the mock does); otherwise it shows "changed (direction not reported)".
- **Affects:** control plane, UI.

## UI-3 · List and revoke personal access tokens

- **What:** `GET /tokens` (the caller's tokens: id, label, createdAt, expiresAt, lastUsedAt) and
  `DELETE /tokens/{id}`.
- **Why:** only `POST /tokens` exists, so people cannot review or revoke tokens they created.
- **Affects:** control plane, UI, gearboxctl.

## UI-4 · Document the error codes the UI branches on

- **What:** add to `Problem.code` examples: `BREAKING_CHANGE` (cutting a schema version with breaking changes
  and `allowBreaking: false`), `NO_CHANGES` (cutting a version identical to the latest), `INVALID_STATE`
  (approving or withdrawing a change request that is not open), `ALREADY_APPROVED`, `NOT_ROLLING_OUT`
  (`type: rollout` for a decision without a rollout), and `CSRF` (403 when `X-CSRF-Token` is missing or stale;
  the client refetches `/session` on it).
- **Why:** "branch on `code`, never on `detail`" needs the codes to be listed.
- **Affects:** control plane, UI.

## UI-5 · `ResourceSummary.subtype` for more kinds

- **What:** document `subtype` for experiments (`draft | running | stopped`) and schemas (root type, e.g.
  `object`, `string`).
- **Why:** list screens show them without fetching every resource. The UI shows them when present.
- **Affects:** control plane, UI.

## UI-6 · AI nodes across decisions (optional)

- **What:** `GET /apps/{app}/ai-nodes` returning `{decision, nodeId, mode, model, timeoutMs, live: {env: version}}`.
- **Why:** the AI nodes screen currently loads every decision's draft to find AI nodes (N+1 requests).
- **Affects:** control plane, UI.
