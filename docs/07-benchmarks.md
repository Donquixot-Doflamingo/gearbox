# 07 · Benchmarks

Every number on this page was measured; none is estimated or copied from elsewhere. The raw output is committed in [`docs/benchmarks/`](benchmarks/), and each table names the command that produced it. When a number is poor or noisy, it is shown as measured.

> **Read this first: the machine.** All runs used a cloud sandbox with **2 vCPUs** (Intel Xeon @ 2.10 GHz, shared, Linux amd64) and Go 1.24.7. The load generator, the sidecar and (for propagation) the control plane all ran **on the same 2 vCPUs**, so they compete for CPU. Production pods with dedicated cores should do better, but that has not been measured yet. Measure on your own hardware before setting SLOs: `make bench-engine bench-sidecar bench-load bench-propagation`.

## Summary

| What | Result | Command |
|---|---|---|
| Engine: `lead_routing` graph (transform → rules with segments and rollout → output), in-process | **9.6–10.1 µs** per decision | `make bench-engine` |
| Engine: one rule, 7 conditions | **1.29–1.43 µs** | `make bench-engine` |
| Full call: Go client → Unix socket → sidecar → engine → back, 1 caller | **p50 212–254 µs, p99 577–753 µs** | `make bench-sidecar` |
| Throughput, one sidecar, Go client, 32 concurrent callers | **945,521 calls/min** (15,758/s), p99 6.2 ms, 0 errors | `make bench-load` |
| Sustained 60 s at 8 concurrent callers | **693,705 calls in 60.03 s** (693,369/min), p99 2.9 ms, 0 errors | `LEVELS=8 DURATION=60s make bench-load` |
| Publish API call: build, test, sign and upload a bundle | **median 17.4 ms** (15.7–21.9) | `make bench-propagation` |
| Publish returns → sidecar serves the new version, jitter off | **median 4.4 ms** (3.3–7.7) | `JITTER=0s make bench-propagation` |
| Same, default 2 s fetch jitter | **median 1,144 ms** (451–1,972): the jitter is deliberate | `JITTER=2s N=8 make bench-propagation` |
| UI: click "Publish" → example service returns the new answer (default jitter) | **1.1 s and 1.41 s** in two runs | `scripts/demo/ui_demo.py` |
| Python and Java clients: latency and throughput | **not measured yet** (see [below](#5-python-and-java-clients)) | `bench.py`, `java -jar … bench` |

## 1. Engine (in process)

What it measures: the time to evaluate one decision on already-decoded input, inside the Go process, with no network. This is the work the sidecar does per call.

How: `cd engine && go test -run '^$' -bench . -benchmem -count 3 .` (`make bench-engine`), with benchmarks defined in [`engine/bench_test.go`](../engine/bench_test.go). Raw output: [`benchmarks/engine-bench.txt`](benchmarks/engine-bench.txt).

| Benchmark | What it evaluates | ns/op (3 runs) | B/op | allocs/op |
|---|---|---|---|---|
| `Rule7Conditions` | one rule with 7 structured conditions | 1,367 · 1,292 · 1,428 | 144 | 2 |
| `Rules50FirstMatch` | 50 rules, only the last matches | 3,716 · 3,709 · 3,541 | 144 | 2 |
| `Transform12Fields` | a transform node with 12 Expr fields | 12,044 · 11,357 · 15,269 | 3,297 | 59 |
| `LeadRoutingGraph` | the example graph: transform (6 fields) → 4 rules using static, dynamic and rollout segments → output | 9,649 · 10,021 · 10,144 | 1,864 | 42 |
| `LeadRoutingWithDecode` | the same, including JSON decode of the input | 15,532 · 15,050 · 13,942 | 4,063 | 90 |
| `ParallelLeadRouting` | the graph from 2 goroutines (`b.RunParallel`) | 5,743 · 4,872 · 5,787 | 1,802 | 38 |
| `CompileLeadSet` | compiling the example definitions (done once per bundle, not per call) | 1,068,862 · 1,083,622 · 1,102,025 | 472,999 | 4,982 |

An earlier run on the same machine gave `Rule7Conditions` 1,258–1,288 ns, `Transform12Fields` 10,879–12,717 ns, `LeadRoutingGraph` 9,396–10,307 ns and `CompileLeadSet` 1.04–1.41 ms. The spread between runs is the shared CPU.

## 2. Full call latency, one caller

What it measures: one Go client calling `Decide("lead_routing", …)` in a loop, one call at a time. This covers serialization, gRPC over a Unix socket, the sidecar, the engine, the decision log (1% sampled) and the way back.

How: `make bench-sidecar` ([script](../scripts/integration/bench-sidecar.sh)). It starts an isolated sidecar on `examples/definitions` with no control plane, then runs `gearbox-probe bench lead_routing in.json -n 5000` three times. Each call uses a different unit ID. Raw output: [`benchmarks/sidecar-isolated.txt`](benchmarks/sidecar-isolated.txt).

| Run | Calls | p50 | p95 | p99 | max |
|---|---|---|---|---|---|
| 1 | 5,000 | 254 µs | 500 µs | 708 µs | 4.1 ms |
| 2 | 5,000 | 236 µs | 430 µs | 753 µs | **54.6 ms** |
| 3 | 5,000 | 212 µs | 386 µs | 577 µs | 4.6 ms |

The 54.6 ms maximum in run 2 is a single outlier. It is most likely a CPU scheduling stall or GC pause on the shared 2-vCPU machine, but it was not investigated further. This is why the clients use a deadline plus fallback: the example services use a 5 ms deadline. Earlier runs gave p50 218–261 µs and p99 663–851 µs.

Of a ~230 µs call, the engine work is about 10–15 µs (table 1). The rest is gRPC, protobuf, JSON and the socket round trip.

## 3. Throughput under load (calls per minute)

What it measures: N goroutines share **one** Go client, as a service would, and call `lead_routing` in a tight loop for 10 s per level. The table reports how many calls completed and the latency of each call.

How: `make bench-load` ([script](../scripts/integration/bench-load.sh)), which runs `gearbox-probe load lead_routing in.json -c 1,4,8,16,32 -d 10s` against an isolated sidecar. Each level starts with 500 warm-up calls. Raw output: [`benchmarks/go-load-cloud.jsonl`](benchmarks/go-load-cloud.jsonl).

| Concurrent callers | Calls in 10 s | Calls/sec | **Calls/min** | p50 | p95 | p99 | max | Errors |
|---|---|---|---|---|---|---|---|---|
| 1 | 38,869 | 3,886 | **233,201** | 230 µs | 445 µs | 726 µs | 7.4 ms | 0 |
| 4 | 92,398 | 9,237 | **554,261** | 362 µs | 851 µs | 1,894 µs | 8.1 ms | 0 |
| 8 | 119,464 | 11,940 | **716,432** | 567 µs | 1,374 µs | 2,596 µs | 24.9 ms | 0 |
| 16 | 133,994 | 13,395 | **803,726** | 1,028 µs | 2,458 µs | 4,191 µs | 11.4 ms | 0 |
| 32 | 157,622 | 15,758 | **945,521** | 1,787 µs | 4,006 µs | 6,178 µs | 38.0 ms | 0 |

**Sustained one minute** at 8 callers ([`benchmarks/go-load-60s.jsonl`](benchmarks/go-load-60s.jsonl)): 693,705 calls in 60.03 s, which is 693,369 calls/min (11,556/s). p50 576 µs, p95 1,445 µs, p99 2,883 µs, max 29.2 ms, 0 errors.

How to read this:

- Above 4 callers, the 2 vCPUs are saturated. They are shared by the load generator and the sidecar, so throughput grows slowly and latency grows roughly in proportion to the queue. These figures are a floor for a 2-core pod, not a limit of the design.
- An earlier run of the same command gave 209,892 / 498,112 / 685,538 / 803,503 / 906,130 calls/min for 1 / 4 / 8 / 16 / 32 callers. Run-to-run variation is about ±10%.
- If one pod needs more than about 700k calls/min with p99 under 3 ms, give the sidecar its own cores. Measure first.

## 4. How fast a change reaches the sidecar

What it measures: a real release (block or unblock a city in `blocked_cities`) repeated N times.

- **publish**: the time for the change request, approval and publish API calls. During publish, the server compiles the bundle, runs every decision's tests, signs it and writes it to the object store.
- **swap**: from the publish call returning until the sidecar reports the new generation (the script reads the sidecar's `gearbox_bundle_generation` metric every 2 ms). This covers the push notification, the download, signature and sha256 checks, compile, bundle tests and the atomic swap.

How: `make bench-propagation` ([script](../scripts/integration/bench-propagation.sh)): server with the memory store and filesystem object store, one sidecar, everything on the same 2 vCPUs. Raw output: [`benchmarks/propagation-jitter0.txt`](benchmarks/propagation-jitter0.txt) and [`benchmarks/propagation-jitter2s.txt`](benchmarks/propagation-jitter2s.txt).

| Setting | Releases | publish min / median / max | swap min / median / max |
|---|---|---|---|
| `GEARBOX_FETCH_JITTER=0s` | 10 | 15.7 / **17.4** / 21.9 ms | 3.3 / **4.4** / 7.7 ms |
| `GEARBOX_FETCH_JITTER=2s` (default) | 8 | 15.0 / **15.9** / 23.9 ms | 450.9 / **1,144.5** / 1,972.3 ms |

The jitter is deliberate. Each sidecar waits a random 0 to `GEARBOX_FETCH_JITTER` before downloading, so a fleet of thousands does not hit the object store at the same instant. With jitter off, the pipeline itself takes about 4 ms. Earlier runs gave a swap median of 4.9 ms (jitter 0) and 685 ms (jitter 2 s).

These runs use the filesystem object store. With MongoDB and LocalStack S3 in `make e2e`, every propagation check passed within its timeout, but propagation time was not measured separately there.

**End to end through the UI** (`scripts/demo/ui_demo.py`): a headless browser clicks **Publish to dev**, then the script polls the Go example service every 50 ms until it answers `reject/city_not_served`. This uses MongoDB storage, the default 2 s jitter, and a sidecar polling every 2 s alongside the push feed.

| Run | Click → new answer |
|---|---|
| 1 | 1.1 s (this version of the script also included a fixed 1 s wait for a screenshot, so the true time was lower) |
| 2 | 1.41 s (polling starts immediately after the click) |

On the VM run with all three services (MongoDB store, 1-second ticker), a change published at 10:31:18 showed in Go, Python and Java on the 10:31:20 tick. The unblock published at 10:31:30 showed by 10:31:33.

## 5. Python and Java clients

**Not measured yet.** The benchmark programs exist and work:

```bash
# isolated sidecar on a short socket path
bin/gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned &   # GEARBOX_SOCKET=/tmp/gbx.sock
python examples/python-service/bench.py --target unix:///tmp/gbx.sock --calls 5000 --threads 1,4,8
java -jar examples/java-service/target/java-service.jar bench 5000 1,4,8        # GEARBOX_TARGET=unix:///tmp/gbx.sock
```

The cloud sandbox where the other numbers were measured cannot install `grpcio` or download Maven dependencies. The machine that can build them was not reachable when these benchmarks ran. Until they are measured, the only Python and Java figures are functional: 5 of 5 Python integration tests and 2 of 2 Java tests passed against a real sidecar, and both example services picked up live changes in the VM demo above. No latency figure is claimed for them.

## 6. What is not benchmarked

- AI nodes (excluded from this release).
- The control plane under concurrent admin load, and MongoDB with large definition sets.
- Many sidecars at once (fleet fan-out). Only one sidecar was used.
- Production hardware.
