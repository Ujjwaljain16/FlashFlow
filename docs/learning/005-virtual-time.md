# Stage 5 Learning Notes — Virtual-Time Engine & Deterministic Reproducibility

## Before Stage 5

Every experiment through Stage 4 ran against real wall-clock time, real HTTP, and (for network degradation) a real or simulated network layer. That gave genuine fidelity, but Stage 4 alone needed a growing pile of one-off concessions to stay controllable: `clock.MockClock` to force deterministic TTL expiry, a pre-reserved fixed local port so an origin could be stopped and restarted deterministically, and an artificial edge-side delay whose only job was widening a race window so a concurrent burst would reliably coalesce instead of racing past the leader registration. Each fix was reasonable alone. Stacked together, they were the concrete, measured cost of continuing to answer timing- and ordering-sensitive questions against real time and real networking — a cost that would only grow with every future experiment needing the same kind of control.

## Why Virtual Time?

Stages 1–4 gave four separate, concrete reasons, not an abstract argument:

- Stage 1: connection behavior and benchmark state have measurable timing that real scheduling noise obscures.
- Stage 2: health transitions depend on time; real process behavior (starting/stopping real servers) adds noise on top of the signal being measured.
- Stage 3: stateful routing policies react differently depending on event ordering and concurrency — Experiment 003-D's EWMA lock-in produced a *different* 94/4/2, 68/29/3, 18/79/3 split on three separate real runs of provably identical targets, because real goroutine scheduling decided which target happened to be sampled first.
- Stage 4: cache expiry, stampedes, coalescing, and failure/recovery are all fundamentally about *when* things happen relative to each other, and 004-C's own stampede needed `clock.MockClock` specifically because a real TTL race is a genuine race — reproducing it on demand isn't possible without controlling time directly.

The central question Stage 5 exists to answer: can the same experiment, given the same configuration and seed, produce the same execution history — not "close," not "similar," the same — while running fast enough that experiments become cheap to repeat rather than expensive to reproduce?

## Initial Design

The design started from the smallest useful piece and added only what a concrete next requirement demanded, per the "earn the abstraction" rule:

1. Extend `clock.MockClock` (already existing since Stage 4) with `AdvanceTo` — jump to an absolute time rather than step by a relative duration, and reject moving backward outright.
2. A pure, clock-agnostic `EventQueue`: ordered by `(timestamp, insertion sequence)`, no notion of "now," no execution — just a deterministic priority structure, independently testable before anything used it.
3. `Engine`: owns a clock and a queue privately, drives them by popping the earliest event, advancing to its exact timestamp, and executing its callback. Two run modes only (`RunUntilEmpty`, `RunUntil(t)`) — not every mode the design document sketched, because nothing yet needed more.
4. `Trace`: minimal recorded history (time, sequence, type, entity, flat fields) — enough to compare two runs, not the final canonical event stream a later stage's telemetry work will want.
5. `Ticker`: added only once health probing (005-D) actually needed recurring work, not speculatively.

Each of these was tested in isolation before the next was built on top of it — the event queue had 12 tests proving ordering, same-timestamp determinism, and cancellation before `Engine` ever touched it.

## Problems Encountered

**The Phase A/B audit changed the scope of the whole stage before any engine code was written.** An audit of every `time.Now`/`time.Sleep`/`time.After`/`time.NewTimer`/`time.NewTicker` in `internal/` found that Stage 2–4's discipline of injecting `clock.Clock` rather than reaching for `time.Now()` directly had already made the domain state machines — `health.Registry`, `cache.Cache`, `proxy.LatencyTracker`/`LoadTracker`, every `TargetSelector` implementation — clock-agnostic. What's genuinely wall-clock-bound is strictly the real-I/O scheduling layer (`health.Checker`'s ticker and HTTP probing, `EdgeServer`/`OriginServer`'s `time.Sleep`, `netsim.Transport`'s `time.After`). This meant Stage 5's actual job was building an engine to drive already-portable domain logic, not rewriting that logic — a much smaller and more tractable task than "migrate everything to virtual time" would have implied.

**A units bug in `Ticker`'s own test caught a real ambiguity before it could hide anywhere else.** The first version of `TestTicker_FiresAtStartThenEveryInterval` mixed a raw `VirtualTime(100)` (100 nanoseconds) start with millisecond-scaled expected fire times. The ticker's actual arithmetic (`start + n*interval`) was correct the whole time; the test's expectations were wrong. Fixed by using consistent millisecond-scaled values throughout — a small thing, but a reminder that `VirtualTime`'s raw-nanosecond representation makes unit mistakes easy to write and easy to overlook if a test's assertions aren't double-checked against the same units the code uses.

**005-E's EWMA cell sent 100% of traffic to the slow target — the opposite of a latency-aware policy's whole point.** Rather than report that as a finding, the actual event ordering was traced by hand (arrival sequence numbers versus dynamically-scheduled completion sequence numbers) to confirm `EWMASelector`'s cold-start logic *should* have switched targets. That confirmed the bug was in the experiment, not the selector: a helper (`runCell`) was constructing a fresh, throwaway `LatencyTracker` instead of using the exact instance the selector had been built with, so the selector was permanently reading a tracker `Observe` was never called on. Fixed by passing the same tracker instance through explicitly. This is the same class of mistake as Stage 4's 004-A cache-stats bug: a plausible-looking wrong number, caught by checking the result against what the code's own logic predicted rather than trusting the first output.

**005-G's probe `Ticker` would have spun forever if the scenario used `RunUntilEmpty`.** A recurring ticker that's never told to stop keeps rescheduling itself indefinitely; without a fixed horizon, `RunUntilEmpty` would have hit the max-event guard and returned an error instead of completing. Caught before running anything, by reasoning about what "the traffic ends but the ticker doesn't know that" actually implies — fixed by using `RunUntil(2s)`, a horizon comfortably past the latest possible completion.

## Event Model

The fundamental mechanism, exactly as designed: pop the earliest event, advance the clock to exactly that event's timestamp, execute its callback (which may schedule more events, never earlier than the time just reached), repeat. Same-timestamp events are ordered by insertion sequence — not Go map iteration, not goroutine scheduling, a documented and tested rule. This is not merely an implementation detail: 005-D and 005-G both deliberately scheduled events to land on the identical virtual timestamp as a scheduled probe tick, and both produced a specific, mechanistically explainable answer (which event's sequence number was lower, and why) rather than an arbitrary one. `Engine` itself has almost no concurrency — the whole loop runs on one goroutine, per the design's explicit "simulated concurrency, not real concurrency" principle. Overlapping requests are represented as overlapping start/complete event pairs, never as real goroutines.

## Determinism

Tested by actually running scenarios repeatedly and comparing, never by asserting the design should provide it: 005-B ran an identical scenario 50 times and compared full traces via `reflect.DeepEqual`; 005-D ran 20 times; 005-G — the richest scenario, composing cache, routing, and failure/recovery together — ran 20 times and compared the *entire* 379-event trace across all six recorded event types, not just aggregate summaries. All were byte-for-byte identical, every time, across every run performed in this stage. No divergence was ever observed that needed chasing down — a result of building each layer (queue, engine, trace) with its own isolated test suite before composing them, not evidence that determinism bugs can't happen.

## Seed Strategy

`P2CSelector`'s Stage 3 design — an explicitly injected `*rand.Rand`, never `math/rand`'s global state — turned out to already be sufficient for full reproducibility under virtual time (005-F: same seed reproduces the exact 300-decision sequence; different seed diverges at request #0; the workload itself never varies regardless of seed). No hierarchical seed tree (separate traffic/routing/failure seeds) was built, because nothing in this stage's experiments has a second source of randomness that would need decoupling from P2C's. Building that structure now would have been speculative, not earned — the foundation (one seed, one component, fully reproducible) is in place for a future stage to extend if a combined scenario ever needs decoupled randomness.

## Virtual Cache

`cache.Cache` — Stage 4's own type — ran the canonical insert/hit/expire/miss/refill/hit scenario (005-C) with **zero source changes**, driven entirely by `Engine.Clock()`. The 150ms-virtual scenario, TTL expiry included, cost an unmeasurable fraction of a real millisecond. This is the cleanest possible confirmation that Stage 4's clock-injection discipline wasn't just tidy engineering — it was the specific decision that made this experiment nearly free to write.

## Virtual Failure

`health.Registry`'s 4-state machine — Stage 2's design — also ran unmodified. Only the *scheduling* mechanism changed: `health.Checker`'s real ticker and real HTTP probing were replaced by a virtual `Ticker` consulting a ground-truth up/down schedule kept deliberately separate from the registry's own observed state, mirroring the real system's actual asymmetry (the registry only ever learns through probes, never has direct access to truth). 005-D reproduced a full HEALTHY→UNHEALTHY→RECOVERING→HEALTHY cycle at exact, arithmetically-predictable virtual timestamps, identically across 20 runs. 005-G then showed why the composition matters: with routing in the loop, "the router kept sending traffic to a target for 100ms after it broke" becomes a measured, quantified consequence of the probe interval and fail threshold — not an assumption about how the pieces would interact once combined.

## Routing

All four Stage 3 policies tested (Round Robin, Least Connections, EWMA, P2C-over-load) ran correctly under purely simulated concurrency, with zero modification to `internal/proxy`. The one friction point — `TargetSelector.SelectTarget` wanting a real `*http.Request` — was resolved with a single shared, documented placeholder value, since no existing selector reads that parameter; re-confirming Stage 3's own finding that the interface didn't need to evolve. 005-E reproduced Stage 3's headline EWMA finding (pure-greedy lock-in among equal-latency targets) with full causal traceability this time — exactly which target won the "unobserved beats observed" race and why, rather than Stage 3's own "we don't fully understand this" caveat on a related result.

## Real vs Virtual

005-H compared 004-C's cache-stampede scenario in both engines at matched configuration (C=10/30/100). Upstream request count matched exactly in both directions at every concurrency level — the stampede's structural property depends only on the absence of coalescing, not on which engine executes it. Latency did not match, in the direction predicted before running anything: real p99 grew from 102.9ms to 115.4ms as C increased (genuine OS/goroutine scheduling contention, present even under Origin's own already-simplified infinite-server model), while virtual held flat at exactly 100.0ms throughout, since the virtual model assigns every request the same fixed service time with no capacity or contention representation at all. Neither number is wrong; the gap is exactly the abstraction boundary `internal/vtime` was built with, made visible by design rather than discovered as a surprise.

## Surprises

In order of how much they changed the plan:

1. **How little of the domain layer needed to change.** Going in, "migrate the domain logic to virtual time" sounded like it could touch every package. It touched none — `cache.Cache`, `health.Registry`, and every `proxy` selector needed zero source modifications. Stage 5's actual work was entirely new (the engine) or entirely additive (one `IsAvailable` filter in 005-G).
2. **005-E's EWMA bug looked exactly like a real, reportable finding at first.** "100% of traffic went to the slow target" is a plausible-sounding headline. Only tracing the actual mechanism by hand — before writing a single word about it — revealed it was a wiring bug in the experiment, not a discovery about the selector.
3. **Same-timestamp resolution wasn't just consistent, it was explainable.** 005-D and 005-G's ties both resolved in the intuitive direction (a ground-truth change "at" a probe's timestamp is seen by that probe) for a specific, statable reason (one-shot setup events carry lower sequence numbers than dynamically-rescheduled recurring ones) — not a coincidence that happened to look right.
4. **The virtual/real latency gap tracked the real engine's own growth almost exactly.** The prediction was directional ("virtual should show less growth"); the actual gap (0ms at C=10, ~15ms at C=100) matched the real engine's own measured growth curve closely enough that it read as validation of the *mechanism* (real scheduling contention), not just the direction.

## Limitations

Stated plainly, matching the project's practice of naming a model's boundary rather than hiding it:

- The virtual network/service model is intentionally flat: a fixed service time per request, no queueing, no finite capacity, no packet-level anything. 005-H's whole result depends on this being true.
- No hierarchical seed tree exists yet — only P2C's single injected `*rand.Rand` has been proven, because nothing built this stage needed more.
- Request coalescing was never ported to the virtual engine. 005-G explicitly does not exercise it, and says so — multiple concurrent misses for the same key each independently dispatch, exactly like the pre-004-D real edge.
- No workload distribution beyond fixed, deterministic arrival schedules was built. Poisson, heavy-tail, and flash-crowd generators remain unimplemented, since nothing in this stage's experiments needed them to demonstrate determinism.
- `ExperimentEngine`-style `Prepare`/`Run`/`Replay` abstraction, a formal experiment manifest schema, and content-addressable experiment identity were all left unbuilt — Stage 5 established the underlying mechanism (clock, queue, engine, trace) these would eventually sit on top of, not the packaging around it.

## Stage 6 Motivation

Stage 5 produced exactly what its own design promised: reproducible event traces, several of them now spanning hundreds of events, generated in a fraction of a real second and provably identical across repeated runs. But a trace is not an analysis. 005-H already surfaced a quantified gap between two engines' latency distributions without any way to say whether a *smaller* gap on some other scenario would be meaningful or just noise; 005-E's EWMA lock-in (94/4/2 in one Stage 3 run, 274/5/21 in this stage's own reproduction) invites a real statistical question — how much of that split is mechanism versus how much is sensitive to exactly which target happens to win the first race — that this stage has no tool to answer. Once traces are cheap and reproducible, the natural next question stops being "did this run happen the way we expected" and becomes "is the difference between two runs statistically meaningful, and by how much" — which is exactly Stage 6's mandate: statistics and queueing attribution, built on top of the deterministic execution machinery this stage exists to provide.
