# Stage 16 — Scope Freeze

Every major capability named in `prd.md`, `trd.md`, or `research.md`, classified against the actual
current source tree (verified by direct inspection, not by trusting prior documentation) as of this
stage's HEAD commit. This freeze exists so no reviewer has to guess which of this project's many
described capabilities were actually built, actually validated, or remain aspirational.

## Classification Legend

- **BUILT**: source code exists and compiles/runs.
- **VALIDATED**: built, and covered by a passing test or an experiment whose result is committed.
- **BUILT BUT NOT VALIDATED**: exists in source, no dedicated test/experiment confirms it behaves as
  intended.
- **DEFERRED**: evaluated, deliberately not built, with a documented reason.
- **REJECTED**: considered and explicitly ruled out (usually for determinism or scope reasons).
- **OUT OF SCOPE**: never part of this project's actual requirements, even if mentioned in a proposal
  document.
- **ASPIRATIONAL / NOT IMPLEMENTED**: appears in `research.md`'s pre-implementation vision document,
  never built, and not planned.

## Core Platform

| Capability | Status | Evidence |
|---|---|---|
| TCP client/server foundations | VALIDATED | `internal/tcp/*`, Stage 1's 120-run connection-lifecycle benchmark |
| HTTP reverse proxy | VALIDATED | `internal/proxy/proxy.go` + tests, Stage 2 experiments |
| Routing policies: RR / WRR / Least-Connections / EWMA / P2C | VALIDATED | `internal/proxy/{round_robin,weighted_round_robin,least_connections,ewma,p2c}.go` + per-policy tests, Stage 3/7 experiments |
| Adaptive router (4 scored signals: Load/Latency/Cache/Cost) | VALIDATED | `internal/proxy/adaptive.go` (13 test cases); README/prd.md/trd.md all correctly say "four scored signals," not the historically-misdescribed "six-signal" |
| Health state machine (4-state, Clock-driven) | VALIDATED | `internal/health/*`, Stage 2/5/7 experiments |
| Caching: TTL + request coalescing (singleflight) | VALIDATED | `internal/cache/{cache,coalesce}.go` + tests, Stage 4 |
| Caching: Stale-While-Revalidate (SWR) | VALIDATED | `internal/cache/swr.go` + tests, wired into the real `EdgeServer` path, Stage 10 |
| Caching: LRU eviction | **DEFERRED** | Never built — `internal/cache/cache.go`'s own comment: "no eviction policy yet ... see the Stage 4 README for when LRU would actually get justified." No experiment ever needed bounded cache memory. Disclosed in `prd.md` §13 and (as of this stage) `trd.md` §19 |
| Virtual-time discrete-event engine | VALIDATED | `internal/vtime/*`; deterministic-replay tests re-run with `-count=5 -shuffle=on` |
| Statistics toolkit (percentile, Mann-Whitney U, Cliff's Delta, bootstrap CI) | VALIDATED | `internal/statistics/*`, each independently checked against hand-computed reference values |
| Counterfactual replay (exogenous/endogenous isolation) | VALIDATED | `internal/replay/*`; identity/divergence/isolation tests using full-trace `reflect.DeepEqual`, not summary stats |
| Queueing-theoretic attribution (Little's Law, utilization) | VALIDATED | `internal/attribution/*`, used live in the Stage 10 demo |
| Backlog dynamics analysis (Timeline, concentration, committed backlog, diversion) | VALIDATED | `internal/backlog/*` (Stage 15), 8 hand-computed unit tests |

## Network & Chaos Modeling

| Capability | Status | Evidence |
|---|---|---|
| In-process network degradation simulator | VALIDATED, correctly NOT `tc netem` | `internal/netsim/netsim.go`'s own doc comment: "This exists in place of `tc netem` ... netem is Linux-only, and these experiments run on Windows." Seeded RNG threaded through for reproducibility |
| Real `tc netem` / Linux traffic control | **REJECTED (never implemented, by design)** | Zero occurrences of `netem`/`qdisc`/`NET_ADMIN` in any `.go` file — evaluated and explicitly not used |
| Docker deployment (real containers running the real proxy/edge/origin binaries) | VALIDATED, distinct from network degradation | `deployments/Dockerfile`, `deployments/docker-compose/stage2.yml` — a real 4-container topology. Must not be conflated with the rejected tc-netem network-shaping claim; this is genuinely built and used |
| Declarative YAML chaos schedules | VALIDATED | `internal/chaos/{virtual,real}.go`, used throughout Stages 12-15 |
| Advanced/arbitrary topology generators (Fat-Tree, Barabási–Albert, Watts-Strogatz) | **OUT OF SCOPE / ASPIRATIONAL-NOT-IMPLEMENTED** | Zero hits anywhere in `internal/`. `internal/topology` is a fixed, small edge/origin topology built per-experiment, never a procedural generator suite. Never part of PRD/TRD scope — only `research.md`'s own proposal document mentions it |

## Tuning

| Capability | Status | Evidence |
|---|---|---|
| Random Search (Tuner v1) | VALIDATED, the actual winning/recommended tuner | `internal/tuning/search.go`; Stage 8's 62.5-70% composite-utility win-rate result |
| Latin Hypercube Sampling (Tuner v2) | BUILT AND VALIDATED, explicitly not the winner | `internal/tuning/lhs.go`; `cmd/experiment-010a`'s real 3-way comparison found neither LHS nor Bayesian Optimization meaningfully beats Random Search on this project's own (small) search space |
| Bayesian Optimization (Tuner v3) | BUILT AND VALIDATED, explicitly not the winner | `internal/tuning/bayesopt.go` (hand-rolled Gaussian Process + Expected Improvement); same `cmd/experiment-010a` finding |
| Development/Holdout separation | VALIDATED | `internal/tuning/scenario.go`: disjoint seed ranges by construction (`DevelopmentSeedStart`/`HoldoutSeedStart`), not a runtime check |
| Pareto/multi-objective scoring | BUILT | `internal/tuning/objective.go` (`Scores.Dominates`, `ParetoFrontier`) — lives in `internal/tuning`, not `internal/statistics` as an early PRD draft implied; a package-location drift, not a missing capability |

## Telemetry & Observability

| Capability | Status | Evidence |
|---|---|---|
| Hand-rolled latency histogram | VALIDATED | `internal/telemetry/histogram.go` — logarithmic buckets, own test suite |
| Prometheus TEXT-EXPOSITION format writer | VALIDATED, NOT a real Prometheus/client_golang integration | `internal/telemetry/prometheus.go`'s own comment: "hand-rolled per Stage 10's confirmed design decision rather than adding `prometheus/client_golang`" |
| OpenTelemetry | **ASPIRATIONAL / NOT IMPLEMENTED** | Zero OpenTelemetry code anywhere; only appears in `research.md`'s proposal document, explicitly disclaimed there as never built |
| Experiment manifest / provenance (hierarchical SeedTree, ConfigHash, GitCommit) | BUILT, PARTIALLY WIRED | `internal/provenance/*`; wired into `cmd/experiment-010a` specifically. Most other experiment binaries (including all of Stages 11-16's) still write their own ad hoc result JSON rather than a full manifest — disclosed, not hidden |

## Explicitly Rejected / Never Attempted

| Capability | Status | Evidence |
|---|---|---|
| Doubly Robust Off-Policy Evaluation (DR-OPE) | **ASPIRATIONAL / NOT IMPLEMENTED** | Zero hits anywhere in `.go` source. `prd.md` §4 lists it as an explicit non-goal/deferral |
| Contextual bandits (LinUCB), reinforcement learning, Thompson Sampling, UCB1, ε-greedy | **REJECTED / NOT IMPLEMENTED** | Zero `.go` hits. `research.md`'s own feature-ranking matrix marks Deep RL "REJECT" |
| Production-grade orchestration / Kubernetes / service mesh | **OUT OF SCOPE** | Zero hits in source; `prd.md` §4 explicitly lists "Kubernetes replacement" as a non-goal |
| Apache Parquet event storage | **ASPIRATIONAL / NOT IMPLEMENTED** | `research.md`-only proposal, correctly scoped out |
| Additional cache algorithms (TinyLFU, ARC) beyond LRU/TTL/SWR | **ASPIRATIONAL / NOT IMPLEMENTED** | Mentioned only in `research.md`'s proposal document, explicitly disclaimed there |
| Full distributed-system realism / byte-for-byte cross-hardware determinism | **OUT OF SCOPE** | `research.md`'s own top-of-file disclaimer names this explicitly as never built and not planned; determinism claims in this project are scoped to "same seed, same machine, same Go version," not cross-hardware |

## Documentation Corrections Made This Stage

Two precise overclaims were found and fixed during this stage's own source-of-truth audit (see
`docs/StageArtifacts/Stage16-ClaimLedger.md` for the full ledger):

1. `trd.md` §8 asserted LRU was implemented in present tense alongside TTL/SWR; corrected, and a missing
   §19 reconciliation entry added (every other section already had one).
2. `prd.md` §6.2 claimed results are "always validated against unseen holdout scenarios to prevent
   overfitting" without noting Holdout shares Development's own scenario distribution (differing only by
   seed) — reworded to state that scope precisely.

A third, lower-priority clarity gap (the opening description calling the real engine simply "a Docker
emulation engine" without the netsim-not-tc-netem clarification nearby) was also fixed. `research.md`'s
own comparison table (the "FlashFlow (Proposed)" row listing bandits/OPE) was reviewed and judged
sufficiently scoped already: the document's own top-of-file disclaimer explicitly names that exact row
and states plainly that everything in it beyond what actually shipped "was never built and is not
planned" — no further edit was made there.

## What This Freeze Does NOT Cover

This freeze intentionally does not re-litigate research FINDINGS (whether Adaptive is "better," whether
committed backlog predicts collapse, etc.) — those are covered in `docs/StageArtifacts/Stage16-ClaimLedger.md`
and `docs/StageArtifacts/Stage16-ResearchSynthesis.md`. This document is scoped to implementation
capabilities only: what exists in code, and how honestly the documentation describes it.
