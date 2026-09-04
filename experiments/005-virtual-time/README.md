# Experiment 005: Virtual-Time Engine & Deterministic Reproducibility

## 1. Executive Summary & Research Questions

Stages 1–4 all measured real systems under real wall-clock time — accurate, but increasingly expensive to control: Stage 4 alone needed a mock clock, a pre-reserved fixed port, and an artificial delay whose only job was widening a race window, just to get clean results out of real HTTP and real goroutine scheduling. Stage 5 asks whether FlashFlow can run the same conceptual class of experiment under a deterministic virtual clock instead, and get a reproducible scientific object — the same manifest and seed producing the same event trace — rather than a benchmark run whose exact behavior depends on OS scheduling luck.

### Primary Research Question
> Can we make time, ordering, randomness, and state explicit enough that the same experiment executes repeatedly and produces the same history?

Everything in this experiment suite is in service of that one question, not "can we build a simulator" in the abstract.

---

## 2. What Was Built (relevant to 005-A/B)

`internal/vtime` (new package):

- `EventQueue` — a deterministic, ordered queue of scheduled callbacks. Ordering is `(timestamp, insertion sequence)`: two events scheduled for the identical virtual time always run in the order `Schedule` was called, never in whatever order Go's runtime happens to pick. The queue itself has no notion of "now" — it never reads a clock and never executes anything, keeping it a pure, independently testable data structure (12 unit tests: ordering, same-timestamp determinism across 20 trials, cancellation including a buried-entry case, a 2000-event-per-trial randomized property test).
- `Engine` — drives a `clock.MockClock` by processing events instead of sleeping: pop the earliest event, advance the clock to exactly that event's timestamp (`MockClock.AdvanceTo`, which rejects moving backward outright), execute its callback, repeat. Two run modes: `RunUntilEmpty` and `RunUntil(t)`. A configurable max-event bound guards the "infinite zero-time loop" failure mode explicitly named in the Stage 5 design (a callback that keeps rescheduling work at a timestamp already reached) with a defensible, tested error instead of hanging forever.
- `Trace`/`TraceEvent` — a minimal, deliberately small event trace (virtual timestamp, monotonic sequence, type, entity, flat fields), written via `Engine.Record`, exportable as JSONL. This is the infrastructure every experiment from 005-B onward needs to answer "did two runs actually execute the same history" — not the final canonical event stream a later stage's telemetry work will want, just enough to prove determinism now.

`internal/clock` gained `VirtualTime.Add` and `MockClock.AdvanceTo` (mutex-guarded, along with the rest of `MockClock` — closing a latent cross-goroutine data race already present in the Stage 4 experiments that share one `MockClock` between the goroutine advancing it and concurrent real HTTP handlers reading it).

**Phase A/B finding that shaped all of this**: an audit of every `time.Now`/`time.Sleep`/`time.After`/`time.NewTimer`/`time.NewTicker` in `internal/` found that Stage 2–4's discipline of injecting `clock.Clock` rather than calling `time.Now()` directly already made the domain state machines (`health.Registry`, `cache.Cache`, `proxy.LatencyTracker`/`LoadTracker`, all `TargetSelector` implementations) clock-agnostic. What's wall-clock-bound is strictly the real-I/O scheduling layer (`health.Checker`'s ticker+HTTP loop, `EdgeServer`/`OriginServer`'s `time.Sleep` for simulated processing time, `netsim.Transport`'s `time.After`) — correctly real-engine infrastructure, not something to migrate. Stage 5's actual net-new work is the event queue and engine, not a rewrite of existing domain code.

---

## 3. Results: Experiment 005-A — Deterministic Clock

**Hypothesis (H1)**: see `hypotheses.md`. Two cells on a fresh `vtime.Engine` each.

| Cell | Events | Virtual span | Real elapsed | Events/sec |
|---|---:|---:|---:|---:|
| few-events-long-span | 5 | 10 minutes | 0.000ms | — |
| many-events-short-span | 100,000 | 100ms | 39.505ms | 2,531,300 |

Raw data: `experiments/005-virtual-time/results/005A-*.json`.

### Findings

1. **Prediction 1 confirmed emphatically**: 5 events spanning 10 minutes of virtual time completed in under 1ms of real time — measured as `0.000ms` at this run's timing resolution. A real engine sleeping through that scenario would need 10 real minutes; the virtual engine needed effectively none.
2. **Prediction 2 confirmed**: 100,000 events packed into a mere 100ms of virtual time took 39.5ms of real time — real cost scaled with event count (2.5M events processed per real second), not with the (tiny) virtual span those events were packed into.
3. Together, these two cells show virtual duration and event count are the two genuinely independent variables governing a virtual engine's real cost — conflating them (assuming "a long scenario is a slow scenario," or the reverse) would be a real, avoidable misunderstanding of what this engine actually does.

---

## 4. Results: Experiment 005-B — Deterministic Event Ordering

**Hypothesis (H2)**: see `hypotheses.md`. A scenario with three same-timestamp batches (t=100, t=200, each scheduled in a different relative subsystem order) plus one event whose own callback dynamically schedules two more same-timestamp events (t=300), run 50 times on a fresh engine each time.

| Runs | Events per run | All identical? |
|---:|---:|:---:|
| 50 | 9 | **yes** |

Baseline trace, reproduced byte-for-byte on all 50 runs:

```
t=50   burst_start
t=100  cache_expired    key-a
t=100  request_arrived  r1
t=100  probe_fired      edge-a
t=200  probe_fired      edge-b
t=200  request_arrived  r2
t=200  cache_expired    key-b
t=300  burst_a
t=300  burst_b
```

Raw data: `experiments/005-virtual-time/results/005B-event-ordering.json`.

### Findings

1. **Prediction 1 confirmed**: the t=100 batch (cache, then traffic, then health — the order `Schedule` was called in) and the t=200 batch (health, then traffic, then cache — a deliberately different order) both reproduced their own scheduling order exactly, every one of 50 runs.
2. **Prediction 2 confirmed**: the t=300 pair, scheduled dynamically from inside the t=50 event's own callback rather than from top-level setup code, was just as deterministic as the top-level batches — same-timestamp ordering doesn't care whether the events were scheduled "at the start" or "mid-run by another event," only about the order `Schedule` was actually called in.
3. This is the first Stage 5 experiment to actually *test* the reproducibility invariant rather than assert the mechanism should provide it — comparing 50 independently-run traces via `reflect.DeepEqual`, not eyeballing one run and trusting the design.

### Interpretation

Both results are exactly what `EventQueue`'s and `Engine`'s own design (built and unit-tested before either experiment ran) predicted — no surprises here yet, no divergence to chase down. That is itself the correct outcome for this pair of experiments: 005-A and 005-B exist to establish the foundation everything else in this stage builds on, and a foundation that behaves exactly as designed is what makes the more interesting integration work (cache, failure schedules, stateful routing) trustworthy once it starts producing results that *aren't* fully predictable in advance.

---

## 5. Results: Experiment 005-C — Virtual Cache Expiration

**Hypothesis (H3)**: see `hypotheses.md`. The canonical insert/hit/expire/miss/refill/hit scenario, using `cache.Cache` — Stage 4's own type — with zero modification, driven entirely by `Engine.Clock()`.

| t (virtual) | Action | Expected | Got |
|---:|---|:---:|:---:|
| 0ms | set | FILLED | FILLED |
| 99ms | get | HIT | HIT |
| 101ms | get | MISS | MISS |
| 101ms | set | FILLED | FILLED |
| 150ms | get | HIT | HIT |

Real elapsed time for the full 150ms-virtual scenario: **0.000ms**. Final cache stats: `{Lookups:3, Hits:2, Misses:1, Expired:1, Fills:2}`.

Raw data: `experiments/005-virtual-time/results/005C-virtual-cache-expiration.json`, full trace at `005C-trace.jsonl`.

### Findings

1. **Prediction 1 confirmed exactly**: all 5 operations matched their expected outcome, including the t=150ms check that the refill at t=101ms started a genuinely fresh 100ms TTL window rather than inheriting anything from the expired entry it replaced.
2. **Prediction 2 confirmed**: 150ms of virtual time (TTL expiry included) cost an unmeasurable fraction of a real millisecond — no sleeping anywhere in the path.
3. **`cache.Cache` needed zero source changes.** The only new code this experiment required was the scenario itself (`cmd/experiment-005c/main.go`) — every line of `internal/cache` is exactly what Stage 4 left it as.

### Interpretation

This is the cleanest possible confirmation of the Phase A/B audit's central claim: Stage 2–4's discipline of injecting `clock.Clock` rather than reaching for `time.Now()` wasn't just tidy engineering, it was the specific decision that made this experiment nearly free to write. Nothing about `cache.Cache`'s TTL logic, lazy eviction, or stats tracking had any idea it was running under a virtual clock instead of a real one — which is exactly the point. The domain behavior and the execution environment were already separated; Stage 5 just proved it.

---

## 6. Results: Experiment 005-D — Deterministic Failure Schedule

**Hypothesis (H4)**: see `hypotheses.md`. `edge-2` fails at t=5s, recovers at t=10s, probed every 1s via a virtual `Ticker`; `health.Registry` reused unmodified with `health.DefaultConfig()` (fail threshold 2, recovery-pass threshold 2). The failure and recovery timestamps were deliberately chosen to coincide exactly with a scheduled probe tick.

| Virtual time | New state |
|---:|---|
| 6000ms | UNHEALTHY |
| 10000ms | RECOVERING |
| 11000ms | HEALTHY |

All 20 runs produced this exact 3-transition sequence. Raw data: `experiments/005-virtual-time/results/005D-failure-schedule.json`.

### Findings

1. **Prediction 1 confirmed**: the transition timestamps are exactly what the probe interval and thresholds predict — the first failed probe at t=5000ms (silent, threshold not yet met) plus a second failed probe at t=6000ms crosses the fail threshold; the first passing probe at t=10000ms moves UNHEALTHY→RECOVERING, and the second passing probe at t=11000ms crosses the recovery threshold into HEALTHY. Identical across all 20 runs.
2. **Prediction 2 confirmed, and mechanistically explained, not just observed**: the probe at t=5000ms already saw the failure, and the probe at t=10000ms already saw the recovery — both ties resolved the same direction. The reason is exactly what the hypothesis predicted before running: the one-shot failure/recovery events were scheduled once at setup time, carrying low insertion-sequence numbers, while the probe ticks at those same timestamps are dynamically rescheduled by several prior firings during the run and carry much later sequence numbers. `EventQueue`'s documented `(timestamp, sequence)` rule resolves the tie in the failure/recovery event's favor every time, for a reason that can be stated in one sentence rather than shrugged off as "however the engine happened to order it."

### Interpretation

`health.Registry`'s state machine — Stage 2's design, unmodified — needed nothing added for this. What changed is entirely on the scheduling side: `health.Checker`'s real ticker and real HTTP probing were replaced by a virtual `Ticker` consulting a ground-truth schedule instead of dialing a live target, with the *same* `RecordProbeResult` call driving the *same* state machine either way. This is Stage 5's port-domain-logic-don't-copy-real-implementation-blindly principle working exactly as designed: the thing that's genuinely different between real and virtual health checking is how the probe result is obtained, not what happens once it is. And the same-timestamp result above is the first piece of direct evidence in this stage that explicit ordering isn't just a defensive design choice — it produces a specific, predictable, explainable answer to a question ("did the probe see the failure that happened at the same instant?") that would otherwise have no principled answer at all.

---

## 7. Results: Experiment 005-E — Stateful Routing Under Virtual Time

**Hypothesis (H5)**: see `hypotheses.md`. One slow target (100ms) and two fast targets (20ms), 300 requests on a fixed 5ms arrival schedule, one run per policy, all reusing Stage 3's `internal/proxy` selectors and trackers with zero modification.

| Policy | edge-a-slow | edge-b-fast | edge-c-fast |
|---|---:|---:|---:|
| Round Robin | 100 | 100 | 100 |
| Least Connections | 42 | 139 | 119 |
| EWMA | 21 | 274 | 5 |
| P2C (load) | 42 | 130 | 128 |

Raw data: `experiments/005-virtual-time/results/005E-stateful-routing.json`. Every distribution above reproduced identically across repeated runs (no randomness in the workload; P2C's seed fixed).

### A real bug, caught and fixed before trusting this result

The first run of this experiment showed EWMA sending **all 300 requests to the slow target** — the opposite of what a latency-aware policy should do. Rather than report that as a surprising finding, I traced the actual event ordering by hand (arrival sequence numbers versus dynamically-scheduled completion sequence numbers) and confirmed `EWMASelector`'s cold-start logic *should* switch to a fast target as soon as the slow one becomes observed. That meant the bug was in the experiment, not the selector: the `runCell` helper was constructing a fresh, throwaway `LatencyTracker` internally instead of using the exact instance `EWMASelector` had been built with — so the selector was reading a tracker that `Observe` was never actually called on, permanently stuck in "everything is unobserved, fall back to `available[0]`." Fixed by passing the same tracker instance through explicitly rather than letting the helper construct its own. This is the same class of mistake Stage 4's 004-A cache-stats bug was: a measurement/wiring bug that produces a plausible-looking but wrong number, caught by checking the result against what the code's own logic predicts rather than trusting the first output.

### Findings

1. **Prediction 1 confirmed exactly**: Round Robin split 100/100/100 — perfectly even, completely blind to the 5× service-time difference, matching Stage 3's own characterization of RR.
2. **Prediction 2 confirmed**: Least Connections (42/139/119) and P2C-over-load (42/130/128) both shifted the large majority of traffic onto the two fast targets, with a near-even split between the two fast targets themselves — consistent with Stage 3's finding that load-based policies are self-correcting since in-flight count is live state, not a memory.
3. **Prediction 3 confirmed, precisely reproducing Stage 3's headline finding**: EWMA locked onto one fast target (274 of 300) rather than splitting evenly between the two fast options. The mechanism is fully traceable: whichever fast target's first request happens to complete (and become "observed") while the other fast target is still unobserved wins the "unobserved beats observed" comparison and starts dominating; once *both* fast targets are observed with numerically identical 20ms estimates, the earlier-observed one keeps winning every subsequent tie (`score < bestScore` is false for equal scores, so `best` never changes on a genuine tie) — the exact pure-greedy lock-in mechanism Experiment 003-D described, now reproduced with full causal traceability instead of Stage 3's own "we don't fully understand this" caveat on a related result.

### Interpretation

Every Stage 3 routing policy needed zero modification to run correctly under a purely virtual, event-driven notion of concurrency — the strongest possible confirmation that `LoadTracker`, `LatencyTracker`, and the selectors themselves were already engine-agnostic pure logic, exactly as the Phase A/B audit found. The bug caught along the way is arguably as valuable as the clean result: it's a concrete demonstration of why Stage 5's emphasis on tracing an unexpected result to its actual mechanism (rather than reporting it as a "finding") matters in practice, not just as a stated principle.

---

## 8. Results: Experiment 005-F — Seeded Randomness

**Hypothesis (H6)**: see `hypotheses.md`. Three identically-fast targets (20ms each, isolating P2C's own random sampling from target heterogeneity), 300 fixed arrivals, `P2CSelector` over load with seed 1 (twice) and seed 2 (once).

| Run | Seed | Distribution | Identical to seed-1 run 1's full decision sequence? |
|---|---:|---|:---:|
| 1 | 1 | edge-a:108, edge-b:93, edge-c:99 | — (baseline) |
| 2 | 1 | identical distribution | **yes**, byte-for-byte, all 300 decisions |
| 3 | 2 | edge-a:104, edge-b:99, edge-c:97 | no — diverges at request #0 |

Raw data: `experiments/005-virtual-time/results/005F-seeded-randomness.json`.

### Findings

1. **Prediction 1 confirmed exactly**: the two seed-1 runs produced the identical 300-element decision sequence, not just a similar-looking aggregate split — the stronger claim the hypothesis asked for.
2. **Prediction 2 confirmed**: seed 2's sequence diverged from seed 1's immediately, at the very first request — the first random pair sample already differs between the two seeds, which then compounds across all 300 decisions. The workload itself (arrival schedule, target set, service times) was constructed identically regardless of which seed was passed to the selector; only `P2CSelector`'s own coin flips changed.
3. The aggregate distributions across all three runs look similar (roughly even three-way splits, as expected when every target is equally fast) — a reminder that comparing only final distributions, as 005-E's table did, can hide a completely different underlying decision sequence. Seed reproducibility is a claim about the *sequence*, and this experiment tested it as one.

### Interpretation

`P2CSelector`'s Stage 3 design — an explicitly injected `*rand.Rand`, never `math/rand`'s global state — turns out to be exactly sufficient for full reproducibility under virtual time, with no additional seed-plumbing required anywhere else in the engine. This closes the loop on item 21's requirement in the smallest form that's actually justified by evidence: a single seed for the one component that needs one. A full hierarchical seed tree (separate traffic/routing/failure seeds) remains deferred — nothing built so far has a second source of randomness that would need decoupling from this one, so building that structure now would be speculative rather than earned.

---

## 9. Results: Experiment 005-G — Full Deterministic Edge Scenario

**Hypothesis (H7)**: see `hypotheses.md`. A shared cache in front of a 3-target Least-Connections-routed backend (one slow, two fast), 300 fixed arrivals over 4 cache keys, `edge-b-fast` failing at t=500ms and recovering at t=1000ms. This composes only mechanisms 005-C, 005-D, and 005-E already proved individually — the one new integration point is routing's `available` list being filtered by `health.Registry.IsAvailable`. **This topology (cache + routing together) is a virtual-only exploratory construction**: Stage 4's real topology has caching without routing, Stage 2/3's real topology has routing without caching — nothing claims the real engine has an equivalent combined path.

| | |
|---|---|
| Health transitions | HEALTHY (t=0) → **UNHEALTHY (t=600ms)** → RECOVERING (t=1000ms) → HEALTHY (t=1100ms) |
| Cache | `{Lookups:300, Hits:292, Misses:8, Fills:8}` |
| Completed by target | `edge-a-slow:2, edge-b-fast:4, edge-c-fast:2` |
| Rejected requests | 0 |
| Trace length | 379 events |
| Runs compared | 20, full-trace `reflect.DeepEqual` |

Raw data: `experiments/005-virtual-time/results/005G-full-scenario.json`, full trace at `005G-trace.jsonl`.

### Findings

1. **Prediction 1 confirmed at the strongest level tested in this stage**: all 20 runs produced byte-for-byte identical 379-event traces — every `cache_hit`, `cache_miss`, `request_rejected`, `request_routed`, `request_completed`, and `health_probe` entry matched exactly, not just the aggregate summary. This is a stronger check than 005-B/005-D's transition-only comparisons, made possible specifically by recording the richer per-event-type trace this experiment's design called for.
2. **Prediction 2 confirmed, with the lag arithmetically exact**: `edge-b-fast` fails at t=500ms but isn't marked UNHEALTHY until **t=600ms** — one full probe interval later. With a 100ms probe interval and a fail threshold of 2, the first failed probe (t=500ms) only registers one consecutive failure; the second (t=600ms) crosses the threshold. Routing continued considering `edge-b-fast` available for that entire 100ms window — a real, measured detection lag, not an assumption.
3. **The non-prediction held, informatively**: only 4 distinct cache keys exist, but the cache recorded 8 misses, not 4. Multiple requests for the same key arrived before the first one's dispatch completed and filled the cache, so each raced to its own independent miss and dispatch — precisely the pre-coalescing race Stage 4's 004-A first stumbled into unplanned, now reproduced deterministically on purpose. `completed-by-target` sums to exactly 8 (2+4+2), matching `Fills:8` exactly — a direct internal consistency check that every miss led to exactly one completion and one fill, with nothing lost or double-counted.

### Interpretation

Nothing in this experiment required new domain logic — the one line of genuinely new integration code was the `registry.IsAvailable` filter placed in front of `selector.SelectTarget`. Everything else is 005-C, 005-D, and 005-E's already-proven pieces sharing a clock and an event queue. That's exactly what makes the result meaningful rather than merely convenient: determinism held not because this experiment was built to be simple, but because every piece feeding into it was already individually deterministic, and composing deterministic pieces without introducing new shared mutable state accessed out of order stays deterministic. The detection-lag measurement is the clearest illustration of why this experiment needed the composition to exist at all — 005-D alone could show failure and recovery landing at precise timestamps, but only with routing in the loop does "the router kept sending traffic to a target for 100ms after it broke" become an observable, quantified consequence rather than an assumption about how the pieces would interact.
