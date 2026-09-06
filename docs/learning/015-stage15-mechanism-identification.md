# Stage 15 Learning Notes — Naming a Mechanism Is Not the Same as Measuring It

## Before Stage 15

Stage 14 ended with a phrase that sounded like an answer: "concentration-proneness under already-
committed queueing." It explained the falsifier that broke Stage 13's own two-regime story — EWMA losing
to round-robin at scale — better than anything before it. But it was still just a phrase. Nothing in the
project had ever measured "committed backlog" as a number. Stage 15's job was to find out whether that
phrase could survive being turned into an actual quantity, computed from data every experiment already
produces, and then attacked as hard as possible.

## The Bug That Would Have Undersold the Whole Stage

The first version of the committed-backlog measurement found the FIRST time a target's queue crossed the
danger threshold and measured backlog from there. It seemed reasonable until the canonical scenario ran
and reported EWMA's committed backlog as 1 — one single request. That number was suspicious for the same
reason Stage 13's own "four identical alpha results" was suspicious: it was too clean, and it disagreed
with everything else about the run (EWMA had the worst mean and p99 of all six policies tested).
Investigating why revealed the actual shape of the data: EWMA's bottleneck target had a tiny, early,
self-resolving congestion blip during cold start, well before the workload's real peak, and the
first-episode-only measurement locked onto that irrelevant blip instead of the much larger episode that
happened later and actually explained the outcome. The fix — anchor the measurement to whichever episode
contains the target's peak depth, not whichever comes first — took about twenty lines of code and a new
test with two episodes of deliberately different sizes. The lesson repeats from Stage 13: a suspiciously
clean number is a bug report, not a result, and the instinct to distrust it rather than write it down is
what actually produces good measurements.

## What the Canonical Scenario Revealed That the Metric Alone Didn't

Once the fix was in, the six-policy comparison produced a genuinely rich, surprising dataset — richer than
expected. Adaptive, this project's own flagship policy since Stage 7, had the WORST P99 latency of all six
policies in this one scenario, despite having a much better mean than EWMA's. A metric that only looked at
averages would have hidden this completely. And two policies — round-robin and Adaptive — both failed to
drain their queues within the horizon, but for opposite reasons once the numbers were laid side by side:
round-robin had a tiny committed backlog (4 requests) yet spent 71% of the entire run above capacity,
while Adaptive had a huge committed backlog (86 requests) matching an almost identical 70% fraction-over-
capacity. Round-robin's failure was never an acute event at all — it was a permanent, structural mismatch
between its fixed even split and its slowest target's actual capacity, present from the first request to
the last. Adaptive's failure was a single large over-commitment during the burst that eight seconds wasn't
long enough to fully clear. Committed backlog, the metric this whole stage was built to validate,
correctly explains Adaptive's failure and is nearly silent about round-robin's. That isn't a flaw in the
metric — it's a discovery that there are at least two different collapse shapes, and conflating them under
one number would have been the actual mistake.

## Turning Ablation Into an Answer, Not a Guess

Adaptive's own worst-of-six P99 result demanded an explanation, not just a note. Stage 13 and 14 had both
treated Adaptive as "the stable one" without ever isolating which of its four signal components (load,
latency, cache, cost) was doing the protective work. Zeroing each one out in turn, on the exact same
scenario, gave a clean answer: removing the LOAD signal more than doubled Adaptive's committed backlog —
from 86 to 206, actually worse than EWMA's own 97 — and shifted its bottleneck target from the slowest
edge to the fastest one, recreating an EWMA-style single-target lock-in almost exactly. Removing latency
made things moderately worse without changing which target locked up. Removing cache barely moved the
numbers at all. This is the kind of answer a controlled intervention can give that a correlation never
can: Adaptive isn't safe because it's "smart" in some general sense, it's safe specifically because it can
see how much work is CURRENTLY sitting at each target, the same information least-connections uses
directly and EWMA structurally lacks.

## The Result That Didn't Confirm the Hypothesis, and Was Reported Anyway

Not every mechanism this stage went looking for turned out to be real. Stage 13's cache-affinity finding —
higher cache weight makes interim latency worse but eventual recovery better — seemed like an obvious
candidate for a committed-backlog explanation: a stronger pull toward a target should delay diverting away
from whatever absorbed its traffic during an outage, letting more work pile up there. Measuring it
directly showed the opposite of that expectation: committed backlog on the absorbing target did not
increase with cache weight at all, even though the interim latency effect was still there. A natural
second guess — that the real backlog forms AFTER recovery, in a rush back to the just-recovered target —
didn't hold up cleanly either, and the measurement itself became unstable at the small scale involved.
The honest conclusion is that this particular effect remains unexplained by the mechanism this stage
built, and the write-up says exactly that instead of stretching either hypothesis to fit. A mechanism that
explains six other things well doesn't get credit for a seventh it doesn't actually explain.

## Following the Falsifier Onto the Real Engine

Stage 14 had already shown that a validated real concurrency ceiling reproduces the virtual model's
Adaptive-versus-EWMA reversal. The natural next question was whether the SAME ceiling reproduces Stage
15's own new falsifier — round-robin beating EWMA — since that result, not the original Adaptive
comparison, is now the more load-bearing claim. It mostly did: round-robin beat EWMA at the two lower
stress levels tested, matching the virtual direction. At the highest stress level, it reversed. That
reversal makes complete sense once thought through mechanistically rather than treated as noise:
round-robin's fixed even split guarantees a permanent, unshakeable share of traffic to the real engine's
slowest edge, while EWMA's lock-in, wrong as it is, concentrates onto whichever edge it happened to settle
on — which can still absorb real throughput a permanently-overloaded slow edge never could. The mechanism
didn't fail here; it revealed a genuine boundary condition that only shows up under enough real pressure,
and reporting that boundary honestly is more useful than either forcing agreement or discarding the whole
comparison.

## The Shape of What Got Built

Stage 15 was never going to end with one universal number that predicts collapse, and its own charter said
as much up front — a compact, explainable quantity was a hope, not a requirement. What it produced instead
is a small, precisely-scoped toolkit: committed backlog for acute over-commitment, fraction-of-time-over-
capacity for chronic under-provisioning, and concentration as a necessary but insufficient precondition
for either. Each piece is measured the same deterministic way, from data every run already produces, and
each piece's own limits are written down next to its strengths — where it generalizes cleanly (topology
size, most of the real-engine comparisons), where it doesn't (workload shape, one specific real-engine
extreme, cache-affinity entirely), and where a genuinely different failure mode hid behind a policy this
project had trusted since Stage 7. That is a more useful outcome than a single elegant formula would have
been, and it is the one the evidence actually supports.
