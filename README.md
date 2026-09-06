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
| **11** | Research validation: an 8-program experimental sweep (162+ runs) mapping policy regime boundaries, attacking Adaptive adversarially, testing distribution shift, and validating virtual-vs-real agreement — using Stage 10's platform, not extending it | ✅ Complete — **PASS WITH LIMITATIONS**, see `docs/StageArtifacts/Stage11.md` |
| **12** | Model fidelity: real-engine load instrumentation, genuine SeedTree axis independence, time-varying target service time, and a minimal finite-capacity contention model — built to close exactly four gaps Stage 11 proved consequential, then re-ran Stage 11's own findings under the corrected model | ✅ Complete — **PASS**, see `docs/StageArtifacts/Stage12.md` |

A note on Stage 10's own numbers: widening `Scenario.Seed` into a hierarchical `SeedTree` (needed
for genuine independent-axis seed control) changed the actual Development/Holdout scenario content,
so Stage 8's originally-reported specific tuning numbers no longer reproduce exactly under the
current code — the search/validation methodology itself is unaffected and was re-verified end to
end. See `docs/StageArtifacts/Stage10.md`'s own callout for the full explanation before citing any
Stage 8 number against a fresh run.

A note on Stage 11's own numbers: Stage 8's "Adaptive wins 62.5-70% of scenarios" and Stage 11's
"Adaptive wins 0/27 regime-map configurations on mean latency" are **not contradictory** — they measure
different things (a composite utility score over a broad random scenario distribution, vs. raw mean
latency over a systematically constructed regime sweep). See `docs/StageArtifacts/Stage11.md` §19
before citing either number against the other.

**A note on Stage 12's own numbers — Stage 11's "EWMA wins" finding was model-dependent, and now we
know exactly where**: `internal/replay.RunWorld` has two modes now. The **flat model** (every
`TargetProfile`'s `Capacity` left at its zero value, `ServiceTimeSchedule` empty) is byte-for-byte the
same model every stage through Stage 11 used — EWMA beats Adaptive on raw mean latency under
heterogeneous load in this mode, exactly as Stage 11 reported. The **contention-enabled model**
(`Capacity > 0` on any target) adds deterministic FIFO queueing; under it, that same finding **reverses
sharply** at the specific capacity level where EWMA's own concentration strategy pushes a target's
utilization past the queueing-stability boundary (ρ=1), and **fully recovers** one capacity level more
forgiving. Neither number is wrong — they describe different models, and Stage 12 exists specifically to
say precisely where they diverge and why. See `docs/StageArtifacts/Stage12.md` §7.1 before citing
either "EWMA wins" or "Adaptive wins" without stating which model produced it.

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

## Stage 11 Research Findings

Stage 10 built the platform; Stage 11 used it to actually answer FlashFlow's central question — under
what conditions does each routing policy, especially Adaptive, help? An 8-program experimental sweep
(`cmd/experiment-011a` through `011h`, 162+ controlled runs) found:

- **Adaptive is not universally better, and that's a regime finding, not a contradiction of Stage 8**:
  it wins 0 of 27 regime-map configurations on raw mean latency under heterogeneous load (EWMA wins 18,
  Round Robin the remaining 9 homogeneous ties), because the virtual engine has no queueing model and
  therefore can't penalize EWMA's unconstrained load concentration — while Adaptive deliberately keeps
  utilization balanced. This doesn't contradict Stage 8's 62.5-70% composite-utility win rate; the two
  measure different things (see README's own callout above and `Stage11.md` §19).
- **A real, causally-confirmed adversarial finding**: Adaptive's cache-affinity signal can trap it on a
  stale routing decision permanently — a hot key's affinity target crashes, recovers, and Adaptive never
  routes back to it (0% of post-recovery decisions), confirmed by zeroing the cache weight and watching
  the return rate jump to 54%.
- **A real bug, found and fixed**: `internal/engine.RealEngine` discarded the per-policy `Instrumentation`
  hook, so EWMA/Least-Connections/P2C/Adaptive selected blind (frozen cold-start signals) for the entire
  duration of any real-engine experiment. Fixed via existing debug-header infrastructure; verified via a
  non-flaky regression test. No prior experiment in this project's history was affected (none previously
  called `RealEngine` for a dynamic policy).
- **A second real bug, found and regression-tested**: `internal/tuning`'s Topology and Failure SeedTree
  axes are not fully independent in one direction, a previously-untested invariant.

Full research plan, all 8 programs' results, mechanism explanations, a What-Would-Falsify-This table per
major claim, limitations, and the final verdict (**PASS WITH LIMITATIONS**):
[`docs/StageArtifacts/Stage11.md`](docs/StageArtifacts/Stage11.md). What changed in our understanding,
narratively: [`docs/learning/011-stage11-research-validation.md`](docs/learning/011-stage11-research-validation.md).
Machine-readable experiment index (command/seed/artifact per finding):
[`experiments/011-research-validation/INDEX.json`](experiments/011-research-validation/INDEX.json).

## Stage 12 Findings — Model Fidelity and Experimental Control

Stage 11 answered FlashFlow's research question but flagged four platform gaps as consequential enough
to revisit. Stage 12 built the minimum mechanism to close each one, then re-ran Stage 11's own findings
under the corrected model:

- **Stage 11's flagship "EWMA beats Adaptive" finding was model-dependent, and now we know exactly
  where it breaks**: `internal/replay.TargetProfile` gained an opt-in `Capacity` field (deterministic
  FIFO queueing when set, byte-identical to the old flat model when left at zero). Under a finite
  capacity that pushes EWMA's own concentration past the queueing-stability boundary (ρ=1), its mean
  latency explodes 8x and **Adaptive wins decisively** — a reversal confirmed robust across 12
  independent seeds (Cliff's Delta 1.000). One capacity level more forgiving, EWMA's dominance is fully
  restored. Not "Adaptive is better now" — a precise, mechanistically-explained regime boundary.
- **H2 (a staleness/oscillation attack) was tested for the first time**, via a new
  `TargetProfile.ServiceTimeSchedule` (discrete, scheduled service-time changes). EWMA gets completely
  and permanently stuck routing to a target after it degrades (100% of decisions during the swap
  window); Adaptive lags less severely (63%) but for a different reason than hypothesized — varying
  `StaleAfter` across three orders of magnitude made no difference.
- **A second real bug found and fixed**: `internal/engine.RealEngine` still read a disconnected,
  never-updated load tracker even after Stage 11's partial latency fix. Fixed by sharing the proxy's own
  genuinely-concurrent tracker with the selector — Adaptive's real-engine behavior now matches its
  virtual behavior closely (max_share 0.500 vs 0.503, was 1.000 before the fix).
- **The Topology/Failure SeedTree leak (Stage 11 §16) is now structurally fixed**, confirmed via a
  mandatory historical-impact check: rerunning Stage 8's entire tuning pipeline under the corrected
  generator found the *same* winning configuration with only small numerical drift — the qualitative
  conclusion is unchanged.

Full findings, the claim-reconciliation table, and the final verdict (**PASS**):
[`docs/StageArtifacts/Stage12.md`](docs/StageArtifacts/Stage12.md). Design plan (written first):
[`docs/StageArtifacts/Stage12-Plan.md`](docs/StageArtifacts/Stage12-Plan.md). What changed in our
understanding, narratively:
[`docs/learning/012-stage12-model-fidelity-and-control.md`](docs/learning/012-stage12-model-fidelity-and-control.md).

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

## Running Stage 11 Research

```bash
go run -buildvcs=true ./cmd/experiment-011a   # Program A: policy regime map (162 runs)
go run -buildvcs=true ./cmd/experiment-011b   # Program B: adversarial adaptive scenarios
go run -buildvcs=true ./cmd/experiment-011c   # Program C: recovery/adaptation dynamics
go run -buildvcs=true ./cmd/experiment-011d   # Program D: distribution shift
go run -buildvcs=true ./cmd/experiment-011e   # Program E: mechanistic attribution
go run -buildvcs=true ./cmd/experiment-011f   # Program F: virtual-vs-real validation
go run -buildvcs=true ./cmd/experiment-011g   # Program G: seed/reproducibility attack
go run -buildvcs=true ./cmd/experiment-011h   # statistical robustness check (12-seed replication)
```

Every result traces back to its exact command/seed/artifact via
[`experiments/011-research-validation/INDEX.json`](experiments/011-research-validation/INDEX.json).

## Running Stage 12 Research

```bash
go run -buildvcs=true ./cmd/experiment-012a   # Program A's flagship finding rebuilt under contention (0/1/2/3 capacity sweep)
go run -buildvcs=true ./cmd/experiment-012b   # H2: staleness/oscillation attack, tested for the first time
go run -buildvcs=true ./cmd/experiment-012c   # B1 cache-affinity trap re-run under contention
go run -buildvcs=true ./cmd/experiment-012d   # attribution re-evaluated with genuine wait-time decomposition
go run -buildvcs=true ./cmd/experiment-012e   # statistical robustness of the contention-reversal finding
go run -buildvcs=true ./cmd/experiment-012f   # recovery dynamics re-run under contention

# Contention model benchmark (flat vs finite-capacity throughput cost)
go test ./internal/replay/... -bench BenchmarkRunWorld -run '^$'

# Track C/D validation suites (analytically hand-computed expected values)
go test ./internal/replay/... -run TestContention -v
go test ./internal/replay/... -run TestServiceTimeSchedule -v
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
| [011](experiments/011-research-validation/) | Research Validation (Programs A-H: regime map, adversarial testing, distribution shift, mechanistic attribution, virtual-vs-real, reproducibility) | ✅ Complete — see [`INDEX.json`](experiments/011-research-validation/INDEX.json) |
| [012](experiments/012-model-fidelity/) | Model Fidelity (contention model, time-varying service time, real-engine load fix, seed-isolation fix — Stage 11's flagship finding re-tested and reversed under a specific, identified regime) | ✅ Complete — see [`Stage12.md`](docs/StageArtifacts/Stage12.md) |

---

## Resume Line

> **FlashFlow — Adaptive Edge Networking Laboratory**: Built a dual-engine distributed system in Go combining a deterministic virtual-time simulator and a real HTTP emulation engine with an in-process network-degradation simulator (built specifically in place of `tc netem`, which was evaluated and not used); implemented adaptive routing, TTL+coalescing edge caching, and a self-tuning parameter optimizer validated via stateful counterfactual replay and statistical queueing analysis. Stage 1 grounded the system in raw TCP semantics through a controlled connection-lifecycle benchmarking study that discovered TIME_WAIT exhaustion and its effects on sequential benchmark contamination.
