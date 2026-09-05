# Experiment 007: Adaptive Routing & Counterfactual Replay

## 1. Executive Summary & Research Questions

Stages 3–6 established, in order: that no single routing signal is universally sufficient (Stage 3), that edge-local state changes system behavior in ways routing alone can't see (Stage 4), that FlashFlow can execute any of this deterministically (Stage 5), and that its findings can be statistically characterized and mechanistically attributed rather than merely observed (Stage 6). Stage 7 asks whether that accumulated evidence can now support two new capabilities: a routing policy that combines multiple signals explicitly, and a deterministic mechanism for asking what would have happened under a different policy given the exact same external conditions.

### Primary Research Question
> Can FlashFlow make routing decisions using multiple signals, and can we use deterministic replay to evaluate alternative policies on the same underlying scenario?

### Signal Provenance
The six signals — Latency, Load, Health, Capacity, Cache, Cost — are not this experiment's invention; they're specified in `trd.md` §4 and `prd.md` §8.2. What this stage had to determine was *how* each becomes a concrete, observable, correctly-directioned score component — see `docs/learning/007-adaptive-routing-replay.md` for the full evidence table connecting each signal to the specific Stage 3–6 finding that justifies it.

---

## 2. Results: Experiment 007-A — Adaptive Signal Validation

**Hypothesis (H1)**: see `hypotheses.md`. Six checks against `AdaptiveSelector.Explain`, using synthetic target states constructed directly — no real HTTP, no virtual engine — independent of `internal/proxy/adaptive_test.go`'s unit tests.

| Check | Result |
|---|---|
| Load monotonicity (levels 0→20) | Scores 0.70 → 0.30 → 0.30 (flat once at capacity), never increasing |
| Latency monotonicity (1ms→2s) | Scores 0.896 → 0.519, strictly decreasing |
| Cost monotonicity (1→20) | Scores 0.70 → 0.60 (flat once past the configured range), never increasing |
| Cold-start neutrality | bad=0.020, **cold=0.500**, good=0.990 — cold sits exactly at the midpoint |
| Staleness collapse | Both a stale-but-excellent and a stale-but-terrible estimate score exactly 0.500 |
| Cache affinity specificity | 1 only for the correct (target, key) pair; 0 for every other combination |

All 6 checks passed. Raw data: `experiments/007-adaptive-replay/results/007A-adaptive-signal-validation.json`.

### Findings

1. **All three monotonicity checks confirmed, with an informative flat region.** Load and Cost scores plateau once a target is at/beyond capacity or at/beyond the configured cost range — expected and correct (the `min(1, utilization)` clamp and min-max normalization both saturate by design), not a sign the signal stopped responding.
2. **Cold-start neutrality confirmed exactly at the midpoint (0.500), strictly between a bad (0.020) and a good (0.990) observed target.** This is the direct, load-bearing contrast with `EWMASelector`'s "unobserved beats observed" rule — Experiment 006-B proved that rule causes deterministic winner-take-all lock-in among literally identical targets; Adaptive's neutral cold start means a new target competes on its other signals rather than automatically winning or losing.
3. **Staleness confirmed to collapse fully to neutral regardless of the untrusted estimate's quality** — a stale "excellent" reading (1ms) and a stale "terrible" reading (5s) both score exactly 0.500 once past the staleness threshold, proving old information isn't half-trusted or partially discounted, it's treated as unknown.
4. **Cache affinity confirmed specific to the exact (target, key) pair** — no cross-contamination between targets or between keys.

### Interpretation

This experiment doesn't produce a routing finding — like 006-A, it produces the license to trust every routing decision `AdaptiveSelector` makes in the experiments that follow. Every check here targets a specific design decision documented in `internal/proxy/adaptive.go`'s own comments, verified independently rather than assumed correct because the code compiled.

---

## 3. Results: Experiment 007-B — Adaptive Routing Under Heterogeneity

**Hypothesis (H2)**: see `hypotheses.md`. 005-E's exact heterogeneous scenario, plus a homogeneous negative-case scenario, all five policies run against both.

### Heterogeneous (1 slow=100ms, 2 fast=20ms)

| Policy | slow | fast-B | fast-C |
|---|---:|---:|---:|
| Round Robin | 100 | 100 | 100 |
| Least Connections | 42 | 139 | 119 |
| EWMA | 21 | **274** | **5** |
| P2C (load) | 42 | 130 | 128 |
| **Adaptive** | **17** | 134 | 149 |

### Homogeneous (3 equal targets, 20ms each) — the negative case

| Policy | x | y | z |
|---|---:|---:|---:|
| Round Robin | 100 | 100 | 100 |
| Least Connections | 120 | 120 | 60 |
| EWMA | **290** | **5** | **5** |
| P2C (load) | 108 | 93 | 99 |
| **Adaptive** | 101 | 100 | 99 |

Raw data: `experiments/007-adaptive-replay/results/007B-adaptive-heterogeneity.json`.

### Findings

1. **Prediction 1 confirmed cleanly, on both halves of the claim.** Adaptive sent only 5.7% of heterogeneous traffic to the slow target — better than Least Connections (14.0%) and P2C (14.0%), competitive with EWMA (7.0%). But unlike EWMA, Adaptive split the two *equally fast* targets almost evenly (134 vs 149, ~47%/53%) rather than locking onto one of them (EWMA: 274 vs 5, a 98%/2% split) — the exact 006-B failure mode this design was built to avoid, confirmed absent under real routing conditions, not just in isolated signal checks.
2. **Prediction 2 confirmed strikingly.** In the homogeneous scenario, Adaptive's distribution (101/100/99) is nearly indistinguishable from Round Robin's exact even split (100/100/100) — while EWMA *still* locked in hard (290/5/5) even with zero heterogeneity to justify any preference at all. Adaptive neither manufactured an advantage nor introduced a pathology where none was warranted.

### Interpretation

This is exactly the shape of result item 76 says is legitimate and item 41 says is necessary: not "Adaptive wins everywhere," but "Adaptive matched Round Robin's fairness where no signal mattered, and specifically avoided the one failure mode (equal-target lock-in) that a single-signal greedy policy still exhibits even in the easiest possible scenario." The mechanism is traceable directly to two design decisions from `internal/proxy/adaptive.go`: utilization (load/capacity) is self-correcting the same way Least Connections' load signal is, and neutral cold start prevents the "first mover wins forever" dynamic that gives EWMA's tie-break rule its bite. Neither decision was tuned to produce this result — both were made before this experiment ran, motivated directly by Stage 3 and Stage 6 evidence, and predicted this outcome in `hypotheses.md` before the experiment was executed.

---

## 4. Results: Experiment 007-C — Adaptation Under Dynamic Change

**Hypothesis (H3)**: see `hypotheses.md`. 2 targets, 3 phases of 500ms (A best → B best → A best again), default `AdaptiveConfig` (`StaleAfter`=1s, longer than a phase).

| Phase | Best | Distribution |
|---|---|---|
| 0 (0–500ms) | A | A=92, B=8 |
| 1 (500ms–1s) | B | A=20, **B=80** |
| 2 (1s–1.5s) | A | **A=86**, B=14 |

| Transition | First switch | Stabilized (5 consecutive) |
|---|---|---|
| Phase 0→1 (new best B) | request #9 (t=545ms, 45ms into phase) | request #19 (t=595ms, 95ms in) |
| Phase 1→2 (new best A) | request #2 (t=1010ms, 10ms into phase) | request #12 (t=1060ms, 60ms in) |

Raw data: `experiments/007-adaptive-replay/results/007C-dynamic-adaptation.json`.

### Findings

1. **Prediction 1 confirmed in all three phases**, including the recovery phase — the distribution flips to favor whichever target is actually best, every time, with no degradation on the "recovery" transition compared to the first one.
2. **Prediction 2 confirmed, and the actual mechanism is identifiable rather than assumed**: adaptation happened within 2–9 requests (10–45ms) of each transition — far faster than the 1s staleness threshold could possibly explain, since that threshold never had time to trigger within a 500ms phase. The real mechanism, visible directly in the data: the "losing" target in each phase still received a meaningful minority of traffic (8–20%) throughout, meaning it stayed in active rotation and its `LatencyTracker` estimate kept receiving fresh observations the entire time — the EWMA smoothing itself tracked the environment change, with no staleness-driven reset required at all.

### Interpretation

This result usefully separates two mechanisms this design provides and shows which one is doing the work here: staleness-driven neutral reset (for a target that stops being selected almost entirely) versus ordinary EWMA tracking (for a target that keeps receiving occasional traffic even while "losing"). Because Adaptive's utilization signal is self-correcting and its cold start is neutral rather than winner-take-all, the losing target in this scenario never gets pushed all the way to zero selections the way EWMA locked a target to near-zero in 007-B's homogeneous case — and that residual minority traffic is precisely what lets the latency estimate stay current without needing the staleness mechanism at all. Experiment 007-D is deliberately designed to isolate the *other* mechanism — genuine staleness-driven rediscovery of a target that receives no traffic for an extended stretch — which this experiment's phase lengths were too short to exercise.

---

## 5. Results: Experiment 007-D — Exploration / Recovery

**Hypothesis (H4)**: see `hypotheses.md`. Target A: moderately bad (200ms) until t=1500ms, then excellent (10ms); target B: consistently good (20ms) throughout. `StaleAfter`=150ms, deliberately shorter than A's own 200ms service time. 500 requests at 5ms spacing.

### A first attempt failed instructively

An earlier version made A extremely bad (2000ms) rather than moderately bad, and the experiment's own correctness check (`log.Fatal` on no evidence of rediscovery) correctly caught a failure: A was selected exactly once (the initial deterministic tie-break) and never again. Reading the raw selection trace showed this wasn't a staleness failure — it was a load-counter artifact. A's single 2000ms in-flight request pinned its `Load` counter at 1 for virtually the whole run (its completion event doesn't fire until t=2000ms), which, combined with the default capacity of 1, maxed out its utilization penalty to the worst possible value independent of any staleness effect — while its latency simultaneously stayed "cold" (0.5, unobserved) since no completion ever landed. A's combined score was permanently below B's for a reason that had nothing to do with the mechanism under test. Redesigning A's bad phase to a moderate, self-clearing 200ms (instead of a pathological 2000ms) removed the confound.

### Results with the redesigned scenario

| Metric | Value |
|---|---|
| A selected before t=1500ms (while terrible) | 8 |
| A selected after t=1500ms (while excellent) | 110 |
| Largest gap between consecutive A-selections before improvement | 205ms (StaleAfter=150ms) |
| Evidence of staleness-driven rediscovery | **true** |
| A's share of traffic in the 200ms window right after improvement | 10.0% (4/40) |

Raw data: `experiments/007-adaptive-replay/results/007D-exploration-recovery.json`.

### Findings

1. **Prediction 1 confirmed.** A — despite being 10x worse than B and therefore losing every direct comparison while trusted — was still selected 8 times before its true improvement, with the largest gap between consecutive A-selections (205ms) exceeding the 150ms `StaleAfter` threshold. That gap size is the direct signature of the mechanism: A wasn't picked because it was competitive, it was picked because its stale data had reset to neutral, giving it a fair chance against B's own (occasionally weak) utilization moments.
2. **Prediction 2 confirmed, with an honest and informative wrinkle.** A's share of traffic did rise after its true improvement, but only to 10.0% in the first 200ms — far short of what a naive "instant rediscovery" story would predict. The reason, found by inspecting the mechanism rather than assuming the number was wrong: a staleness reset clears a target's *score* to neutral, but does not erase its `LatencyTracker`'s EWMA-smoothed *estimate*. A's tracked latency still reflected ~200ms from its pre-improvement observations immediately after t=1500ms, and only pulled toward the new ~10ms truth gradually, one fresh observation at a time — the same smoothing-lag mechanism 007-C identified for a "losing" target that stays in rotation.

### Interpretation

Two distinct mechanisms are visible here in sequence, and this experiment is the first to cleanly separate them: staleness-driven neutral reset is what gets a locked-out target re-observed at all, and ordinary EWMA smoothing is what gates how fast that re-observation turns into sustained preference. Rediscovery is real — the router did not need any explicit forced-exploration algorithm to find A again — but it is not instantaneous, and treating "not instantaneous" as a defect would have been the wrong conclusion. As with every experiment in this stage, the failed first attempt was worth keeping in the record: it is a genuine finding about how utilization and cold-start latency interact under an extreme, unrealistic parameter choice, not a mistake to quietly erase once the second attempt worked.

---

## 7. Results: Experiment 007-F — Counterfactual Identity

**Hypothesis (H6)**: see `hypotheses.md`. 007-B's exact heterogeneous scenario (1 slow=100ms, 2 fast=20ms, 300 requests), run through `AdaptivePolicy` via `internal/replay.RunWorld` -- the auditable, recorded counterpart to `internal/replay/world_test.go`'s unit tests, on a real, previously-studied scenario rather than a synthetic fixture.

| Property | Result |
|---|---|
| Identity (2 runs, same Scenario+PolicySpec) | 600-event traces, byte-for-byte identical: **true** |
| Divergence (failure introduced at t=150ms) | Trace diverged at t=200ms, at/after the cutoff: **true** |
| Isolation (unrelated run interleaved) | Outcome unchanged: **true** |

Raw data: `experiments/007-adaptive-replay/results/007F-counterfactual-identity.json`.

### Findings

All three properties held on a real scenario this project has already studied and trusted, exactly as they do in the unit tests -- but on evidence independent of them. The divergence check specifically avoided the pitfall `world_test.go`'s own first draft hit (see internal/replay's commit history): both the with-failure and without-failure variants run identical always-on health-probe machinery, so the only difference between their traces is the failure's actual effect, not the mere presence of probe events.

### Interpretation

This matches the precedent 006-A and 007-A set: unit tests establish a mechanism's internal correctness; a recorded experiment establishes the same claims as auditable evidence, on a scenario worth trusting for its own sake. `internal/replay` is now validated on both fronts before any counterfactual comparison built on top of it (007-G, 007-H) is asked to carry any evidentiary weight.

---

## 8. Results: Experiment 007-G — Counterfactual Policy Comparison

**Hypothesis (H7)**: see `hypotheses.md`. One shared Scenario (1 slow=100ms target, 2 fast=20ms targets, one fast target fails from t=500ms to t=1600ms), run through all five policies via `RunWorld` -- the same exogenous conditions, five independently-evolving endogenous states.

| Policy | Slow share | Selections during outage | Early-recovery share (fast-B) | Late-steady share (fast-B) | Gap |
|---|---:|---:|---:|---:|---:|
| Round Robin | 40.8% | 0 | 31.7% | 33.3% | 1.7pp |
| Least Connections | 16.6% | 0 | 46.7% | 46.7% | 0.0pp |
| EWMA | 4.2% | 0 | 98.3% | 100.0% | 1.7pp |
| P2C-load | 16.4% | 0 | 43.3% | 41.7% | -1.7pp |
| Adaptive | 5.2% | 0 | 60.0% | 61.7% | 1.7pp |

Raw data: `experiments/007-adaptive-replay/results/007G-counterfactual-policy-comparison.json`.

### Findings

1. **Prediction 1 confirmed, with one instructive edge case investigated rather than dismissed.** Every policy selected the failed target zero times strictly after its detection as unhealthy. During verification, EWMA showed one selection at exactly t=600ms -- the identical virtual instant its own probe first recorded it UNHEALTHY. Tracing it down: arrivals are scheduled up front in `RunWorld`, ahead of the health Ticker's own recursively-scheduled next firing, so a tie at that exact timestamp resolves in the arrival's favor -- a same-timestamp event-ordering question the engine answers deterministically, not a health-filtering defect. It was absent from every selection strictly after that instant, for all five policies. 007-E's health-eligibility claim generalizes cleanly: it was never Adaptive-specific, because it lives entirely in upstream filtering, outside any selector.
2. **Prediction 2 did NOT hold, and the reason is itself the finding.** Adaptive's gap between its early-post-recovery share (60.0%) and late-steady-state share (61.7%) of the recovered target was 1.7 percentage points -- the same order of magnitude as every other policy's own (structurally impossible, since none of them has any per-target memory that could produce one) "gap" at this 60-request window size, which ranged 0.0-1.7 points of pure sampling noise. The undershoot 007-D demonstrated is real, but this experiment shows it is not automatic: 007-D isolated it by making the recovering target's *true performance* change at the same moment its data went stale, giving its neutral reset a genuinely different value to converge toward over hundreds of requests. Here (and in 007-E), the recovering target's true performance never changed -- only its health did -- so a stale-then-neutral latency score converges back to essentially the *same* already-correct estimate within a handful of requests, too fast to show up in a 300ms aggregation window.

### Interpretation

This experiment's value is in the prediction it corrected, not just the one it confirmed. Health eligibility is confirmed policy-agnostic -- a property of the upstream filtering pattern used since Stage 3, not of any one selector. The staleness mechanism remains exclusively Adaptive's among the five, but this result adds a real qualification 007-D and 007-E's individual scenarios couldn't reveal on their own: the mechanism's *visible cost* depends on how much the recovering target's actual performance has diverged from what its stale data implied, not merely on whether a staleness reset occurred at all. A router feature can be real, mechanistically distinct from every alternative, and still produce no measurable difference in a specific scenario -- and the honest way to find that out is to run the comparison, not to assume the mechanism's existence guarantees its visibility everywhere.

---

## 9. Results: Experiment 007-H — Paired Multi-Seed Counterfactual Study

**Hypothesis (H8)**: see `hypotheses.md`. 30 replicates of 007-B's heterogeneous shape, arrival timing jittered ±2ms per replicate (seeded), Least Connections and Adaptive run against the byte-for-byte identical jittered Scenario within each replicate -- a paired design exploiting the replay engine's exact counterfactual property.

| Metric | Value |
|---|---|
| Mean paired diff (LC slow-share − Adaptive slow-share) | 0.0812 |
| 95% bootstrap CI on the mean diff | [0.0801, 0.0823] |
| Sign consistency | 30/30 replicates favored Adaptive |
| Evidence tier | **strong** |

Raw data: `experiments/007-adaptive-replay/results/007H-paired-multiseed-counterfactual.json`.

### Findings

The paired difference was positive in every single replicate, and the bootstrap CI on its mean sits entirely above zero with a narrow width (0.0801-0.0823) relative to its magnitude -- strong evidence by this project's own tiering standard. The mean (8.12 percentage points) lands almost exactly on 007-B's single-run estimate (14.0% − 5.7% = 8.3 points), which is itself informative: arrival-timing jitter alone doesn't materially perturb which target either policy's largely deterministic scoring favors, so 007-B's single run was already a reliable read of the underlying effect, not a lucky draw -- this experiment converts that single reliable-looking observation into an actual, checked claim instead of leaving it as an assumption.

### Interpretation

This is the capstone the whole stage was building toward: Stage 6's statistics, applied through Stage 7's replay engine, to a question about Stage 7's own router, using the correct statistical unit throughout (one full scenario replicate, never one request) and the correct tool for a paired design (bootstrapping the differences directly, not treating paired samples as independent). The result itself confirms what 007-B already suggested — Adaptive's load-based self-correction avoids a slow target at least as well as Least Connections' — but confirming it this way is the actual point: a single favorable run and a robust, statistically-supported finding look identical until someone checks, and this experiment is what checking looks like.

---

## 6. Results: Experiment 007-E — Failure and Health

**Hypothesis (H5)**: see `hypotheses.md`. 3 equally-good targets (20ms each), reusing 005-D/005-G's `health.Registry` + ground-truth up/down map + probe `Ticker` pattern unmodified. B fails at t=500ms, recovers at t=1600ms (a 1.1s outage, deliberately longer than the default 1s `StaleAfter`). 500 requests, rotating cache keys.

| Metric | Value |
|---|---|
| Availability transitions for B | false at t=600ms, true at t=1700ms (detection lag ≈100ms each direction) |
| B selections during confirmed-unhealthy window | **0** |
| Gap since B's last selection when it rejoins | 1120ms (> 1000ms `StaleAfter`) — data stale on return |
| Distribution before failure | A=41, B=40, C=39 |
| Distribution early after recovery (first 300ms) | A=20, B=20, C=20 |
| Distribution late steady state | A=20, B=20, C=20 |

Raw data: `experiments/007-adaptive-replay/results/007E-failure-and-health.json`.

### Findings

1. **Prediction 1 confirmed exactly.** B received zero selections during the entire confirmed-unhealthy window. This holds by construction — `AdaptiveSelector` never receives B as a candidate at all, the same upstream `available := filter(allTargets, registry.IsAvailable)` pattern used for every earlier policy — but confirming it directly, rather than trusting the pattern was wired correctly from a code read, is the point of running the experiment.
2. **Prediction 2 confirmed, cleanly.** By the time B rejoins the candidate list, 1120ms have elapsed since its last selection — past the 1000ms `StaleAfter` threshold — so its latency estimate resets to neutral (0.5) exactly as 007-D characterized. B's distribution snaps back to parity with A and C immediately (20/20/20 in the first 300ms after recovery, unchanged into steady state), matching the near-even pre-failure split (41/40/39). There is no post-recovery warm-up penalty and no unearned advantage.

### Interpretation

This experiment doesn't introduce a new mechanism — it demonstrates that two independently-validated mechanisms compose correctly at a boundary neither one was built specifically to handle. Health eligibility (upstream filtering, unchanged since Stage 3) keeps `AdaptiveSelector` from ever considering an unhealthy target; staleness (validated in 007-D for the unrelated case of exploration/rediscovery) happens to also be exactly the right behavior when a target returns from a health-driven absence, because absence-from-traffic and staleness-of-data are the same underlying condition regardless of *why* a target stopped being selected. No special-cased "recovery" logic was written, and none was needed — which is itself evidence that `AdaptiveSelector`'s signal design in `internal/proxy/adaptive.go` decomposes health, staleness, and load/latency scoring into genuinely independent concerns rather than a bundle of case-specific rules.
