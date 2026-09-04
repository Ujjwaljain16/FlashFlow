# Stage 3 — Server-Side Scaling & Routing Policies: Exit Artifact

## What Was Built

| Component | File(s) | Description |
|---|---|---|
| Weighted Round Robin | `internal/proxy/weighted_round_robin.go` | Smooth WRR (nginx/LVS algorithm); config-only state, no interface change |
| Application-level load tracking | `internal/proxy/load_tracker.go` | Proxy-owned, ambient in-flight-request counter — deliberately not derived from TCP socket count |
| Least Connections | `internal/proxy/least_connections.go` | Reads `LoadTracker`; discovers capacity without being told the ratio |
| EWMA latency tracking | `internal/proxy/latency_tracker.go` | Proxy-owned, ambient exponentially-weighted latency estimator per target |
| Latency-Aware EWMA | `internal/proxy/ewma.go` | Cold-start-aware greedy latency selector; shares `preferScore` with P2C |
| Power of Two Choices | `internal/proxy/p2c.go` | Seeded random-pair sampling over a pluggable `P2CScorer` (load or latency) |
| Proxy wiring | `internal/proxy/proxy.go` | `LoadTracker`/`LatencyTracker` getters; increment/defer-decrement and observe-on-success wired into `ServeHTTP` |
| Six experiment suites | `cmd/experiment-003a` … `003f` | RR baseline, WRR, Least Connections, EWMA, P2C, and the Burst/Failure comparison matrix |
| Experiment documentation | `experiments/003-routing-policies/{hypotheses,README}.md` | H1–H6, methodology, and full results for all six experiments |
| Learning notes | `docs/learning/003-routing-policies.md` | Full before/after, surprises, mechanism, and motivation-for-next-stage narrative |

**No changes were made to Stage 1 or Stage 2 code**, except the one documentation-comment hardening (health cumulative-error semantics) completed and verified before Stage 3 began. `TargetSelector`'s interface is byte-for-byte unchanged from Stage 2 — every Stage 3 policy, including the two with genuine runtime state (Least Connections, EWMA/P2C), was built via constructor-injected dependencies (`LoadTracker`, `LatencyTracker`) rather than an interface change, which is a real architectural finding in its own right: the interface evolution anticipated in the Stage 2→3 recon turned out not to be necessary.

---

## Repository Tree (Stage 3 additions)

```text
flashflow/
├── cmd/
│   ├── experiment-003a/main.go   # Round Robin fairness baseline
│   ├── experiment-003b/main.go   # Weighted Round Robin, static heterogeneous capacity
│   ├── experiment-003c/main.go   # Least Connections, static + dynamic + low-concurrency
│   ├── experiment-003d/main.go   # EWMA, static + dynamic + alpha sensitivity
│   ├── experiment-003e/main.go   # P2C, lock-in check + comparison matrix + recovery
│   └── experiment-003f/main.go   # Comparison matrix: burst and failure
├── docs/
│   └── learning/003-routing-policies.md
├── experiments/003-routing-policies/
│   ├── hypotheses.md              # H1-H6
│   ├── README.md                  # Full methodology + results, sections 1-8
│   └── results/                   # 49 JSON result files
└── internal/proxy/
    ├── weighted_round_robin.go / _test.go
    ├── load_tracker.go / _test.go
    ├── least_connections.go / _test.go
    ├── latency_tracker.go / _test.go
    ├── ewma.go / _test.go
    ├── p2c.go / _test.go
    ├── proxy.go                   # modified: LoadTracker/LatencyTracker wiring
    └── proxy_test.go              # modified: +7 integration tests
```

---

## Tests Written (47 new, on top of Stage 2's 4 proxy-package tests)

| Area | Representative tests | Covers |
|---|---|---|
| WRR | `TestWRR_ExactPeriodicDistribution`, `TestWRR_NoBurstiness`, `TestWRR_TargetTemporaryRemoval`, `TestWRR_ConcurrentSelection` | Exact-period invariant (not approximate), verified burst bound, health-exclusion interaction, concurrency safety |
| LoadTracker | `TestLoadTracker_DecrementFlooredAtZero`, `TestLoadTracker_ConcurrentIncrementDecrement` | Defensive floor, net-zero correctness under 100 concurrent goroutines |
| Least Connections | `TestLeastConnections_ReactsToChangingLoad`, `TestLeastConnections_TieBreaksToFirstInOrder` | Dynamic reaction, deterministic tie-break (later shown to be a real limitation at low concurrency) |
| Proxy/LC integration | `TestProxy_LeastConnections_DecrementsAfterUpstreamError` | Proves no leak on the real HTTP error path — the exact leak scenario flagged in the pre-Stage-3 recon |
| LatencyTracker | `TestLatencyTracker_SmoothingFormula`, `TestLatencyTracker_OneOutlierDoesNotDominate` | Exact EWMA formula check, numerically verified outlier resistance |
| EWMA | `TestEWMA_UnobservedBeatsObserved`, `TestEWMA_ReactsToChangingLatency` | Cold-start exploration rule, dynamic reaction |
| Proxy/EWMA integration | `TestProxy_EWMA_NoObservationOnUpstreamError` | Proves failures never pollute the latency estimate |
| P2C | `TestP2C_DeterministicUnderSeededRandomness`, `TestP2C_ExplorationDoesNotLockIn`, `TestP2C_EqualRivalsBothStayFresh`, `TestP2C_LatencyBased_CannotDetectRecoveryOfLosingTarget`, `TestP2C_LoadBased_RecoveryIsAlwaysDetectable` | Reproducible randomness, no-lock-in property, and the precise, corrected boundary of what P2C does/doesn't fix (see Findings) |
| Proxy/P2C integration | `TestProxy_P2C_EndToEnd_AvoidsBusyEdge` | Real concurrent HTTP traffic correctly steers around a busy edge |

Full list: 51 top-level test functions now in `internal/proxy` (4 from Stage 2 + 47 new); 72 tests pass across the whole repository.

## Test Results

```
gofmt -l .        clean
go build ./...    clean
go vet ./...      clean
go test ./...     ok  (all 7 packages)
```

`go test -race` is **not available in this environment** (`gcc` not found; `CGO_ENABLED=1` build fails on `runtime/cgo`) — stated honestly per project policy, not claimed as passed. Concurrency safety is instead argued explicitly in code comments (e.g. `WeightedRoundRobinSelector`'s and `LoadTracker`'s mutex-guarded compound operations) and exercised by dedicated concurrent-access tests (`TestWRR_ConcurrentSelection`, `TestLoadTracker_ConcurrentIncrementDecrement`, `TestLatencyTracker_ConcurrentObserve`, `TestP2C_ConcurrentSelection`, `TestProxy_P2C_EndToEnd_AvoidsBusyEdge`, run repeatedly during development with no observed failures).

---

## Experiment Inventory & Key Results

| # | Title | Central Finding |
|---|---|---|
| 003-A | Round Robin fairness baseline | Confirmed under real 97-way concurrent contention (exact floor/ceil split), not just arithmetic |
| 003-B | WRR under static heterogeneous capacity | Correct weights (100:100:1) recovered 2.9× the throughput RR lost in 002-D, but fell well short of the homogeneous baseline |
| 003-C | Least Connections | Matched/beat hand-tuned WRR without knowing the ratio; adapted to mid-run degradation with zero reconfiguration; **at c=1, degenerates to permanently picking whichever target is first in config order** (verified by reversing that order and flipping the outcome from best-of-all-policies to worst) |
| 003-D | EWMA | **Headline finding**: pure greedy selection permanently locks onto one of several genuinely equal targets (three replications: 94/4/2, 68/29/3, 18/79/3), and can never detect that an unselected target's performance has changed |
| 003-E | P2C | Reduces lock-in from "1 survivor" to "2 survivors" among 3 equal targets (a real, partial fix, precisely characterized — not "solved"); P2C-over-latency inherits EWMA's blindness to recovery, P2C-over-load does not, because load is live truth and latency is a memory |
| 003-F | Comparison matrix: Burst & Failure | Least Connections' documented race worsens measurably under burst concurrency; the health/routing architectural separation holds for every policy under a hard failure; one under-replicated result (error-rate ranking) explicitly flagged as insufficient evidence rather than reported as a finding |

Full data, methodology, and per-experiment interpretation: `experiments/003-routing-policies/README.md` (8 sections, ~49 JSON result files).

---

## Known Limitations (carried forward, not fixed in Stage 3)

These are documented in code (`LoadTracker.Get`, `EWMASelector`, `P2CSelector` doc comments) and in the experiment README, not silently left implicit:

1. **`LeastConnectionsSelector` and `EWMASelector`/`P2CSelector`** all have a documented read-then-act race between reading current state and the proxy's later increment/observe call — bounded and self-correcting under normal load, but responsible for the c=1 Least-Connections lock-in and contributes to burst-concurrency degradation.
2. **A target that stops being selected by a latency-scored policy (EWMA, P2C-over-latency) can never be shown to have recovered.** This is the most important unresolved limitation of the stage — verified at both the unit level and, decisively, in a live proxy (Experiment 003-E, degradation-then-recovery).
3. **Experiment 003-F's failure-scenario error-rate comparison is a single trial per policy** and is explicitly flagged as requiring replication before it supports any ranking claim.
4. **P2C-over-latency's extreme post-failure lock-in (95.5% onto one survivor)** is reported as an observed, unexplained phenomenon, not a mechanistically confirmed finding — a candidate for a dedicated follow-up experiment.

None of these are "bugs" in the sense of violating a stated invariant — all are precisely the kind of policy-level trade-off Stage 3 exists to surface, and per the project's "earn the abstraction" rule, none are patched here; each motivates a specific future capability rather than a Stage 3 workaround.

---

## Gate-by-Gate Verdict

| Gate | Status | Notes |
|---|---|---|
| **1 – Implementation** | ✅ PASS | RR, WRR, Least Connections, EWMA, P2C all implemented; `TargetSelector` unchanged |
| **2 – Concurrency Safety** | ✅ PASS (by design + tests, not `-race`) | All compound state operations mutex-guarded; dedicated concurrent tests; `-race` unavailable in this environment (documented, not glossed over) |
| **3 – Health Integration** | ✅ PASS | Health/routing separation preserved throughout; explicitly re-verified under a real hard failure in 003-F |
| **4 – Testing** | ✅ PASS | 72 tests pass repo-wide, `gofmt`/`go vet` clean |
| **5 – Empirical Research** | ✅ PASS | 6 experiment suites, 49 JSON result files, all 6 comparison-matrix scenarios (Homogeneous/Heterogeneous/Slow-edge/Burst/Failure/Recovery) covered |
| **6 – Evidence Discipline** | ✅ PASS | Two of the stage's own hypotheses (H3c, the P2C-recovery unit test premise) were found wrong and corrected in the record rather than hidden; a measurement bug (warmup contamination) and a methodology bug (mismatched WRR weights) were both caught and fixed before results were trusted; one result set explicitly flagged as under-replicated rather than reported as a ranking |
| **7 – Documentation** | ✅ PASS | `hypotheses.md` (H1-H6), `README.md` (8 sections), `docs/learning/003-routing-policies.md`, this exit artifact |

---

## Stage 4 Readiness

**READY**

> Stage 3 did not produce a single "this changes everything" finding the way Experiment 002-D did for Stage 2 → 3. It produced five independently reproduced, non-overlapping reasons no single routing signal is sufficient — count (RR), weight (WRR), load (Least Connections/P2C-load), and latency (EWMA/P2C-latency) each fail a different, now-evidenced scenario. That is the correct shape of evidence for justifying a combined-signal adaptive router later (Stage 7), but Stage 4's own mandate — edge caching, request coalescing, and `tc netem` realism checks — is unrelated to that finding and should proceed on its own merits, not be treated as an excuse to jump ahead to adaptive routing.
