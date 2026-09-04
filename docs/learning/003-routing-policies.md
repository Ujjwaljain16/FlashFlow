# Stage 3 Learning Notes — Server-Side Scaling & Routing Policies

## 1. Initial Assumptions

Before Stage 3, our assumptions based on Stage 2's findings were:
1. **Round Robin's flaw is capacity-blindness.** Experiment 002-D already proved RR collapses cluster throughput 89.6% when one edge degrades, because it distributes by count, not by consequence. We assumed the fix would be a straightforward progression: give the router more information (weights, then load, then latency), and routing quality would improve roughly monotonically at each step.
2. **Least Connections needs application-level in-flight count, not TCP socket count** — flagged explicitly in Stage 2's learning notes ("Critical Discovery for Stage 3") as the key architectural gap to close.
3. **EWMA would be "the good one."** We expected latency-aware routing, once built, to be a strict improvement over Least Connections — reacting to the thing that actually matters (response time) rather than a proxy for it (concurrent count).
4. **P2C was expected to be a minor scalability optimization** — "get most of the benefit without scanning every target" — not a correctness fix for anything.
5. We expected the `TargetSelector` interface to need evolving once we reached Least Connections, since it would need to expose per-target state the original interface didn't carry.

Several of these were wrong, in informative ways.

## 2. What We Built

Five routing policies, each earning its abstraction from the previous one's demonstrated limitation, all still implementing the original, unmodified `TargetSelector` interface from Stage 2:

```text
internal/proxy/
├── selector.go              # TargetSelector interface, StaticSelector, RoundRobinSelector (Stage 2)
├── weighted_round_robin.go  # WeightedRoundRobinSelector — smooth WRR (nginx/LVS algorithm)
├── load_tracker.go          # LoadTracker — proxy-owned, ambient in-flight-request counter
├── least_connections.go     # LeastConnectionsSelector — reads LoadTracker
├── latency_tracker.go       # LatencyTracker — proxy-owned, ambient EWMA latency estimator
├── ewma.go                  # EWMASelector — reads LatencyTracker; shares preferScore with P2C
├── p2c.go                   # P2CSelector — seeded random-pair sampling over a pluggable scorer
└── proxy.go                 # ServeHTTP: increment/decrement LoadTracker, observe LatencyTracker
```

Six experiment binaries (`cmd/experiment-003a` through `003f`), 49 recorded JSON results under `experiments/003-routing-policies/results/`, and 32 new unit/integration tests (72 tests pass across the whole repository at Stage 3's close, up from 18 at Stage 2's).

## 3. What Experiments We Ran, and What Question Each One Asked

| Experiment | Question |
|---|---|
| 003-A | Does Round Robin's fairness hold under real concurrent contention, not just in principle? |
| 003-B | Does correctly-configured static weighting fix what 002-D broke? |
| 003-C | Does Least Connections discover the same fix without being told the capacity ratio, and does it adapt when capacity changes after configuration? |
| 003-D | Does EWMA react correctly to latency, and how sensitive is it to its smoothing constant? |
| 003-E | Does P2C's randomized sampling fix the exploration failure Experiment 003-D found? |
| 003-F | How do all five policies behave under a concurrency burst and a hard failure — the two scenarios not yet covered? |

## 4. What Happened

The short version, with numbers, is in `experiments/003-routing-policies/README.md`. The arc:

- **RR** distributes exactly evenly, verified down to the exact floor/ceil split under real 97-way concurrent contention, not just as arithmetic.
- **WRR**, correctly configured (100:100:1 against a 1ms/1ms/100ms topology), recovered 2.9× the throughput RR lost in Experiment 002-D — but nowhere near the fully-homogeneous baseline, and its recovery is only as good as its configuration is accurate and current.
- **Least Connections** matched or beat WRR's hand-tuned result *without* being told the capacity ratio, and — the headline result — adapted to a mid-run capacity change with zero reconfiguration, something WRR structurally cannot do.
- **EWMA**, expected to be strictly better than Least Connections, instead revealed the most important finding of the stage: as a pure greedy policy, it can permanently lock onto one of several genuinely equal targets, and can never detect that a target it has stopped selecting has changed at all — for better or worse.
- **P2C**, over the same latency signal, measurably reduced that lock-in (from one dominant survivor to two competitive ones) but did not eliminate it, and — this was the more important discovery — inherits EWMA's blindness to recovery for exactly the same reason (only the dispatched/winning candidate is ever re-observed). P2C over the *load* signal does not share that blindness, because in-flight count is live truth, not a memory.
- Under **burst** concurrency, Least Connections specifically degraded (its documented race window gets worse, not just theoretically but measurably); P2C-over-latency was the most robust policy of all five.
- Under **failure**, the health/routing architectural separation held perfectly for every policy — but the transient error-rate comparison across policies was under-replicated (one trial each), and is reported as such rather than dressed up as a ranking.

## 5. What Surprised Us

In order of how much they changed our understanding:

1. **EWMA's greedy lock-in.** Three independent runs of three *provably identical* 1ms edges produced 94/4/2, then 68/29/3, then 18/79/3 — a different "winner" every time, never anything close to 33/33/33. This is the textbook multi-armed-bandit "greedy exploitation without exploration" failure, and we had not anticipated it at all going into Stage 3; the original plan assumed EWMA would simply be "the good one."
2. **The alpha-sensitivity experiment (003-D, H4c) failed at its own stated purpose.** We designed it to compare how fast three different smoothing constants adapted to an oscillating edge. What we found instead was that the target's EWMA estimate never changed after the first phase, for any alpha — because the target had already lost the lock-in race and was never resampled again. The confound we were trying to control for (alpha) was dwarfed by a confound we hadn't anticipated (whether the target gets sampled at all).
3. **A unit test we wrote for P2C was wrong, and told us something true by failing.** We assumed P2C's random sampling would let a previously-losing target "recover" once its true latency improved. It doesn't — only the *winner* of a sampled pair is ever re-observed, so a target has to already be winning to keep being checked on. This reshaped the whole hypothesis for Experiment 003-E before any of it was run.
4. **P2C did not achieve 3-way fairness among equal targets — it achieved 2-way fairness.** Every homogeneous-lock-in run pushed exactly one of three equal edges down to ~2-4%, while the other two split evenly near 47-50% each. We expected either "fixed" or "not fixed"; the actual result was a precise, mechanistically explicable partial fix.
5. **P2C-over-latency's post-failure behavior.** After a hard-killed edge was correctly excluded, P2C-over-latency's traffic swung from a balanced 3-way split to 95.5% on a single survivor — a *more* extreme lock-in than before the failure, not just "avoided the dead edge." We do not fully understand this and said so explicitly rather than force an explanation.
6. **A measurement bug taught us something about our own benchmark harness.** Investigating why correctly-weighted WRR only tripled throughput instead of approaching the homogeneous baseline (003-B) led us into `internal/httpx/benchmark.go`: wall-clock RPS is bounded by the *slowest* worker goroutine, not the average, so a small number of slow requests landing on a small number of workers can disproportionately hurt the aggregate number in a way that has nothing to do with routing quality.

## 6. Why the Results Happened (Mechanistic Interpretation)

The single unifying mechanism behind items 1, 2, 3, and 4 above: **a target that stops being selected stops being observed.** Every stateful Stage 3 policy updates its per-target knowledge only in reaction to requests it actually dispatches. Full-scan argmin (EWMA, Least Connections at low concurrency) needs to beat *every* rival to keep being dispatched, so one early loss is permanent. Random-pair sampling (P2C) only needs to beat *one* randomly chosen rival, so an equal target keeps getting occasional chances to refresh itself — weakening the lock-in but not eliminating it, since a target still needs to win *something* to stay fresh. And no sampling strategy changes what happens for a signal that is fundamentally a memory of the past (latency) rather than a live read of the present (in-flight load) — a target that stops winning stops being sampled, so a latency-based policy has no path back to "try it again and see," while a load-based one is automatically self-correcting because 0 in-flight is always true regardless of history.

## 7. What Changed in Our Understanding

Going in, we treated "more state-aware" as roughly synonymous with "better." Coming out, the operative distinction is not *how much* information a policy uses, but *what kind*: a **count** (RR) needs no state and is perfectly fair but blind to capacity; a **weight** (WRR) encodes capacity but goes stale the moment reality changes; a **load** signal (Least Connections, P2C-load) is self-correcting because it reflects the present, but needs sustained concurrency to reveal anything at all; a **latency memory** (EWMA, P2C-latency) is the most direct signal for "is this target actually slow," and simultaneously the one most prone to permanent blindness once it stops being checked. None of the five policies built this stage is a general-purpose answer — each one is a defensible answer to a specific, now-evidenced question, and a bad answer to at least one other question that mattered in this stage's own experiments.

## 8. What This Motivates Next

Every prior stage's exit doc pointed at the next stage as an obvious consequence of a single dominant finding (002-D's static-routing collapse motivated all of Stage 3). Stage 3's evidence is more textured: no single experiment result cleanly "requires" the six-signal adaptive router the way 002-D required dynamic routing. What the evidence actually shows is a set of specific, non-overlapping gaps — WRR's staleness, Least Connections' need for real concurrency, EWMA and latency-scored P2C's shared blindness to recovery, load-based scoring's slowness to detect purely-latency-driven problems, and P2C's only-partial fairness fix — each pointing at a different missing capability. That is precisely the shape of evidence the project's stated philosophy asks for before building a combined multi-signal policy: not "adaptive routing sounds sophisticated," but "here are five independently reproduced reasons no single signal suffices, and here is what each one is missing." The six-signal adaptive router (Stage 7, per `trd.md`) is the first architecture in this project actually earned by evidence rather than assumed from the roadmap — though Stage 3 does not build it; that evidence is simply now on the record for whichever stage picks it up next, alongside two concrete, un-actioned methodology notes: Experiment 003-F's failure-scenario error-rate comparison needs replication before it supports any claim, and P2C's post-failure lock-in behavior deserves its own dedicated experiment.
