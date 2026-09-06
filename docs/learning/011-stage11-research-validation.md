# Stage 11 Learning Notes — What Stage 11 Actually Changed About What We Knew

## Before Stage 11

Stage 10 had built the complete research platform — traffic generation, a generalized attribution
engine, declarative chaos, a provenance/SeedTree system, a unified `ExperimentEngine`, three tuner
tiers — but none of it had been pointed at FlashFlow's own central scientific question: under what
conditions does each routing policy, especially Adaptive, actually help? What we believed going in came
entirely from Stage 6-8: Adaptive wins 62.5-70% of scenarios against five other policies, on a composite
utility metric, across a randomly-generated scenario distribution, with a documented fairness cost. We
had never systematically swept regime boundaries, never deliberately tried to make Adaptive lose, and
had validated the virtual and real engines' agreement only informally, through the Stage 10 demo's one
hand-picked scenario.

## The Central Surprise: Mean Latency and Composite Utility Are Not the Same Question

Program A's regime map (162 runs, 27 scenario configurations × 6 policies) found Adaptive winning
**zero** of 27 configurations on mean latency, with EWMA winning 18 and Round Robin the remaining 9 (an
exact tie under homogeneous load). Read next to Stage 8's 62.5-70% figure, this looked at first like a
contradiction serious enough to question one of the two findings. It wasn't one. The mechanism, once
traced, was almost embarrassingly simple: `internal/replay.RunWorld` has never modeled queueing or
contention — a documented Stage 5 design choice — so a policy's mean latency is minimized by piling as
much traffic as possible onto whichever single target has the lowest fixed service time, with zero
penalty for the resulting overload. EWMA does exactly this (97% of hot-key traffic onto one target in
the sharpest case, driving that target's own utilization to ρ=1.09 — already past overload — while the
other two sit nearly idle at ρ=0.02 and ρ=0.08). Adaptive's multi-signal design deliberately spreads
load instead (ρ=0.56/0.74/0.74 in the same scenario) — the behavior you would actually want from a
router protecting real infrastructure — and is penalized for it by a metric that cannot express why
concentration is dangerous. Rerunning the same scenario with Stage 8's own *tuned* configuration
(not the hand-chosen default every other Stage 11 program used) narrowed the gap meaningfully
(27.38ms → 22.45ms) but didn't close it against EWMA's 15.90ms. The honest resolution: Stage 8 and
Program A are answering different, complementary questions — a composite utility score over a broad
random distribution, versus raw mean latency in a specific, deliberately constructed regime — not
competing verdicts on the same one. That distinction did not exist in our vocabulary before Stage 11;
it does now, and it changes how every future cross-stage latency comparison in this project should be
stated.

## A Confirmed, Causally-Verified Adversarial Finding

Program B set out to make Adaptive lose on purpose, and one of its two constructions worked decisively.
A hot key's affinity target (the fastest edge) crashes and recovers, objectively regaining "best target"
status. Adaptive never once routed the hot key back to it for the remaining two seconds of the scenario
— a complete lock-in, while latency-only EWMA correctly reverted 94% of the time. What made this a
finding rather than a suspicious correlation was checking the mechanism directly rather than trusting
the pattern: forcing `AdaptiveConfig.Weights.Cache` to zero and rerunning the identical scenario raised
the return rate from 0% to 54%. This is now the clearest, most concrete evidence this project has that
Adaptive's cache-affinity signal, exactly as designed, can trap it on a stale-but-sticky decision
indefinitely — not a hypothesis anymore, a demonstrated and reproducible mechanism.

The other adversarial construction, correlated simultaneous failure, did not make Adaptive lose — if
anything it was the best-balanced of three policies tested. This is reported as a real negative result,
not quietly dropped: a scenario that fails to produce a hypothesized failure is itself evidence, and
this project's own discipline says to record it rather than keep redesigning until something breaks.

## Finding a Real Bug by Trying to Validate the Platform, Not the Policy

Program F set out to check whether the virtual engine's conclusions survive contact with the real
engine — and found something more consequential than a fidelity gap. `internal/engine.RealEngine`
discarded the `Instrumentation` `policy.New` returns, meaning EWMA, Least Connections, P2C, and
Adaptive's own trackers never received a single real dispatch/completion event for the entire duration
of any real-engine-driven experiment in this project's history. Every decision after cold start was a
tie, resolved once by an essentially arbitrary tie-break, and repeated for the whole run — a target
winning 100% of traffic not because it was fast, but because of how Go happened to iterate a map that
day. Confirming this took ruling out a plausible alternative explanation first (cache affinity, tested
directly via an ablation with fully distinct request keys) before accepting the simpler, more damning
one. The fix used infrastructure that already existed for exactly this purpose — `ExposeDebugHeaders`
and the `X-Selected-Edge` response header — and restored EWMA's real-engine behavior to closely match
its virtual behavior (0.973 concentration in both). It did not fully fix Adaptive, whose load signal
remains uninstrumented — a precisely scoped, disclosed limitation rather than a vague "needs more work."
Checking the blast radius mattered as much as the fix itself: no prior experiment binary in this
project's history ever called `RealEngine` for a dynamic policy, so nothing previously published was
silently wrong. That's a fact worth having checked, not assumed.

## Reproducibility Work Found a Second, Independent Bug

Program G's job was to attack the SeedTree's independence claims directly rather than trust the Stage
10 design comment asserting they held. Repeat-run reproducibility held perfectly. Axis independence
did not, in one specific direction nobody had tested before: varying only the Topology seed, holding
Failure fixed, changed the failure outcome anyway. The existing test
(`TestGenerate_IndependentAxisControl`) had only ever checked the reverse direction — that varying
Failure leaves Topology alone — which is true, and left the actual asymmetry invisible. The mechanism
traced cleanly: target count is topology-seed-dependent, and it controls the range a separate,
correctly-seeded `failureRNG.Intn(n)` draws from, so the same failure-seed draw sequence can select a
different outcome purely because topology changed the size of `n`. Confirmed, not just observed, by
fixing `n` and watching the leak disappear entirely. This doesn't affect anything Stage 11 concluded
(none of its own experiments used the generator that has this property), but it does mean a claim this
project has quietly relied on since Stage 10 — that Topology and Failure vary independently — was never
actually true in one direction, only tested and confirmed in the other.

## What We No Longer Believe Without a Caveat

- "Adaptive wins" is not a standalone sentence anymore. It wins on Stage 8's composite metric across a
  broad scenario distribution; it loses, sometimes badly, on raw mean latency in specific heterogeneous
  regimes — both true, simultaneously, about different questions.
- "The virtual and real engines agree" was previously an assumption resting on one hand-picked demo
  scenario. It's now a checked, partially-confirmed, partially-open finding: true for EWMA once a real
  defect was fixed, not yet demonstrated for Adaptive's full behavior.
- "SeedTree axes are independent" was a design claim backed by one test covering one direction. It's
  now known to be true in that direction and false in the other, for a specific, understood reason.
- Attribution's Little's Law check, run for the first time this stage, confirms the attribution
  engine's own bookkeeping is internally consistent — it does not, and was never going to, confirm
  anything about real queueing, because the model being measured has none.

## What Stage 11 Did Not Try to Settle

No time-varying per-target latency exists in the platform, so a genuine oscillation/staleness attack
(H2) could not be constructed — declining to fake one with an unrelated mechanism was itself a decision
worth recording, not a gap to quietly paper over. Only one genuinely shifted distribution was tested,
so "Adaptive is robust to distribution shift" remains an open question, not a closed one. RealEngine's
load signal is a named, scoped, not-yet-fixed limitation, not a forgotten one.
