# Contract change requests (data plane)

## 1. engine.proto: decision_version comment
`DecideResponse.decision_version` says `"lead_routing@v17"`; the lead decided `name@17`
everywhere. Fix the comment. Affects: docs only.

## 2. engine.Set: installation HMAC key for pii segments
`engine.Compile(set)` cannot know the installation key used to HMAC `pii` segment members.
Worked around with an additive `engine.CompileWithOptions(set, CompileOptions{PIIKey})`
(the sidecar passes `GEARBOX_HMAC_KEY_FILE`) and `engine.SegmentMemberHMAC(key, member)`
(= hex HMAC-SHA256, identical to the control plane's `PIIMember`). Request: document
`CompileOptions` in `api.go` or move the key into the contract. Affects: control plane
(evaluate/test console with pii segments must call `CompileWithOptions` with its key).

## 3. engine/api.go Error reasons
`CheckGate`/`GetVariant`/`CheckSegment` now return `*engine.Error` with reasons
`GATE_NOT_FOUND`, `EXPERIMENT_NOT_FOUND`, `SEGMENT_NOT_FOUND`, `INVALID_ARGUMENT`,
`NODE_ERROR`; `Decide` may also return `NOT_READY`. Request: list them in the `Error` comment.

## 4. AICall request ID / shadow results
`AICall` has no request ID and no mode; shadow outcomes arrive after `Decide` returns.
Worked around with an optional `engine.AIShadowRecorder` interface and a per-request
wrapper in the sidecar. Request: add `RequestID` and `Mode` to `AICall` in a future revision.

## 5. Experiment/gate node output shapes (OpenAPI)
The OpenAPI does not define node outputs. Implemented: gate `{value, rule}`, experiment
`{variant, parameters, inExperiment, reason}`, switch `{case}` (switch passes its input
through to the next node), all_matches/collect `{matches: [...]}`. Request: document them
in components/schemas (NodeBase or a NodeOutputs section). Affects: UI autocomplete.

## 6. Not-in-experiment variant
`VariantResult.variant` is filled with the default variant (stoppedVariant, else the first)
and its parameters when `inExperiment=false`. Request: document in the OpenAPI/proto.
