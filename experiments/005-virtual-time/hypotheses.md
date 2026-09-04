# Hypotheses: Experiment 005 — Virtual-Time Engine & Deterministic Reproducibility

## H1 — Virtual Time Advances Independently of Wall-Clock Time

The central claim motivating the whole engine: virtual duration and real execution time are not the same quantity, and conflating them (e.g. by using `time.Sleep` to represent a delay) is exactly what Stage 5 exists to avoid.

- *Setup*: two cells run on the same `vtime.Engine`. Cell 1 schedules a handful of events spanning a *large* virtual duration (10 minutes). Cell 2 schedules a *large number* of events spanning a *small* virtual duration (100ms).
- *Prediction 1*: Cell 1's real execution time should be near-instant (well under 100ms) despite its 10-minute virtual span — real cost does not scale with the size of the gaps between events.
- *Prediction 2*: Cell 2's real execution time should scale with event *count*, not with its (tiny) virtual duration — real cost is a function of how much work the engine actually does, not how much virtual time that work is spread across.
- *Purpose*: these two cells together establish that "real time" and "virtual duration" are independent variables, which is easy to state but worth demonstrating rather than assuming — a naive reader could otherwise believe a virtual engine is just "a clock that lies about `Now()`" while everything underneath still blocks on real time proportional to the scenario's virtual length.

## H2 — Equal-Timestamp Events Are Processed in a Deterministic, Documented Order

Section 11 of the Stage 5 design is explicit that same-timestamp ordering is part of an experiment's *definition*, not an implementation detail — a cache expiring and a request arriving at the same virtual millisecond can produce a hit or a miss depending on which the engine treats as happening first.

- *Setup*: schedule several events at the identical virtual timestamp, run the scenario many times (fresh engine each run), and compare the resulting trace's event order across all runs.
- *Prediction 1*: every run produces the exact same order for same-timestamp events — insertion order, per `EventQueue`'s documented (timestamp, sequence) rule — with zero variation across repeated runs.
- *Prediction 2*: this holds even when the same-timestamp events are scheduled by different, interleaved callers (not just scheduled back-to-back in one place), since the ordering rule is about *when Schedule was called*, not about proximity in source code.
- *Purpose*: this is the first experiment to actually test the reproducibility invariant (`same config + same seed + same initial state = same trace`) rather than merely asserting the mechanism exists. A single passing run proves nothing about determinism; only repeated runs with an explicit comparison do.

## H3 — Stage 4's Cache Runs Correctly Under Virtual Time With Zero Modification

The Phase A/B audit found `cache.Cache` already takes an injected `clock.Clock` rather than calling `time.Now()` — this hypothesis is the direct test of whether that was actually sufficient, or whether some hidden assumption still ties it to wall-clock time.

- *Setup*: the canonical scenario from the Stage 5 design (item 32): insert an entry with a 100ms TTL at t=0, confirm a HIT at t=99ms, confirm a MISS at t=101ms (past expiry), refill, and confirm a fresh HIT at t=150ms (proving the refill started its own TTL window rather than inheriting the expired entry's).
- *Prediction 1*: every step produces exactly the expected HIT/MISS outcome, with `cache.Cache` used completely unmodified — `Engine.Clock()` standing in for any `clock.Clock` the type already accepted.
- *Prediction 2*: the entire scenario, spanning 150ms of virtual time, completes in a negligible fraction of a real millisecond — no `time.Sleep` anywhere in the path from experiment setup to cache state changes.
- *Purpose*: this is the clearest possible demonstration of why Stage 2–4's clock-injection discipline mattered — not as an abstract good practice, but as the specific thing that makes this experiment nearly free to write.

## H4 — Failure and Recovery Reproduce Precisely, and Same-Timestamp Ordering Explains Exactly When

`health.Registry`'s 4-state machine is reused unmodified; only its scheduling mechanism (`health.Checker`'s real ticker + real HTTP) is replaced with a virtual probe loop consulting a ground-truth up/down schedule instead of a live network probe.

- *Setup*: `edge-2` fails at t=5s, recovers at t=10s, probed every 1s, `health.DefaultConfig()` (fail threshold 2, recovery-pass threshold 2). Deliberately chosen so the failure and recovery events land on the *exact same virtual timestamp* as a scheduled probe tick (t=5000ms and t=10000ms) — an instance of the same-timestamp-ordering question item 11 calls out as scientifically important, not avoided by picking off-grid timestamps.
- *Prediction 1*: the state sequence (HEALTHY → UNHEALTHY → RECOVERING → HEALTHY) lands at precise, arithmetically-predictable virtual timestamps derived from the probe interval and configured thresholds, and reproduces identically across many repeated runs.
- *Prediction 2 (the interesting one)*: at t=5000ms, the probe scheduled for that exact instant will already observe the failure that also takes effect at that instant — not because of an arbitrary tie-break, but because the failure event was scheduled once at setup time (a low insertion sequence number), while the t=5000 probe tick is dynamically rescheduled during the run by four prior tick firings and therefore carries a much later sequence number. The documented `(timestamp, sequence)` rule resolves this deterministically and explainably, not accidentally. The same reasoning predicts the t=10000 probe already observes the recovery.
- *Purpose*: demonstrates that same-timestamp resolution isn't merely "consistent" (any fixed rule would give that) but *mechanistically explainable* from the engine's own documented ordering rule — which is what makes it trustworthy as part of an experiment's definition rather than an opaque implementation detail a researcher has to take on faith.
