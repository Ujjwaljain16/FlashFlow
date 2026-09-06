# Stage 12 Learning Notes — What Building the Missing Mechanisms Actually Changed

## Before Stage 12

Stage 11 left us with a specific kind of uncertainty, not a vague one: we knew EWMA beat Adaptive on
raw mean latency under heterogeneous load, and we knew exactly why the platform couldn't tell us
whether that was a real property of the policies or an artifact of a model with no queueing. We knew
`RealEngine`'s load signal was still disconnected even after fixing latency. We knew the Topology and
Failure seed axes leaked into each other in one direction. We knew H2 — does Adaptive lag behind a
rapidly-changing "best target" — had never been tested at all, because nothing in the platform let a
target's own latency change mid-run. Four precise, named gaps, each traced to a specific Stage 11
sentence. Stage 12's job was to build exactly enough to close them, not to build a better simulator for
its own sake.

## The Central Result Was Sharper Than Expected

The plan going in was "add finite capacity, see if EWMA still wins." What actually happened was more
interesting than a simple yes/no: EWMA's mean latency exploded eightfold (15.90ms → 131.06ms) at
Capacity=1, then recovered almost completely (16.36ms) at Capacity=2. This isn't "contention changes the
answer" — it's a sharp threshold sitting exactly where queueing theory says it should. Stage 11's own
flat-model utilization accounting had already computed edge-a's ρ at 1.09 under EWMA — already past the
stability boundary — without anyone at the time treating that number as a prediction about what would
happen if queueing were ever added. It was. The reversal appeared exactly where the earlier, unrelated
number said it would, which is a stronger kind of confirmation than the experiment alone would have
been: two independently-computed pieces of evidence, from two different stages, agreeing on a specific
numeric threshold neither was built to predict.

The corollary matters as much as the reversal: at Capacity=2 and 3, EWMA's dominance is back, nearly
identical to the flat model's own numbers. Adaptive's advantage isn't "real load-awareness beats a
naive policy" as a general story — it is real, specific, and confined to the regime where a naive
policy's own strategy pushes a target into instability. Outside that regime, concentrating everything
onto the fastest target is still the better bet, at least by this metric. That's a less flattering,
more precise story than "Adaptive wins now," and it's the one the evidence actually supports.

## A Bug Fix That Simplified the Code It Fixed

Stage 11's partial fix for `RealEngine`'s load signal added a header-reading bridge in the dispatch
loop — read `X-Selected-Edge` from the response, manually call the two instrumentation hooks after the
fact. It worked for latency, and was explicitly disclosed as not working for load, because calling
`OnDispatch` and `OnComplete` back-to-back after a response already returned can't reconstruct genuine
concurrent in-flight state. The real fix didn't add a second, more sophisticated bridge — it removed the
first one. `proxy.ReverseProxy` was already correctly tracking load and latency in real time, inside
each request's actual lifetime; the selector just needed to read the same objects instead of its own
disconnected pair. Reordering construction (build the proxy first, then the selector, using the proxy's
own trackers) made the entire header-reading workaround unnecessary. The fixed version of `real.go` is
shorter than the broken one, not longer — a sign the original bug was a wiring mistake, not a missing
capability.

## A Negative Result Worth Keeping

H2's design hypothesis was that Adaptive's `StaleAfter` mechanism — the explicit reset that treats an
old observation as neutral rather than trusted — would explain why it lags behind a target whose real
performance is changing quickly. Sweeping `StaleAfter` across 100ms, the 1-second default, and 3
seconds produced identical results every time. The mechanism this experiment was built to probe simply
wasn't the one driving the outcome. The more likely explanation — that `LatencyTracker`'s own
exponential smoothing takes several observations to catch up to a changed value, regardless of whether
the staleness reset ever fires — was not chased down further, on purpose. Stage 11's own discipline
about not redesigning a scenario indefinitely until it produces the hoped-for mechanism applies here
too: the finding is "Adaptive lags, and it's not for the reason we thought," which is a complete,
honest answer, not an unfinished one.

## The Cache-Affinity Trap Didn't Get Fixed — It Got a Second Force Acting Against It

Program B's B1 finding (Adaptive's cache-affinity signal can permanently misroute a hot key) looked like
a clean, closed result in Stage 11: 0% return rate, confirmed causally by zeroing the weight. Under
contention, that same scenario shows a 68% return rate with the DEFAULT weights unchanged. Nothing about
the cache-affinity mechanism itself was touched. What changed is that the "wrong" target, now genuinely
overloaded by continuing to receive the hot key's traffic, generates real queueing delay that eventually
outweighs the fixed 0.1 score bonus keeping the router stuck there. The trap is still real — 68% wrong is
not a small number — but it now competes against a force the flat model had no way to express. This is
the kind of result that makes "did Stage 12 fix Adaptive's cache-affinity problem" the wrong question:
the mechanism identified in Stage 11 still exists exactly as described; a second, independent mechanism
now exists alongside it, partially counteracting the first.

## What This Stage Did Not Try to Settle

Program C's recovery-dynamics rerun showed a real transition-cost penalty appearing under contention
(1.00x → 1.20x) for the first time — confirming Stage 11's own diagnosis that the flat model structurally
couldn't show one. But the penalty was nearly identical across all three policies tested, not a story
about one policy adapting faster than another. That may be a property of this specific scenario (losing
a target reduces total system capacity regardless of who's routing) rather than a general finding, and
it was reported as exactly that rather than stretched into a broader claim the one data point doesn't
support. Similarly, the capacity threshold identified in the central finding (§7.1) is specific to one
arrival-rate/service-time combination — a real, useful, mechanistically-grounded number, not evidence
that ρ=1 is a universal predictor across every scenario shape this project could construct.
