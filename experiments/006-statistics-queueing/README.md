# Experiment 006: Statistics, Distributional Analysis & Queueing Attribution

## 1. Executive Summary & Research Questions

Stage 5 gave FlashFlow deterministic, reproducible execution — the same manifest and seed now produce the same event trace, every time. That closed one gap and opened another: reproducibility tells you an experiment ran the way it was defined to run, not whether an observed difference between two experiments is real, how large it is, or what mechanism produced it. Stage 6 builds the smallest analytical layer that can answer those three questions for FlashFlow's actual findings, not a general-purpose statistics library.

### Primary Research Question
> How do we turn FlashFlow's reproducible execution traces into statistically defensible engineering conclusions?

### Central Discipline (stated once here, assumed throughout)
Every analysis in this experiment suite is explicit about its **statistical unit** — is one observation a request, a run, or a load level — because these are not interchangeable, and treating 20 runs × 10,000 requests as 200,000 independent replications is exactly the kind of manufactured statistical power this stage exists to avoid.

---

## 2. Results: Experiment 006-A — Statistical Validation

**Hypothesis (H1)**: see `hypotheses.md`. Five synthetic scenarios with a known expected outcome each, run against `internal/statistics`'s Mann-Whitney U, Cliff's Delta, and bootstrap CI implementations — the methodology gate every later 006 experiment depends on.

| Scenario | Result |
|---|---|
| Identical distributions | p=0.129 (not significant), Cliff's Delta negligible (-0.088) |
| Clearly shifted (3σ) distributions | p<0.000001, Cliff's Delta large (-0.981) |
| Same effect, n=20 vs n=400 | CI width narrowed from 12.95 to 2.85 as n grew 20× |
| Outlier-heavy (3 extreme points in 100) | Mean shifted by 4497, median by 0.13, Cliff's Delta stayed negligible |
| Known bootstrap case (Uniform(0,100), true mean 50) | 95% CI = [48.73, 51.25], contains 50 |
| Deterministic analysis seed | Two runs, same data + seed → byte-identical `BootstrapResult` |

All 6 scenarios passed. Raw data: `experiments/006-statistics-queueing/results/006A-statistical-validation.json`.

### Findings

1. **Predictions 1 and 4 confirmed cleanly**: identical distributions produced no evidence of a shift and a negligible effect size; a 3-sigma shift was detected with an overwhelming p-value and a large effect size; a bootstrap CI for a distribution with a known true mean correctly bracketed it.
2. **Prediction 2 confirmed for precision, with an honest caveat on the effect-size estimate itself.** The bootstrap CI narrowed by more than 4× going from n=20 to n=400, exactly as expected. But the *point estimate* of Cliff's Delta itself moved more than a first guess would suggest (-0.490 at n=20 vs -0.309 at n=400) — both estimates of the same true effect, but the small-sample one is a noisier estimate of it, not just a less-certain one. Worth stating plainly: sample size doesn't just narrow the interval around an effect-size estimate, it also makes the estimate itself more trustworthy. Both are "precision," but they're not quite the same claim.
3. **Prediction 3 confirmed sharply**: three extreme outliers among 100 points moved the mean by ~4497 units while moving the median by 0.13 and leaving Cliff's Delta negligible — direct, quantified evidence for why this project uses percentile and rank-based statistics for latency data rather than means.
4. **Determinism confirmed**: identical analysis seed and data produce a byte-identical `BootstrapResult`, satisfying item 54's explicit requirement before any bootstrap result from a real experiment can be trusted.

### Interpretation

This experiment doesn't produce a FlashFlow finding — it produces the license to trust every finding that follows. Every later 006 experiment (routing variability, cache/coalescing consistency, queueing attribution, real-vs-virtual stability) reports a Mann-Whitney p-value, a Cliff's Delta, or a bootstrap CI computed by exactly this code; 006-A is the evidence that code does what it claims to do, checked against cases whose right answer was known before running anything.

---

## 3. Results: Experiment 006-B — Routing Policy Variability (EWMA Lock-In)

**Hypothesis (H2)**: see `hypotheses.md`. This experiment's real story is that its first design produced a null result that turned out to be more informative than the originally-predicted one, and a second cell was built specifically because of what that null result revealed.

### Cell 1: permutation only

| | |
|---|---|
| Runs | 50 (one per permutation seed) |
| Max-share median / p10 / p90 / min / max | **0.9667 / 0.9667 / 0.9667 / 0.9667 / 0.9667** |
| Fair share (3 equal targets under Round Robin) | 0.333 |
| Winner matched first-in-permuted-order target | 50/50 runs (100%) |

**Zero variance.** Every one of 50 independent permutation seeds produced *exactly* the same 0.9667 max-share — only which target won changed, always matching the tie-break order exactly.

### Cell 2: permutation + ±2ms service-time jitter

| | |
|---|---|
| Runs | 50 (same seed sequence as Cell 1) |
| Max-share median / p10 / p90 / min / max | 0.9667 / 0.9667 / 0.9733 / 0.9667 / 0.9733 |
| Winner matched first-in-permuted-order target | **18/50 runs (36%)** |
| stddev of max-share | 0.0000 (Cell 1) vs 0.0023 (Cell 2) |
| Mann-Whitney comparing the two cells' max-share distributions | p<0.0001 |

Raw data: `experiments/006-statistics-queueing/results/006B-ewma-lock-in-variability.json`.

### Findings

1. **Cell 1's result was not the one predicted, and is reported as such rather than smoothed over.** The original hypothesis expected lock-in *severity* to vary run to run; instead severity was a fixed constant and only the winner's identity varied. Traced to its actual mechanism rather than accepted at face value: permuting labels among genuinely, exactly identical targets under an identical fixed workload is a pure relabeling — the timing dynamics that decide how many exploratory picks happen before full lock-in are isomorphic across every permutation. There is no random variable to characterize here; the design measured a constant.
2. **Cell 2 revealed something more interesting than "now there's variance."** Severity itself barely moved (median unchanged, spread from 0.9667 to 0.9733 — real but small). What changed dramatically was *who wins*: the first-in-order target won only 36% of the time under jitter, versus 100% without it, landing close to the 33.3% a uniformly random winner among 3 targets would produce by chance. Mechanism: once jitter makes targets genuinely (if slightly) unequal, EWMA's comparison rule — lower *observed* latency wins, not just "unobserved beats observed" — lets whichever target happens to draw the lowest jittered service time override the tie-break-order advantage entirely.
3. **The two cells together support a precise, mechanistic claim Stage 3 and Stage 5 couldn't make**: tie-break order controls EWMA's lock-in outcome *only* when targets are exactly, unrealistically equal; the moment any real timing difference exists, however small, it dominates instead. Stage 3's own real-engine variability (94/4/2, 68/29/3, 18/79/3) is now explained, not just observed — it's the Cell 2 mechanism, not the Cell 1 one, since real targets are never *exactly* identical.

### Interpretation

This is the discover-limitation-then-refine cycle working exactly as the project's philosophy describes, compressed into a single experiment: the first design answered a well-posed but narrower question ("who wins when targets are identical") than the one actually motivating it ("how variable is lock-in under realistic conditions"), and the gap between those two questions was itself the finding. Adding the smallest realistic source of variation the original Stage 3 phenomenon actually depended on — not a bigger model, just genuine timing noise — didn't merely add spread to an existing measurement; it revealed that the mechanism controlling outcomes changes entirely between "targets are equal" and "targets are almost equal." That distinction did not exist as a stated fact anywhere in this project before this experiment.
