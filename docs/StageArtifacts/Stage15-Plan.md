# Stage 15 Plan — Mechanism Identification

Written before any Stage 15 experiment runs, per this stage's own charter (Section 4: do not start by
coding).

## 1. Mechanism To Investigate

Stage 14 found that "load-blind vs. load-aware" does not predict collapse (EWMA, load-aware by signal,
loses to round-robin at scale) and that raw single-target rho becomes insufficient as target count grows.
The working replacement hypothesis is **concentration-proneness under already-committed queueing**: a
policy collapses not because it lacks a signal, but because (a) it concentrates traffic onto one
target/group, and (b) once that target begins queueing, its signal does not cause it to divert NEW work
away fast enough to prevent the committed backlog from growing catastrophically. Stage 15's job is to
quantify this precisely enough to survive falsification, or replace it with whatever the evidence
actually supports.

## 2. Candidate Predictors (Competing Hypotheses)

Five non-exclusive candidates, per the assignment's M1-M5, each with concrete metrics computable from
existing event data (Section 6 below):

- **M1 (instantaneous concentration)**: `top1Share`, `topKShare`, `entropyBits` — all already used in
  Stage 14's experiment binaries (`014a`-`014c`), just never fed into a mechanism comparison directly.
- **M2 (peak pressure)**: `maxRho` (already used throughout Stage 13/14), plus a genuinely NEW
  `peakQueueDepth` reconstructed from raw dispatch/completion timelines (the technique `014h`'s
  `queueDrainTimeMs` already established) rather than only the trace's own recorded queue_depth values
  (which are recorded at enqueue time only, per Stage 14's own documented limitation).
- **M3 (duration above threshold)**: `timeAboveRho(threshold)`, `fractionOfHorizonOverThreshold` — new,
  built from the same reconstructed timeline as M2's peak queue, evaluated at 2-3 thresholds (e.g.
  rho>0.9, rho>1.0) rather than one, since the assignment explicitly warns against picking a threshold
  after seeing results.
- **M4 (committed backlog)**: `committedBacklog` — operationally defined in Section 10 below; this is the
  central, most novel measurement Stage 15 must get right, since Stages 12-14 never measured it directly.
- **M5 (unlock/diversion dynamics)**: `timeToFirstDiversion`, `diversionFraction`, `timeToStableSelection`,
  `queueDrainTime` (already built once, in `014h`, generalized here).

No candidate is assumed to win. The experiments in Sections 11-15 of the assignment are designed
specifically so that at least one comparison can falsify each candidate independently.

## 3. Falsification Experiments (Overview — Detailed Designs in Section 29 of the Main Doc)

- F1: high concentration, low consequence (enough capacity / fast drain / low service time) — attacks M1.
- F2: moderate concentration, high consequence (slow target / long residence / poor capacity allocation)
  — attacks M1 from the other direction.
- F3: similar rho, different committed backlog (two policies/configs matched on achieved rho but differing
  in how much work each commits before diverting) — attacks "rho is sufficient," motivates M4.
- F4: similar committed backlog, different concentration shape (single-target vs. group, reusing `014b`'s
  bimodal topology) — tests whether M4 survives a concentration-SHAPE change.
- F5: similar initial state (concentration, rho, queue), different subsequent diversion behavior — isolates
  M5 from M1-M4 by holding the "how bad did it get before anyone reacted" variables fixed and varying only
  "how did the policy react afterward."
- F6: P2C vs. EWMA at matched initial concentration — Section 19's mandated distinction (smoothed lock-in
  vs. sampling-driven insufficient visibility).

## 4. Variables Held Fixed (Within Each Comparison)

Per comparison: target count, service-time distribution/topology shape, workload pattern, total offered
load, seed tree, engine (virtual unless the comparison is explicitly the real-engine one), horizon. Stage
14's own causal-isolation discipline (state exactly what changed and what didn't, for every comparison)
carries forward unchanged.

## 5. Variables Independently Varied

Across the falsification program: policy identity (the six from Stage 13/14), target count (N=3/5/8,
reusing Stage 14's own graduated/bimodal topologies rather than inventing new ones), workload shape
(Constant/Burst/FlashCrowd, reusing `internal/traffic`'s existing generators), engine (virtual vs.
validated real ceiling, reusing `014e`'s own validated `MaxConnsPerHost` mechanism), and — only where a
specific hypothesis requires it — Adaptive's own weight configuration (Section 20's ablations: default,
cache=0, load=0, latency=0) and EWMA's smoothing alpha (reusing `014g`'s custom-alpha `PolicySpec`
pattern).

## 6. Existing Instrumentation That Can Be Reused

Confirmed by inspecting source directly (not assumed from documentation):

- `replay.WorldResult.Records` (dispatch decisions, `VirtualTimeMs`+`Target`) and `.Completions`
  (`VirtualTimeMs`+`Target`+`Latency`) — the raw event streams every Stage 13/14 custom metric was already
  built from. Sufficient, on inspection, for every M1-M5 metric: concentration shares and entropy from
  `CompletedByTarget`; queue-depth timelines from merging Records (+1) and Completions (-1) events per
  target (the exact technique `014h`'s `queueDrainTimeMs` used); diversion timing from watching which
  target each Record after a threshold crossing points to.
- `internal/attribution` (`UtilizationFromWorld`, `CheckLittlesLaw`, `Explain`/`Compare`) — confirmed by
  reading `utilization.go`/`littleslaw.go`/`finding.go` directly: this package computes only a SINGLE,
  whole-run rho per target from total completions, with no time-resolved queue-depth, no duration-above-
  threshold, and no committed-backlog concept at all. It cannot express M2 (peak, as opposed to average),
  M3, M4, or M5 as currently written.
- `internal/chaos` (`ParseYAML`/`ToFailureWindows`) — reused unchanged for any Stage 15 scenario needing
  deterministic degradation (Section 21/26's canonical scenario), following the exact pattern `014d`/`014h`
  already established.
- `internal/challenge`'s metamorphic-test pattern (`TestMetamorphic_*` in `metamorphic_test.go`) — the
  right place for Section 31's regression tests, once a genuine discovery (not every exploratory number)
  is ready to be locked in.
- `internal/statistics` (`Mean`, `Percentile`, `CliffsDelta`, `BootstrapDiffCI`, `MeanStat`) — unchanged,
  reused for Section 28's statistical confirmation exactly as `013j`/`014i` already did.

## 7. Minimum New Instrumentation Required

`internal/attribution`'s existing scope (whole-run, single-number utilization) is confirmed insufficient
for M2-M5 by direct inspection (Section 6). Per this stage's own Section 7 instruction ("add a focused
analysis package rather than burying calculations inside experiment binaries" — the exact anti-pattern
Stage 13/14 fell into, duplicating `offeredRhoPerTarget`/`maxQueueDepth`/`topKShare` across nine separate
`cmd/experiment-014*` files), Stage 15 adds ONE new package, `internal/backlog`, computing purely from
existing `replay.WorldResult`/`replay.TargetProfile` data — no new fields on `TargetProfile`, no new trace
event types, no changes to `RunWorld` itself. Planned exports, deliberately the SMALLEST set that can
distinguish M1-M5 (per Section 7's own "do not implement every metric automatically"):

- `ConcentrationMetrics{Top1Share, TopKShare, EntropyBits}` — M1.
- `QueueTimeline` + `PeakQueueDepth`, `AreaUnderQueue` (the queue-integral the assignment names in Section
  7) — M2, reconstructed once, shared by every metric that needs it rather than each recomputing its own
  timeline.
- `TimeAboveThreshold(timeline, rho_threshold)` — M3, parameterized so multiple thresholds can be compared
  without picking one in advance (Section 30's anti-fishing requirement).
- `CommittedBacklog` (operational definition in Section 10 of the main plan below) — M4.
- `DiversionTimeline{TimeToFirstDiversion, DiversionFraction, TimeToStableSelection, QueueDrainTime}` —
  M5, generalizing `014h`'s one-off `adaptationTimeMs`/`queueDrainTimeMs` into a reusable, tested function.

If any experiment shows this set cannot distinguish two candidates, the plan is to extend this package
minimally, not to build a second one.

## 8. Expected Confounders

- **Cold-start exploration** (Stage 14's own finding): EWMA/Adaptive/LC all sample every target once at
  the very start regardless of load, which can look like "diversion" without being a REACTION to
  congestion. `DiversionTimeline` must only count dispatches AFTER the congestion condition is first met,
  not raw target diversity from cold start.
- **Horizon truncation**: a policy that never lets the queue drain within the horizon (like `014h`'s EWMA
  cell) makes `QueueDrainTime` undefined, not zero — must be reported as "did not drain," never silently
  clamped.
- **Probe-interval lag** (confirmed in `014h`: 5-10% of during-failure decisions still targeted a crashed
  target due to the 100ms health-probe cycle) — any failure-based scenario must budget for this, not
  interpret it as a routing bug.
- **Threshold choice for "congestion condition"**: rho>0.9 vs. rho>1.0 can shift when M3/M4 "start
  counting" — Section 30 requires stating candidate thresholds BEFORE looking at results and checking
  robustness across them, not selecting the one that produces the cleanest story.
- **Policy-specific cold-start rules differ** (RR cycles deterministically; EWMA/LC/Adaptive/P2C sample
  once each; WRR uses weights from the first decision) — comparing "time to first diversion" across
  policies with different cold-start semantics needs the comparison to start counting from the SAME
  externally-defined congestion event, not from each policy's own first decision.

## 9. Stopping Criteria

Stage 15 stops once (mirroring the assignment's own Section 40, restated as this plan's operational
target): a canonical scenario produces measurable backlog dynamics for all six policies; at least two
competing mechanisms (M1-M5) have been actively pitted against each other with a real falsification
attempt for each; the load-blind/load-aware classification is replaced by a mechanism precise enough to
explain EWMA, P2C, LC, WRR, and Adaptive individually; the strongest claim has independent-seed
confirmation; and negative results are written up as plainly as positive ones. Stage 15 does NOT continue
searching for a single perfect scalar predictor once the evidence shows none exists cleanly — a
multi-factor, precisely-scoped explanation is an acceptable, planned outcome, not a fallback.
