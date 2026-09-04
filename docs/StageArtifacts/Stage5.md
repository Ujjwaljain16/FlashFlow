# Stage 5 — Virtual-Time Engine & Deterministic Reproducibility: Exit Artifact

## What Was Built

| Component | File(s) | Description |
|---|---|---|
| Clock extension | `internal/clock/clock.go` | `VirtualTime.Add`; `MockClock.AdvanceTo` (jump to an absolute time, rejecting backward moves); `MockClock` mutex-guarded |
| Deterministic event queue | `internal/vtime/queue.go` | `EventQueue` — min-heap ordered by `(timestamp, insertion sequence)`; no notion of "now," no execution; a pure, independently testable data structure |
| Virtual execution engine | `internal/vtime/engine.go` | `Engine` — owns a clock and queue privately; `RunUntilEmpty`/`RunUntil(t)`; max-event guard against infinite zero-time loops |
| Recurring virtual timer | `internal/vtime/ticker.go` | `Ticker` — event-driven equivalent of `time.Ticker`; each firing reschedules itself, no real background goroutine |
| Deterministic trace | `internal/vtime/trace.go` | `Trace`/`TraceEvent` — minimal recorded history (time, sequence, type, entity, fields), JSONL export |
| Eight experiment suites | `cmd/experiment-005a` … `005h` | Clock, ordering, cache, failure/recovery, routing, seeded randomness, combined scenario, virtual-vs-real comparison |
| Experiment documentation | `experiments/005-virtual-time/{hypotheses,README}.md` | H1–H8, methodology, and full results for all eight experiments |
| Learning notes | `docs/learning/005-virtual-time.md` | Before/after, initial design, problems encountered, event model, determinism, seed strategy, surprises, limitations, Stage 6 motivation |

**No existing domain package required source changes.** `cache.Cache`, `health.Registry`, `proxy.LatencyTracker`/`LoadTracker`, and every `TargetSelector` implementation (RR, WRR, Least Connections, EWMA, P2C) ran under the virtual engine with zero modification — a direct, measured payoff of Stage 2–4's discipline of injecting `clock.Clock` instead of calling `time.Now()` directly, confirmed by an explicit audit before any engine code was written (see Architecture, below).

---

## Architecture

**Domain logic vs. execution environment**, the boundary this stage exists to make explicit:

| Layer | Examples | Wall-clock/real-I/O bound? |
|---|---|---|
| Domain state machines | `health.Registry`, `cache.Cache`, `proxy` trackers/selectors | No — clock-injected since Stage 2–4, unmodified this stage |
| Real-engine scheduling | `health.Checker` (ticker + HTTP), `EdgeServer`/`OriginServer` (`time.Sleep`), `netsim.Transport` (`time.After`) | Yes — legitimately, real I/O actually happens here |
| Virtual-engine scheduling | `vtime.Engine`, `vtime.Ticker` | No — event-driven, single goroutine, no real sleeping |
| CLI/benchmark instrumentation | `httpx/benchmark.go`, `tcp/client.go` | Yes — legitimately, measuring real performance is the point |

The audit that produced this table (Phase A/B of this stage's work) is the single most consequential finding of Stage 5: it meant the engine's job was to drive already-portable domain logic, not migrate it. The one exception worth naming: `TargetSelector.SelectTarget` takes a real `*http.Request`, unused by every existing selector — resolved with one shared, documented placeholder value rather than an interface change, re-confirming Stage 3's own finding that the interface didn't need to evolve.

`Engine` deliberately has almost no concurrency of its own — one goroutine, one loop, popping the earliest event and executing it. Overlapping "concurrent" requests are represented as overlapping start/complete event pairs (`LoadTracker.Increment` on arrival, `Decrement` on completion), never as real goroutines — the "simulated concurrency, not real concurrency" principle held throughout every experiment in this stage.

---

## Clock Design

`clock.Clock` remains `Now() VirtualTime` — unchanged. `WallClock` (real time) and `MockClock` (manually advanced) are the two implementations; `Engine` owns a `MockClock` privately and is the *only* thing that ever calls `Advance`/`AdvanceTo` on it, exposing a read-only `clock.Clock` view (`Engine.Clock()`) for domain objects to construct against. `AdvanceTo` — the one addition this stage needed — rejects moving backward outright rather than clamping silently, making a "time travel" bug an explicit error instead of silent corruption of an experiment's causal history.

---

## Event Model

Pop the earliest event, advance the clock to exactly that event's timestamp, execute its callback, repeat — callbacks may schedule more events, never earlier than the time just reached. Same-timestamp events are ordered by `(timestamp, insertion sequence)`, a documented, tested rule, not Go map iteration or goroutine scheduling. This ordering is scientifically load-bearing, not an implementation detail: 005-D and 005-G both deliberately scheduled a ground-truth change to land on the same virtual timestamp as a scheduled probe tick, and both produced a specific, mechanistically explainable resolution (a one-shot setup event's low sequence number beats a dynamically-rescheduled recurring tick's much later one) rather than an arbitrary one.

---

## Determinism

Tested by actually running scenarios repeatedly and comparing full recorded traces via `reflect.DeepEqual`, never by asserting the design should provide it:

| Experiment | Runs compared | What was compared |
|---|---:|---|
| 005-B | 50 | Full 9-event trace |
| 005-D | 20 | Health state transition sequence |
| 005-G | 20 | Full 379-event trace across 6 event types, plus cache/health/routing summary state |
| 005-F | 2 (same seed) + 1 (different seed) | Full 300-decision routing sequence |

Every comparison in this stage was byte-for-byte identical across repeated runs, with no divergence ever needing investigation — a consequence of testing each layer (queue, engine, trace) in isolation before composing them, not a claim that determinism bugs are impossible.

---

## Seed Strategy

`proxy.P2CSelector`'s existing Stage 3 design — an explicitly injected `*rand.Rand`, never `math/rand`'s global state — proved exactly sufficient for full reproducibility under virtual time (005-F). No hierarchical seed tree was built: nothing in this stage has a second randomness source that would need decoupling from P2C's, so building that structure now would have been speculative rather than earned. The foundation (one seed, fully reproducible, workload unaffected by which seed is used) is in place for a future stage to extend.

---

## Experiments

| # | Title | Central Finding |
|---|---|---|
| 005-A | Deterministic Clock | 5 events spanning 10 virtual minutes complete in <1ms real time; 100,000 events packed into 100ms virtual span take ~40ms real time (2.5M events/sec) — virtual duration and event count are independent variables |
| 005-B | Deterministic Event Ordering | Identical scenario run 50 times produces byte-for-byte identical traces, including same-timestamp events scheduled dynamically mid-run |
| 005-C | Virtual Cache Expiration | Stage 4's `cache.Cache`, unmodified, runs the canonical TTL scenario correctly; 150ms of virtual time costs an unmeasurable fraction of a real millisecond |
| 005-D | Deterministic Failure Schedule | Stage 2's `health.Registry`, unmodified, reproduces a full failure/recovery cycle at exact virtual timestamps, 20/20 runs identical; same-timestamp probe/failure ties resolve deterministically and explainably |
| 005-E | Stateful Routing | All four Stage 3 policies (RR, LC, EWMA, P2C) run correctly under simulated concurrency with zero code changes; a real experiment bug (a disconnected `LatencyTracker`) was caught and fixed before trusting the result; EWMA's Stage 3 lock-in finding reproduced with full causal traceability |
| 005-F | Seeded Randomness | Same seed reproduces the exact 300-decision routing sequence; different seed diverges at request #0; workload stays identical regardless of seed |
| 005-G | Full Deterministic Edge Scenario | Composes 005-C/D/E's proven mechanisms plus one new integration point (`registry.IsAvailable` gating routing); 20/20 runs produce byte-for-byte identical 379-event traces; health detection lag measured at exactly one probe interval |
| 005-H | Virtual vs. Real Comparison | Upstream request count matches exactly between engines at every concurrency level; latency diverges in the predicted direction (real shows queueing growth, virtual stays flat) — the gap is the model's stated boundary, not a bug |

Full data, methodology, and per-experiment interpretation: `experiments/005-virtual-time/README.md` (10 sections, 12 JSON/JSONL result files).

---

## Virtual vs. Real Findings

005-H is the only experiment in this stage explicitly designed to compare engines, and its result is precise: **structural findings transfer, timing fidelity does not, and the gap is explainable in advance.** Upstream request counts (a property of whether coalescing exists, not of engine implementation) matched exactly at every tested concurrency level. Latency did not match — real p99 grew from 102.9ms to 115.4ms as concurrency rose from 10 to 100 (genuine OS/goroutine scheduling contention, present even under Origin's own already-documented infinite-server simplification), while the virtual engine held a flat 100.0ms throughout, since its model assigns every request an identical fixed service time with no queueing or capacity representation at all. Neither number is wrong; they answer different questions, and this stage's contribution is making that boundary visible and quantified rather than assumed.

---

## Surprises

1. **How little of the domain layer needed to change.** Zero source modifications to `cache.Cache`, `health.Registry`, or any `proxy` selector — the audit before any engine code was written already told us this was likely, but seeing every single integration experiment confirm it with no exceptions was still the stage's most consequential result.
2. **005-E's EWMA cell initially looked like a real, reportable finding** (100% of traffic to the slow target) and turned out to be a wiring bug in the experiment (a disconnected `LatencyTracker`), caught only by tracing the actual event ordering by hand before writing anything up.
3. **Same-timestamp resolution wasn't just consistent, it was explainable** — both 005-D and 005-G's ties resolved in the intuitive direction for a specific, statable mechanical reason, not by coincidence.
4. **The virtual/real latency gap tracked the real engine's own growth curve closely enough to read as validation**, not just directional agreement.

---

## Limitations

- The virtual network/service model is intentionally flat: fixed service time per request, no queueing, no finite capacity, no packet-level anything.
- No hierarchical seed tree exists — only P2C's single injected `*rand.Rand` has been proven under virtual time.
- Request coalescing was never ported to the virtual engine; 005-G explicitly does not exercise it and says so.
- No workload distribution beyond fixed, deterministic arrival schedules was built (no Poisson, heavy-tail, or flash-crowd generators).
- A formal `ExperimentEngine` (`Prepare`/`Run`/`Replay`) abstraction, an experiment manifest schema, and content-addressable experiment identity were all left unbuilt — this stage established the mechanism (clock, queue, engine, trace) a future stage's packaging would sit on top of, not the packaging itself.

None of these are bugs relative to a stated invariant — each is either an explicit modeling boundary or a deliberate scope decision made from evidence already sufficient to close the stage, consistent with "earn the abstraction."

---

## Testing

```
gofmt -l .        clean
go build ./...    clean
go vet ./...      clean
go test ./...     ok  (all 10 packages)
```

144 tests pass across the whole repository at Stage 5's close (108 at Stage 4's close + 36 new: 31 in `internal/vtime`, 5 in `internal/clock`). `go test -race` remains **unavailable in this environment** (no `gcc`; `CGO_ENABLED=1` fails building `runtime/cgo`) — stated honestly, as in every prior stage. Notably, the virtual engine itself needs this caveat less than prior stages' concurrent code: it has almost no real concurrency to race in the first place, by design.

---

## Performance

From Experiment 005-A, measured (not assumed):

| Scenario | Events | Virtual span | Real elapsed | Events/sec |
|---|---:|---:|---:|---:|
| Few events, long span | 5 | 10 minutes | 0.000ms | — |
| Many events, short span | 100,000 | 100ms | 39.5ms | ~2,531,300 |

Virtual duration and event count are independent variables: a scenario's real cost tracks how many events it processes, not how much virtual time separates them. No performance optimization was attempted beyond what the design naturally provided — per the stage's own priority order (correctness > determinism > clarity > performance), and because the measured throughput was already more than sufficient for every experiment this stage ran.

---

## Learning

Full narrative in `docs/learning/005-virtual-time.md`. The short version: Stage 5's real work turned out to be building a small, disciplined event-driven engine on top of domain logic that Stages 2–4 had already made engine-agnostic without anyone setting out to achieve that specifically — it fell out of consistently injecting `clock.Clock` instead of reaching for `time.Now()`. The engine itself needed only five pieces (a clock extension, an event queue, the engine loop, a ticker, a trace recorder), each earned by a concrete requirement from the next experiment in line, not designed upfront. Every determinism claim in this stage was tested by repeated execution and comparison, and the one real bug caught along the way (005-E's disconnected tracker) was caught by the same discipline: trace the mechanism, don't trust the first number.

---

## Stage 6 Motivation

Stage 5 produced reproducible event traces — several spanning hundreds of events, generated in a fraction of a real second, provably identical across dozens of repeated runs. A trace is not an analysis. 005-H already quantified a real-vs-virtual latency gap with no tool to say whether a *smaller* gap on some other scenario would be meaningful or just noise; 005-E's EWMA lock-in reproduced Stage 3's own finding but at different exact proportions (94/4/2 in one Stage 3 run, 21/274/5 in this stage's reproduction) — a real statistical question (how much of that split is mechanism versus how sensitive it is to exactly which target wins the first race) this stage has no machinery to answer. Once execution is cheap and reproducible, the natural next question stops being "did this run happen the way we expected" and becomes "is the difference between two runs or two configurations statistically meaningful, and by how much" — Stage 6's mandate: statistics and queueing attribution, built directly on top of the deterministic execution machinery this stage exists to provide.
