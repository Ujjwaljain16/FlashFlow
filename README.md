# FlashFlow

**Adaptive Edge Networking & Flash-Crowd Resilience Laboratory**

> Dual-engine architecture: a deterministic virtual-time simulator for rigorous, counterfactual-verified policy tuning — combined with a real HTTP emulation engine (Go `net/http`, in-process network-degradation simulation) for validation against actual Linux networking behavior.

---

## What is FlashFlow?

FlashFlow is a programmable Go laboratory for studying distributed edge topologies under extreme load and partial failure. The project is built stage-by-stage from raw TCP fundamentals upward, with every engineering decision grounded in experimental evidence.

```text
                       FlashFlow
                           │
          ┌────────────────┴────────────────┐
          │                                 │
   Virtual-Time Engine              Real Emulation Engine
   (deterministic)                  (net/http + internal/netsim)
          │                                 │
          └────────────────┬────────────────┘
                           │
                  Per-Component Trace/Metrics
                           │
               ┌───────────┴───────────────┐
               ▼                           ▼
       Internal Statistics          Experiment Result JSON
```

**Implementation status, stated plainly**: as of Stage 10, the two engines ARE unified behind a
single `internal/engine.ExperimentEngine` interface (`VirtualEngine`/`RealEngine`, both
compile-time-verified against `Prepare/Run/Replay`); network degradation is a real, in-process Go
simulator (`internal/netsim`) built specifically in place of `tc netem`, which this project
evaluated and did not use (Linux-only, unavailable on the Windows host this was developed on — see
`docs/StageArtifacts/Stage4.md`); metrics are computed via `internal/statistics` (percentiles,
Mann-Whitney U, Cliff's Delta, bootstrap CI) for this project's own scientific claims, AND via
`internal/telemetry` (a hand-rolled histogram + Prometheus text-exposition format, live at
`cmd/proxy -metrics-addr`) for operational export; `internal/provenance.Manifest` (hierarchical
Traffic/Topology/Failure/Policy seeds, a configuration hash, git commit/dirty state) exists and is
tested, though most individual experiment binaries still write their own ad hoc result JSON rather
than a manifest — see `docs/StageArtifacts/Stage10.md` for exactly which experiments call it. Full
per-finding disposition: `docs/audit/RESOLUTION.md`.

---

## Build Sequence

FlashFlow is built learning-first — not architecture-first.

| Stage | Ships | Status |
|---|---|---|
| **1** | Raw TCP server/client, connection lifecycle, framing, benchmarks | ✅ Complete |
| **2** | HTTP reverse proxy, 3-edge topology, real emulation engine | ✅ Complete |
| **3** | Round-robin → EWMA routing policies | ✅ Complete |
| **4** | LRU (deferred)+TTL edge cache, `internal/netsim` network degradation | ✅ Complete |
| **5** | Virtual-Time Engine, Clock abstraction, Event Stream | ✅ Complete |
| **6** | Internal statistics (percentile/Mann-Whitney/Cliff's Delta/bootstrap), Little's-Law-based queueing analysis (one-off, per-experiment — a generalized attribution engine is Stage 10 scope) | ✅ Complete |
| **7** | P2C + Four-Signal Adaptive Router (six tunable parameters), Counterfactual Replay | ✅ Complete |
| **8** | Auto-Tuner (Random Search v1), Live Dashboard | ✅ Complete |
| **9** | Post-Stage-8 adversarial audit remediation — every finding fixed or honestly disclosed; no new capability shipped | ✅ Complete |
| **10** | Traffic generator, SWR cache, declarative YAML chaos engine, experiment manifest/provenance, a generalized queueing-attribution engine, HdrHistogram+Prometheus telemetry, LHS/Bayesian tuner tiers, a formal `ExperimentEngine` interface | ✅ Complete — see `docs/StageArtifacts/Stage10.md` |

A note on Stage 10's own numbers: widening `Scenario.Seed` into a hierarchical `SeedTree` (needed
for genuine independent-axis seed control) changed the actual Development/Holdout scenario content,
so Stage 8's originally-reported specific tuning numbers no longer reproduce exactly under the
current code — the search/validation methodology itself is unaffected and was re-verified end to
end. See `docs/StageArtifacts/Stage10.md`'s own callout for the full explanation before citing any
Stage 8 number against a fresh run.

---

## Stage 1 — TCP Foundations

**Research question**: What is the performance difference between creating a new TCP connection for every request versus reusing persistent connections?

**Key findings**:
1. TCP is a byte stream. Application-level message boundaries require explicit framing — one `Read()` does not correspond to one message.
2. Even on loopback, `net.Dial()` (3-way handshake) costs ~0.5–1.5ms p99. On real networks this is 50–200ms.
3. At c=100, per-request mode app RTT degrades to ~9.4ms (p50) vs ~0µs for persistent — the penalty is systemic OS pressure, not just dial overhead.
4. TIME_WAIT socket exhaustion contaminates sequential benchmarks. Correct methodology requires fresh server instances per experiment cell.

### Running Stage 1

```bash
# Start the echo server
go run ./cmd/tcp-server --addr 127.0.0.1:9000

# Run benchmark (persistent mode)
go run ./cmd/tcp-client --addr 127.0.0.1:9000 --requests 10000 --concurrency 10 --connection-mode persistent

# Run benchmark (per-request mode)
go run ./cmd/tcp-client --addr 127.0.0.1:9000 --requests 10000 --concurrency 10 --connection-mode per-request

# Run full benchmark matrix (120 measured runs)
go run ./cmd/benchmark-runner

# Run causal decomposition experiment 001-A
go run ./cmd/experiment-001a
```

### Running Tests

```bash
go test ./...
go vet ./...
```

`scripts/final-validation.sh` and `scripts/nginx-reference-benchmark.sh` are bash scripts and
require Git Bash or WSL on Windows (they will not run under a plain `cmd.exe` or PowerShell
prompt); the NGINX benchmark additionally requires Docker.

---

## Stage 10 Demo

**The question**: when one edge target is both overloaded and prone to temporary failure, does
FlashFlow's adaptive router actually route around the problem better than blind round-robin — and
can that be proven, explained, and reproduced, not just asserted?

**The controlled experiment** (`cmd/demo-stage10`): 3 heterogeneous edges (20ms / 15ms / 60ms
service time), a real generated workload (`internal/traffic`, 300 requests), and a declarative
failure schedule (`internal/chaos`) crashing the fastest edge at t=1s and recovering it at t=2s.
Round Robin and Adaptive are compared via `internal/engine`'s `Run`/`Replay` against the
byte-for-byte identical `Scenario`.

**Real output** (from an actual run — reproduced below exactly, nothing hand-edited):

```text
policy             mean(ms)    p99(ms)   rejected   completed by target
round-robin           34.35      60.00          0   edge-a=117  edge-b=67  edge-c=116
adaptive              28.73      60.00          0   edge-a=122  edge-b=100  edge-c=78

Mean latency: adaptive's 28.73ms vs round-robin's 34.35ms -- a 16.4% reduction in this
specific scenario. p99 ties at 60.00ms under BOTH policies: Adaptive did not "solve" the
tail here, only the mean -- a small-sample-size effect Stage 8's own tuning work already
found and corrected for (p99 is a weak discriminator at this request count).

--- Proof moment: counterfactual divergence ---
The two policies' event traces are IDENTICAL up through event #8, then diverge -- proof
the difference above is a real routing-decision effect, not two runs that quietly saw
different conditions.
```

**Why it happened** (`internal/attribution`, not asserted — computed): the attribution model shows
Adaptive reduces the estimated offered-load-to-capacity ratio on the overloaded edge (edge-c) from
**ρ=1.99 to ρ=1.34** — still overloaded either way (no routing policy gives a fixed-capacity target
more capacity), but meaningfully less severe, because Adaptive shifts load onto edge-a/edge-b, which
have headroom.

**Reproducibility** — the same experiment run 3 separate times (clean state, same-seed repeat, and a
full `demo/output/` wipe followed by a fresh run):

| Run | Mean latency reduction | Trace divergence from Run 1 |
|---|---:|---|
| 1 — baseline | 16.4% | — |
| 2 — same seed, no cleanup | 16.4% | 0 |
| 3 — fresh state | 16.4% | 0 |

A real provenance manifest (seed tree, configuration hash, git commit) is written to
`demo/output/stage10-demo/manifest.json` on every run.

**What this does and doesn't show**: this is not evidence that Adaptive always wins — Stage 8's own
broader evaluation found it wins 62.5–70% of scenarios, not all of them, and trades fairness for
latency. It's evidence that, under this one controlled failure scenario, FlashFlow can reproduce the
comparison, identify exactly where the policies diverge, and connect the performance difference to a
measurable system mechanism, rather than reporting a number with no explanation behind it.

```bash
go run -buildvcs=true ./cmd/demo-stage10
# or: scripts/demo-stage10.sh
```

Full recording script, on-screen captions, claims audit, and a secondary (real-engine + live
telemetry) demo: [`docs/demo/Stage10Demo.md`](docs/demo/Stage10Demo.md). Independent adversarial
validation of every Stage 10 capability: [`docs/StageArtifacts/Stage10DemoValidation.md`](docs/StageArtifacts/Stage10DemoValidation.md).

## Running Stage 10 Features

```bash
# Tuner comparison: Random Search vs LHS vs Bayesian Optimization
# (also writes real provenance manifests to experiments/010-stage10-features/runs/ --
# -buildvcs=true is required for git_commit/git_dirty to populate: plain `go run`
# defaults to -buildvcs=auto, which silently omits VCS info for a `go run` build)
go run -buildvcs=true ./cmd/experiment-010a

# Live Prometheus metrics from a running proxy
go run ./cmd/proxy -addr :8081 -targets http://127.0.0.1:8000 -metrics-addr :9090 &
curl http://127.0.0.1:9090/metrics

# Dashboard (Playground / Experiment browser / Tuning view)
go run ./cmd/dashboard    # http://127.0.0.1:7070

# Package-level tests double as runnable demonstrations of each Stage 10
# capability (traffic patterns, SeedTree axis-independence, SWR staleness/
# revalidation, chaos YAML parsing, metamorphic invariants):
go test ./internal/traffic/... -v
go test ./internal/chaos/... -v
go test ./internal/cache/... -run SWR -v
go test ./internal/challenge/... -run Metamorphic -v
```

## Specifications

- [PRD v3.1](prd.md) — Product requirements and build sequence authority
- [TRD v3.1](trd.md) — Technical architecture and implementation authority
- [Research](research.md) — Research methodology reference

---

## Experiments

| # | Title | Status |
|---|---|---|
| [001](experiments/001-tcp-connection-lifecycle/) | TCP Connection Lifecycle | ✅ Complete |
| [001-A](experiments/001-tcp-connection-lifecycle/results/) | Connection Cost Decomposition | ✅ Complete |
| [002](experiments/002-http-reverse-proxy/) | HTTP Reverse Proxy | ✅ Complete |
| [003](experiments/003-routing-policies/) | Routing Policies | ✅ Complete |
| [004](experiments/004-caching-failures/) | Caching & Failures | ✅ Complete |
| [005](experiments/005-virtual-time/) | Virtual Time | ✅ Complete |
| [006](experiments/006-statistics-queueing/) | Statistics & Queueing | ✅ Complete |
| [007](experiments/007-adaptive-replay/) | Adaptive Routing & Replay | ✅ Complete |
| [008](experiments/008-tuning-validation/) | Tuning & Final Validation | ✅ Complete |
| [010-A](experiments/010-stage10-features/) | Tuner Comparison (Random Search vs LHS vs Bayesian Optimization) | ✅ Complete |

---

## Resume Line

> **FlashFlow — Adaptive Edge Networking Laboratory**: Built a dual-engine distributed system in Go combining a deterministic virtual-time simulator and a real HTTP emulation engine with an in-process network-degradation simulator (built specifically in place of `tc netem`, which was evaluated and not used); implemented adaptive routing, TTL+coalescing edge caching, and a self-tuning parameter optimizer validated via stateful counterfactual replay and statistical queueing analysis. Stage 1 grounded the system in raw TCP semantics through a controlled connection-lifecycle benchmarking study that discovered TIME_WAIT exhaustion and its effects on sequential benchmark contamination.
