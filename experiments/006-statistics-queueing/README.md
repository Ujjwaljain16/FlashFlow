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
