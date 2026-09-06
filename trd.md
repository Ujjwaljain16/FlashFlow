# FlashFlow — Technical Requirements Document (v3.1 — Dual-Engine Architecture)

Companion to `prd.md`. This specifies the implementation blueprint for the dual-engine FlashFlow architecture. Advanced research elements (e.g., LinUCB, DR-OPE) are explicitly deferred to later phases.

---

## 1. Repository Structure

The domain structure separates engine-independent logic (Routing, Cache, Traffic) from execution environments (Virtual vs. Emulation).

```text
flashflow/
├── cmd/
│   ├── flashflow/          # top-level orchestrator binary
│   └── ...
├── internal/
│   ├── engine/                 # Dual execution engines
│   │   ├── virtual/              # Deterministic discrete-event loop
│   │   ├── emulation/            # Docker/net/http execution
│   │   └── interface.go          # ExperimentEngine API
│   ├── clock/                  # VirtualTime and WallClock interfaces
│   ├── router/                 # Engine-independent policies (RR, P2C, Adaptive)
│   ├── proxy/                  # net/http reverse proxy + connection manager
│   ├── cache/                  # Cache logic + coalescing
│   ├── health/                 # Clock-driven 4-state health machine
│   ├── traffic/                # Generators (Zipf, Pareto, Flash-crowd)
│   ├── chaos/                  # Declarative failure schedules + tc netem translations
│   ├── tuner/                  # Weight optimizers
│   ├── replay/                 # Stateful counterfactual replay execution
│   ├── provenance/             # Immutable manifest ledgers + seed trees
│   ├── statistics/             # Mann-Whitney U, Cliff's Delta, Bootstrap CIs
│   ├── analysis/               # Queueing-theoretic attribution + MAUT/Pareto
│   └── telemetry/              # Canonical Event Stream -> HDR/Prometheus
├── ...
```

---

## 2. Core Data Models & Clock Interfaces

Domain logic must use the `Clock` interface; no direct wall-clock access is permitted. The abstraction is the important part, allowing wall-clock and virtual-time implementations to be interchangeable.

```go
// internal/clock/clock.go
type VirtualTime int64 // Nanoseconds

type Clock interface {
    Now() VirtualTime
    SleepUntil(t VirtualTime)
}
```

```go
// pkg/model/request.go
type Request struct {
    RequestID          string
    ClientID           string
    Timestamp          VirtualTime
    Target             string
    PayloadSize        int64
    // ...
}
```

### Immutable Experiment Manifests (Provenance)

```go
type Experiment struct {
    ID              string
    Name            string
    Topology        TopologyConfig
    TrafficProfile  string
    
    // Hierarchical Deterministic Seeding
    GlobalSeed      int64
    TrafficSeed     int64
    TopologySeed    int64
    FailureSeed     int64
    PolicySeed      int64
    
    ConfigurationHash string
    GitCommit         string
}
```

---

## 3. Dual Engine Architecture

The system is accessed through a unified interface. The rest of FlashFlow is engine-agnostic.

```go
type ExperimentEngine interface {
    Prepare(exp Experiment) error
    Run(exp Experiment) (RunResult, error)
    Replay(exp Experiment, policy RoutingPolicy) (RunResult, error)
}
```

* **Virtual-Time Engine**: Deterministic state evolution. Time advances logically.
* **Real Emulation Engine**: Real `net/http` proxies in Docker. Time advances physically. Results provide implementation-fidelity validation but are not numerically identical to the virtual engine.

---

## 4. Routing Layer

Progression from simple to adaptive:

1. **Round Robin**
2. **Weighted Round Robin**
3. **Least Connections**
4. **Latency-Aware EWMA**
5. **Health-Aware** (Wrapper)
6. **Power of Two Choices (P2C)**: Samples two healthy edges, chooses the one with the lowest load/latency. Mitigates herd behavior.
7. **Adaptive**: The flagship scored router. As planned here: six signals (Latency, Load, Health, Capacity, Cache, Cost normalized to `[0,1]`). As built (see §19): four scored signals (Load/Latency/Cache/Cost) — Health is a pre-filter applied before this selector runs, and Capacity is folded into Load as a utilization ratio rather than standing alone — across six tunable parameters (4 weights + 2 durations).

*Note: Contextual Bandits (LinUCB) are deferred to advanced research phases.*

---

## 5. Reverse Proxy

The `net/http` implementation remains exactly as described previously, but its **policy layer** is extracted:

```text
Real Proxy Component
 ├── TCP/Transport
 ├── Connection Manager (ActiveConns)
 ├── Router Policy (Shared)
 └── Metrics
```

---

## 6. Network Degradation & `tc netem` (planned) / `internal/netsim` (as built — see §19)

Network configuration is declared logically in the YAML spec (not yet built — see §19).
- The **Virtual Engine** interprets latency mathematically as transmission delay.
- The **Emulation Engine** was planned to shell into Docker and run
  `tc qdisc add dev eth0 root netem delay 100ms loss 2%`. As built, it instead uses
  `internal/netsim`, an in-process Go `http.RoundTripper` wrapper injecting latency/jitter/loss —
  `tc netem` was evaluated and not used (Linux-only, unavailable on this project's Windows host;
  see `docs/StageArtifacts/Stage4.md`).

---

## 7. Health State Machine

The standard 4-state machine (HEALTHY, DEGRADED, UNHEALTHY, RECOVERING). Timers, check intervals, timeouts, and recovery ramps use the `Clock` interface, enabling deterministic virtual-time transitions.

---

## 8. Cache Layer

Core implements No Cache, TTL, LRU, and Stale-While-Revalidate (SWR), including **Request Coalescing** (singleflight) to mitigate stampedes.

**Invariant:** Cache evolution is explicitly stateful. Replay runs must reconstruct cache memory logically from scratch.

---

## 9. Experiment Ledger & Provenance

When an experiment concludes, the runtime commits an immutable ledger to `runs/<experiment-id>/`:

```text
manifest.json       # includes all configuration hashes + hierarchical seeds
config.yaml
events.jsonl        # The canonical event stream
metrics.csv
summary.json
statistics.json
replay.json
```

---

## 10. Stateful Counterfactual Replay

A massive upgrade from naive log replay. 

**Strict Correctness Invariant:** A counterfactual comparison may not reuse endogenous state produced by the behavior policy. Only exogenous inputs are shared; each policy must begin from an equivalent initial state and evolve its own endogenous state independently.

* **Exogenous Inputs (Shared)**: Request arrival intent times, topology graph, link capacities, failure schedules, global seeds.
* **Endogenous State (Isolated)**: Queue depth, cache hits, retries, origin load, internal connection pools.

---

## 11. Weight Auto-Tuner & Generalization

The tuner is an experimental subject itself.

```go
type Tuner interface {
    Suggest(previous []TrialResult) Weights
}
```

Progression:
1. **Tuner v1**: Random Search (Dirichlet distribution).
2. **Tuner v2**: Latin Hypercube Sampling.
3. **Tuner v3**: Bayesian Optimization (Gaussian Process).

**Generalization Rule:** Weights optimized against `training_scenarios` must be evaluated against unseen `holdout_scenarios` (e.g., shifted Zipf parameters or unannounced failures) before acceptance.

---

## 12. Declarative Chaos Engine

Failures are defined one abstraction level higher than `tc`:

```yaml
failures:
  - at: 15s
    target: edge-a
    action: latency
    delay: 150ms
  - at: 30s
    target: edge-a
    action: crash
```

The `FailureScheduler` feeds these into the active `ExperimentEngine` at the correct `VirtualTime` or Wall Clock moment.

---

## 13. Telemetry: Canonical Event Stream

The system's backbone is the internal Event Stream:

```text
Event Stream
     │
 ┌───┼───────────┐
 ▼   ▼           ▼
HDR Prometheus  Experiment
                Artifacts
```

Events (e.g., `RequestArrived`, `RouteSelected`, `LinkDegraded`) flow into the stream. 
- **Prometheus** provides live aggregate operational metrics.
- **HdrHistogram** tracks high-resolution, zero-allocation request latency distributions (p99/p99.9).

---

## 14. Queueing-Theoretic Attribution

When a tail latency spike occurs, the system attributes it mathematically rather than just logging it. 
It measures server utilization $\rho = \lambda / \mu$. If $\rho \to 1$ resulting in non-linear queue buildup, the attribution model outputs:

> "Queue saturation detected. Traffic skew pushed origin utilization to ρ = 0.96. Resulting M/M/1 queue buildup is the primary cause of tail latency amplification."

*(Note: This is an analytical explanation layer. Causality is proven via Counterfactual Replay).*

---

## 15. Statistical Analysis & MAUT

```text
internal/statistics/
```
- **Tests**: Mann-Whitney U, Bootstrap CI, Cliff's Delta (effect size). Statistical tests must be selected according to the specific experiment design and independence structure, rather than treating any single test as universally appropriate.
- **MAUT (Multi-Attribute Utility Theory)**: Generates Pareto frontiers showing multi-dimensional trade-offs (e.g., p99 latency vs. bandwidth cost vs. cache hit rate).

---

## 16. Testing Strategy & Metamorphic Invariants

- **Virtual-Engine Correctness**: Prove that given the same configuration and seed, the engine outputs an identical byte-for-byte event trace.
- **Metamorphic Testing**: Define relational invariants. Examples:
  - Increase every network delay by 2x $\rightarrow$ Total path latency must not decrease.
  - Halve total offered load $\rightarrow$ Server utilization ($\rho$) must monotonically decrease.

---

## 17. Deployment Modes

1. **Mode 1 (Research)**: Local Go virtual-time execution engine.
2. **Mode 2 (Real Emulation)**: Docker Compose + Linux + `tc netem`.
3. **Mode 3 (Observability)**: Prometheus + Grafana connected to the real emulation engine.

---

## 18. Build Sequence

1. **Real Networking**: TCP, `net/http`, connections.
2. **Abstract Policy Layer**: Separate `Select(edges, req)` from proxy.
3. **The Virtual-Time Engine**: Built as the engineering answer to the wall-clock reproducibility problem.
4. **Controlled Experiments**: Running large sweeps in virtual time.
5. **Stateful Counterfactual Replay**.
6. **Statistics & Attribution**.
7. **Optimization (Tuner)**.
8. **Real Validation**: Testing optimized weights in Docker.

---

## 19. Implementation Status (added Stage 9 — post-audit; updated Stage 10)

An adversarial audit after Stage 8 (`docs/audit/`) found this document's repository map, some type
sketches, and several described-but-unbuilt subsystems no longer matched the actual codebase at the
time. Stage 9 fixed every correctness/security/reproducibility finding; Stage 10
(`docs/StageArtifacts/Stage10.md`) then built every subsystem this section originally listed as
unbuilt. See `docs/audit/RESOLUTION.md` for the full per-finding disposition.

- **§1 Repository structure**: the actual package layout is `internal/{attribution,cache,challenge,
  chaos,clock,dashboard,engine,health,httpx,netsim,proxy,provenance,replay,statistics,tcp,telemetry,
  topology,traffic,transport,tuning,vtime}` plus ~50 `cmd/experiment-*` binaries — closer to this
  section's original sketch than it was at Stage 9's close, though `router`/`tuner`/`analysis`
  never became separate packages (that responsibility lives in `internal/proxy` and
  `internal/tuning` respectively, and no `analysis` package was ever needed).
- **§2 `Clock` interface**: the actual interface is `Clock interface { Now() VirtualTime }` — no
  `SleepUntil`. Time advancement is push-based (`Engine.Schedule`), not the blocking-sleep model
  sketched above; a deliberate, documented improvement (`docs/StageArtifacts/Stage5.md`), not an
  oversight, but this document's original sketch was never updated to match (still true as of
  Stage 10 — a cosmetic drift, not a functional gap).
- **§3 `ExperimentEngine` interface**: built — `internal/engine.ExperimentEngine`
  (`Prepare/Run/Replay`), implemented by `VirtualEngine` and `RealEngine`, both compile-time-verified
  against the interface. `ValidateConsistency` additionally cross-checks that a `Scenario` and its
  `RealExperimentConfig` name the same topology, closing a real gap Stage 10's own demo-readiness
  audit found (the two could otherwise silently describe different experiments).
- **§9 `manifest.json`/hierarchical seeds**: built — `replay.Scenario.Seeds` is a real `SeedTree`
  (Global/Traffic/Topology/Failure/Policy, genuine independent-axis control, not a derive-only
  convenience); `internal/provenance.Manifest`/`ConfigHash`/`GitCommit` write real manifests, wired
  into `cmd/experiment-010a` specifically.
- **§11 Tuner v2/v3**: built — `internal/tuning.Tuner` interface with `LHSTuner` and `BayesOptTuner`
  (hand-rolled Gaussian Process + Expected Improvement) alongside `RandomSearchTuner`, sharing one
  `RunSearch` loop. `cmd/experiment-010a`'s real 3-way comparison found neither meaningfully beats
  Random Search on this project's own search space — the expected result, not a wasted effort.
- **§12 YAML chaos**: built — `internal/chaos` (hand-rolled flat 4-key schema parser;
  `ToFailureWindows` for the virtual engine, `ToRealSchedule`/`RunReal` plus a new
  `EdgeServer.SetDown` for the real engine).
- **§13 HdrHistogram/Prometheus**: built — `internal/telemetry.Histogram` (hand-rolled, logarithmic
  buckets) and `WriteText` (Prometheus text-exposition format), live at `cmd/proxy -metrics-addr`.
- **§14 automated attribution engine**: built — `internal/attribution` (`CheckLittlesLaw`/
  `Utilization`/`UtilizationFromWorld`/`Explain`/`Compare`); `cmd/experiment-006d` refactored onto it.
- **§4 Adaptive router**: implements four scored signals (Load, Latency, Cache, Cost) — Health is a
  pre-filter, Capacity folds into Load — across six tunable parameters, not six independently
  scored signals. Unchanged by Stage 10.
- **§16 Metamorphic testing**: built — `internal/challenge/metamorphic_test.go` implements both
  named invariants (doubled service time → latency must not decrease; halved arrival count →
  utilization must not increase), each verified by Stage 10's own demo-readiness audit to actually
  catch a deliberately-injected violation, not just pass vacuously.