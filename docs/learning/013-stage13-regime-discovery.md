# Stage 13 Learning Notes — Testing Whether One Scenario's Story Was the Whole Story

## Before Stage 13

Stage 12 ended with a striking number: at Capacity=1, EWMA's mean latency exploded from 15.90ms to
131.06ms while Adaptive barely moved. Stage 12 was careful, on its own initiative, not to call this a
general law — the reversal happened at exactly the capacity level where EWMA's own flat-model
utilization number (ρ=1.09, computed a full stage earlier for an unrelated reason) crossed the queueing
stability boundary. That agreement between two independently-derived numbers was suggestive, but one
scenario agreeing with itself isn't evidence of generality. Stage 13's whole purpose was to find out
whether that agreement was a real regularity or a coincidence dressed up as an insight.

## The Bug That Almost Produced the Wrong Headline

The single most important methodological moment in this stage was also the most mundane: while testing
whether "Capacity=1" itself was special (scaling capacity and arrival rate together to hold pressure
constant), the first version of the analysis computed ρ as `lambda * serviceTime` without dividing by
capacity. The printed numbers showed ρ climbing steeply with capacity — 0.89, 1.78, 3.63, 7.76 — which
would have supported exactly the WRONG conclusion (that raw capacity, not normalized pressure,
determines the outcome). The fix was one division. Once corrected, the same four cells showed ρ sitting
in a tight band around 0.89-0.97 regardless of capacity, and Adaptive won decisively in every one — the
actual finding this stage needed. The lesson worth keeping: a result that would make a good headline is
exactly the result to distrust hardest, especially when it comes from analysis code that was written to
test that specific headline.

## A Second Bug, Same Lesson

It happened again in the smoothing-alpha experiment. Four alpha values (0.05 to 0.9) produced byte-
identical results — which the code's own interpretation logic initially reported as "consistent with
smoothing being causal," because a flat, non-increasing sequence technically passed a loose monotonicity
check. The real explanation was that the custom instrumentation built for this experiment never called
`LatencyTracker.Observe` at all, so the tracker sat at its cold-start value regardless of alpha. Four
identical numbers should have been the first thing questioned, not the last. After the fix, the real
signal appeared: swap-window degraded-share decreased strictly as alpha increased, genuine causal
evidence this time, obtained specifically because the earlier all-identical result seemed too clean to
trust.

## The Reframing That Mattered More Than the Confirmation

Extending the flagship boundary scenario from two policies to all six produced the most important single
result of this stage, and it wasn't the one the stage set out to find. Round-robin and EWMA — the two
policies with no live load signal at all — both collapsed catastrophically under contention. Weighted-
round-robin, least-connections, P2C-load, and Adaptive — every policy that reads SOME load signal,
whether a static configured weight or a live tracker — all stayed nearly unaffected. The question this
project has been asking since Stage 11 ("does Adaptive beat EWMA?") turns out to be a narrower version
of a cleaner question: does the routing policy know anything about load at all? Four of six answered
yes and were fine; two answered no and weren't. That's a more useful, more general, and more falsifiable
claim than anything phrased in terms of the two specific policies this project happened to compare first.

## Where the Story Held, and Precisely Where It Didn't

Low and moderate heterogeneity never destabilized at any capacity from 0 to 5 — not because heterogeneity
doesn't matter, but because both configurations share the same 10ms fastest target, and 73 req/s against
a 100 req/s single-slot capacity simply never gets close to instability regardless of how much slower the
other targets are. Only the severe configuration's 15ms fastest target, absorbing the same offered load
against a lower 67 req/s ceiling, crossed the line. This reframes "heterogeneity causes the reversal" into
something sharper and less catchy: the fastest target's absolute service time relative to concentrated
offered load is what matters, and heterogeneity is only relevant insofar as it determines how much
concentration a greedy policy will produce. The heterogeneity-ratio experiment confirmed this directly —
widening the gap between the slower targets never destabilized anything by itself; it worked entirely by
making EWMA concentrate harder, which then pushed the fastest target's own utilization up as a
downstream consequence.

The workload-shape experiment drew the boundary even more precisely. A severe burst, one whose peak rate
exceeded the entire three-target system's combined capacity, made Adaptive's advantage evaporate — but
that's not a counterexample to the concentration story, it's a different regime entirely: no routing
policy can serve more total demand than the system has total capacity for, no matter how intelligently
it's distributed. A milder burst, one that stayed under total system capacity but still created genuine
concentration pressure, reproduced Adaptive's advantage cleanly even though the overload was transient
rather than sustained. The distinction that actually matters isn't sustained-versus-transient; it's
concentration-fixable versus capacity-fixed.

## What Stayed Open, Honestly

The virtual-versus-real triangulation did not close cleanly, and it wasn't forced to. At the highest
real concurrency level tested, EWMA still won — the opposite direction from the virtual model's
Capacity=1 reversal. This isn't a contradiction to explain away; it's the direct, empirical shape of a
limitation Stage 12 had already named but not yet measured: a real Go HTTP server handles hundreds of
concurrent goroutines without the blocking, single-slot contention the virtual model's explicit capacity
abstraction creates. The concentration pattern itself replicated faithfully (Adaptive balanced, EWMA
concentrated, in both engines) — only the LATENCY CONSEQUENCE of that concentration failed to transfer,
because the real engine at these request volumes never actually queues the way the virtual model's
single-slot targets do. Reporting that divergence plainly, rather than searching for a real-engine
configuration that would have produced the more satisfying answer, is the correct scientific response to
a genuine negative result.

## The Shape of the Final Claim

Stage 13 was never going to end with a universal law, and didn't try to manufacture one. What it produced
instead is a boundary that is precise where the evidence is precise (offered ρ around 0.89-0.97, in the
one topology family and workload family actually tested) and explicitly uncertain where the evidence
runs out (more than three targets, a fundamentally different heterogeneity shape, a real engine with an
actual concurrency ceiling). That is the intended shape of the result — a well-supported boundary with
named exceptions and a working mechanism, not a rule dressed up as bigger than the experiments that
produced it.
