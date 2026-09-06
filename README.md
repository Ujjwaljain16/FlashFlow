# FlashFlow

**A research laboratory, in Go, for controlled experiments on distributed edge-routing behavior under
heterogeneous conditions and finite serving capacity.**

## What FlashFlow Studies

FlashFlow studies one question with increasing precision across 16 development stages: under
heterogeneous edge conditions (targets with different speeds, occasional failures, finite serving
capacity), which routing policies handle concentrated load well, and why? It is not a benchmark
leaderboard — every stage's conclusion was tested against its predecessor's, and several were later
narrowed or outright falsified as evidence accumulated. See **Key Research Findings** below for the
current best answer, and **Research History** for the full sequence of corrections that produced it.

## Architecture

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

Both engines implement one shared `internal/engine.ExperimentEngine` interface
(`Prepare`/`Run`/`Replay`, compile-time-verified), so any experiment specification runs unmodified
against either.

### Engines

- **Virtual-Time Engine** (`internal/vtime`, `internal/replay`): a deterministic discrete-event
  simulator. Given the same seed, it is byte-for-byte reproducible on the same machine/Go toolchain —
  confirmed directly, not just assumed (see **Reproduction Commands**).
- **Real Emulation Engine** (`internal/engine/real.go`): real Go `net/http` servers and a real reverse
  proxy, with an in-process network-degradation simulator (`internal/netsim`) built specifically in
  place of `tc netem` (Linux-only, unavailable on this project's Windows development host — evaluated,
  not used). Subject to genuine OS scheduling and wall-clock timing noise, disclosed and measured, not
  hidden — see **Evidence Boundaries**.

### Routing Policies

Six policies, each mechanistically distinct (see **Key Research Findings**): round-robin (no signal),
weighted-round-robin (static configured weights), least-connections (current in-flight count), EWMA
(smoothed latency history), P2C (sampled comparison of two random targets), and Adaptive (a weighted
combination of Load, Latency, Cache, and Cost signals — four scored signals, not the six-signal count an
early draft of this document incorrectly used). Source: `internal/proxy/`.

### Cache & Failure Modeling

TTL caching with request coalescing (singleflight, prevents redundant concurrent fetches for the same
key) and stale-while-revalidate (serves a stale response immediately while refreshing in the background)
— `internal/cache/`. LRU eviction was evaluated and deliberately deferred: no experiment in this
project's history has ever needed bounded cache memory. Failure injection is a declarative YAML chaos
schedule (`internal/chaos/`) that crashes and recovers targets at specified times, backed by a 4-state
health machine (`internal/health/`).

### Virtual Time

`internal/vtime`: a logical clock that only advances when a discrete event is popped from a priority
queue, not on a wall-clock timer — the mechanism that makes the virtual engine deterministic.

### Replay

`internal/replay`: stateful counterfactual replay. Two policies run against the byte-for-byte identical
exogenous trace (arrivals, failures) while each evolves its own isolated endogenous state (cache hits,
queue depths) — confirmed via full-trace identity/divergence tests, not summary statistics.

### Statistics

`internal/statistics`: percentile, Mann-Whitney U, Cliff's Delta, and bootstrap confidence intervals,
each independently checked against hand-computed reference values, not just "runs without panicking."

### Tuning

`internal/tuning`: three tuners (Random Search, Latin Hypercube Sampling, Bayesian Optimization) sharing
one search loop, with development/holdout scenario sets drawn from disjoint seed ranges. Random Search
is the one that actually wins on this project's own (small, 6-parameter) search space — LHS and Bayesian
Optimization were built and directly compared, and neither meaningfully improves on it. See **Project
Status** for why that outcome was kept rather than "fixed."

### Dashboard

`cmd/dashboard` (`internal/dashboard`): a local (`127.0.0.1`-only by default) experiment browser, tuning
visualizer, and live policy playground. It reads existing result JSON directly from disk and never
recomputes or overrides a number found there — the dashboard is a presentation surface, never the
authoritative source for a research claim.

---

## Key Research Findings

FlashFlow's experiments indicate that routing collapse is not explained by "load-aware vs. load-blind"
routing alone. Under finite capacity, concentration can produce two distinguishable failure shapes:
**acute over-commitment**, where committed backlog (work already dispatched to a target between the
moment it becomes congested and the moment a policy materially diverts new work elsewhere) becomes the
strongest available predictor of tail collapse in the tested topology-size generalization, and **chronic
over-allocation**, where sustained time above capacity is more informative than any single-event metric.

This result is not a universal law across workload distributions or arbitrary real systems: cross-
workload prediction is imperfect, the cache-affinity mechanism (Stage 13's own interim-latency finding)
remains unresolved, and virtual/real behavior diverges under the most extreme real overload tested.

Five stages of corrections produced this answer, each narrowing or falsifying the one before it:

1. **Stage 11**: EWMA sometimes beats Adaptive under heterogeneity in a flat (no-queueing) model.
2. **Stage 12**: adding a minimal finite-capacity model reverses that finding sharply in one scenario —
   at Capacity=1, EWMA's mean latency explodes 8x while Adaptive barely moves (12-seed confirmation,
   Cliff's Delta=1.000).
3. **Stage 13**: two independent sweeps locate the reversal's true driver at offered ρ≈0.89-0.97, and
   reframe the deeper boundary as "load-blind vs. load-aware routing" — round-robin and EWMA (no live
   signal) collapse; every policy with SOME load signal stays stable.
4. **Stage 14**: scaling to more targets falsifies that reframing directly — EWMA (load-aware by signal)
   loses outright to round-robin at N=8, confirmed across 10 independent seeds. Rho's own predictive
   power decreases as target count grows even as severity worsens.
5. **Stage 15**: direct backlog measurement (a new `internal/backlog` package) replaces both prior
   explanations with a measured, two-mechanism model — committed backlog for acute collapse (perfect
   rank agreement across a target-count generalization test where peak rho is badly misordered),
   fraction-of-time-over-capacity for chronic collapse (round-robin's own failure: tiny committed
   backlog, 71% of the run over capacity, never drains).

Full ledger with exact evidence for every claim: [`docs/StageArtifacts/Stage16-ClaimLedger.md`](docs/StageArtifacts/Stage16-ClaimLedger.md).
Full narrative: [`docs/StageArtifacts/Stage16-ResearchSynthesis.md`](docs/StageArtifacts/Stage16-ResearchSynthesis.md).

## Flagship Result

One scenario shows the whole mechanism at once: 5 heterogeneous targets (15-75ms), each with exactly one
serving slot, under a FlashCrowd workload peaking at t=2.5s, run across three independent seeds. EWMA and
Adaptive both build the deepest queues and show severe, often-non-draining acute collapse (committed
backlog 93-127 for Adaptive, consistently among the worst P99 latencies of all six policies — in every
seed, not just one). Round-robin shows a completely different, chronic failure: a tiny committed backlog
(4) but the highest fraction-of-time-over-capacity of the six (71%), and it never drains either.
Weighted-round-robin, least-connections, and P2C-load all stay comparatively mild.

**This finding is deliberately not framed as "Adaptive wins."** Adaptive's own P99 was the worst of all
six policies tested in this exact scenario, in every one of the three seeds — a controlled ablation
traced its resistance to collapse specifically to its Load signal, not general "smartness."

Full walkthrough: [`docs/StageArtifacts/Stage16-FlagshipDemo.md`](docs/StageArtifacts/Stage16-FlagshipDemo.md).
Reproduce it yourself: `./scripts/reproduce-flagship.sh`.

## Evidence Boundaries

- Committed backlog generalizes with **perfect rank agreement** across topology size (N=3/5/8, plus a
  bimodal shape); its cross-**workload**-shape generalization is real but imperfect.
- A validated real concurrency ceiling reproduces the mechanism's direction at most, not all, tested
  real-engine overload levels — it reverses at the most extreme level tested.
- The cache-affinity interim-latency effect (higher cache weight worsens interim latency, improves
  eventual recovery) was tested directly against this mechanism in two candidate locations and remains
  **unresolved**.
- Virtual-engine results are byte-for-byte reproducible except a timestamp field (confirmed directly this
  stage); the one real-engine experiment is **not** byte-identical across reruns — genuine OS/timing
  variance, disclosed and measured rather than hidden.
- LRU cache eviction, DR-OPE, contextual bandits/RL, real `tc netem`, and OpenTelemetry are **not
  implemented** anywhere in this codebase — see [`docs/StageArtifacts/Stage16-ScopeFreeze.md`](docs/StageArtifacts/Stage16-ScopeFreeze.md)
  for the full capability-by-capability audit.

## Reproduction Commands

```bash
# Full release-readiness gate (formatting, vet, tests, deterministic replay,
# statistical/tuning validation, challenge suite)
./scripts/final-validation.sh

# Reproduce every Stage 15 (mechanism-identification) experiment in order
./scripts/reproduce-stage15.sh

# Reproduce the flagship demonstration across 3 independent seeds
./scripts/reproduce-flagship.sh
```

`scripts/*.sh` require Git Bash or WSL on Windows (not a plain `cmd.exe`/PowerShell prompt).
Per-stage experiment commands are listed in **Research History** below.

## Project Status

All 16 planned stages are complete. The research program (Stages 11-15) reached a measured, precisely-
bounded mechanistic explanation rather than a universal law, and Stage 16 froze that evidence, re-audited
every strong claim against source, fixed one real security gap and two documentation overclaims found
during that audit, and confirmed reproducibility from a clean checkout. Final verdict: **READY WITH
DOCUMENTED LIMITATIONS** — see [`docs/StageArtifacts/Stage16.md`](docs/StageArtifacts/Stage16.md).

| Stage | Ships | Status |
|---|---|---|
| **1** | Raw TCP server/client, connection lifecycle, framing, benchmarks | ✅ Complete |
| **2** | HTTP reverse proxy, 3-edge topology, real emulation engine | ✅ Complete |
| **3** | Round-robin → EWMA routing policies | ✅ Complete |
| **4** | LRU (deferred) + TTL edge cache, `internal/netsim` network degradation | ✅ Complete |
| **5** | Virtual-Time Engine, Clock abstraction, Event Stream | ✅ Complete |
| **6** | Internal statistics, Little's-Law-based queueing analysis | ✅ Complete |
| **7** | P2C + Four-Signal Adaptive Router, Counterfactual Replay | ✅ Complete |
| **8** | Auto-Tuner (Random Search v1), Live Dashboard | ✅ Complete |
| **9** | Post-Stage-8 adversarial audit remediation | ✅ Complete |
| **10** | Traffic generator, SWR cache, YAML chaos engine, provenance, attribution engine, LHS/Bayesian tuning, telemetry, `ExperimentEngine` | ✅ Complete — [`Stage10.md`](docs/StageArtifacts/Stage10.md) |
| **11** | Research validation: 8-program sweep mapping policy regime boundaries | ✅ **PASS WITH LIMITATIONS** — [`Stage11.md`](docs/StageArtifacts/Stage11.md) |
| **12** | Model fidelity: finite-capacity contention model reveals the first reversal | ✅ **PASS** — [`Stage12.md`](docs/StageArtifacts/Stage12.md) |
| **13** | Regime discovery: ρ≈0.9 and "load-blind vs. load-aware" | ✅ **PASS** — [`Stage13.md`](docs/StageArtifacts/Stage13.md) |
| **14** | Scale & topology generalization: falsifies "load-blind vs. load-aware" | ✅ **PASS WITH LIMITATIONS** — [`Stage14.md`](docs/StageArtifacts/Stage14.md) |
| **15** | Mechanism identification: committed backlog + chronic/acute collapse | ✅ **PASS WITH LIMITATIONS** — [`Stage15.md`](docs/StageArtifacts/Stage15.md) |
| **16** | Final synthesis, reproducibility audit, release, flagship demo | ✅ **READY WITH DOCUMENTED LIMITATIONS** — [`Stage16.md`](docs/StageArtifacts/Stage16.md) |

## Known Limitations

- The finite-capacity model is a single FIFO queue per target, not a real multi-resource scheduler.
- No cross-hardware determinism guarantee — "deterministic" means same machine, same Go toolchain
  version, same seed.
- The real engine is subject to genuine OS scheduling and wall-clock timing noise; its results are
  directionally, not byte-for-byte, reproducible.
- Cache-affinity's interim-latency mechanism remains unexplained.
- Committed backlog's cross-workload-shape generalization is imperfect.
- Most experiment binaries after Stage 10's `experiment-010a` write ad hoc result JSON rather than a full
  provenance manifest (disclosed, not hidden).
- Never run against real production traffic.

## Non-Goals

FlashFlow is a research laboratory for controlled experiments on distributed edge-routing behavior. It is
**not**: a production CDN, a replacement for Envoy/NGINX/HAProxy, a complete packet-level network
simulator (ns-3, Mininet), a universal routing optimizer, or a proof that Adaptive always wins. Doubly
Robust Off-Policy Evaluation, contextual bandits/reinforcement learning, real `tc netem`/Kubernetes
orchestration, and OpenTelemetry integration were all evaluated in this project's own planning documents
and are explicitly **not implemented** — see [`docs/StageArtifacts/Stage16-ScopeFreeze.md`](docs/StageArtifacts/Stage16-ScopeFreeze.md).

---

## Research History

The full stage-by-stage narrative, preserved as a sequence of corrections rather than rewritten as if the
final answer were obvious from the start. A note on Stage 10's own numbers: widening `Scenario.Seed` into
a hierarchical `SeedTree` changed the actual Development/Holdout scenario content, so Stage 8's originally
-reported specific tuning numbers no longer reproduce exactly under current code — the methodology itself
was re-verified end to end (`docs/StageArtifacts/Stage10.md`). A note on Stage 11's own numbers: Stage 8's
"Adaptive wins 62.5-70% of scenarios" and Stage 11's "Adaptive wins 0/27 regime-map configurations on mean
latency" are **not contradictory** — they measure different things (composite utility vs. raw mean
latency); see `Stage11.md` §19.

### Stage 1 — TCP Foundations

**Research question**: What is the performance difference between creating a new TCP connection for
every request versus reusing persistent connections?

1. TCP is a byte stream; application-level message boundaries require explicit framing.
2. Even on loopback, `net.Dial()` costs ~0.5–1.5ms p99. On real networks this is 50–200ms.
3. At c=100, per-request mode app RTT degrades to ~9.4ms (p50) vs ~0µs for persistent.
4. TIME_WAIT socket exhaustion contaminates sequential benchmarks.

```bash
go run ./cmd/tcp-server --addr 127.0.0.1:9000
go run ./cmd/tcp-client --addr 127.0.0.1:9000 --requests 10000 --concurrency 10 --connection-mode persistent
go run ./cmd/benchmark-runner       # full 120-run benchmark matrix
go run ./cmd/experiment-001a        # causal decomposition
```

### Stage 10 Demo

**The question**: when one edge target is both overloaded and prone to temporary failure, does
FlashFlow's adaptive router actually route around the problem better than blind round-robin — and can
that be proven, explained, and reproduced?

3 heterogeneous edges (20ms/15ms/60ms), a real generated workload (300 requests), a declarative failure
schedule crashing the fastest edge at t=1s and recovering it at t=2s:

```text
policy             mean(ms)    p99(ms)   rejected   completed by target
round-robin           34.35      60.00          0   edge-a=117  edge-b=67  edge-c=116
adaptive              28.73      60.00          0   edge-a=122  edge-b=100  edge-c=78
```

Adaptive's 28.73ms vs. round-robin's 34.35ms is a 16.4% mean-latency reduction in this specific scenario
(p99 ties — Adaptive did not "solve" the tail here, only the mean). The attribution model shows why: it
reduces the overloaded edge's estimated ρ from 1.99 to 1.34 by shifting load onto edges with headroom.
Reproduced identically across 3 separate runs (same seed, and a full state wipe). This is not evidence
Adaptive always wins — Stage 8's own broader evaluation found 62.5-70%, not 100% — it's evidence the
platform can reproduce, isolate, and mechanistically explain one specific comparison.

```bash
go run -buildvcs=true ./cmd/demo-stage10   # or: scripts/demo-stage10.sh
```

Full recording script: [`docs/demo/Stage10Demo.md`](docs/demo/Stage10Demo.md).

### Stage 11 — What Appeared to Be True

An 8-program sweep (162+ runs) found Adaptive wins 0 of 27 regime-map configurations on raw mean latency
under heterogeneous load in the FLAT (no-queueing) model — because the model had no way to penalize
EWMA's unconstrained concentration. Also found: a real cache-affinity trap (Adaptive can permanently fail
to route back to a recovered target), a real `RealEngine` instrumentation bug (dynamic policies selected
blind), and a real SeedTree independence gap.

```bash
go run -buildvcs=true ./cmd/experiment-011a   # Program A: policy regime map (162 runs)
go run -buildvcs=true ./cmd/experiment-011h   # statistical robustness (12-seed replication)
```

Full findings: [`Stage11.md`](docs/StageArtifacts/Stage11.md) · [`011-stage11-research-validation.md`](docs/learning/011-stage11-research-validation.md)

### Stage 12 — Finite Capacity Reverses the Finding

Adding a minimal, opt-in finite-capacity model (`TargetProfile.Capacity`, FIFO queueing) reverses Stage
11's flagship finding sharply: once EWMA's own concentration pushes a target past ρ=1, its mean latency
explodes 8x while Adaptive barely moves — confirmed across 12 seeds (Cliff's Delta=1.000). Explicitly
scoped to one tested scenario, not claimed as a general law.

```bash
go run -buildvcs=true ./cmd/experiment-012a   # capacity sweep 0/1/2/3
go run -buildvcs=true ./cmd/experiment-012e   # 12-seed statistical robustness
go test ./internal/replay/... -run TestContention -v
```

Full findings: [`Stage12.md`](docs/StageArtifacts/Stage12.md) · [`012-stage12-model-fidelity-and-control.md`](docs/learning/012-stage12-model-fidelity-and-control.md)

### Stage 13 — Toward Rho and Concentration

Two independent sweeps locate the reversal's driver at offered ρ≈0.89-0.97. The deepest reframing:
round-robin and EWMA (no live load signal) collapse under contention; every policy with SOME load
signal — static or live — stays nearly unaffected. "Load-blind vs. load-aware," not "EWMA vs. Adaptive."

```bash
go run -buildvcs=true ./cmd/experiment-013b   # is Capacity=1 special, or does normalized rho matter?
go run -buildvcs=true ./cmd/experiment-013i   # multi-policy regime map
go test ./internal/replay/... -run TestContention_ScaleInvariance -v
```

Full findings: [`Stage13.md`](docs/StageArtifacts/Stage13.md) · [`013-stage13-regime-discovery.md`](docs/learning/013-stage13-regime-discovery.md)

### Stage 14 — Scale Falsifies the Simplification

EWMA (load-aware by Stage 13's own classification) loses outright to round-robin at N=8, confirmed
across 10 independent seeds (Cliff's Delta=1.000) — falsifying "load-blind vs. load-aware" as the
deepest regime boundary. Achieved rho DECREASES as target count grows even as severity WORSENS. A
validated real concurrency ceiling reproduces the virtual reversal Stage 13's own real engine couldn't.

```bash
go run -buildvcs=true ./cmd/experiment-014c   # the central rho-matched cross-scale test
go run -buildvcs=true ./cmd/experiment-014f   # full 6-policy set + falsification attempt
go run -buildvcs=true ./cmd/experiment-014i   # 10-seed statistical confirmation
```

Full findings: [`Stage14.md`](docs/StageArtifacts/Stage14.md) · [`014-stage14-scale-and-topology.md`](docs/learning/014-stage14-scale-and-topology.md)

### Stage 15 — Direct Backlog Measurement

A new `internal/backlog` package measures committed backlog directly: perfect rank agreement with
severity across a target-count generalization test where peak rho is badly misordered. Reveals collapse
has (at least) two shapes — round-robin's own failure has tiny committed backlog yet is chronically over
capacity, the opposite pairing from EWMA/Adaptive's acute failures. A controlled ablation traces
Adaptive's resistance specifically to its Load signal; Adaptive's own P99 was nonetheless the worst of
six policies tested in the canonical scenario.

```bash
go run -buildvcs=true ./cmd/experiment-015a   # canonical scenario, full 6-policy backlog dynamics
go run -buildvcs=true ./cmd/experiment-015c   # Adaptive signal ablation
go run -buildvcs=true ./cmd/experiment-015e   # predictor generalization across topology/workload
go test ./internal/backlog/... -v
```

Full findings: [`Stage15.md`](docs/StageArtifacts/Stage15.md) · [`015-stage15-mechanism-identification.md`](docs/learning/015-stage15-mechanism-identification.md)

### Stage 16 — Final Synthesis & Release

Froze the research scope, audited every strong claim against source (fixing two documentation overclaims
and one real security gap — `internal/topology/origin.go`'s `OriginServer` lacked the same
`ReadHeaderTimeout`/error-logging every other real server had had since Stage 9), built the flagship
demonstration, and confirmed reproducibility from a clean checkout.

Full findings: [`Stage16.md`](docs/StageArtifacts/Stage16.md) · [`Stage16-ClaimLedger.md`](docs/StageArtifacts/Stage16-ClaimLedger.md) · [`Stage16-ResearchSynthesis.md`](docs/StageArtifacts/Stage16-ResearchSynthesis.md) · [`Stage16-FlagshipDemo.md`](docs/StageArtifacts/Stage16-FlagshipDemo.md) · [`016-stage16-final-synthesis.md`](docs/learning/016-stage16-final-synthesis.md)

---

## Specifications

- [PRD v3.1](prd.md) — Product requirements and build sequence authority
- [TRD v3.1](trd.md) — Technical architecture and implementation authority
- [Research](research.md) — Research methodology reference (pre-implementation vision document; see its
  own top-of-file status note for what was and wasn't built)

## Experiments

| # | Title | Status |
|---|---|---|
| [001](experiments/001-tcp-connection-lifecycle/) | TCP Connection Lifecycle | ✅ Complete |
| [002](experiments/002-http-reverse-proxy/) | HTTP Reverse Proxy | ✅ Complete |
| [003](experiments/003-routing-policies/) | Routing Policies | ✅ Complete |
| [004](experiments/004-caching-failures/) | Caching & Failures | ✅ Complete |
| [005](experiments/005-virtual-time/) | Virtual Time | ✅ Complete |
| [006](experiments/006-statistics-queueing/) | Statistics & Queueing | ✅ Complete |
| [007](experiments/007-adaptive-replay/) | Adaptive Routing & Replay | ✅ Complete |
| [008](experiments/008-tuning-validation/) | Tuning & Final Validation | ✅ Complete |
| [010-A](experiments/010-stage10-features/) | Tuner Comparison (Random Search vs LHS vs Bayesian Optimization) | ✅ Complete |
| [011](experiments/011-research-validation/) | Research Validation (Programs A-H) | ✅ Complete — [`INDEX.json`](experiments/011-research-validation/INDEX.json) |
| [012](experiments/012-model-fidelity/) | Model Fidelity (finite-capacity reversal) | ✅ Complete — [`Stage12.md`](docs/StageArtifacts/Stage12.md) |
| [013](experiments/013-regime-discovery/) | Regime Discovery (rho, load-blind vs. load-aware) | ✅ Complete — [`Stage13.md`](docs/StageArtifacts/Stage13.md) |
| [014](experiments/014-scale-topology/) | Scale & Topology Generalization (falsification) | ✅ Complete — [`Stage14.md`](docs/StageArtifacts/Stage14.md) |
| [015](experiments/015-mechanism-identification/) | Mechanism Identification (committed backlog) | ✅ Complete — [`Stage15.md`](docs/StageArtifacts/Stage15.md) |
| [016](experiments/016-final-synthesis/) | Final Synthesis (flagship demonstration) | ✅ Complete — [`Stage16.md`](docs/StageArtifacts/Stage16.md) |

---

## Resume Line

> **FlashFlow — Adaptive Edge Networking Laboratory**: Built a dual-engine distributed system in Go
> combining a deterministic virtual-time simulator and a real HTTP emulation engine with an in-process
> network-degradation simulator; implemented six mechanistically-distinct routing policies, TTL+SWR edge
> caching, and a self-tuning parameter optimizer — then ran a genuine, evolving research program across
> six further stages that discovered routing collapse under finite capacity splits into two measurable
> shapes (acute over-commitment, explained by committed backlog; chronic over-allocation, explained by
> sustained time-over-capacity), falsifying two of its own earlier explanations along the way and
> disclosing, rather than hiding, the boundaries of what the evidence actually supports.
