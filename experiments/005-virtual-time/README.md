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
