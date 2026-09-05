# Hypotheses: Experiment 008 — Auto-Tuning, Experiment Operations & Final Validation

## H1 — Tuning Machinery Validation

Before any search algorithm is trusted to explore `AdaptiveSelector`'s configuration space, every piece the search will depend on — config sampling, scenario generation, the Development/Holdout split, and the objective function — must be checked against a known answer, independent of `internal/tuning`'s own unit tests. This matches the precedent 006-A set for `internal/statistics`, 007-A set for the adaptive signals, and 007-F set for the counterfactual replay engine: unit tests establish internal correctness, a recorded experiment establishes the same claims as auditable evidence.

- *Setup*: `internal/tuning`'s `ConfigSpace`, `ScenarioSpace`, `Split`, `Metrics`/`Scores`/`Utility`/`ParetoFrontier`, and `Evaluate` — exercised directly, not through the search loop (008-B), since none of that exists yet at this point in the stage.
- *Prediction 1*: 1000 samples drawn from `ConfigSpace.Sample` all satisfy `ConfigSpace.Valid` — the sampler cannot produce an invalid candidate.
- *Prediction 2*: the four `AdaptiveWeights` are scale-invariant for routing purposes — a weight vector scaled by an arbitrary positive constant (and therefore technically outside `ConfigSpace.Valid`'s sum-to-1 requirement) produces byte-identical `Scores` to the unscaled version, confirmed empirically via `Evaluate` on a real scenario set, not merely asserted from reading `AdaptiveSelector.SelectTarget`'s argmax math.
- *Prediction 3*: scenario generation is seed-deterministic (regenerating from the same seed produces the identical scenario shape) and every generated scenario executes through `RunWorld` without error.
- *Prediction 4*: the Development (40 scenarios) and Holdout (20 scenarios) sets, generated from disjoint seed ranges, share zero seeds.
- *Prediction 5*: the objective function orders a synthetic "perfect" case (zero latency, zero rejections, perfectly even load) strictly above a synthetic "worst" case (extreme latency, all rejected, total concentration) — the same known-answer-before-real-data discipline 006-A applied to the statistics primitives.
- *Prediction 6*: `ParetoFrontier` correctly identifies a hand-constructed non-dominated pair (one candidate better on latency, one better on fairness) as both non-dominated, while a third candidate strictly dominated by the first is excluded.
- *Prediction 7*: the full `Evaluate` pipeline (config → `AdaptivePolicyWithConfig` → `RunWorld` → `ComputeMetrics` → `ComputeScores`) is deterministic end to end for a fixed config and scenario set.

## H2 — Random Search Tuner v1

Per the master context's own earn-the-abstraction progression (rule 4/13), Random Search is the first search algorithm tried — not because it is expected to be sophisticated, but because its own behavior (does it improve on a hand-chosen baseline at all, how many evaluations does that take, does it plateau) determines whether a more elaborate algorithm (Latin Hypercube, Bayesian Optimization) is ever justified.

- *Setup*: 200 evaluations, optimizer seed 20260908 (independent of every Scenario's own exogenous seed and of any later statistical-analysis seed), sampling `AdaptiveConfig` from `DefaultConfigSpace` and scoring each candidate against the full 40-scenario Development set via `Evaluate`. The hand-chosen `proxy.DefaultAdaptiveConfig()` is scored against the identical Development set as the baseline to beat.
- *Prediction 1*: automated search finds at least one configuration whose Development utility exceeds the hand-chosen baseline's — establishing that the search space and objective are non-trivial to explore, not merely that any point in the space beats any other.
- *Prediction 2*: the full search ledger (every evaluation, not just the winner) makes the search's convergence behavior directly inspectable — whether the best-so-far value is still improving or has plateaued by the end of the run, and how many evaluations were needed to reach the eventual best.
- *Explicit non-claim*: a Development-set improvement, however real, is not evidence of a better configuration in any general sense (master context rule 9/10) — it only proves the search process itself works. Whether the winning candidate's advantage survives evaluation against Holdout scenarios the search never saw is 008-C's question, evaluated exactly once.

## H3 — Holdout Validation & Generalization Gap

The sacred rule (master context rule 9): the tuner may use Development, never Holdout, during optimization. This experiment is the one and only time 008-B's winning candidate is shown a Holdout scenario — its result is recorded as-is, whatever it says, with no second attempt and no manual re-selection after looking.

- *Setup*: 008-B's search is reproduced (same `OptimizerSeed`, same Development set — a determinism check in its own right) to obtain the identical winning candidate, then evaluated against the 20-scenario Holdout set for the first time. The hand-chosen baseline is evaluated on Holdout too, for a fair comparison.
- *Prediction 1*: the generalization gap (training improvement minus holdout improvement) is computable and will be reported honestly regardless of its sign or size — including the possibility that it reveals the winning candidate does NOT generalize (master context rule 41: "the generalization gap itself is an important metric... do not hide it").
- *Prediction 2*: per-scenario robustness (mean/median/worst-case/stddev of utility across the scenario set, not the pooled aggregate alone) is reported for both configs on both sets, per master context rule 22's explicit instruction not to judge a configuration by its average alone.

## H4 — Sensitivity Analysis

A configuration search can find either a robust basin or a fragile knife-edge that happens to score well at one exact point (master context rule 21). This experiment perturbs every one of 008-B's winning candidate's 6 tunable parameters and checks which it is.

- *Setup*: each of the 4 weights perturbed by +/-10% (renormalized back onto the simplex), `ReferenceLatency` and `StaleAfter` perturbed by +/-100ms (clamped to `ConfigSpace` bounds) — rule 21's own examples, applied literally. Re-evaluated on both Development and Holdout.
- *Prediction*: no single perturbation moves utility by more than a small fraction of the baseline utility — evidence the search found a genuinely stable region of the configuration space, not an isolated spike that a slightly different sample would have missed entirely.

## H5 — Adversarial Tuner Test

The tuner itself must be attacked (master context rule 40): construct a synthetic case where one configuration looks at least as good as another during training, but fails badly on unseen scenarios, and confirm the holdout-evaluation methodology reveals that failure rather than hiding it.

- *Setup*: Config A weighs ONLY the Cache signal (pure memorization of whichever target answered a shared cache key first); Config B is `proxy.DefaultAdaptiveConfig`'s genuinely signal-driven weights. A Development scenario where the alphabetically-first target is genuinely fastest; a Holdout scenario with the fast/slow assignment swapped.
- *Prediction*: Config A performs at least as well as Config B on Development (the dangerous case — nothing about training results would give a reason to prefer B), while Config B performs meaningfully better than Config A on Holdout — confirming the pipeline's Development-then-Holdout ordering would surface a configuration that cannot generalize, even when Development gives no advance warning whatsoever.

## H6 — Final Policy Evaluation

Master context rule 42: the final Adaptive router should be compared against Round Robin, Weighted Round Robin, Least Connections, EWMA, and P2C on Development, Holdout, and Challenge scenarios, reporting win rate, non-inferiority rate, and per-challenge behavior — not assumed to win everywhere (rule 43 explicitly permits, and expects, a simpler policy to win some scenarios).

- *Setup*: the tuned Adaptive configuration from 008-B/008-C compared against the five other policies (`WeightedRoundRobinPolicy` given the single most favorable case it can be given — per-scenario weights derived from perfect knowledge of each target's true `ServiceTime`) across the 40-scenario Development set, the 20-scenario Holdout set, and 3 hand-crafted challenge scenarios (identical targets, an extreme 20x capacity ratio, health failure/recovery) — the latter a deliberately smaller, illustrative set, not the full permanent regression suite the master context describes as a further, distinct deliverable.
- *Prediction 1*: Adaptive achieves the highest win rate and non-inferiority rate of the six policies on both Development and Holdout, without winning literally every scenario — some scenarios should be legitimately won by a simpler policy (rule 43), an outcome the pipeline must be able to report honestly rather than suppress.
- *Prediction 2*: health eligibility holds with zero exceptions across all six policies on the failure-recovery challenge, generalizing 007-E/007-G's finding once more as part of this project's final comparison.
- *Prediction 3*: on the identical-targets challenge, all six policies perform comparably — Adaptive should not manufacture an advantage where heterogeneity doesn't justify one, reconfirming 007-B/007-H.

## H7 — Final Performance Benchmarks & Open-Loop Load Sweep

Master context rules 51-53: report throughput, latency percentiles, routing decision cost, virtual-engine event throughput, and tuner evaluation rate; and produce a load sweep that distinguishes offered load from completed throughput (rule 52) — something 006-D/006-E, both closed-loop by design, structurally cannot show, since fixing a worker pool's concurrency keeps offered and completed load locked together at all times.

- *Setup*: Go `testing.B` benchmarks for the virtual engine's raw event throughput (`internal/vtime`), each selector's per-decision routing cost (`internal/proxy`), the full `RunWorld` pipeline's virtual-request throughput (`internal/replay`), and Random Search's evaluation rate (`internal/tuning`). Separately, a new open-loop HTTP load generator — every request's dispatch goroutine launched up front, each sleeping to its own precise absolute target time, never a shared ticker a busy receiver could cause to drop ticks — sweeping offered rate from 20 to 600 req/s against 006-D/006-E's exact real bottleneck (`MaxConnsPerHost=5`, 20ms Origin delay, analytical ceiling 250 req/s).
- *Prediction 1*: routing decision cost is negligible next to real network latency for every selector, including Adaptive's more expensive multi-signal computation.
- *Prediction 2*: the load sweep's observed "knee" (the offered rate where latency inflects sharply upward) lands close to the analytical ceiling (capacity/serviceTime = 250 req/s) 006-D already predicted and confirmed under closed-loop concurrency — this experiment's job is to confirm the same bottleneck produces the expected signature under genuinely independent, open-loop offered load, which no earlier experiment's generator design could show.

## H8 — Minimal NGINX Reference Benchmark

Master context rule 55: where genuinely apples-to-apples, benchmark selected scenarios against a mature reference system, framed explicitly as a reference point, never a claim that FlashFlow replaces it.

- *Setup*: FlashFlow's real `cmd/proxy` and NGINX (Docker, a minimal `proxy_pass` config) both fronting the byte-for-byte identical backend (`cmd/http-origin`, 20ms artificial delay), benchmarked with a small, deliberately modest client (200 requests, concurrency 10 — light load, no saturation sweep).
- *Prediction*: at this light load, both systems' latency stays close to Origin's own 20ms baseline, with neither claimed as definitively "faster" from a single light-load reference run — the point is establishing a reference number, not a verdict.
