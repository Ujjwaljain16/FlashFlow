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
