# Stage 16 — Research Synthesis

A narrative account of how FlashFlow's central research question evolved, stage by stage, preserving the
sequence rather than collapsing it into a single tidy theory arrived at from the start.

## 7.1 Problem

FlashFlow was built to study a concrete, bounded question: under heterogeneous edge conditions (targets
with different speeds, occasional failures, finite serving capacity), which routing policies handle
concentrated load well, and why? The project wanted an answer with actual scientific discipline behind
it — deterministic, reproducible, statistically validated, counterfactually isolated — not a benchmark
leaderboard.

## 7.2 Initial Architecture

Before any research question could be asked, the project built the instruments needed to ask it: raw TCP
connection handling and its TIME_WAIT behavior (Stage 1), an HTTP reverse proxy with health checking
(Stage 2), the first routing policies and a caching layer (Stages 3-4), a deterministic virtual-time
discrete-event engine (Stage 5), a statistics toolkit built to the same rigor as the engine itself (Stage
6), the Adaptive multi-signal router and counterfactual replay (Stage 7), and a self-tuning parameter
optimizer with holdout validation (Stage 8). An adversarial audit after Stage 8 (`docs/audit/`) found
real gaps between what was documented and what was built; Stage 9 fixed every correctness/security
finding, and Stage 10 built every missing subsystem the audit had found (traffic generator, SWR cache,
YAML chaos, provenance manifests, LHS/Bayesian tuning, hand-rolled histogram/Prometheus-style telemetry).
Only after this foundation was solid did the actual research program begin.

## 7.3 Stage 11: What Appeared to Be True

The first real research sweep (8 programs, 162+ runs) found that EWMA — a simpler, latency-only policy —
beat Adaptive under certain heterogeneity conditions in the FLAT model (no queueing, no finite capacity).
It also found a real bug: the real engine's own load tracking wasn't reflecting genuine concurrent
pressure. The apparent conclusion at the time was narrow and honest: Adaptive doesn't unconditionally
win, and the model's own instrumentation had a gap that needed fixing before drawing stronger conclusions.

## 7.4 Stage 12: What Changed Once Finite Capacity Existed

Stage 12 fixed the real-engine load-tracking bug and, more consequentially, added a genuinely new
capability: a minimal finite-capacity (FIFO, per-target slot count) contention model. Re-running Stage
11's own flat-model finding under this corrected model produced a sharp reversal: at Capacity=1 on one
specific severe-heterogeneity scenario, EWMA's mean latency exploded from 15.90ms to 131.06ms while
Adaptive barely moved. This was the first sign that "does Adaptive beat EWMA" was the wrong-shaped
question — the real variable was whether a policy's own behavior interacted badly with actual, finite
queueing. Stage 12 was explicit that this was one scenario's result, not yet a general rule.

## 7.5 Stage 13: Toward Rho and Concentration

Stage 13 asked directly whether Stage 12's reversal was a real regime or a one-scenario artifact. Two
independent sweep dimensions (scaling arrival rate with capacity; fixing capacity and varying rate) both
located the transition at offered ρ (rho) ≈ 0.89-0.97 — strong, reproducible evidence within that one
topology family. It also produced the stage's most important reframing: expanding to the full six-policy
set showed round-robin and EWMA (no live load signal) both collapsing under contention, while
weighted-round-robin, least-connections, P2C, and Adaptive (every policy with SOME load signal) stayed
nearly unaffected — "load-blind vs. load-aware routing," not specifically "EWMA vs. Adaptive." This felt,
at the time, like the deep structural answer.

## 7.6 Stage 14: Scale and Topology Falsify the Simplification

Stage 14 tested whether the ρ boundary and the load-blind/load-aware split survived beyond the one
3-target topology that produced them. The qualitative phenomenon (some policies handle concentration far
better than others) generalized cleanly across N∈{3,5,8} and a structurally different bimodal topology.
But two specific claims did not survive: rho's predictive power decreased as target count grew (achieved
rho went DOWN as N increased even as severity WORSENED), and — decisively — the full six-policy sweep at
N=8 found EWMA, a "load-aware" policy by Stage 13's own classification, losing outright to round-robin, a
"load-blind" one. Confirmed across 10 independent seeds with Cliff's Delta=1.000, this was not a fluke.
"Load-blind vs. load-aware" was falsified as the deepest regime boundary. The replacement language Stage
14 proposed — "concentration-proneness under already-committed queueing" — was accurate in spirit but
still just a phrase, not a measured quantity.

## 7.7 Stage 15: Direct Backlog Measurement

Stage 15 built the instrument Stage 14's phrase implied but never had: `internal/backlog`, computing
concentration, peak/duration-of-pressure, and — the central new measurement — committed backlog (work
dispatched to a target between the moment it becomes congested and the moment a policy materially
diverts new work elsewhere) directly from existing dispatch/completion event data. This produced the
project's most quantitatively decisive result: across a target-count generalization test, committed
backlog achieved PERFECT rank agreement with actual severity, while peak rho was badly misordered. It
also produced the project's most important self-correction of its own history: round-robin's failure in
the canonical scenario had a tiny committed backlog yet was chronically, permanently over capacity —
revealing that collapse has (at least) two distinct shapes, acute and chronic, that no single scalar
metric captures both of. And it produced a genuine, valuable negative result: a controlled ablation on
Adaptive's own signal weights found Load specifically (not latency smoothing) explains its resistance to
collapse, while Adaptive's own worst-of-six-policies P99 in that same scenario showed "Adaptive is safe"
had never been fully true.

## 7.8 Final Mechanistic Model

The best-supported current explanation, precisely bounded:

```
concentration (necessary, not sufficient)
      ↓
capacity pressure
      ↓
      ├── acute over-commitment → committed backlog → tail collapse
      │     (best explained by: committed backlog; policies that react to
      │      CURRENT pressure — least-connections, Adaptive's Load signal,
      │      P2C's sampling — avoid this)
      │
      └── chronic over-allocation → sustained time-over-capacity → tail collapse
            (best explained by: fraction-of-time-over-capacity; a policy
             that never adapts at all — round-robin — falls here regardless
             of any acute event)
```

No single scalar predicts both branches. Which branch a given policy falls into is itself informative:
EWMA and (under signal ablation) a Load-blinded Adaptive fall into the acute branch; round-robin falls
into the chronic branch; least-connections, P2C, and a correctly-configured Adaptive avoid both.

## 7.9 Boundaries

- Committed backlog's cross-topology generalization is decisive (perfect rank agreement); its
  cross-workload generalization is real but imperfect (rank distance 2 of a possible 4, not 0).
- The mechanism's direction on the real engine reproduces at most tested stress levels but reverses at
  the most extreme overload level tested — a genuine, disclosed boundary, not a contradiction requiring
  the mechanism to be discarded.
- The cache-affinity interim-latency effect (Stage 13's own finding) remains genuinely unexplained by this
  mechanism, tested directly in two candidate locations.
- All quantitative thresholds (congestion ratio, diversion window, diversion share) were fixed before
  results were inspected in each stage's own confirmatory runs, but Stage 16's own multi-seed flagship
  reproduction found the diversion-share threshold itself needed to scale with target count — fixed as a
  generalization of the existing 3-target convention, not a new number chosen to fit a story, but a real
  reminder that even a carefully pre-registered threshold can carry hidden scope assumptions.

## 7.10 What FlashFlow Does NOT Claim

FlashFlow does not claim: that Adaptive always wins; that ρ≈1 is a universal queueing-collapse law; that
committed backlog is a single scalar sufficient for every workload shape; that its real engine and virtual
engine agree under every condition; that its cache-affinity finding is mechanistically explained; that its
results are byte-identical across different machines or Go versions; that it is a production-ready load
balancer, a general-purpose network simulator, or a replacement for Envoy, HAProxy, ns-3, or Mininet. It
is a controlled research laboratory whose central value is the sequence of increasingly precise, evidence-
driven corrections documented above — not a single conclusion asserted from the start.
