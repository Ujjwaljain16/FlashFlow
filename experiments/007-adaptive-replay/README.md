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
