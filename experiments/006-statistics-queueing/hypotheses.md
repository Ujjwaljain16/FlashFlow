# Hypotheses: Experiment 006 — Statistics, Distributional Analysis & Queueing Attribution

## H1 — The Statistical Primitives Behave Correctly on Known Synthetic Data

Before trusting any statistical claim about a real FlashFlow experiment, the methods making that claim need their own validation, independent of `internal/statistics`'s unit tests (which check internal correctness; this checks end-to-end behavior on realistic-shaped data).

- *Setup*: five synthetic scenarios, each with a known expected outcome: (a) two samples from the identical distribution, (b) two samples with a large, unambiguous shift, (c) the same practical effect size at two very different sample sizes, (d) a distribution contaminated with extreme outliers, (e) a bootstrap CI for a distribution whose true mean is known exactly.
- *Prediction 1*: identical distributions produce a large Mann-Whitney p-value and a negligible Cliff's Delta; clearly shifted distributions produce a small p-value and a large Cliff's Delta.
- *Prediction 2*: the same practical effect size (a fixed shift) produces a similar-magnitude Cliff's Delta regardless of sample size, but a *narrower* bootstrap CI and typically a smaller p-value at the larger sample size — sample size buys precision and power, not a larger effect.
- *Prediction 3*: Cliff's Delta (rank-based) is not dominated by a handful of extreme outliers the way a raw mean difference would be — outlier contamination should shift the mean noticeably more than it shifts the delta or the median difference.
- *Prediction 4*: a bootstrap CI for the mean of a large sample from a distribution with a known true mean brackets that true value.
- *Purpose*: this is the methodology gate every later 006 experiment depends on — if these five scenarios don't behave as expected, nothing built afterward on top of these primitives can be trusted, no matter how interesting the resulting numbers look.
