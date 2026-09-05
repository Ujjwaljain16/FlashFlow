# Hypotheses: Experiment 006 — Statistics, Distributional Analysis & Queueing Attribution

## H1 — The Statistical Primitives Behave Correctly on Known Synthetic Data

Before trusting any statistical claim about a real FlashFlow experiment, the methods making that claim need their own validation, independent of `internal/statistics`'s unit tests (which check internal correctness; this checks end-to-end behavior on realistic-shaped data).

- *Setup*: five synthetic scenarios, each with a known expected outcome: (a) two samples from the identical distribution, (b) two samples with a large, unambiguous shift, (c) the same practical effect size at two very different sample sizes, (d) a distribution contaminated with extreme outliers, (e) a bootstrap CI for a distribution whose true mean is known exactly.
- *Prediction 1*: identical distributions produce a large Mann-Whitney p-value and a negligible Cliff's Delta; clearly shifted distributions produce a small p-value and a large Cliff's Delta.
- *Prediction 2*: the same practical effect size (a fixed shift) produces a similar-magnitude Cliff's Delta regardless of sample size, but a *narrower* bootstrap CI and typically a smaller p-value at the larger sample size — sample size buys precision and power, not a larger effect.
- *Prediction 3*: Cliff's Delta (rank-based) is not dominated by a handful of extreme outliers the way a raw mean difference would be — outlier contamination should shift the mean noticeably more than it shifts the delta or the median difference.
- *Prediction 4*: a bootstrap CI for the mean of a large sample from a distribution with a known true mean brackets that true value.
- *Purpose*: this is the methodology gate every later 006 experiment depends on — if these five scenarios don't behave as expected, nothing built afterward on top of these primitives can be trusted, no matter how interesting the resulting numbers look.

## H2 — EWMA Lock-In Variability Across Controlled Seeds

Stage 3 observed extreme, apparently-random target imbalance among genuinely equal targets (94/4/2, 68/29/3, 18/79/3 across three real runs); Stage 5 reproduced a different split again. `EWMASelector` has no RNG of its own — its documented cold-start rule is "ties among unobserved targets fall back to `available` order." This hypothesis asks how variable the resulting lock-in actually is under a seed-controlled source of variation targeting that exact mechanism.

- *Setup, Cell 1*: 3 targets with identical nominal service time, a fixed deterministic 300-request arrival schedule (unchanged across runs), and the `available` slice's order permuted by a seeded shuffle — 50 seeds, 50 runs.
- *Original prediction (stated here even though it turned out wrong, per this project's practice of recording incorrect predictions rather than only the ones that held)*: the maximum-target-share statistic would vary meaningfully run to run, characterizable by a median, a percentile range, and a bootstrap CI.
- *What actually happened*: zero variance. Every one of 50 runs produced exactly the same 0.9667 max-share; only the winning target's identity varied, matching the permuted order 50/50 times. Investigated rather than reported as-is: permuting labels among truly identical targets under an identical workload is a pure relabeling — the underlying timing dynamics are isomorphic across every permutation, so severity is a deterministic structural constant of (spacing, service time, request count), not a random variable under this design at all.
- *Cell 2, added because of that discovery*: the same 50 seeds, plus a small (±2ms on a 20ms base) per-run service-time jitter — the actual kind of timing noise that produced Stage 3's original variability, which Cell 1's design never introduced.
- *Revised prediction for Cell 2*: genuine (if small) timing differences between targets should let real latency differences compete with tie-break order for control of the outcome.
- *Purpose*: an honest two-part story — first a null result that revealed a hidden assumption (permutation alone doesn't create genuine variability among literally-identical targets), then a targeted refinement that tests what actually varies once realistic noise exists.
