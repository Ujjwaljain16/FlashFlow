# Hypotheses: Experiment 007 — Adaptive Routing & Counterfactual Replay

## H1 — Adaptive Signal Validation

Before trusting the adaptive score in any real routing scenario, each individual signal must behave monotonically and each documented design decision (neutral cold start, positive-evidence-only staleness) must hold under direct, synthetic inspection — independent of `internal/proxy/adaptive_test.go`'s unit tests, which check internal correctness; this checks the same claims as an auditable, recorded experiment.

- *Setup*: synthetic target states constructed directly (no real HTTP, no virtual engine) — controlled load, latency, cost, and staleness values assigned explicitly, `AdaptiveSelector.Explain` inspected rather than `SelectTarget`'s single winner.
- *Prediction 1*: increasing a target's load (relative to its configured capacity) strictly does not increase its score; increasing its latency strictly does not increase its score; increasing its configured cost strictly does not increase its score.
- *Prediction 2*: a target with no latency observation scores exactly 0.5 on the latency signal — neither better nor worse than a mediocre observed target — and a target whose data is stale by construction also scores exactly 0.5, distinguishing cold-start-neutral from optimistic-cold-start (EWMA's rule) and from pessimistic-cold-start (neither of which this design uses).
- *Prediction 3*: cache affinity contributes positively only for the target that most recently served the exact request key, and never for any other target or any other key.
- *Purpose*: this is the methodology gate every later 007 experiment depends on, matching Stage 6's own precedent (006-A) of validating a mechanism against known-answer synthetic cases before trusting it on a real scenario.
