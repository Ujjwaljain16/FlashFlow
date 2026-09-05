# Stage 7 — Adaptive Routing & Counterfactual Replay: Exit Artifact

## What Was Built

| Component | File(s) | Description |
|---|---|---|
| Adaptive selector | `internal/proxy/adaptive.go` | Six-signal router per `trd.md`/`prd.md` (Latency, Load, Health, Capacity, Cache, Cost); Health via upstream filtering (not scored), Capacity folded into Load as utilization, neutral (not optimistic) cold start, positive-evidence-only staleness, deterministic alphabetical tie-break |
| Adaptive unit tests | `internal/proxy/adaptive_test.go` | 13 tests: monotonicity, cold-start neutrality, staleness collapse, cache affinity, deterministic tie-break, concurrency safety |
| Counterfactual replay engine | `internal/replay/{scenario,world,policies,compare}.go` | `Scenario` (exogenous state only) + `PolicySpec`/`RunWorld` (fresh selector, trackers, and `*vtime.Engine` per call — no shared mutable state between runs); adapters for all five existing selectors |
| Replay engine unit tests | `internal/replay/world_test.go` | Identity (same Scenario+PolicySpec+seed → identical trace), divergence-only-after-intervention (causal check, not just equality), isolation (an unrelated run cannot affect another) |
| Eight experiment suites | `cmd/experiment-007a` … `007h` | Signal validation, heterogeneity, dynamic adaptation, exploration/recovery, failure/health, counterfactual identity, policy comparison, paired multi-seed study |
| Experiment documentation | `experiments/007-adaptive-replay/{hypotheses,README}.md` | H1–H8, methodology, and full results for all eight experiments |
| Learning notes | `docs/learning/007-adaptive-routing-replay.md` | Signal design rationale, per-experiment findings, surprises, limitations, evidence-tiering, Stage 8 motivation |

**No existing domain package required modification** beyond the new `internal/proxy/adaptive.go`. `internal/replay` is entirely new and self-contained, consuming the existing `proxy`, `health`, `vtime`, and `clock` packages exactly as every prior experiment's hand-written harness already did — it formalizes an existing convention rather than replacing working code.

One real design correction changed the shape of Stage 7's later work: 007-D's first attempt (an extreme 2000ms "bad" service time) failed not from a staleness bug but from an unrelated load-counter artifact, diagnosed by reading the raw trace rather than assumed; 007-G's own central prediction (only Adaptive shows a recovery-transition gap) also did not hold, and the reason — the recovering target's *true performance* never actually changed in that scenario — became a more precise finding than the one predicted going in.

---

## Signal Design

Each of the six signals was resolved against a specific piece of evidence already in this project, not designed in the abstract:

- **Health**: not scored at all — folded into the existing upstream `available := filter(allTargets, registry.IsAvailable)` pattern every selector since Stage 3 already relies on. Verified directly (007-E, then generalized to all five policies in 007-G) rather than assumed correct from a code read.
- **Capacity**: folded into Load as a utilization ratio (`load/capacity`), reusing Stage 3's WRR lesson that raw counts without a capacity denominator mislead across heterogeneous targets.
- **Cold start**: neutral (0.5), a direct, evidence-grounded contrast with EWMA's optimistic "unobserved beats observed" rule that 006-B proved causes deterministic lock-in.
- **Staleness**: discounts only data with positive evidence of being old (a recorded, aged `lastSelected` timestamp) — an early over-conservative version that treated "no record yet" as stale was caught and fixed before any test was written against it.
- **Cache**: router-maintained key-affinity (last server to serve a key), not real cache introspection — a stated, intentional simplification.
- **Tie-breaking**: candidates sorted alphabetically before scoring, so identical scores never depend on map iteration order.

---

## Synthetic Validation

007-A ran six checks against `AdaptiveSelector.Explain` on synthetic target states, independent of `adaptive_test.go`'s unit tests: load/latency/cost monotonicity (all confirmed, with an expected flat region once a signal saturates), cold-start neutrality (0.500 exactly between an observed-bad 0.020 and observed-good 0.990), staleness collapse (a stale-excellent and stale-terrible reading both collapse to exactly 0.500 — old data is treated as unknown, not half-trusted), and cache affinity specificity (1 only for the correct target/key pair). All 6 checks passed.

`internal/replay`'s own load-bearing properties were validated twice: as unit tests (`world_test.go`, on a synthetic scenario built to exercise each property) and as a recorded, auditable experiment (007-F, on 007-B's real heterogeneous scenario) — matching the same internal-correctness-vs-evidence distinction 006-A/007-A established.

---

## Experiments

| # | Title | Central Finding |
|---|---|---|
| 007-A | Adaptive Signal Validation | All 6 synthetic checks passed — the methodology gate every later experiment depends on |
| 007-B | Adaptive Routing Under Heterogeneity | Avoided a slow target better than LC/P2C (5.7% vs 14.0%/14.0%) while splitting two equally-fast targets nearly evenly (134/149), unlike EWMA's hard lock-in (274/5); matched Round Robin in a homogeneous negative case where EWMA still locked in anyway |
| 007-C | Adaptation Under Dynamic Change | Tracked the actual best target through 3 phase transitions, adapting within 2–9 requests; mechanism identified as residual EWMA tracking (losing target still received 8–20% of traffic), not staleness (1s threshold never had time to trigger in a 500ms phase) |
| 007-D | Exploration / Recovery | First attempt failed from a load-counter artifact (diagnosed, not assumed); redesigned scenario showed genuine staleness-driven rediscovery (205ms gap > 150ms StaleAfter) and gradual, EWMA-lag-gated recovery (10% share in the first 200ms after true improvement) |
| 007-E | Failure and Health | Zero selections of an unhealthy target by construction; on recovery, the same staleness mechanism resets stale data to neutral for free, restoring exact traffic parity (20/20/20) with no special-cased recovery logic |
| 007-F | Counterfactual Identity | Identity, divergence-only-after-intervention, and isolation all re-verified as a recorded experiment on 007-B's real scenario, independent of the unit tests |
| 007-G | Counterfactual Policy Comparison | Health eligibility confirmed policy-agnostic across all five policies (one traced, explained same-timestamp edge case, not a defect); the predicted Adaptive-only recovery gap did NOT hold — root-caused to the recovering target's true performance never having changed in this scenario, unlike 007-D's |
| 007-H | Paired Multi-Seed Counterfactual Study | 007-B's single-run slow-target-avoidance finding (8.3-point gap) confirmed robust across 30 paired replicates: mean diff 8.12 points, 95% CI [8.01, 8.23] entirely excluding zero, 30/30 sign-consistent |

Full data, methodology, and per-experiment interpretation: `experiments/007-adaptive-replay/README.md` (9 sections, 8 JSON result files).

---

## Routing Findings

Adaptive's combination of self-correcting utilization (Load/Capacity) and neutral cold start reproduces Least Connections/P2C's slow-target avoidance while specifically avoiding the one failure mode a single-signal greedy policy (EWMA) still exhibits even in the easiest possible scenario: locking onto one of several genuinely equal targets. This was confirmed both in a single real run (007-B) and, robustly, across 30 independently-varying replicates (007-H) — the same finding checked twice, by two different kinds of evidence.

---

## Recovery and Health Findings

Two distinct capabilities were shown to compose correctly at a boundary neither was built specifically for: health eligibility (upstream filtering, unchanged since Stage 3) keeps every policy — not just Adaptive — from ever considering an unhealthy target; staleness (Adaptive's own mechanism, validated for the unrelated case of exploration/rediscovery in 007-D) happens to also be exactly the right behavior when a target returns from a health-driven absence, because absence-from-traffic and staleness-of-data are the same underlying condition regardless of *why* a target stopped being selected. 007-G's multi-policy comparison added an important qualification 007-D and 007-E's individual scenarios couldn't reveal alone: the staleness mechanism's visible cost depends on how much the recovering target's true performance actually diverged from what its stale data implied, not merely on whether a reset occurred — a real correction to this stage's own prediction, arrived at by running the comparison rather than assuming the mechanism's existence guarantees its visibility everywhere.

---

## Counterfactual Replay Engine

`internal/replay` separates exogenous state (a `Scenario`: target service times, arrivals, failures, seed) from endogenous state (a fresh selector and trackers per `RunWorld` call), so two runs of the same Scenario+PolicySpec never share a mutable object. Three properties were demonstrated, not assumed: identity, divergence-only-after-intervention, and isolation — each verified twice, as a unit test and as a recorded experiment on real evidence. The divergence test's own first draft was a false positive worth documenting: it diverged at t=0 from an instrumentation-shape mismatch (one scenario had no health registry at all) rather than from the causal intervention under test, fixed by a `UseHealthRegistry` flag giving both scenario variants identical always-on probe machinery.

---

## Surprises

1. **007-D's first failed attempt was more informative than a clean success would have been** — an extreme "badness" parameter created a load-counter artifact unrelated to staleness, caught by reading the raw trace.
2. **007-G's central prediction was wrong, and the correction was the more valuable result** — the staleness-driven undershoot (007-D) is real but requires the recovering target's true performance to have actually changed, not just its health.
3. **`internal/replay`'s own divergence test initially failed for the wrong reason** — a reminder that a test asserting "these differ" needs the same scrutiny as one asserting "these match."
4. **007-H's 30-replicate mean (8.12 points) landed almost exactly on 007-B's single-run estimate (8.3 points)** — confirming the original single run was a reliable read of the effect, converting an assumption into a checked claim.

---

## Limitations

- All Adaptive findings use the default `AdaptiveConfig` (`StaleAfter`=1s, `ReferenceLatency`=100ms, default weights) — no sensitivity analysis across alternative configurations was performed, deliberately out of scope.
- The Cache signal is router-maintained key-affinity, not introspection into any real cache's contents — a stated simplification, not a claim about real cache behavior.
- 007-G's recovery-transition comparison used one window size (300ms/60 requests) and found it too coarse to detect an effect 007-D showed exists under a different scenario shape — a stated boundary of that specific measurement, not a claim the effect never exists.
- `internal/replay`'s divergence guarantee has been demonstrated for a health-failure intervention specifically, not separately re-checked for every other kind of exogenous change a future `Scenario` field might introduce.
- No DR-OPE, dashboard, or tuning machinery was built, per this stage's explicit scope boundary — the replay engine supports counterfactual comparison of a fixed set of policies on a fixed Scenario, not off-policy evaluation.

---

## Evidence Discipline

**Strong** (deterministic, replicated, mechanism identified): all 6 007-A signal-validation checks; health eligibility across all five policies with zero exceptions after detection (007-E, 007-G); `internal/replay`'s identity/divergence/isolation properties, verified both as unit tests and as a recorded experiment (007-F); the 007-H paired multi-seed result (95% CI entirely excluding zero, 30/30 sign-consistent). **Suggestive, precisely characterized**: 007-D's staleness-driven rediscovery and gradual recovery, demonstrated on one designed scenario, not yet replicated across seeds the way 007-H replicated the slow-target-avoidance finding. **Unresolved, explicitly flagged**: whether the recovery-transition undershoot would become visible at a finer measurement granularity than 007-G's 300ms window, on a failure-only (no true-performance-change) scenario shape.

---

## Testing

```
gofmt -l .        clean
go build ./...    clean
go vet ./...      clean
go test ./...     ok  (all 12 packages)
```

203 tests pass across the whole repository at Stage 7's close (186 at Stage 6's close + 13 in `internal/proxy/adaptive_test.go` + 4 in `internal/replay/world_test.go`). `go test -race` remains **unavailable in this environment** (no `gcc`; `CGO_ENABLED=1` fails building `runtime/cgo`) — stated honestly, as in every prior stage.

---

## Stage 8 Readiness

**READY**

> Stage 7 built a six-signal router whose every scoring decision traces to a specific, evidence-grounded design choice — none of it a black box — and a counterfactual replay engine proven, not assumed, to separate exogenous conditions from independently-evolving endogenous state. It also demonstrated both what that machinery is good for (007-G's honest correction of its own prediction, 007-H's statistically-supported robustness claim) and precisely where its current boundary sits (recovery-transition visibility depends on measurement granularity and on whether a target's true performance actually changed). Stage 8's tuning, evaluation, or dashboard work — whatever form it takes — now has a router whose behavior is understood well enough to know what "improved" would mean, and a replay engine already proven not to leak state between the counterfactuals it will be asked to compare.

---

## Stage 9 Correction (terminology, not behavior)

A post-Stage-8 audit found this exit artifact's own "six-signal" language (also present in the 007/008 learning notes and experiment READMEs) overstates what `internal/proxy/adaptive.go` actually scores. Appended here rather than editing the text above, to keep this document's own historical record intact: the router scores **four** signals (Load, Latency, Cache, Cost) — Health is a pre-filter applied to the candidate set before this selector ever runs (correctly described as such earlier in this same document's "Adaptive selector" row), and Capacity is folded into Load as a utilization ratio rather than standing alone as a fifth scored term. "Six" accurately describes the count of *tunable parameters* Stage 8's tuner searches (4 weights + `ReferenceLatency` + `StaleAfter`), which is where the terminology likely got conflated. None of Stage 7's actual findings, tests, or the replay engine's correctness are affected — this is a naming correction only. See `docs/audit/RESOLUTION.md` (F-25).
