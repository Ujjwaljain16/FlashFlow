# Stage 14 Learning Notes — When the Headline Claim Turns Out to Be the Wrong Shape

## Before Stage 14

Stage 13 ended with a clean, satisfying story: routing policies split into two regimes, load-blind and
load-aware, and the load-aware ones were safe from the capacity-pressure collapse that broke round-robin
and EWMA. It was a better, more general finding than "Adaptive beats EWMA," and it felt like the kind of
result a project should be able to stand on. Stage 14's job was to stress-test that story against
everything Stage 13 hadn't tried: more targets, a different heterogeneity shape, a real concurrency
ceiling, alpha at the main boundary instead of just H2, and recovery in a bigger topology. The honest
expectation going in was that most of these tests would confirm the story with minor caveats.

## The Test That Broke the Headline

It didn't work out that way. Running the full six-policy set at eight targets, near the boundary that
`014c` had just carefully re-established, produced a number that didn't fit anywhere in the two-regime
story: EWMA — the policy Stage 13 itself classified as "load-aware" because it reads a live latency
signal — lost to round-robin, which reads nothing at all. Not by a little. 307ms versus 170ms at one
load level, 589ms versus 377ms at the next. This is exactly the kind of result Stage 14's own instructions
asked for under Section 28 (actively try to break the classification) and exactly the kind of result that
is easy to want to explain away, because it contradicts a finding the project had just spent an entire
stage establishing. It didn't get explained away. It got confirmed, ten independent seeds later, with a
Cliff's Delta of 1.000 in both directions.

## Why It Happened, Once Traced Through

The instinct is to ask "what's wrong with EWMA's load signal" — but that's the wrong question, because
EWMA does have a load signal; it just doesn't act on it in a way that helps once things go wrong. The
mechanism, once traced mechanistically rather than assumed, is about timing, not information: EWMA
commits early requests to whatever target looks fastest during cold start. Once that target starts
queueing, EVERY later routing decision faces a fait accompli — a backlog of already-dispatched requests
sitting in that target's queue, which no amount of future rerouting intelligence can retroactively drain.
The alpha experiment (`014g`) made this precise: varying EWMA's smoothing speed across a sixteen-fold
range had literally no effect on whether it beat round-robin. If the problem were "EWMA notices too
slowly," a faster alpha should have helped. It didn't, at all, in either direction. The problem isn't
recognition speed; it's that recognition, however fast, cannot un-commit work that's already queued.
Least-connections and Adaptive avoid this specific trap not because they have a "better" signal in the
abstract, but because their signal (in-flight count, for LC; a load-and-latency blend, for Adaptive)
reacts to CURRENT congestion rather than a smoothed historical average, so they redirect NEW requests away
from a backlog while it is still forming, rather than after it has already been observed.

## What This Means for the Two-Regime Story

The right correction isn't "the two-regime story was wrong" — it's "the two-regime story used the wrong
axis." Load-blind versus load-aware asks whether a policy has ANY signal. The evidence from `014f` says
the operative question is whether a policy's signal causes it to CORRECT AWAY from an already-forming
backlog, or merely to notice one, eventually, after the fact. Round-robin has no signal and is
predictably bad in a different, more even way (its bottleneck is always the slowest target, confirmed
again in the bimodal topology in `014b`). Weighted-round-robin has a signal but a frozen one, and does
fine here specifically because its static weights happen to be accurate. EWMA has a live signal and still
fails, because the signal updates too late relative to the decisions that matter. That's not one bit of
information (load-aware: yes/no); it's at least two (has a signal at all; does the signal correct
ongoing formation of backlog or only report on it afterward). A one-dimensional classification, however
appealing, was hiding a two-dimensional reality.

## The Rho Number Also Didn't Survive Intact

A second, quieter erosion happened in `014c`, the experiment built specifically to defend the ρ≈0.9
boundary at larger scale. The design was careful: scale request counts so each target count's own
concentrated target reaches the same achieved rho, using each topology's OWN measured concentration
percentage rather than an assumed one. It didn't work as cleanly as hoped — EWMA's concentration
percentage is not a fixed property of the topology; it shifts with request volume and target count in
ways that undermined the very matching the experiment was trying to hold constant. The achieved rho
actually DECREASED as target count grew (0.915→0.833→0.716) while the actual degradation got WORSE
(93.78ms→201.81ms→307.32ms) — the opposite of what a clean rho-as-predictor story would need. This isn't
a failure of the experiment; it's a real finding, precisely because the experiment was careful enough to
notice the mismatch rather than declare success at "same rho, similar-ish outcome" without checking
whether the rho-matching actually held.

## The Result That Reversed, With a Good Explanation

Not every generalization test produced a negative or narrowing result. Stage 13 ended with an honest,
disclosed failure to reproduce its own virtual-model finding on the real engine — EWMA won on hardware,
the opposite direction from the virtual model's Capacity=1 collapse. Stage 14 revisited this with the one
tool Stage 13 explicitly flagged as untested: an actual concurrency ceiling, rather than hoping enough
concurrent requests would create equivalent pressure on their own. The first attempt (ceiling=2) still
didn't reproduce the reversal cleanly, and the reason was mundane and worth sitting with: the ceiling's
own implied throughput was never actually exceeded by the offered load, so there was nothing for the
ceiling to constrain. Recalibrating to ceiling=1 — matching the virtual model's own Capacity=1 assumption
exactly, rather than picking a ceiling value that merely sounded plausible — produced a clean, monotonic
reproduction of the virtual reversal's direction, with the gap widening from 62ms-vs-17ms at low load to
2914ms-vs-103ms at high load. The earlier failure to reproduce wasn't evidence the two models disagree in
principle; it was evidence that nobody had yet built a mechanism that made the real engine actually queue.
Once one existed, and was validated on its own before being trusted (a raw atomic-counter probe run
before any policy was involved), the two engines told the same story.

## The Shape of the Final Claim

Stage 14 did not confirm Stage 13's headline as stated, and it also did not discover that Stage 13 was
wrong. What actually happened is more useful than either: the qualitative phenomenon Stage 13 found —
some policies handle concentration-driven capacity pressure far better than others — turned out to be
real and structural, surviving every topology, scale, and engine change tested. The specific number
(ρ≈0.9) and the specific classification used to describe WHY (load-blind vs. load-aware) turned out to be
artifacts of the one topology that produced them, in the ordinary sense that a first good hypothesis is
often the right shape but the wrong resolution. Replacing "does the policy have a signal" with "does the
policy's signal correct an already-forming backlog before it's too late" is a sharper, more falsifiable,
and — critically — a more accurate claim, arrived at only because Stage 14 built the exact experiment
designed to break the old one, and then trusted what it found instead of what it expected to find.
