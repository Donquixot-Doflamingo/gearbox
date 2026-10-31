# Engine benchmarks

Machine: 2 vCPU sandbox, Intel Xeon @ 2.10GHz, Go 1.24.7, linux/amd64.
Command: `go test -run XXX -bench . -benchmem -count 3 ./engine/ ./engine/internal/...`
(numbers below are the median of 3 runs).

All targets exclude JSON decode of the input (`Snapshot.DecideParsed` with a
pre-decoded document). They include the rules node's runtime input-schema
check, condition evaluation, rollout bucketing, data slots, output-schema
checks and JSON encoding of the output.

| Benchmark | What | Result | Target |
|---|---|---|---|
| `BenchmarkRule7Conditions` | one rule, `all` of 7 structured conditions (BETWEEN, LTE, LT, IN, IN, GTE, CONTAINS), typed input schema | **1.28 µs**, 144 B, 2 allocs | ≤ 2 µs ✔ |
| `BenchmarkTransform12Fields` | 12-field transform (nested targets, `out` reads, string, arithmetic, `in`, `len`) | **11.0 µs**, 3.3 KB, 59 allocs | ≤ 25 µs ✔ |
| `BenchmarkRules50FirstMatch` | 50 rules, only the last (7 conditions) matches | **3.56 µs**, 144 B, 2 allocs | ≤ 30 µs ✔ |
| `BenchmarkLeadRoutingGraph` | lead_routing example: transform (6 fields incl. `mean(map(...))`) → rules (static + dynamic segment, 25 % rollout) → output | 9.3 µs, 1.9 KB, 42 allocs | – |
| `BenchmarkLeadRoutingWithDecode` | the same through `Decide` (includes `encoding/json` decode of the input) | 13.7 µs | – |
| `BenchmarkParallelLeadRouting` | the graph from 2 goroutines (lock-free hot path) | 5.1 µs/op aggregate | – |
| `BenchmarkCompileLeadSet` | compile the 8-resource example set | 1.0 ms | – |
| `internal/hashing BenchmarkRolloutBucket` | murmur3 allocation bucket | 83 ns, 0 allocs | – |
| `internal/jsonschema BenchmarkValid` | validate the lead input document | 645 ns, 0 allocs | – |

Full call through the sidecar (client → Unix socket → sidecar → engine → back,
`clients/go BenchmarkDecideOverUnixSocket`): **≈126 µs** per call, sequential,
same machine (most of it is gRPC framing and the socket round trip).

## Why it is fast

- Structured conditions compile to Go closures over pre-split field paths
  (no expression VM, no string building); list operators use pre-built sets.
- Expressions are compiled once (Expr bytecode); each call reuses pooled
  `vm.VM`s and run state (`sync.Pool`), so the hot path takes no locks.
- Constant `then`/`default`/fallback values are encoded to JSON at compile time;
  when the final output is such a constant, the cached bytes are returned.
- Schema validation of valid documents allocates nothing.
