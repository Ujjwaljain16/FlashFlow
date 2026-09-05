# Requirement Traceability Matrix

Cross-references every meaningful PRD/TRD requirement against its actual implementation, tests,
experiment evidence, and documentation. Status categories: **IMPLEMENTED+VERIFIED** ·
**IMPLEMENTED+PARTIALLY VERIFIED** · **IMPLEMENTED DIFFERENTLY** (real drift, argued or disclosed) ·
**PARTIALLY IMPLEMENTED** · **DEFERRED/OUT OF SCOPE** (explicitly stated) · **MISSING** (no trace of
an attempt) · **CONTRADICTORY**.

See [FINDINGS.md](FINDINGS.md) for full evidence on each cross-referenced item.

| # | Requirement (source) | Code | Test | Experiment | Docs | Status |
|---|---|---|---|---|---|---|
| 1 | Dual-Engine Architecture (PRD §6.1/§7; TRD §3) | `internal/vtime`+`internal/replay` (virtual) vs. `internal/topology`,`internal/cache`,`internal/netsim`,`cmd/proxy` (real) — no unifying interface | `world_test.go`, `engine_test.go` | 005-H, 006-F | `docs/FinalResearchReport.md:49` | IMPLEMENTED DIFFERENTLY — engines share selector/health code but no `Prepare/Run/Replay` interface exists ([F-04](FINDINGS.md#f-04--p1--experimentengine-interface-trd-3-was-never-built)) |
| 2 | `ExperimentEngine` interface (TRD §3) | none | — | — | Disclosed once at Stage 5, never closed at Stage 8 | MISSING, disclosed-then-abandoned ([F-04](FINDINGS.md)) |
| 3 | `Clock` interface shape (TRD §2) | `internal/clock/clock.go` — `Now()` only, no `SleepUntil` | clock tests | — | Stage5.md explains the real (better) design | CONTRADICTORY vs. TRD text, internally coherent code ([F-48](FINDINGS.md)) |
| 4 | Weight Auto-Tuner: Random→LHS→Bayesian (PRD §6.2 "core, not optional"; TRD §11) | `internal/tuning/search.go` — Random Search only | search_test.go, search_bench_test.go | 008-A..D | Stage8.md explicitly discloses LHS/Bayesian unbuilt | PARTIALLY IMPLEMENTED, well-argued, PRD text stale ([F-22](FINDINGS.md)) |
| 5 | Stateful Counterfactual Replay, exogenous/endogenous separation (PRD §6.3; TRD §10) | `internal/replay/{scenario,world,policies,compare}.go` | full identity/isolation/divergence suite | 007-F, 007-G, 007-H | Stage7.md | IMPLEMENTED+VERIFIED, with one unenforced boundary caveat ([F-18](FINDINGS.md)) |
| 6 | Queueing-Theoretic Attribution, automated causal explanation (PRD §6.4 "core, not optional"; TRD §14) | no reusable module; math inlined once in `cmd/experiment-006d`/`006e` | none (one-off scripts) | 006-D, 006-E | Narrative written by hand in Stage6.md | PARTIALLY IMPLEMENTED / IMPLEMENTED DIFFERENTLY, undisclosed against spec ([F-08](FINDINGS.md)) |
| 7 | Traffic Generator: constant/ramp/burst/flash-crowd + Fuze import (PRD §8.1) | none — hardcoded `[]Arrival` per scenario | — | — | Zero mentions anywhere outside prd.md | MISSING, no deferral trail found ([F-06](FINDINGS.md)) |
| 8 | Global Router progression RR→WRR→LC→EWMA→P2C→Adaptive (PRD §8.2; TRD §4) | `internal/proxy/{weighted_round_robin,least_connections,ewma,p2c,adaptive}.go` | full per-file unit suites | 003-*, 007-* | Stage3.md, Stage7.md | IMPLEMENTED+VERIFIED |
| 9 | Adaptive "six-signal" router (PRD §8.2; TRD §4.7) | `internal/proxy/adaptive.go` — 4 scored signals (Load/Latency/Cache/Cost); Health is a pre-filter, Capacity folds into Load | adaptive_test.go (13 cases) | 007-A..H, 008-B..F | Stage7.md documents the collapse; README/learning notes still say "six-signal" | IMPLEMENTED DIFFERENTLY, terminology not reconciled ([F-25](FINDINGS.md)) |
| 10 | Reverse Proxy shared policy layer (PRD §8.3) | `internal/proxy` selectors reused directly by `cmd/proxy` and `internal/replay/policies.go` | proxy_test.go | 002-*, 005-E | Stage5.md | IMPLEMENTED+VERIFIED — genuinely shared code, confirmed by direct trace (though `cmd/proxy`'s own CLI can only select Round Robin, [F-30](FINDINGS.md)) |
| 11 | Connection Management (PRD §8.4) | `internal/transport/pool.go` | transport tests | 002-A1/A2, 006-D | Stage2.md, Stage6.md | IMPLEMENTED+VERIFIED |
| 12 | Edge Cache — No Cache/TTL/LRU/SWR (PRD §8.5; TRD §8) | `internal/cache/cache.go` — fixed-TTL + coalescing only, no LRU, no SWR | cache_test.go, coalesce_test.go | 004-A..F | LRU deferral documented (Stage4.md); SWR never mentioned | PARTIALLY IMPLEMENTED — LRU disclosed, SWR is not ([F-09](FINDINGS.md)) |
| 13 | Health 4-state machine, Clock-driven (PRD §8.6; TRD §7) | `internal/health/state.go` | health_test.go, challenge/health_test.go | 002-B, 005-D, 007-E | Stage2.md | IMPLEMENTED+VERIFIED — 100% Clock-driven, no wall-clock calls found |
| 14 | Declarative Chaos Engine (YAML) + `tc netem` translation (PRD §8.7; TRD §6,§12) | No YAML anywhere; failures are Go struct literals; real engine uses `internal/netsim`, not Docker/tc | netsim_test.go, challenge/network_test.go | 004-F | `tc netem` substitution documented at Stage4; YAML gap undocumented; top-level docs still claim tc netem | MISSING (YAML) / IMPLEMENTED DIFFERENTLY, but misrepresented in README/prd/trd ([F-02](FINDINGS.md), [F-10](FINDINGS.md)) |
| 15 | Experiment Ledger & Provenance: manifest.json, hierarchical seeds, ConfigurationHash (PRD §8.8; TRD §9) | none — `Scenario.Seed` is the only seed field | — | — | Disclosed once at Stage 5; `FinalResearchReport.md` wording overclaims | MISSING, disclosure not closed out, final report language misleading ([F-05](FINDINGS.md)) |
| 16 | Metrics & Analytics: HdrHistogram/Prometheus/Bootstrap CI/Mann-Whitney/Cliff's Delta/Pareto (PRD §8.9; TRD §13/§15) | `internal/statistics/*` (non-parametric stats + Pareto real); no HdrHistogram, no Prometheus, no typed canonical Event Stream | full stats test suite | 006-A..F | Stage6.md | PARTIALLY IMPLEMENTED — the statistical core is correct and verified; the named telemetry stack was substituted without disclosure ([F-23](FINDINGS.md)) |
| 17 | Live Dashboard, deferred to Stage 8 (PRD §8.10) | `cmd/dashboard`, `internal/dashboard` | dashboard_test.go (traversal test is a false negative, see [F-01](FINDINGS.md)) | Manual browser verification per Stage8.md | Stage8.md; not mentioned in README at all ([F-20](FINDINGS.md)) | IMPLEMENTED, one confirmed security defect ([F-01](FINDINGS.md)) |
| 18 | Success Criteria: Auto-Tuner improves on P2C baseline, holdout-validated, counterfactual-verified (PRD §10) | Full `internal/tuning` pipeline via `replay.RunWorld` | evaluate_test.go, robustness_test.go, sensitivity_test.go | 008-B, 008-C, 008-D, 008-F | Stage8.md, numbers independently re-traced and matched | IMPLEMENTED+VERIFIED, with the generalization-claim scope caveat at [F-17](FINDINGS.md) |

## Cross-cutting observations

**Honestly disclosed drift** (argued, traceable, low residual risk): tuner tier reduction (F-22),
`tc netem`→`internal/netsim` substitution at the Stage 4 level (though not propagated to top-level
docs, F-02), LRU cache deferral, six-signal→four-signal router collapse, `ExperimentEngine`
deferral (disclosed once, never closed).

**Silently dropped, no deferral trail found anywhere**: Traffic Generator (F-06), SWR cache policy
(F-09), HdrHistogram/Prometheus telemetry (F-23), the automated queueing-attribution engine as
originally specified (F-08), the experiment manifest/provenance/hierarchical-seed system (F-05),
metamorphic invariant testing (F-07), declarative YAML chaos schedules (F-10).

**Meta-finding**: Stage 8's own "Limitations" section lists six specific, real limitations
(objective weighting, LHS/Bayesian unbuilt, challenge-suite coverage, NGINX-comparison scope,
CPU-metric fidelity, dashboard auth) — all accurate as far as they go — but does not mention any of
the six items in the paragraph above, despite Stage 8 being the natural place, as the declared
final stage, to formally close out every PRD/TRD item that was never built. A reader trusting that
section as the complete list of known gaps would significantly overestimate PRD/TRD completeness.

**research.md is not a build contract.** It describes a substantially larger vision (Parquet,
OpenTelemetry, DR-OPE, CausalSim tensor completion, contextual bandits, Fat-Tree/Barabási–Albert
topology generators) than PRD/TRD ever scoped or than shipped. PRD's own Non-Goals section (§4)
correctly narrows this and is not itself contradicted — but research.md's own "Critical Novelty
Assessment" comparison table presents several of these as delivered capabilities without
consistently marking them "Proposed," which is a documentation-honesty gap confined to that one
document (F-49).
