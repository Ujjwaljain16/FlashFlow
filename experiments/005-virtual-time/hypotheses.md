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
