# Experiment 008: Auto-Tuning, Experiment Operations & Final Validation

## 1. Executive Summary & Research Questions

Stage 7 built a six-signal adaptive router and a counterfactual replay engine, both individually validated and both shown to produce real, statistically-supported findings. Stage 8 asks the final engineering question this project's progression has been building toward: can FlashFlow automatically search the adaptive router's configuration space, and determine whether whatever it finds generalizes to scenarios it never saw during search — rather than merely winning the scenarios it was tuned on?

### Primary Research Question
> Can FlashFlow automatically find a useful adaptive-routing configuration without overfitting to the scenarios used during tuning?

### Scope Discipline
Per the master context's own repeated instruction, Stage 8 does not begin with a large optimization framework. The tunable parameter space is exactly `AdaptiveWeights`' four fields plus `AdaptiveConfig`'s `ReferenceLatency` and `StaleAfter` — the parameters the actual Stage 7 implementation exposes, not an invented, more-impressive-looking set. The scenario generator is bounded to exactly what `internal/replay.Scenario` can execute (target count, service-time heterogeneity, arrival timing, one optional failure/recovery window) — not the master context's full illustrative dimension list, since `internal/replay` has no cache/TTL model or network-impairment wiring to generate scenarios against. See `docs/learning/008-tuning-final-validation.md` for the full scope rationale.

---

## 2. Results: Experiment 008-A — Tuning Machinery Validation

**Hypothesis (H1)**: see `hypotheses.md`. Seven checks against `internal/tuning`'s config space, scenario generator, Development/Holdout split, and objective function, independent of `internal/tuning`'s own unit tests (`*_test.go`).

| Check | Result |
|---|---|
| Config sampling stays within bounds | 1000/1000 samples valid |
| Weight scale-invariance (empirical) | 7x-scaled weights produced byte-identical `Scores` to the unscaled default |
| Scenario generation determinism | 3 seeds, regenerated twice each, identical shape |
| Development/Holdout seed disjointness | 40 + 20 scenarios, zero shared seeds |
| Objective ordering (perfect > worst) | utility(perfect)=0.9667 > utility(worst)=0.0000 |
| Pareto frontier (known non-dominated pair) | Frontier = {0, 1}; index 2 (dominated) correctly excluded |
| `Evaluate` end-to-end determinism | Two calls on the identical config+scenario set produced identical `Metrics` and `Scores` |

All 7 checks passed. Raw data: `experiments/008-tuning-validation/results/008A-tuning-machinery-validation.json`.

### Findings

1. **The weight scale-invariance claim in `internal/tuning/space.go`'s design comment is not just a mathematical argument — it was checked empirically.** `AdaptiveSelector.SelectTarget` picks an argmax across candidates for one request, and `CombinedScore` is a weighted sum of the same four per-target scores for every candidate; scaling all four weights by a positive constant multiplies every candidate's score by that constant, which cannot change the argmax. A weight vector scaled by 7x — invalid under `ConfigSpace.Valid`'s sum-to-1 requirement, since 7x the default weights sums to 7, not 1 — produced `Scores` identical down to the last digit to the unscaled default when actually run through `Evaluate` against 10 real generated scenarios. This confirms the decision to search the 3-dimensional weight simplex rather than the unconstrained 4D non-negative space wasn't a convenience simplification; it eliminates a real, confirmed redundancy in the search space.
2. **Every other check passed on the first attempt.** Unlike several Stage 6/7 experiments (006-C's Mann-Whitney misapplication, 007-D's load-counter artifact), no design flaw was discovered while building this validation — a fair outcome to report honestly, not a reason to invent one.

### Interpretation

This experiment plays the same role for `internal/tuning` that 006-A played for `internal/statistics` and 007-A/007-F played for the adaptive router and replay engine: it is not itself a tuning result, it is the license to trust every tuning result that follows. Every piece of machinery a Random Search tuner (008-B) will depend on — sampling, scenario generation, the sacred Development/Holdout split, and the objective it optimizes — has now been checked against a known answer rather than assumed correct because the code compiled.

---

## 3. Results: Experiment 008-B — Random Search Tuner v1

**Hypothesis (H2)**: see `hypotheses.md`. 200 evaluations (optimizer seed 20260908), sampled from `DefaultConfigSpace`, each scored against the full 40-scenario Development set. The hand-chosen `proxy.DefaultAdaptiveConfig()` scored against the identical set as the baseline.

*Note: the numbers below are post-correction — see §7 for a significant objective-function flaw discovered and fixed while building 008-F, which changed these results. An earlier version of this section reported a different (misleading) winner; it is not reproduced here.*

| Metric | Baseline (hand-chosen) | Best found (Random Search) |
|---|---:|---:|
| Utility | 0.6441 | 0.6594 |
| Mean latency | 93.6ms | 78.3ms |
| p99 latency | 196.5ms | 196.5ms |
| Rejected rate | 0.000 | 0.000 |
| Mean max share | 0.658 | 0.770 |
| Weights (load/latency/cache/cost) | 0.400 / 0.400 / 0.100 / 0.100 | 0.161 / 0.568 / 0.051 / 0.220 |
| ReferenceLatency / StaleAfter | 100ms / 1s | 192.3ms / 3.74s |

Search completed in ~9 seconds (8,000 `RunWorld` calls). Raw data: `experiments/008-tuning-validation/results/008B-random-search-summary.json` (summary) and `008B-search-ledger.json` (all 200 evaluations, nothing discarded).

### Findings

1. **Prediction 1 confirmed, and mechanistically clean.** The best candidate found (utility 0.6594) improved on the hand-chosen default (0.6441) by +0.0153, a 2.37% relative gain, driven by a real mean-latency reduction (93.6ms → 78.3ms) — the search discovered that weighing Latency far more heavily than Load (0.568 vs the default's 0.4, against Load's 0.161 vs 0.4) reduces average latency substantially, at the cost of a less even split (`MeanMaxShare` rose from 0.658 to 0.770 — more traffic concentrated on the genuinely fastest target, which is the point).
2. **Prediction 2 confirmed: the ledger makes convergence directly inspectable, and it shows a real plateau.** The best-so-far value last improved at evaluation #24 of 200 and did not improve at all across the final 25% of the run. The top 5 candidates by utility cluster around a similar shape (Latency weight well above default, Load weight well below) — consistent with the search finding one real basin.
3. **Zero cache hits across 200 continuous-space draws**, as expected — Random Search over a continuous simplex essentially never resamples an exact previous point.

### Interpretation

Automated search found a real, mechanistically-explicable improvement over a hand-chosen baseline by correctly discovering that this project's Development scenarios reward weighing Latency more heavily than the hand-chosen default does. It converged well before exhausting its evaluation budget and plateaued — no evidence yet that a more sophisticated algorithm (Latin Hypercube, Bayesian Optimization) is justified. **None of this is evidence the winning configuration is actually better in general** — only that the search process works and the objective has real structure to find. The winning candidate has not been shown a single Holdout scenario yet.

---

## 4. Results: Experiment 008-C — Holdout Validation & Generalization Gap

**Hypothesis (H3)**: see `hypotheses.md`. 008-B's search reproduced (identical `OptimizerSeed`, identical Development set) to obtain the same winning candidate, then evaluated against the 20-scenario Holdout set for the first and only time.

| | Development | Holdout |
|---|---:|---:|
| Baseline utility | 0.6441 | 0.6607 |
| Winner utility | 0.6594 | 0.6782 |
| Improvement over baseline | +0.0153 | +0.0175 |

**Generalization gap** (training improvement − holdout improvement): **−0.0022**, **95% bootstrap CI [−0.0112, +0.0065] (includes zero)**

| Robustness (per-scenario utility) | Mean | Median | Worst | StdDev |
|---|---:|---:|---:|---:|
| Baseline, Holdout | 0.6853 | 0.6857 | 0.5235 | 0.0863 |
| Winner, Holdout | 0.7023 | 0.6957 | 0.5235 | 0.0899 |

Worst-case regression on Holdout (winner's worst scenario − baseline's worst scenario): **+0.0000** — the same worst-case scenario is equally hard for both; the winner doesn't make it worse.

Raw data: `experiments/008-tuning-validation/results/008C-holdout-validation.json`.

### Findings

1. **008-B's search reproduced byte-identically** (config hash `3215da2cc0dd6ad3`, identical Development utility) — a live determinism check, not just a convenience, and it held.
2. **The pooled generalization gap is negative, but its bootstrap CI includes zero.** The winner's improvement over baseline nominally *grew* on Holdout (+0.0175) relative to Development (+0.0153). An adversarial audit of this experiment caught that this headline number had never been given a confidence interval — inconsistent with every other quantitative claim in this project since Stage 6. Added: a 95% bootstrap CI on the per-scenario paired difference (dev-minus-holdout improvement, 5000 resamples, `internal/statistics.BootstrapDiffCI`, the 007-H paired-differences pattern) comes out to **[−0.0112, +0.0065]** — it includes zero. At Holdout's per-scenario utility spread (stddev ≈ 0.086–0.090), the gap's *sign* is not distinguishable from noise at this scenario-set size.
3. **Evidence tier: suggestive, not strong.** Corrected from an earlier "strong" tiering that used only the pooled point estimate. The honest read: the tuned configuration *does* improve utility on both Development and Holdout (that part is well-supported — see the per-scenario robustness table above, where the winner's Holdout mean/median both clear the baseline's), but the specific claim that improvement "grew rather than shrank" on Holdout is not something this scenario-set size can actually support statistically.
4. **Two scope notes this experiment's own record was missing, now recorded in `008C-holdout-validation.json`'s `scenario_distribution_note` and `holdout_touch_note` fields**: (a) Development and Holdout are drawn from the *identical* `ScenarioSpace` distribution, differing only by seed range — this experiment tests generalization to unseen same-distribution samples, not to a distributionally different traffic shape (008-F's hand-crafted challenges are the distribution-shift check); (b) Holdout was technically scored twice across the stage's lifetime (once under the original p99-based objective, again here under the corrected mean-latency objective) — recorded explicitly as required by master context rule 9, with the note that the objective correction was motivated entirely by Development-side evidence, never by looking at Holdout first.

### Interpretation

The tuned configuration does improve utility on both Development and Holdout — that conclusion holds. What doesn't hold, once actually tested with a confidence interval, is the stronger claim that the improvement "grew rather than shrank" (a negative generalization gap) as anything more than a point estimate whose sign this experiment cannot distinguish from zero. Report this as "no evidence of overfitting, and no statistically confirmed extra generalization bonus either" — not as "the tuned configuration generalizes even better than it trained," which is what the un-intervalled pooled number alone would suggest.

---

## 5. Results: Experiment 008-D — Sensitivity Analysis

**Hypothesis (H4)**: see `hypotheses.md`. Every one of the winning configuration's 6 tunable parameters perturbed +/-10% (weights) or +/-100ms (durations), re-evaluated on both Development and Holdout.

| Set | Baseline utility | Max \|delta\| | Mean \|delta\| | Max as % of baseline |
|---|---:|---:|---:|---:|
| Development | 0.6594 | 0.0025 | 0.0004 | 0.4% |
| Holdout | 0.6782 | 0.0015 | 0.0004 | 0.2% |

Raw data: `experiments/008-tuning-validation/results/008D-sensitivity-analysis.json`.

### Findings

1. **Robust, by a wide margin against the 10%-of-baseline fragility threshold.** The largest single perturbation on either set moved utility by well under 1% of the baseline value.
2. **`Weights.Cost` and `StaleAfter` perturbations produced exactly zero utility change in both directions**, consistent with this project's scenarios never configuring a cost map (Cost scores identically for everyone regardless of weight) and this configuration's 3.74s `StaleAfter` sitting far outside where any generated scenario's failure window could interact with it. `ReferenceLatency` and the Load/Latency weights showed the only measurable sensitivity.

### Interpretation

The search did not find a fragile knife-edge. A configuration whose neighborhood is this flat is exactly what "the search found a genuinely good region, not a lucky isolated point" should look like — reinforcing 008-C's generalization result rather than standing apart from it.

---

## 6. Results: Experiment 008-E — Adversarial Tuner Test

**Hypothesis (H5)**: see `hypotheses.md`. Config A (Cache-only, pure memorization) vs Config B (`proxy.DefaultAdaptiveConfig`) on a hand-crafted Development/Holdout pair where the fast/slow target assignment is swapped between the two sets.

| | Development | Holdout |
|---|---:|---:|
| Config A (cache-only) utility | 0.8455 | 0.6000 |
| Config B (balanced) utility | 0.8455 | 0.8420 |
| Config A mean latency | 10.0ms | 100.0ms |
| Config B mean latency | 10.0ms | 10.9ms |

(Mean and p99 coincide exactly in this scenario, since a single cache key sends 100% of traffic to one target — this result is unaffected by §7's objective-function fix.)

Raw data: `experiments/008-tuning-validation/results/008E-adversarial-tuner-test.json`.

### Findings

1. **The construction did not produce strict domination as originally planned — it produced an exact tie, and investigating why is itself the finding.** `AdaptiveSelector`'s neutral (not optimistic) cold-start means a real signal-based policy that has already found a genuinely good target has no reason to explore away from it, so it pays zero cost for being "real" rather than memorized, as long as nothing in Development ever contradicts the memorized answer. Development performance alone provides **literally zero signal** to distinguish a policy that is actually adapting from one that got lucky once and never adapted again.
2. **On Holdout, the tie breaks catastrophically.** Config A, blind to load and latency, keeps routing 100% of traffic to the now-overloaded target it happened to lock onto (utility collapses to 0.6000, latency balloons 10x to 100ms); Config B's real load signal detects the growing in-flight load within the first couple of requests and corrects away (utility 0.8420, latency barely moves to 10.9ms) — a 0.242-point swing that Development gave no warning of whatsoever.
3. **The pipeline's Development-then-Holdout ordering would catch this immediately**, and on the *harder* version of the test: a naive "prefer whoever wins Development" rule has no basis to prefer either config here, exactly the scenario where an implementation detail (evaluation order, hash comparison, floating-point noise) could silently select the one that cannot generalize. Only checking Holdout reveals the difference.

### Interpretation

This is a stronger validation of the methodology than the originally-planned "A dominates training" framing would have been. It demonstrates that FlashFlow's holdout-validation step is necessary even in the case where Development results give *no reason for suspicion at all* — ties are not safe, and the only way to know a configuration generalizes is to actually check scenarios it never saw. Rewriting the test's success criterion to match the actual result, rather than re-engineering the scenario until it matched a predetermined narrative, follows the same practice established in 006-B, 007-D, and 007-G: investigate an unexpected result mechanistically before deciding whether the original hypothesis or the construction was wrong.

---

## 7. A Discovered Objective-Function Flaw: p99 vs. Mean Latency

While building 008-F (the final comparison against RR, WRR, Least Connections, EWMA, and P2C), the tuned Adaptive configuration came back **losing to Round Robin on 90% of Development scenarios** — a result flatly contradicting every finding since Stage 3, all of which showed heterogeneity-aware policies beating Round Robin under real service-time variation. Rather than accept the number or quietly redesign the comparison, the discrepancy was investigated directly, per this project's standing practice.

**Diagnosis.** `LatencyScore` was originally built from `P99LatencyMs` — a reasonable-seeming choice, since Stage 6 made tail latency this project's usual latency concern. On this project's randomly generated scenarios (300 requests each), p99 sits at roughly the 3rd-worst sample. As long as ANY policy sends even a handful of requests to a scenario's single worst target — true of literally every policy tested, including Adaptive, via cold-start exploration alone — p99 lands on that target's raw service time, regardless of how much *other* traffic that policy correctly steered away from it. Direct evidence: in one generated Development scenario, Adaptive's median latency was **52ms** against Round Robin's **130ms** — a real, large, mechanistically obvious routing-quality difference — while both policies' **p99 was identical at 166.5ms**. With `LatencyScore` (weight 0.6) contributing almost no discriminating signal, the 0.1-weighted `FairnessScore` ended up dominating the ranking instead — and fairness (an even split) is not what a heterogeneity-aware router should be judged on, since successfully avoiding a bad target necessarily makes the split *less* even. Round Robin "winning" was an artifact of the metric, not a real finding.

**Fix.** Since `internal/replay`'s engine has no queueing model — a request's latency is exactly its dispatched target's fixed service time, with no contention-based extra delay — mean latency across a scenario's completions is mathematically exactly the share-weighted average of each target's service time: precisely the quantity routing quality should be judged on, with none of a percentile's small-sample discreteness blind spot at this request count. `ComputeScores` now builds `LatencyScore` from `MeanLatencyMs`; `P50LatencyMs`/`P99LatencyMs` remain in `Metrics` as informational tail-latency data, just no longer the quantity the objective optimizes.

**Consequence.** 008-B through 008-E were all re-run under the corrected objective (the numbers in §3-§6 above already reflect this); the winning configuration changed (a new search optimum emphasizing Latency over Load), but every qualitative conclusion — real Development improvement, negative generalization gap, a robust (non-fragile) optimum, and the adversarial test's tie-then-collapse pattern — held under the corrected objective, in some cases (008-B's improvement, from 1.22% to 2.37% relative) more strongly than before.

This is the same class of mistake as 006-C's Mann-Whitney misapplication: a familiar, well-regarded statistic (p99 for latency, rank-sum tests for distributional comparison) applied to a question it doesn't actually answer at the sample size and comparison being made. The fix follows the same discipline: diagnose the mechanism with direct evidence (not a guess), correct the tool, and re-verify every downstream conclusion rather than patching the one number that looked wrong.

---

## 8. Results: Experiment 008-F — Final Policy Evaluation

**Hypothesis**: is the tuned Adaptive configuration actually better than Round Robin, Weighted Round Robin (given the most favorable case possible — perfect per-scenario profiling), Least Connections, EWMA, and P2C-load — across Development, Holdout, and 3 hand-crafted challenge scenarios (identical targets, an extreme 20x capacity ratio, and a health failure/recovery)?

| Policy | Dev Utility | Dev Win% | Dev NonInf% | Holdout Utility | Holdout Win% | Holdout NonInf% |
|---|---:|---:|---:|---:|---:|---:|
| Round Robin | 0.6412 | 35.0% | 42.5% | 0.6459 | 10.0% | 30.0% |
| Weighted Round Robin | 0.6532 | 2.5% | 57.5% | 0.6656 | 5.0% | 35.0% |
| Least Connections | 0.6526 | 0.0% | 52.5% | 0.6631 | 5.0% | 40.0% |
| EWMA | 0.6427 | 0.0% | 20.0% | 0.6607 | 5.0% | 25.0% |
| P2C-load | 0.6497 | 0.0% | 55.0% | 0.6587 | 5.0% | 35.0% |
| **Adaptive (tuned)** | **0.6594** | **62.5%** | **80.0%** | **0.6782** | **70.0%** | **85.0%** |

Score-component breakdown on Development (higher is better; Latency weighted 0.6, Reject 0.3, Fairness 0.1):

| Policy | LatencyScore | RejectScore | FairnessScore |
|---|---:|---:|---:|
| Round Robin | 0.4675 | 1.0000 | 0.6075 |
| Weighted Round Robin | 0.5123 | 1.0000 | 0.4581 |
| Least Connections | 0.5087 | 1.0000 | 0.4738 |
| EWMA | 0.5310 | 1.0000 | 0.2406 |
| P2C-load | 0.4976 | 1.0000 | 0.5112 |
| **Adaptive (tuned)** | **0.5607** | **1.0000** | **0.2296** |

| Challenge scenario | Round Robin | WRR | Least Conn. | EWMA | P2C-load | Adaptive |
|---|---:|---:|---:|---:|---:|---:|
| Identical targets | 0.8667 | 0.8667 | 0.8600 | 0.8033 | 0.8650 | 0.8663 |
| Extreme 20x capacity ratio | 0.7128 | 0.8753 | 0.8574 | 0.7560 | 0.8459 | **0.8923** |
| Failure/recovery | 0.7446 | 0.7902 | 0.7844 | 0.7864 | 0.7813 | **0.8237** |

Health eligibility held with zero exceptions for all six policies on the failure-recovery challenge. Raw data: `experiments/008-tuning-validation/results/008F-final-policy-evaluation.json`.

### Findings

1. **Adaptive has the clear highest LatencyScore of all six policies (0.5607 vs. the next-best EWMA's 0.5310 and Round Robin's 0.4675)**, and wins 62.5% of Development scenarios and 70% of Holdout scenarios outright — not universal dominance, exactly the honest outcome master context rule 43 says must be allowed. It is non-inferior (within 1% of the best) in 80-85% of scenarios on both sets.
2. **Adaptive's tuned FairnessScore (0.2296) is the *lowest* of all six policies — lower even than EWMA's long-established lock-in problem (0.2406).** This is an honest, informative tradeoff the tuner made explicitly: 008-B's search found that weighing Latency far above Load (0.568 vs 0.161) improves average latency substantially, at the cost of concentrating traffic on the best target even more aggressively than EWMA's notorious lock-in does. Whether that tradeoff is desirable depends on whether fairness matters for a given deployment — exactly the multi-objective judgment call master context rule 8 says must be stated explicitly, not hidden inside one blended number.
3. **On the extreme-capacity-ratio and failure-recovery challenges, Adaptive wins outright.** WRR — given the single most favorable case it can be given (perfect per-scenario profiling) — comes close on the capacity-ratio case (0.8753 vs Adaptive's 0.8923) but has no mechanism to react to the failure-recovery case's dynamics, landing behind every load-aware policy there.
4. **On the identical-targets challenge, all six policies land within 0.06 of each other** (0.80-0.87), confirming 007-B/007-H's earlier finding one more time: Adaptive doesn't manufacture an advantage where none exists.

### Interpretation

Round Robin's strong showing here (winning 35% of Development scenarios — second only to Adaptive on outright wins, though middle-of-the-pack on non-inferiority at 42.5%) is not a contradiction of Adaptive's design — it's exactly what master context rule 43 calls a mature outcome: RR is genuinely competitive on scenarios where heterogeneity is mild enough that avoiding a slightly-worse target isn't worth the complexity, and this evaluation says so plainly rather than forcing Adaptive to appear universally superior. What §7's correction made visible is the *actual* mechanism separating these policies — mean latency, not tail latency or fairness — and once that mechanism is measured correctly, Adaptive's real advantage (better routing quality, at a stated, explicit cost to fairness) is exactly where Stage 3 through Stage 7's evidence always said it should be.

---

## 9. The Permanent Challenge Suite (`internal/challenge`)

Master context rules 38-39 and 63 ask for a permanent, reproducible regression suite: a fixed golden scenario with known invariants, plus adversarial cases across routing, health, cache, network, replay, and tuning specifically built to break the system, re-runnable on every future change. This is built as an ordinary Go test package — `go test ./...` (already this project's standing quality gate) already runs it, needing no new tooling.

Before writing new tests, a survey of existing coverage found the cache-hit-never-reaches-upstream invariant (rule 38's own named example) **already permanently tested** at the real-engine level (`internal/topology`'s `TestEdgeServer_Cache_MissThenHit`), so it was not duplicated. `internal/challenge` fills the genuine gaps: 23 new tests across 6 files.

- **Golden scenario** (`golden.go`/`golden_test.go`): one fixed-manifest, fixed-seed scenario (3 heterogeneous targets, one mid-run health degradation, hot/cold key rotation) with 3 structural invariants checked, not approximate benchmark numbers — no unhealthy target ever selected, the trace is deterministic across reruns, and counterfactual worlds evaluated against it remain isolated.
- **Routing** (`routing_test.go`): identical targets (confirming RR/LC/P2C/Adaptive split fairly while anchoring EWMA's well-established lock-in as an expected, not-a-bug regression marker) and extreme capacity ratios (1000:1 for WRR, 50x service-time for Adaptive).
- **Health** (`health_test.go`): flapping below and at the exact threshold, a RECOVERING target's real (and non-obvious) resilience to a single fail, simultaneous independent multi-target failure, and DEGRADED→UNHEALTHY interaction.
- **Cache** (`cache_test.go`): hot key, a cold-cache burst of 2,000 never-seen keys, synchronized expiry of 500 keys at an identical TTL boundary, and correctness (not performance) at a 10,000-key working set.
- **Network** (`network_test.go`): a link recovering from total loss to clean, and jitter+loss applied together (not just independently, as existing `internal/netsim` tests already cover).
- **Replay** (`replay_test.go`): divergence at time zero, deterministic tie-breaking for simultaneous-timestamp events, and confirming an early intervention produces a materially different *final* metric, not just a different trace.
- **Tuning** (`tuning_test.go`): a genuinely flat objective (a single-target scenario set where no weight combination can matter), a search space collapsed to its own boundary, and an aggressively noisy scenario generator.

### Two Genuine Discoveries While Building It

Consistent with this project's practice throughout, two of the replay challenge tests failed on their first attempt for real mechanistic reasons, investigated rather than patched around:

1. **A load-counter confound identical to 007-D's.** An early version of the "stateful consequences" test used 5ms arrival spacing against a 10ms service time; both the intervened and non-intervened scenarios converged to an identical 50/50 split regardless of the health intervention. The cause: overlapping in-flight requests (spacing shorter than service time) made both targets' own utilization scores oscillate independently of cache affinity or the intervention, washing out the signal the test existed to isolate. Fixed by spacing arrivals safely past each request's own completion.
2. **A detection-latency mismatch.** The same test's health intervention (a 50ms outage) had no effect at all in a second attempt — not a filtering bug, but because `health.Config`'s default `UnhealthyFailThreshold=2` requires two consecutive probe failures 100ms apart before a target is actually marked unavailable, and the outage recovered before the second probe could ever fire. Fixed by extending the outage past the ~200ms minimum real detection time.

Neither discovery changed any conclusion from earlier experiments — both were artifacts of the new tests' own parameter choices — but both are now permanent, documented regression anchors, and both are a live demonstration of why this project insists on investigating a surprising result before adjusting a number to make it go away.

---

## 10. Results: Experiment 008-G — Final Performance Benchmarks & Open-Loop Load Sweep

**Hypothesis (H7)**: see `hypotheses.md`. Go `testing.B` benchmarks for routing decision cost, virtual-engine event throughput, replay-pipeline throughput, and tuner evaluation rate; a new open-loop HTTP load generator swept against 006-D/006-E's exact real bottleneck.

### Micro-benchmarks

| Component | Metric | Result |
|---|---|---|
| Virtual engine (`internal/vtime`) | Raw event throughput | ~3.05M events/sec |
| Virtual engine | Arrival+completion pattern (2 events/request) | ~2.17M events/sec |
| `RunWorld` pipeline (`internal/replay`), Adaptive | Virtual requests/sec | ~277K/sec |
| `RunWorld` pipeline, Round Robin | Virtual requests/sec | ~358K/sec |
| Random Search (`internal/tuning`) | Evaluations/sec (20-scenario set) | ~36.6/sec |

Routing decision cost (`internal/proxy`, ns/op, per `SelectTarget` call):

| Selector | Cost |
|---|---:|
| Round Robin | 4.4ns |
| P2C-load | 49.3ns |
| Least Connections | 49.3ns |
| Weighted Round Robin | 57.0ns |
| EWMA | 74.0ns |
| Adaptive | 259.0ns |

Raw benchmark output is reproducible via `go test ./... -bench=. -benchmem -run=^$` (no results JSON — Go's own benchmark output is the artifact, per this project's "reuse existing tooling" preference for anything `go test` can already report).

### Open-Loop Load Sweep

**Setup**: 006-D/006-E's exact real bottleneck — a real, in-process `internal/topology.OriginServer` with a fixed 20ms delay, fronted by a client `transport.TrackedTransport` with `MaxConnsPerHost=5` — analytical ceiling 250 req/s (capacity ÷ service time). Swept offered rate from 20 to 600 req/s, 2 seconds per level.

| Offered (req/s) | p50 (ms) | p95 (ms) | p99 (ms) | Peak goroutines |
|---:|---:|---:|---:|---:|
| 20 | 20.8 | 21.6 | 22.3 | 49 |
| 150 | 20.6 | 21.6 | 22.3 | 324 |
| 240 | 20.9 | 22.8 | 23.3 | 515 |
| **250** | **54.6** | **92.1** | **97.5** | 534 |
| 300 | 263.7 | 489.0 | 509.8 | 635 |
| 450 | 885.7 | 1675.9 | 1746.6 | 1294 |
| 600 | 1495.7 | 2846.2 | 2965.4 | 2183 |

**Observed knee: 250 req/s — exactly matching the 250 req/s analytical prediction.** Full sweep data: `experiments/008-tuning-validation/results/008G-open-loop-load-sweep.json`.

### Two Corrections Made Before Trusting This Result

1. **The generator's own dispatch loop was the bottleneck, not Origin.** A first version used a single `time.Ticker` consumed by one dispatch loop. Above ~200 req/s, the *measured dispatch rate* itself started falling short of the intended offered rate (e.g. 177/s achieved at 200/s intended) — and completed throughput tracked the *achieved* dispatch rate almost exactly, meaning the apparent "saturation" was the generator confounding itself. Diagnosis: `time.Ticker` silently drops ticks when its receiver falls behind, and as offered rate (and therefore in-flight goroutine count) grew, the single dispatch loop occasionally took longer than one tick interval to loop back around. Fixed by launching every request's goroutine up front, each sleeping to its own precise absolute target time — no shared channel to fall behind on. After the fix, achieved dispatch rate matched intended offered rate to within 0.5% at every level up to 600 req/s.
2. **The planned knee-detection metric (throughput/offered ratio) never triggered.** With a 5-second per-request timeout generous enough that every dispatched request eventually completes even under severe queueing, completed count tracks offered load almost exactly at *every* level — nothing is ever dropped for that ratio to detect. Latency inflection is what actually carries the saturation signal in this design, and the data shows it unambiguously: p99 stays within 1ms of Origin's own 20ms baseline below the ceiling, then grows to 4x, 15x, and finally over 130x baseline as offered load climbs to 600 req/s. The knee-detection metric was changed to "first offered rate where p99 exceeds 2x baseline" to match what the data actually shows, rather than keeping a metric that silently reported "no knee found."

### Interpretation

This closes the gap 006-D and 006-E's closed-loop design left open (master context rule 52): both experiments already confirmed this exact bottleneck's ceiling analytically and via Little's Law, but a closed-loop generator — a fixed worker pool, each worker blocking on the previous response before sending the next — cannot by construction let offered load exceed completed capacity; the two are locked together at exactly `concurrency` requests in flight at all times. An open-loop generator can, and this sweep shows precisely what that divergence looks like: flat latency and 1:1 throughput tracking below the ceiling, then a sharp, precisely-located latency knee starting exactly at the predicted 250 req/s. Both corrections made along the way are worth keeping in the record for the same reason every prior mid-experiment correction in this project has been: the first version of a load generator is itself part of the system under test, and its own limitations can produce a plausible-looking but wrong result if not checked against what should mechanistically be happening.

---

## 11. Results: Experiment 008-H — Minimal NGINX Reference Benchmark

**Hypothesis (H8)**: see `hypotheses.md`. FlashFlow's real `cmd/proxy` versus NGINX (Docker), both fronting the byte-for-byte identical `cmd/http-origin` (20ms artificial delay), at light load (200 requests, concurrency 10, plus a 20-request discarded warm-up).

| | FlashFlow proxy | NGINX |
|---|---:|---:|
| Throughput (req/s) | 473.4 | 378.6 |
| Mean (ms) | 21.08 | 25.42 |
| p50 (ms) | 21.12 | 24.16 |
| p95 (ms) | 21.78 | 31.74 |
| p99 (ms) | 22.17 | 33.06 |
| Errors | 0 | 0 |

Raw data: `experiments/008-tuning-validation/results/008H-*.json`.

### Three Issues Caught Before Trusting This Result

A first run reported NGINX at ~2.16ms mean latency — *faster than Origin's own 20ms artificial delay could possibly allow if genuinely proxied through*. That impossibility was the tell, investigated rather than reported:

1. **NGINX was silently serving its own default welcome page, never reaching Origin at all.** Root cause: Git Bash on Windows auto-converts Unix-style paths in shell commands to Windows paths, including the *container-side* path in a Docker volume mount argument (`-v host:/etc/nginx/nginx.conf`) — producing a nonsense mount that silently failed, leaving NGINX's built-in default config (and its default HTML page) in place. The response was a completely valid HTTP 200 with the wrong content — a class of bug no status-code or error-count check can catch, only content verification. Fixed with `MSYS_NO_PATHCONV=1` on the affected `docker run` command, and a permanent readiness check added to the script that verifies both endpoints' response bodies actually contain Origin's real payload before benchmarking either one.
2. **A stray background process from manual debugging held port 8000 across script runs**, causing a second run's own Origin instance to fail to start while an orphaned one from an earlier session kept serving requests — the results were still valid (a real Origin was reached), but the provenance was muddled. No code fix; a operational lesson recorded here for future runs of this script.
3. **An adversarial audit of this benchmark caught that "identical configuration" wasn't quite true.** `cmd/proxy` was run with its default `-debug-headers=true`, adding `X-Request-ID`/`X-Selected-Edge`/`X-Upstream-Latency` response headers (and the small per-request cost of computing them) that NGINX's plain passthrough never adds — so response bytes were not actually identical between the two systems, contrary to what this section originally claimed. The same audit noted the benchmark client had no explicit warm-up phase and relied on Go's `http.DefaultTransport` default of `MaxIdleConnsPerHost=2`, which at concurrency 10 forces most requests onto a freshly-dialed connection rather than a reused one — symmetric across both systems (the same client benchmarks both), so it never biased the comparison, but it also meant neither number reflected steady-state connection reuse. Fixed: the script now passes `-debug-headers=false` to `cmd/proxy`, and `cmd/experiment-008h` now takes a `-warmup` flag (20 discarded requests by default) and raises `MaxIdleConnsPerHost` to match `-concurrency`. The table above is the rerun under these fixes.

### Interpretation

At this light load, both systems' latency sits close to Origin's own 20ms baseline; FlashFlow's proxy measured modestly faster in this run, but this is a single, light-load, single-run reference point — not a statistically powered comparison (unlike this project's other findings, no repeated-replicate confidence interval was computed here, deliberately, since that level of rigor isn't the point of a reference check) and not a claim that FlashFlow's real engine is "better than" NGINX in any general sense. NGINX is a mature, battle-tested reverse proxy serving production traffic at a scale this project has never attempted to operate at; FlashFlow's research capabilities — deterministic virtual execution, counterfactual replay, adaptive routing, statistical analysis, automatic tuning — are entirely outside what this narrow proxy-overhead comparison measures. The more interesting result here may be methodological: a benchmark's own setup can silently break in a way that still returns valid-looking, successful numbers, and the only defense is checking whether a result is mechanistically *possible* before trusting it (bugs 1-2), and, once it runs cleanly, whether "identical configuration" was actually verified rather than assumed (bug 3, caught only by a later adversarial audit) — the same discipline this project has applied to its own routing, health, and tuning findings, now applied to a reference comparison against an external system too.

---

## 12. Deliverables Beyond Experiments

Two Stage 8 deliverables are tools, not experiments, and so don't carry their own hypothesis or JSON result — they're described here for completeness and covered fully in `docs/StageArtifacts/Stage8.md`:

- **`scripts/final-validation.sh`** — the one command (master context rule 36) establishing release readiness: formatting, static checks, all tests, deterministic replay tests, statistical validation (006-A, 007-A, 007-F, 008-A, 008-E), the challenge suite, the core benchmark suite, and (in full mode) informational reruns of 008-B/C/D/F/G. Prints a final validation matrix; exits non-zero if any gate fails.
- **`cmd/dashboard`** — the operator interface (rules 29-35): a Go HTTP server, embedded static frontend (plain JS, no build step, no framework — this project has no Node toolchain and introducing one for a single dashboard wasn't earned). Three views: a **Playground** that runs a canonical scenario against any policy live (via the real `internal/replay.RunWorld`, nothing canned) with a topology visualization and filterable event timeline, plus a counterfactual comparison surfacing the first point of divergence between two policies; an **Experiment browser** reading `experiments/*/results/*.json` directly (never a second data store, per rule 34); and a **Tuning view** showing the search ledger's best-so-far curve and Development-vs-Holdout utility with Holdout kept visually distinct (rule 32). Backed by `internal/dashboard`, unit-tested including path-traversal rejection for the two HTTP-reachable file-browsing endpoints. Verified interactively in a real browser session, not just via unit tests: Playground Run/Compare, the Experiment browser's three-level drill-down, and the Tuning view all confirmed working against this project's real artifacts.
