# Stage 15 — Mechanism Identification: What Actually Predicts Collapse?

**All results below come from experiments actually run (`cmd/experiment-015a` through `015f`,
`experiments/015-mechanism-identification/results/`), plus the new `internal/backlog` analysis package
(fully unit-tested with hand-computed expected values). Nothing here was decided before its corresponding
experiment executed.**

## Motivation

Stage 14 ended PASS WITH LIMITATIONS having falsified its own predecessor's deepest claim: "load-blind vs.
load-aware" does not predict collapse (EWMA, a policy with a live signal, loses to round-robin at scale),
and raw single-target rho becomes necessary-but-insufficient as target count grows. The replacement
language Stage 14 proposed — "concentration-proneness under already-committed queueing" — was itself only
a hypothesis, named but not measured. Stage 15 exists to make that hypothesis precise: define committed
backlog operationally, measure it directly, and try hard to break it before trusting it.

## Stage 14 Findings Motivating Stage 15

Restated because Stage 15 is organized directly around them: EWMA can have a live latency signal and
still collapse; round-robin can beat EWMA; least-connections and Adaptive stay stable by reacting to
CURRENT in-flight pressure; weighted-round-robin stays stable without any live signal when its static
weights already match capacity; EWMA/P2C can remain locked onto a target/group after queueing begins;
later recognition cannot retroactively drain work already committed; rho becomes insufficient as target
count grows because the concentration process itself changes; a real concurrency ceiling can reproduce
the virtual reversal once genuine sustained queueing exists; alpha matters for H2 but not the main
collapse.

## Research Questions

The central question, unchanged from the assignment: what measurable property predicts whether a routing
policy will escape or become trapped in an already-forming backlog? Not "which policy wins" — which
quantity, computed from data every policy already produces, actually explains why.

## Competing Mechanism Hypotheses

Five candidates were defined and tested, none assumed to win in advance (`docs/StageArtifacts/Stage15-Plan.md`):
M1 (instantaneous concentration: top1/top-k share, entropy), M2 (peak pressure: max rho, peak queue
depth), M3 (time above a danger threshold), M4 (committed backlog: work dispatched to a target between
congestion onset and material diversion), M5 (unlock/diversion dynamics: how fast and how completely a
policy corrects).

## Measurement Definitions

Built once, in a new package (`internal/backlog`), after confirming by direct inspection that
`internal/attribution` computes only a single whole-run rho per target with no time-resolved queue depth,
duration-above-threshold, or committed-backlog concept at all:

- **Timeline**: a target's queue-depth-over-time series, reconstructed by merging dispatch (+1) and
  completion (-1) events from `replay.WorldResult`'s own `Records`/`Completions` — no new fields, no new
  trace event types, nothing in `RunWorld` changes.
- **PeakDepth / AreaUnderCurve** (M2): the queue integral, distinguishing a brief deep spike from a long
  shallow queue, which peak depth alone cannot.
- **TimeAboveThreshold / FractionAboveThreshold** (M3): parameterized by an explicit ratio threshold, not
  hardcoded to one value, so robustness could be checked rather than assumed.
- **Committed backlog** (M4, Section 10's operational definition applied literally): the count of
  dispatches to a target strictly within `[congestion onset, diversion)`, where congestion onset is the
  first time depth/capacity exceeds an explicit threshold and diversion is the first time a trailing
  window of dispatch decisions shows that target's own share drop below an explicit fraction. Both
  thresholds (ratio=1.0, window=20, share=0.5) were fixed BEFORE any canonical-scenario result was
  inspected.
- A genuine mid-stream correction: the first implementation used the FIRST-ever congestion episode for a
  target. Building the canonical scenario exposed why that is wrong — a target can have a tiny, early,
  self-resolving congestion blip followed by a much larger episode during the workload's real peak. A
  first-episode-only analysis reported EWMA's committed backlog as 1, silently missing the 97-request
  backlog that actually explained its worst-in-the-run outcome. Fixed by adding a second onset finder,
  `FindPeakEpisodeCongestionOnset`, anchored to whichever episode contains the target's peak depth, and
  covered by a dedicated hand-computed test with two episodes of different size.

## Canonical Scenario

5 heterogeneous targets (15/30/45/60/75ms), Capacity=1, a FlashCrowd workload (`BaseRate=20`,
`PeakRate=300`, peak at t=2.5s) over an 8-second horizon — long enough to observe the full build → detect
→ respond → recover sequence within one run, unlike most of Stage 14's own boundary scenarios, which
typically ended still overloaded. All six policies run unmodified, no tuning.

A methodological bug was caught before trusting results: the initial version identified "the concentrated
target" by raw completion count, which is meaningless for round-robin (whose completions are roughly even
by construction). Fixed by separating `TopByCompletions` (M1, completion-count concentration) from
`BottleneckTarget` (the target with the worst QUEUE outcome, i.e. highest peak depth) — the two can and do
diverge: round-robin spreads completions evenly (top1=0.212) but still overloads its slowest target the
hardest in queueing terms.

## Backlog Dynamics

| Policy | Peak Depth | Committed Backlog | Time Above ρ>1.0 | Fraction Above ρ>1.0 | Drains? |
|---|---:|---:|---:|---:|---|
| round-robin | 52 | 4 | 5689ms | 0.711 | **No** |
| weighted-round-robin | 81 | 9 | 2448ms | 0.306 | Yes (4773ms) |
| least-connections | 37 | 6 | 2032ms | 0.254 | Yes (4356ms) |
| ewma | 95 | 97 | 4383ms | 0.548 | Yes (6850ms) |
| p2c-load | 38 | 6 | 1445ms | 0.181 | Yes (3791ms) |
| adaptive | 83 | 86 | 5597ms | 0.700 | **No** |

Two policies never drain within the horizon — but for DIFFERENT reasons, visible only by comparing
multiple metrics at once: round-robin has a LOW committed backlog (4) yet a high fraction-above-threshold
(0.711) — a **chronic** failure (permanently, structurally over capacity because its fixed 1/5 allocation
to the slowest target exceeds that target's own capacity, not because of any acute backlog spike).
Adaptive has a HIGH committed backlog (86) matching its own high fraction-above-threshold (0.700) — an
**acute** failure (a large, one-time over-commitment during the burst that the run's remaining time isn't
long enough to drain). EWMA has the highest peak/backlog of all six (95/97) yet DOES eventually drain
(6850ms) — its failure is acute but not permanent. No single metric — concentration, peak, committed
backlog, or fraction-above-threshold — captures every collapse shape alone; they are complementary
instruments for different failure modes, not competing candidates for one winner.

A second, easily-missed nuance: Adaptive's MEAN latency (576.32ms) looks far better than EWMA's
(950.28ms), but Adaptive's P99 (4852.78ms) is actually the WORST of all six policies in this canonical
scenario, marginally exceeding even EWMA's own (4721.58ms). Mean latency alone would have hidden this.

## Concentration Analysis (M1)

`ComputeConcentration` (Top1Share/Top3Share/EntropyBits) was computed for every scenario throughout this
stage. F1 (Section 11) directly attacked M1's sufficiency: Adaptive at load far under capacity reached
`top1_share=1.000` — complete concentration — with ZERO congestion and ZERO committed backlog.
**Concentration alone does not predict collapse**; genuine capacity pressure is a necessary second
ingredient the mechanism must keep separate from M1, confirming Stage 14's own implicit assumption
explicitly rather than leaving it unstated.

## Rho Analysis (M2/M3)

`014a`'s original counterexample (N=8/Capacity=1/EWMA: rho_max=0.709, nominally stable, yet 75%
wait-share) is now explained precisely: peak instantaneous pressure and committed backlog are different
quantities, and a WHOLE-RUN AVERAGE rho (the only kind `internal/attribution` ever computed) averages away
exactly the transient spike that actually causes the damage. The cross-topology predictor test (below)
quantifies this: peak rho's rank-ordering of severity across N=3/5/8 is backwards (it decreases as N
grows while severity increases), while committed backlog's rank-ordering is perfect.

## Unlock/Diversion Analysis (M5)

Congestion onset, diversion, and drain times were computed for every policy in the canonical scenario
(see the Mandatory Mechanism Table below). The key finding is not diversion SPEED in isolation but its
interaction with burst intensity: least-connections and EWMA divert at similar times in DECISION-COUNT
terms (a 20-decision trailing window), but the ABSOLUTE volume of traffic flowing during that window
differs enormously depending on how concentrated the burst is — producing radically different committed
backlogs (6 vs. 97) from similarly-fast reactions. Diversion speed alone, without accounting for what is
actually flowing during the reaction window, would have missed this.

## Policy Mechanism Comparison

- **Round-robin**: no state-based correction at all. Fails chronically on whichever target is slowest,
  regardless of momentary load — the SAME target every time, structurally, not because of an acute event.
- **Weighted-round-robin**: static capacity-aware distribution. Safe when weights match reality (as they
  do here, by construction); Stage 14 already showed this fails once reality changes and weights go
  stale.
- **Least-connections**: reacts to CURRENT in-flight count. Diverts new work away from a target the
  moment it starts accumulating in-flight requests, before a large backlog can form — the cleanest
  "unlock" mechanism among all six.
- **EWMA**: a smoothed LATENCY HISTORY. Its own past success (having found a fast target) becomes the
  reason it keeps sending more work there even as that target degrades, because the smoothed estimate
  lags the target's actual current state.
- **P2C**: a SAMPLED comparison between two random targets each decision. Structurally never fully
  commits to one target the way EWMA's global-comparison-and-lock does — confirmed as a real, seed-
  independent difference (Section 19, below), not a lucky coincidence.
- **Adaptive**: a weighted combination of load, latency, cache, and cost signals. Ablation (Section 20,
  below) isolates WHICH component actually protects it.

## Cache-Affinity Mechanism

Tested directly against the committed-backlog hypothesis (Section 21) by reusing Stage 13's exact B1
scenario. The straightforward version of the hypothesis — higher cache weight delays diversion away from
whichever target absorbs the hot key's traffic during the crash, letting more work commit there — is
**falsified**: committed backlog on the absorbing target does not increase with cache weight (14/8/8
across weights 0.02/0.10/0.30) even though mean latency does worsen at the highest weight (101.97/
100.68/168.55ms). A natural alternative (a post-recovery rush back to the just-recovered target) was
checked directly and also does not explain it cleanly — worse, the committed-backlog operationalization
itself becomes noisy when applied to a target whose true congestion is marginal (peak depth 1-5) rather
than sustained, a genuine boundary condition on the METHOD, not just the mechanism. Reported honestly as
an unresolved case, per Section 21's own instruction not to claim causality an intervention doesn't
support.

## H2/Smoothing Relationship

An alpha ablation on EWMA (0.05 to 0.8, same topology/workload/seed as the canonical scenario, isolating
alpha as the only variable) found committed backlog does NOT decrease monotonically with alpha
(98/97/98/84) — alpha has SOME structural effect (it changes WHICH target becomes the bottleneck at the
highest value tested, edge-02 → edge-03) but does not cleanly fix the mechanism. This is consistent with,
not contradicting, `014g`'s own finding that alpha has no effect on the main boundary's mean-latency
outcome at a sustained near-boundary load: smoothing speed affects how much backlog accumulates before
recognition in some conditions, but does not determine WHETHER a damaging backlog forms at all. H2's
sustained-lag mechanism and the main collapse mechanism remain correctly separate, as Stage 14 already
established — Stage 15 adds a finer-grained (backlog-level, not just latency-level) confirmation of that
separation rather than unifying them.

## Predictor Search

Cross-topology (Stage 14's own N=3/5/8 graduated near-boundary cells plus its N=8 bimodal cell, EWMA
throughout): committed backlog achieves **perfect rank agreement** with actual severity (total rank
distance = 0 across 4 scenarios), while peak rho is badly misordered (distance = 4) — it decreases as N
grows (0.915→0.833→0.716) while severity increases, exactly reproducing and now rigorously quantifying
`014c`'s own "rho necessary but increasingly insufficient" finding.

Cross-workload (canonical N=5 topology under Constant/Burst/FlashCrowd): committed backlog does better
than peak rho (rank distance 2 vs. 4) but NOT perfectly — reported honestly rather than smoothed into a
clean universal win. Burst produces the highest committed backlog (177) of the three workloads, yet
FlashCrowd's own P99 (4721ms) actually exceeds Burst's (3761ms) despite FlashCrowd's much lower backlog
(97), because Burst's backlog never fully drains within the horizon while FlashCrowd's does. Committed
backlog predicts severity well across topology SIZE; it is a real but imperfect predictor across workload
SHAPE.

## Cross-Topology Validation

See Predictor Search above — this is the same experiment (`015e` Part 1), serving both purposes at once
per the assignment's own economy-of-experiments discipline.

## Cross-Workload Validation

See Predictor Search above (`015e` Part 2).

## Virtual-vs-Real Validation

Extends `014e`'s own validated real concurrency ceiling (`MaxConnsPerHost=1`, matching the virtual
model's Capacity=1 exactly) to test Stage 15's own falsifier (round-robin beating EWMA), not just
re-confirm Adaptive vs. EWMA. A disclosed limitation stated before running: `internal/engine.RealMetrics`
exposes only a latency histogram and per-target completion counts — no per-request dispatch/completion
timeline — so committed backlog itself is not directly measurable on the real engine with existing
instrumentation; this validates DIRECTIONAL agreement only.

Result, genuinely mixed: round-robin beats EWMA on P99 at `below_ceiling` and `near_ceiling` (matching the
virtual falsifier's direction), but REVERSES at `above_ceiling` — EWMA's P99 (3235.94ms) actually beats
round-robin's (4055.09ms) under the most extreme real overload tested. Not a contradiction requiring the
mechanism to be discarded: round-robin's forced uniform split guarantees a fixed share of ALL traffic to
the permanently-underprovisioned slowest target forever, while EWMA's lock-in, even though "wrong" in the
committed-backlog sense, concentrates onto whichever target it locked onto — which can still absorb more
real throughput than the slowest target ever could. A genuine boundary of the mechanism: relative severity
depends on WHICH target gets the fixed disadvantage, not just whether concentration occurs.

## Falsification Experiments

See the Mandatory Falsification Table below for the full accounting. Headline results: F1 (high
concentration, low consequence) CONFIRMED — concentration alone is insufficient. F4 (concentration shape)
found the mechanism generalizes to, and is if anything WORSE under, bimodal group lock-in (committed
backlog 102 vs. graduated's 97, never draining vs. graduated's clean drain). F6 (P2C vs. EWMA, 8
independent seeds with genuine arrival-stream jitter) found EWMA shows committed_backlog>50 in 8/8 seeds,
P2C-load in 0/8 — a real, seed-independent structural distinction, not a lucky draw.

## Statistical Confirmation

F6's 8-seed replication (Cliff's-delta-level separation: 8/8 vs. 0/8, with genuine jittered arrival-stream
variation caught and added after an initial run without jitter accidentally varied only P2C's own internal
randomness, not the arrival process itself) is this stage's flagship statistical result. The cross-
topology predictor comparison (n=4 scenarios) additionally shows a decisive, unambiguous ranking
difference (distance 0 vs. 4) rather than a marginal one, though n=4 is too small for a formal confidence
interval — reported as a strong qualitative signal, not oversold as a hypothesis-tested statistic.

## Negative Results

- F3 (alpha ablation): committed backlog does not decrease monotonically with alpha — a real, disclosed
  non-monotonicity, not smoothed into "alpha helps."
- Cache-affinity (Section 21): neither the original nor the refined hypothesis explains Stage 13's interim
  -latency effect; recorded as genuinely unresolved.
- Cross-workload predictor ranking: committed backlog is not a perfect predictor across workload SHAPE,
  only across topology SIZE — a real, disclosed limit on generality.
- Real-engine validation: the round-robin-beats-EWMA falsifier reverses at the highest real overload level
  tested — a genuine point of virtual-vs-real divergence, documented rather than hidden.

## Mechanistic Synthesis

The evidence converges on committed backlog — not raw concentration, not peak rho, not signal presence —
as the best available EXPLANATORY quantity for ACUTE collapse: a policy that lets substantial work
accumulate at a target between the moment it becomes congested and the moment it materially diverts new
work elsewhere will show that accumulation directly in its own tail latency, and this holds across target
count, topology shape (graduated and bimodal), and — for the DIRECTION of the effect, if not its exact
magnitude — the real engine. Adaptive's own resistance to this failure mode traces specifically to its
LOAD signal (Section 20's ablation: removing it more than doubles committed backlog, from 86 to 206,
worse than EWMA's own 97), not its latency smoothing. P2C's resistance is a genuine, seed-independent
structural property of sampling-based comparison, not luck.

But committed backlog is not the whole story, and this stage's own canonical-scenario data shows exactly
where it stops working: round-robin's failure is CHRONIC (a permanently-undersized fixed allocation, low
committed backlog, high fraction-of-time-over-capacity) rather than ACUTE (a large one-time
over-commitment), and no acute-backlog metric captures it — `FractionAboveThreshold` does. The honest
synthesis is not one scalar but two complementary measurements for two distinct collapse shapes, plus
concentration (M1) as a necessary-but-insufficient precondition for either.

## Historical Reconciliation

| Stage | Best Explanation at the Time | What Later Evidence Changed |
|---|---|---|
| 11 | Adaptive beats EWMA under heterogeneity; virtual model has no contention | Stage 12 found a real-engine load-tracking bug and a flat-model limitation that made this comparison premature |
| 12 | A finite-capacity contention model reverses the Stage 11 result sharply at Capacity=1 | Stage 13 found the reversal tracks normalized ρ, not the literal capacity number — a real but narrower mechanism |
| 13 | ρ≈0.89-0.97 predicts the transition; the deeper split is load-blind vs. load-aware routing | Stage 14 found ρ's predictive power decreases as target count grows, and directly falsified load-blind vs. load-aware (EWMA loses to round-robin at scale) |
| 14 | The real mechanism is "concentration-proneness under already-committed queueing" — named but not measured | Stage 15 operationally defined and measured committed backlog directly, confirmed it explains ACUTE collapse (perfect rank agreement across topology size), and found it does NOT explain round-robin's CHRONIC failure mode |
| 15 | Committed backlog explains acute collapse; fraction-of-time-over-capacity explains chronic collapse; concentration alone is necessary but insufficient for either | Left open: whether these two failure-mode categories are exhaustive, or a third shape exists that neither metric captures |

This project's own history is a sequence of narrowing, not a single consistent theory arrived at all at
once — each stage's "best explanation" was genuinely believed and evidenced at the time, and each was
later shown to be a special case of something more precise. Stage 15's own synthesis should be read the
same way: the best current explanation, not a final one.

## Supported Claims

- Committed backlog, operationally defined as dispatches to a target between congestion onset and
  material diversion, is a strong, quantitatively-confirmed predictor of acute-collapse severity, with
  perfect rank agreement across a target-count generalization test where peak rho is badly misordered.
- Concentration (M1) is necessary but not sufficient for collapse — confirmed by direct intervention
  (F1), not merely assumed.
- The committed-backlog mechanism generalizes to a structurally different concentration shape (bimodal
  group lock-in), and is if anything worse there.
- Adaptive's resistance to committed-backlog collapse is attributable specifically to its LOAD signal, not
  its latency smoothing — confirmed via controlled ablation.
- P2C and EWMA are mechanistically distinct in a real, seed-independent way (structural sampling-based
  avoidance of full lock-in vs. smoothed-history lock-in), not merely different by chance in one scenario.
- A validated real concurrency ceiling reproduces the concentration-proneness mechanism's DIRECTION for
  most conditions tested, extending Stage 14's own real-engine validation beyond one policy pair.
- There are at least two distinct collapse shapes (acute and chronic), each better explained by a
  different metric; no single scalar predictor captures both.

## Claims NOT Supported by Evidence

- That committed backlog is a universal severity predictor across workload SHAPE — it is imperfect across
  workload variation (rank distance 2, not 0), even though it is perfect across topology SIZE variation.
- That the cache-affinity interim-latency effect is explained by committed backlog — directly tested and
  falsified in both the originally-hypothesized location and the natural alternative location.
- That alpha is irrelevant to committed backlog in general — it has SOME structural effect (changes which
  target locks up) even though it does not cleanly reduce backlog monotonically.
- That the round-robin-beats-EWMA falsifier holds universally on the real engine — it reverses at the
  highest overload level tested.
- That Adaptive is simply "safe" under any burst — its own canonical-scenario P99 was the WORST of all six
  policies tested, despite a comparatively good mean.

## Limitations

1. Committed backlog's operational definition depends on three explicit thresholds (congestion ratio,
   diversion window, diversion share); robustness was checked qualitatively (F1/F3/F4 vary conditions, not
   thresholds directly) rather than via a full threshold-sensitivity sweep.
2. The cache-affinity investigation (Section 21) ends genuinely unresolved, not merely narrowed — a
   mechanism for Stage 13's own interim-latency effect remains unidentified.
3. Real-engine validation (Section 26) is directional only; committed backlog itself cannot be measured on
   the real engine with existing instrumentation, and adding that instrumentation was explicitly
   considered and rejected as out of scope for this stage.
4. The "chronic vs. acute" collapse-shape distinction was discovered from a 6-policy, single-scenario
   comparison; whether a third distinct shape exists was not systematically searched for beyond the
   falsification program's own six targeted attacks.
5. Statistical confirmation (F6) covers one claim (P2C vs. EWMA) with 8 seeds; the flagship committed-
   backlog-predicts-severity claim itself was confirmed via a 4-point deterministic ranking test, not an
   independently-seeded replication.

## Unresolved Questions

- Is there a compact combination of committed backlog and fraction-above-threshold that predicts BOTH
  acute and chronic collapse in one quantity, or are they fundamentally separate axes?
- What actually explains Stage 13's cache-affinity interim-latency effect, if not committed backlog in
  either location tested?
- Does the real-engine reversal at extreme overload (round-robin beating EWMA reverses to EWMA beating
  round-robin) generalize to other topologies, or is it specific to this 3-edge configuration?
- Are P2C's and EWMA's mechanistic differences (sampling vs. smoothed-history lock-in) the SAME
  distinction that explains why weighted-round-robin also avoids lock-in, or a separate phenomenon that
  happens to produce a similar outcome?
- Does committed backlog's imperfect cross-workload ranking improve with a workload-normalized variant
  (e.g., backlog divided by total offered load), or is the imperfection structural to comparing different
  workload shapes at all?

---

## Mandatory Mechanism Table

| Policy | Initial Concentration | Peak Pressure | Queue Formed? | Detection (Congestion Onset) | First Diversion | Stabilization | Final Outcome | Mechanism Class |
|---|---|---|---|---|---|---|---|---|
| RR | Low (top1=0.212) | Moderate (peak=52) | Yes, chronically | 2326ms | 2393ms (structural, not reactive) | Never | Never drains; mean=844ms, p99=3758ms | Chronic structural under-provisioning |
| WRR | Moderate (top1=0.439) | High (peak=81) | Yes, acutely | 2343ms | 2403ms | 4773ms | Drains; mean=433ms, p99=1214ms | Static capacity-aware (safe because weights match reality) |
| LC | High (top1=0.626) | Low (peak=37) | Briefly | 2362ms | 2416ms | 4356ms | Drains; mean=450ms, p99=2500ms | Current-pressure-aware anti-concentration |
| EWMA | High (top1=0.500) | Highest (peak=95) | Yes, acutely | 2487ms | 2688ms | 6850ms | Drains late; mean=950ms, p99=4722ms | Smoothed-history lock-in |
| P2C | Low (top1=0.284) | Low (peak=38) | Briefly | 2351ms | 2409ms | 3791ms | Drains; mean=519ms, p99=2539ms | Sampling-based anti-concentration |
| Adaptive | High (top1=0.599) | High (peak=83) | Yes, acutely | 2403ms | 2745ms | Never | Never drains; mean=576ms, p99=4853ms (WORST p99 of all six) | Multi-signal, protected primarily by LOAD component |

## Mandatory Predictor Table

| Predictor | Canonical (6 policies) | N=5 | N=8 | Bimodal | Bursty | Real | Overall |
|---|---|---|---|---|---|---|---|
| max rho | Not computed per-policy in canonical (flat-model rho undefined pre-burst) | Rank-misordered (decreases as N grows) | Rank-misordered | Low (0.236) yet still degrades — insufficient alone | Rank distance 4/3 (worst of the three predictors tested) | Not directly comparable (real engine has no rho concept) | **Poor**: necessary but not sufficient, worsens with scale |
| peak queue depth | Distinguishes acute (EWMA/Adaptive) from low-peak (LC/P2C) but not RR's chronic case | Correlates with backlog in this dataset | Correlates with backlog | Present (96) even in group-lock-in | High (171) for Burst | Not measurable (no per-request timeline) | **Good for acute cases**, blind to chronic ones |
| time above threshold | Cleanly separates RR/Adaptive (chronic, 0.70-0.71) from LC/P2C (brief, 0.18-0.25) | Not separately tested at this N | Not separately tested at this N | Not separately tested | Not separately tested | Not measurable | **Best for the chronic failure mode** specifically |
| committed backlog | Tracks EWMA/Adaptive's worst outcomes; near-zero for LC/P2C; low-but-misleading for RR (chronic, not acute) | Rank-agreement: perfect (0 distance) | Rank-agreement: perfect | Present and slightly WORSE (102 vs. 97) | Rank distance 2/3 (better than rho, not perfect) | Direction only (not directly measurable) | **Best overall for acute collapse**; the flagship predictor of this stage |
| unlock latency (diversion timing) | Similar across LC/EWMA/P2C/Adaptive in decision-count terms; explains little alone without volume context | Not separately tested at this N | Not separately tested at this N | Not separately tested | Not separately tested | Not measurable | **Necessary context, not a standalone predictor** — must be combined with offered volume during the reaction window |

## Mandatory Falsification Table

| Emerging Claim | Falsifier | Result | Status |
|---|---|---|---|
| Concentration predicts collapse | High concentration (top1=1.000) without congestion or committed backlog (F1: Adaptive at low load) | **FOUND** | CONFIRMED as a falsifier: concentration alone is insufficient |
| Committed backlog is important | Round-robin: low committed backlog (4) yet chronic non-draining collapse | **FOUND** | Refines, doesn't discard: committed backlog explains ACUTE collapse only; a separate metric (fraction-above-threshold) is needed for CHRONIC collapse |
| Earlier diversion prevents collapse | LC vs. EWMA on the identical canonical scenario: similar diversion TIMING but wildly different committed backlog and outcome | **FOUND, refined** | Diversion timing alone is insufficient — the VOLUME flowing during the reaction window matters as much as the timing |
| P2C differs from EWMA mechanistically | 8 independent seeds with genuine arrival-stream jitter: EWMA committed_backlog>50 in 8/8, P2C in 0/8 | **NOT found** (the distinction survives) | CONFIRMED: a real, seed-independent structural difference |
| Adaptive's advantage is load-driven | Signal ablation: load=0 more than doubles committed backlog (86→206), worse than EWMA's own 97; latency=0 and cache=0 have smaller effects | **NOT found** (the claim survives ablation) | CONFIRMED: Adaptive's resistance is attributable specifically to its Load component |

---

## Stage 15 Verdict

**PASS WITH LIMITATIONS.**

**CENTRAL MECHANISM**: Collapse under concentration-driven capacity pressure is best explained not by one
scalar but by two complementary, precisely-measurable quantities — committed backlog for ACUTE
over-commitment (a policy locks onto a target and lets substantial work accumulate before diverting) and
fraction-of-time-over-capacity for CHRONIC under-provisioning (a policy never adapts at all and
permanently over-allocates to an undersized target) — with concentration (M1) as a necessary but
insufficient precondition for either.

**CONCENTRATION**: Necessary, not sufficient — directly confirmed by intervention (F1), not merely
assumed.

**PEAK PRESSURE**: A real but incomplete signal — distinguishes acute cases from each other but is blind
to round-robin's chronic, moderate-but-permanent failure mode.

**TIME ABOVE PRESSURE**: The best available metric specifically for the CHRONIC failure mode that
committed backlog misses.

**COMMITTED BACKLOG**: Operationally defined, measured, and validated as the strongest available
predictor for ACUTE collapse — perfect rank agreement across a target-count generalization test where raw
peak rho is badly misordered.

**UNLOCK / DIVERSION**: Measurable, but not a standalone predictor — diversion TIMING must be interpreted
together with the VOLUME of traffic flowing during the reaction window, or it misleadingly suggests LC and
EWMA are similarly responsive when their outcomes diverge enormously.

**BEST PREDICTOR**: Committed backlog, for acute collapse specifically — no single predictor covers both
failure shapes.

**RHO**: What remains true: rho correctly identifies THAT a target is under pressure. What does not:
whole-run average rho's magnitude does not reliably rank collapse severity once target count or workload
shape varies — it can even move in the wrong direction (decreasing as severity increases).

**EWMA**: Smoothed-history lock-in — its own past success becomes the reason it keeps over-committing to a
degrading target, because the smoothed estimate lags the target's current state.

**P2C**: Sampling-based comparison structurally avoids full lock-in — confirmed as a real, seed-
independent mechanistic difference from EWMA, not a lucky draw.

**LC**: Reacts to CURRENT in-flight count — the cleanest "unlock" mechanism among all six, diverting before
a large backlog can form.

**WRR**: Static capacity-aware distribution — safe here because its weights already match reality; Stage
14 already showed this fails once reality changes.

**ADAPTIVE**: A multi-signal combination whose resistance to committed-backlog collapse is attributable
specifically to its LOAD component (ablation: removing it more than doubles committed backlog, worse than
EWMA's own); its own P99 was nonetheless the WORST of all six policies in the canonical scenario, showing
"Adaptive is safe" was never fully true even before this ablation.

**CACHE-AFFINITY**: Genuinely unresolved — neither the originally-hypothesized location (absorbing target
during crash) nor the natural alternative (recovered target post-crash) explains Stage 13's interim-
latency effect; reported as an open question, not forced to fit.

**H2 / ALPHA**: Relationship confirmed separate at a finer grain than Stage 14 could show: alpha has SOME
structural effect on the main boundary (changes which target locks up) but does not cleanly reduce
committed backlog, consistent with (not contradicting) alpha's real, causal role in H2's distinct
transient-recognition-lag mechanism.

**CROSS-TOPOLOGY**: Committed backlog generalizes with perfect rank agreement (Level 3 evidence: virtual
engine, multiple topologies).

**CROSS-WORKLOAD**: Committed backlog generalizes imperfectly — a real, disclosed limit, not smoothed
over.

**VIRTUAL VS REAL**: The mechanism's direction reproduces for most conditions (extending Stage 14's own
validated-ceiling reproduction beyond one policy pair) but reverses at the most extreme real overload
level tested — Level 3 evidence with a documented exception, not Level 4 universality.

**FALSIFIERS**: Round-robin's chronic-not-acute failure (refines committed backlog's scope); the cache-
affinity mechanism (genuinely unresolved); the real-engine reversal at extreme overload (a real boundary,
not explained away).

**STATISTICAL ROBUSTNESS**: F6 (P2C vs. EWMA) confirmed across 8 independent seeds with genuine arrival-
stream jitter, Cliff's-delta-level separation (8/8 vs. 0/8). The flagship committed-backlog ranking claim
is a decisive 4-point deterministic comparison (distance 0 vs. 4), not a seeded replication — a real,
disclosed limitation of this stage's own statistical confirmation.

**STRONGEST NEW CLAIM**: Committed backlog, not raw concentration or whole-run rho, is the strongest
available predictor of acute collapse severity, with perfect rank agreement across a target-count
generalization test.

**STRONGEST NEGATIVE RESULT**: Adaptive's own canonical-scenario P99 was the worst of all six policies
tested, despite a comparatively good mean — "Adaptive is safe" was never fully true, and mean latency
alone would have hidden this.

**CLAIMS RETIRED**: "Concentration alone predicts collapse" (never actually claimed this strongly, but F1
forecloses it explicitly); "committed backlog is a universal predictor" (retired in favor of "acute
collapse specifically, alongside a separate chronic-collapse metric"); "the round-robin-beats-EWMA
falsifier is directionally universal" (retired — it reverses under extreme real overload).

**NEW LIMITATIONS**: Threshold sensitivity for committed backlog's own operational definition was checked
qualitatively, not via a full sweep; cache-affinity's mechanism remains unidentified; real-engine
committed backlog is unmeasurable with current instrumentation; the chronic/acute distinction was found
in one 6-policy comparison, not systematically searched for a third shape.

**NEW UNRESOLVED QUESTIONS**: Whether acute and chronic collapse are fundamentally separate axes or
combinable into one quantity; what actually explains the cache-affinity interim-latency effect; whether
the real-engine extreme-overload reversal generalizes beyond this one topology; whether P2C's and WRR's
both being lock-in-resistant reflects the same or different underlying mechanisms.

**EXPERIMENTS**: 6 (`015a`-`015f`), plus the `internal/backlog` package itself with 8 unit tests.
**TESTS**: full existing suite passing throughout (`go test ./...`), plus 8 new hand-computed tests in
`internal/backlog`. **BENCHMARKS**: none added — no performance bottleneck was encountered running these
experiments. **FILES**: `internal/backlog/backlog.go` and `backlog_test.go`; 6 new experiment commands;
6 new result JSON artifacts; `docs/StageArtifacts/Stage15-Plan.md`, this document, and its companion
learning notes; README updates. **COMMITS**: 8 (plan, backlog package + canonical scenario, falsification
program, Adaptive ablation, cache-affinity test, predictor generalization, real-engine validation, and
this documentation commit).

Can FlashFlow now explain policy collapse in terms of a measurable mechanism, rather than simply saying
one policy wins and another loses? **Yes, with a precisely-stated scope**: the sequence that creates
collapse is concentration → committed backlog (acute) or chronic over-allocation → tail-latency collapse;
the point where policies diverge is measurable (committed backlog's magnitude, or fraction-of-time-over-
capacity for the chronic case); the interventions that change that sequence are identified (Adaptive's
Load signal; P2C's sampling structure; a validated real concurrency ceiling); the major policy families
are explained mechanistically (six distinct mechanism classes, not two); and the explanation's limits are
stated precisely (cache-affinity's own effect remains unexplained; cross-workload prediction is imperfect;
the real engine diverges at extreme overload). That is the strongest outcome this stage's own charter
asked for, and no stronger claim than the evidence supports is made here.
