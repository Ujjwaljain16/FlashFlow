# Stage 11 — Research Validation: Design Plan and Findings

**Status at start of this document: plan only (sections 1-7). Results/findings sections are appended
as experiments actually run — nothing below "Controls" was written before its corresponding
experiment executed.**

## 1. Motivation

Stage 10 built the experimental platform (traffic generation, provenance/SeedTree, declarative
chaos, an `ExperimentEngine` unifying virtual and real execution, queueing attribution, telemetry,
and three tuner tiers). None of that machinery has yet been pointed at the actual scientific question
this project exists to answer: under what conditions does each routing policy — especially
Adaptive — actually help, and why? Stage 7/8's own evidence (Adaptive wins 62.5-70% of scenarios, not
all of them, with a stated fairness cost) already implies real, uncharacterized regime boundaries.
Stage 11 uses Stage 10's platform to map them, attack Adaptive directly, and test whether the virtual
engine's conclusions actually transfer to a distribution it wasn't tuned on.

## 2. Research Questions (verbatim from the assignment, restated for tracking)

- **Q1 (regime map)**: under what workload/topology/failure conditions does each policy win or lose?
- **Q2 (adaptive failure)**: can we construct credible scenarios where Adaptive loses to something simpler?
- **Q3 (mechanism)**: can wins/losses be explained via utilization/queueing/latency/failure/cache
  mechanisms, not just aggregate latency?
- **Q4 (robustness)**: do conclusions hold across seeds, workload variation, and unseen combinations?
- **Q5 (virtual vs. real)**: does the virtual engine preserve conclusion *direction and ranking*, even
  where absolute latency differs?
- **Q6 (reproducibility)**: can every claim be regenerated from a documented scenario/seed/provenance
  artifact?

## 3. Hypotheses (stated before running anything, to be confirmed/refuted, not fitted after)

- **H1**: Round Robin is competitive with or beats Adaptive specifically under homogeneous, stable
  (no-failure) load, since Adaptive's signals carry no useful information when every target is
  identical — a direct extension of Stage 3's own EWMA-lock-in finding to the newer policy.
- **H2**: Adaptive's advantage narrows or reverses when the "best" target changes faster than
  `StaleAfter` allows the router to notice — an oscillation/staleness attack.
- **H3**: Adaptive's cache-affinity signal can be made to work against it by making the
  frequently-requested key's affinity target the one that later degrades.
- **H4**: The virtual engine will preserve *policy ranking* (which policy has the lowest mean latency)
  across a scenario shared with a real-engine run, even though absolute latencies won't match (the
  real engine has no queueing model to reproduce exactly, per Stage 10's own attribution caveats).
- **H5**: Under a genuinely distribution-shifted evaluation set (not just a disjoint seed range from
  the same generator — Stage 10's own audit already flagged that Stage 8's Holdout is same-
  distribution), Adaptive's win margin will shrink relative to the in-distribution case, though
  whether it disappears, reverses, or merely narrows is not assumed in advance.

## 4. Methodology

All experiments run on the virtual engine unless a program explicitly targets Program F
(virtual-vs-real). Every experiment is built through `internal/engine.Experiment` +
`replay.DeriveSeeds`, so every run carries a full `SeedTree` (Global/Traffic/Topology/Failure/Policy)
rather than one flat seed — this is what lets Program G hold three axes fixed while deliberately
varying one. Traffic is generated via `internal/traffic.Generate` (not hand-rolled arrival lists) so
workload shape is an explicit, inspectable parameter, not implicit in a scenario literal. Failures are
expressed as `internal/chaos.Schedule` YAML compiled to `replay.FailureWindow` via
`ToFailureWindows`, for the same reason. Where a claim needs uncertainty quantification, `internal/
statistics`'s existing tools are used (`BootstrapCI`/`BootstrapDiffCI` for CIs, `MannWhitneyU`/
`CliffsDelta` for distribution comparisons) — chosen per-question, not by default, matching Stage 6's
own discipline. Attribution claims go through `internal/attribution.UtilizationFromWorld`, not ad hoc
math.

## 5. Experiment Matrix (bounded, not an arbitrary giant sweep)

**Topology** (3 levels): homogeneous (3 identical targets), moderate heterogeneity (targets differ up
to ~2x), severe heterogeneity (targets differ up to ~4x, matching Stage 10's own demo scenario).

**Workload** (3 patterns, from `internal/traffic`): Constant, Burst (a load spike mid-run), FlashCrowd
(asymmetric fast-rise/slow-decay spike).

**Failure** (3 conditions): none, single isolated failure/recovery, failure timed to coincide with a
load transition (the "failure during flash crowd" case §5 names explicitly).

**Policies**: all 6 (`round-robin`, `weighted-round-robin`, `least-connections`, `ewma`, `p2c-load`,
`adaptive`).

This gives 3×3×3 = 27 scenario configurations × 6 policies = 162 runs for the regime map (Program A)
— large enough to find real regime structure, small enough to run in well under a minute on the
virtual engine and to read the output of by hand. Programs B/C/D/F/G are separate, smaller, and each
individually justified below rather than folded into this grid (an adversarial scenario is
constructed to test one specific mechanism, not swept generically).

## 6. Controls

- Every pairwise policy comparison within one scenario configuration uses the *identical* `Scenario`
  value (same object, same `Seeds`) — `engine.VirtualEngine.Run` for the first policy,
  `.Replay` for every subsequent one, matching Stage 10's own established pattern.
- Program D (distribution shift) changes the *generating parameters* themselves (heterogeneity range,
  workload pattern, failure timing distribution), not merely the seed — Stage 10's audit's own
  criticism of Stage 8's Holdout is not repeated here.
- Program G explicitly holds three of four `SeedTree` axes fixed while varying the fourth, per
  Stage 10's own `TestGenerate_IndependentAxisControl` precedent, extended to a full research check
  rather than a unit test of the mechanism alone.
- No scenario is regenerated after seeing its result to chase a different outcome. A scenario that
  fails to show an expected adversarial effect is reported as a negative result (Program B's own
  explicit instruction).

---

## 7. Program A Results — Policy Regime Map

**Experiment**: `cmd/experiment-011a`, artifact `experiments/011-research-validation/results/011A-policy-regime-map.json`
(162 runs: 27 scenario configs x 6 policies, virtual engine, `Run` for the first policy per config then
`Replay` for the remaining 5 against the byte-identical `Scenario`).

**Headline table** (mean-latency winner by scenario, full table in the JSON artifact):

| Topology | Mean-latency winner (all 9 workload/failure combinations) |
|---|---|
| homogeneous | round-robin, every time, exact tie at 30.00ms with every other policy |
| moderate heterogeneity | ewma, every time |
| severe heterogeneity | ewma, every time |

Win count across all 27 configs: round-robin 9/27 (all 9 homogeneous configs), ewma 18/27 (all 18
heterogeneous configs), weighted-round-robin/least-connections/p2c-load/**adaptive 0/27**.

**H1 confirmed exactly, and more strongly than hypothesized**: under homogeneous load, every policy
ties Round Robin at 30.00ms mean latency — not "competitive," identical. With three interchangeable
targets, no signal any policy computes carries information, so every selection strategy converges to
the same outcome. This is a clean, expected result, not a discovery.

**The real finding is why EWMA beats Adaptive under heterogeneity, and it is a virtual-engine
artifact, not evidence about real routing quality.** `internal/replay.RunWorld` has never had a
queueing/contention model — this is a documented design choice from Stage 5 ("the virtual network/
service model is intentionally flat: a fixed service time per request, no queueing, no finite
capacity," `docs/learning/005-virtual-time.md`) and Stage 10's own plan ("`RunWorld` has no queueing/
contention model," `docs/StageArtifacts/Stage10-Plan.md`). Concretely: a request's simulated latency
is `serviceTimes[target]`, a fixed per-target constant, looked up once at dispatch
(`internal/replay/world.go:200-201`) — it never depends on how many other requests are concurrently
in flight at that same target. Piling every request onto the single fastest target therefore costs
**nothing** in this simulator, no matter how overloaded that target's real capacity would be.

EWMA (a pure latency-tracking policy, no fairness or capacity signal) exploits exactly this: in
`severe/constant/none`, EWMA sends 97.3% of all requests (`max_share=0.973`) to edge-a (15ms service
time), driving its attribution-computed utilization to **ρ=1.09 — already past the overload
threshold** — while edge-b and edge-c sit at ρ=0.02 and ρ=0.08, almost completely idle. Because the
model applies zero latency penalty for that overload, EWMA's mean latency (15.90ms) is nonetheless the
best of all six policies. Adaptive, by contrast, keeps all three targets in a comfortable, balanced
band (ρ=0.56 / 0.74 / 0.74, `max_share=0.503`) — the behavior you would actually want from a
capacity-aware router protecting real infrastructure — and is penalized for it under this metric
(mean 27.38ms, worse than EWMA's 15.90ms). Weighted-round-robin and least-connections show the same
pattern relative to EWMA (balanced utilization, worse raw mean). The magnitude of EWMA's advantage
scales with heterogeneity severity exactly as this mechanism predicts: 10.9-36.8% under moderate
heterogeneity, 24.2-72.2% under severe — the gap widens as concentrating onto the single fastest
target becomes more attractive relative to spreading load.

**What this does and does not show**: it does not show EWMA is a better policy than Adaptive, or that
Adaptive is broken. It shows that *mean latency measured by the current virtual engine* structurally
rewards maximal load concentration onto whichever target has the lowest fixed service time, because
the engine cannot express the cost that concentration would actually incur (queueing delay, timeout
risk, cascading failure under a real capacity limit). This is precisely the Q3 "mechanism, not just
aggregate latency" requirement: the aggregate number here is real and reproducible, but taking it at
face value as "EWMA routes better than Adaptive" would be an overclaim the model cannot support. It
also means **Program F (virtual vs. real validation) is not merely a robustness check for Stage 11 —
it is the only way to know whether this specific ranking (EWMA > everything else on mean latency under
heterogeneity) survives contact with a real engine that actually queues concurrent requests.** That
comparison is reported in Section 10 below.

**What Would Falsify This claim** (mechanism: EWMA's win is a no-queueing-model artifact, not real
superiority):

| Claim | Falsifier |
|---|---|
| EWMA's mean-latency win under heterogeneity is explained by unconstrained load concentration, not by superior routing | If the real engine (which does have real, OS-level queueing/contention) shows EWMA *still* beating Adaptive on mean latency by a comparable margin in the equivalent scenario, this explanation is wrong or at least incomplete |
| The virtual engine cannot express a latency cost for overutilization | If a future version of `RunWorld` is found to already model contention some other way (e.g., via `health.Registry` degradation) that this analysis missed |

Fairness signal (max_share) corroborates the mechanism independently of the utilization numbers: EWMA
concentrates 85-99% of traffic on one target in every heterogeneous config; Adaptive stays in the
30-73% range, consistent with Stage 7/8's own previously-reported "Adaptive trades some fairness for
latency" framing being, if anything, backwards *relative to EWMA specifically* — here Adaptive is the
policy buying fairness/balance, at a cost, not the one taking it.

## 10. Program F Results — Virtual vs Real Validation

**Experiment**: `cmd/experiment-011f`, artifact `experiments/011-research-validation/results/011F-virtual-vs-real.json`.
Rather than a generic sweep, this program targets Program A's single sharpest, most surprising
finding directly: does the virtual engine's "EWMA beats Adaptive on mean latency under heterogeneity"
ranking (Section 7) survive contact with the real engine, which has genuine OS-level concurrency the
virtual engine's flat, queueing-free model cannot express? Same topology (severe: 15/30/60ms),
same traffic pattern (constant, `HotColdKeys(0.5)`), same 3 policies (round-robin, ewma, adaptive), run
through both `VirtualEngine` and `RealEngine`.

**This program found a real, severe, previously-undiscovered defect in `internal/engine.RealEngine`,
not just a modeling-fidelity gap.** The first run produced a result that looked like a dramatic
cross-engine disagreement: EWMA and Adaptive both showed `max_share=1.000` in the real engine —
100% of all 300 requests landing on a single target — but repeating the run several times (fresh
process each time) showed the *specific* locked target changing from run to run (sometimes the
fastest 15ms edge, sometimes the slowest 60ms one), which is inconsistent with "the real engine
correctly found a genuinely dominant target" and consistent instead with "the decision is arbitrary."

**Mechanism, confirmed by direct code inspection and an ablation, not assumed:**

- `internal/replay.RunWorld` (the virtual engine) explicitly bridges every dispatch/completion to the
  policy's own trackers via `Instrumentation.OnDispatch`/`OnComplete` (`world.go:198,203`).
- `internal/engine.RealEngine.run` built the selector via `selector, _ := policy.New(...)` — **the
  `Instrumentation` return value was discarded**. `policy.New` (for `least-connections`, `ewma`,
  `p2c-load`, and `adaptive`) always constructs its OWN fresh `LoadTracker`/`LatencyTracker` internally
  (`internal/replay/policies.go`). `proxy.ReverseProxy` separately maintains its OWN, DIFFERENT pair of
  trackers (`internal/proxy/proxy.go`'s `p.loadTracker`/`p.latencyTracker`, updated correctly on every
  real dispatch/completion at `proxy.go:186-187,300`). These are two different objects. Nothing wired
  them together, so the selector's own tracker — the one it actually reads from — never received a
  single real observation for the ENTIRE duration of any `RealEngine`-driven experiment. Every decision
  after cold start was a tie among identical neutral scores, decided once by a fixed, essentially
  arbitrary tie-break (alphabetical URL sort for Adaptive; `available` iteration order for EWMA), and
  then repeated identically for all 300 requests — hence exactly 100% concentration, onto whichever
  target the tie-break happened to favor (itself dependent on Go's randomized map iteration over
  `RealExperimentConfig.Edges`, explaining the run-to-run variation in which target won).
- **Ablation, to rule out an alternative hypothesis**: before concluding this, cache affinity (a real,
  documented Adaptive signal, weight 0.1) was considered as a competing explanation — the workload's
  `HotColdKeys(0.5)` traffic has a hot key that could plausibly self-reinforce onto one target. Rerunning
  with every request given a fully distinct key (`uniqueKeyTrafficParams`, so `scoreCache` is always 0
  for every target on every request) still produced `max_share=1.000` for both EWMA and Adaptive,
  ruling cache affinity out as the (or even a necessary) cause and confirming the frozen-tracker
  explanation directly (`cmd/experiment-011f`'s ablation block; see the JSON artifact's `real-ablation-
  unique-keys` entries).

**This is not a modeling-fidelity limitation like Program A's — it is a wiring bug** (this project's
own stated Stage 10 goal, "the same policy code runs correctly in both engines," was silently violated
for every dynamic policy run through `RealEngine`). It means every prior `RealEngine`-driven result for
`least-connections`, `ewma`, `p2c-load`, or `adaptive` (through this project's history) reflects
cold-start tie-break behavior, not the policy's intended live-signal logic.

**Fix applied** (`internal/engine/real.go`): the real engine's `proxy.Config` now sets
`ExposeDebugHeaders: true`, and the dispatch loop reads the `X-Selected-Edge` response header (already
existing, purpose-built observability infrastructure — `internal/httpx`'s `HeaderSelectedEdge`) to
learn which target served each real request, then calls the previously-discarded `instr.OnDispatch`/
`OnComplete` to feed that observation back into the policy's own tracker. This restores the **latency**
signal correctly and completely: it is a genuine, real-time, per-request observation, semantically
identical to what the virtual engine's own bridge does. **The load (in-flight count) signal is
deliberately NOT restored the same way and remains a disclosed, open limitation**: `OnDispatch`/
`OnComplete` are only knowable after a response returns in this architecture, so calling both back-to-
back post-hoc would net to zero without ever reflecting genuine concurrent in-flight state — worse
than clearly disclosing the gap. Fixing load tracking properly requires either changing
`PolicySpec.New`'s contract to accept externally-owned trackers or `proxy.Config` to accept
pre-built ones (`proxy.ReverseProxy` already exposes `LoadTracker()`/`LatencyTracker()` accessors and
`SetSelector`, suggesting this was anticipated but never finished for real-engine use) — scoped as a
follow-up, not attempted here, since it is a genuine cross-package contract change, not a local fix.

**Result after the fix** (`cmd/experiment-011f`, rerun 3x, all consistent):

| Policy | Virtual p50 | Real p50 (post-fix) | Virtual max_share | Real max_share (post-fix) |
|---|---:|---:|---:|---:|
| round-robin | 30.00ms | 30.4-30.7ms | 0.337 | 0.333 |
| ewma | 15.00ms | 15.5-15.6ms | 0.973 | **0.973** |
| adaptive | 15.00ms | 15.5-15.6ms | 0.503 | **1.000** |

**H4 is confirmed for EWMA, and precisely NOT (yet) confirmed for Adaptive, for a specific, now-known
reason.** EWMA's real-engine behavior now closely tracks its virtual-engine behavior on both metrics
(max_share 0.973 in both; p50 within 0.6ms) — the ranking agrees (EWMA has the lowest p50 in both
engines), directly supporting H4. Adaptive's ranking direction now agrees too (lowest p50 tied with
EWMA in both engines, correctly identifying the fast target — a real improvement from before the fix,
when Adaptive's real p50 was essentially a coin flip across the three targets), but its concentration
degree does not (0.503 virtual vs 1.000 real) — precisely because Adaptive weighs Load equally with
Latency (`DefaultAdaptiveWeights`: 0.4/0.4), and Load remains uninstrumented in the real engine. With a
correct latency signal but a permanently-idle-looking load signal, Adaptive correctly finds the fast
target (matching virtual's direction) but has no real-engine mechanism telling it to spread away from
that target once found (unlike the virtual engine, where Load is correctly tracked and does exactly
that). This is a clean, mechanistically-explained partial validation, not an unexplained discrepancy.

**What Would Falsify This**:

| Claim | Falsifier |
|---|---|
| The original 100%-lock-in was caused by `RealEngine` never feeding real observations to the policy's tracker, not by a real property of the scenario | If reverting the `real.go` fix and rerunning still shows the SAME target winning every time across many fresh processes (would mean something other than tracker-freezing determines the outcome) |
| Fixing only the latency signal (not load) explains Adaptive's remaining virtual/real concentration gap | If Adaptive's real max_share stayed at 1.000 even after ALSO wiring load tracking (a follow-up not attempted here) — would mean load isn't the (or the only) remaining cause |
| Cache affinity is not the cause of the original lock-in | Already tested directly via the unique-key ablation; confirmed not the cause |

**Consequence for Program A's Section 7 finding**: Program A's virtual-only conclusion — "EWMA wins on
mean latency under heterogeneity because it concentrates load without penalty in a queueing-free model"
— is now independently corroborated on the real engine for EWMA specifically (post-fix), which is
evidence the *ranking* (not the *absolute numbers*, and not the *degree of concentration* for every
policy) transfers. It is not evidence that Adaptive's virtual-engine balancing behavior would actually
protect real infrastructure the way Section 7 speculated — that specific claim remains unverified,
pending the load-tracking follow-up noted above.

## 11. Regression Coverage Added

`TestRealEngine_Run_EWMAPrefersFastRealTarget` (`internal/engine/real_test.go`) — a genuine discovery
warranting a regression test per this stage's own charter (a confirmed bug, not a speculative addition).
Two real edges with a large, deliberate service-time gap (2ms vs 100ms); asserts the real engine's
aggregate p50 lands well below the slow target's fixed delay and that one target's share is decisively
majority — both would fail intermittently (roughly coin-flip, across repeated runs) under the pre-fix
frozen-tracker behavior and pass consistently once real observations reach the policy's tracker. Run 5x
fresh (`-count=1`) during development with no failures.

## 12. Program B Results — Adversarial Adaptive Testing

**Experiment**: `cmd/experiment-011b`, artifact `experiments/011-research-validation/results/011B-adversarial-adaptive.json`.

**Capability gap disclosed up front**: H2 (a latency-oscillation/staleness attack — "the best target
changes faster than `StaleAfter` allows detection") is **not implemented**. It requires a per-target
service time that changes mid-run, and `internal/replay.TargetProfile` has no such mechanism —
`ServiceTime` is one fixed value for the entire `Scenario`. Rather than fake this via an unrelated
proxy mechanism (e.g. treating a crash/recover cycle as a stand-in for "got slow, got fast"), Program B
reports this honestly as a genuine platform-capability gap: **H2 as originally stated cannot be tested
with Stage 10's current machinery.** Adding time-varying `ServiceTime` would be a real capability
addition, not attempted here per this stage's own charter (add capability only when genuinely
necessary and demonstrated missing — this is such a case, but implementing it is scoped as a follow-up,
not squeezed into this investigation).

### B1 — Cache-Affinity Deception (H3): CONFIRMED, decisively

**Setup**: 3 targets (edge-a 15ms, edge-b 30ms, edge-c 60ms), constant workload with a hot key (50% of
traffic). edge-a — the fastest, and the hot key's natural affinity target — crashes at t=1s and
recovers at t=2s, objectively regaining "best target" status for the remaining 2s of the 4s run.

**Hypothesized mechanism**: Adaptive's cache-affinity signal (weight 0.1) rewards whichever target
last served a given key, independent of that target's current latency. If the fixed 0.1 score gap
isn't overcome by the real latency difference once weighted (0.4), Adaptive should show a measurably
*lower* post-recovery share of hot-key traffic returning to the recovered target than a latency-only
policy (EWMA) does.

**Result**: of the 75 post-recovery hot-key routing decisions,

| Policy | % routed back to recovered edge-a | Mean latency (whole run) |
|---|---:|---:|
| round-robin | 30% (chance level, ignores latency entirely) | 37.42ms |
| ewma (latency only) | **94%** | 19.67ms |
| adaptive (default weights) | **0%** | 30.51ms |
| adaptive, `Weights.Cache` forced to 0 | **54%** | 28.29ms |

**Adaptive never once routed the hot key back to the recovered, objectively-fastest target for the
remaining two seconds of the scenario — a complete, permanent cache-affinity lock-in, confirmed
exactly as hypothesized, and confirmed CAUSALLY, not just by pattern-match**: zeroing out
`AdaptiveConfig.Weights.Cache` and rerunning the identical scenario jumps the return rate from 0% to
54% — direct evidence the affinity term itself, not some other coincidental factor, is what causes the
lock-in (the falsifier from the table below was checked, not just proposed). The 54% (vs EWMA's 94%)
still falls short of full latency-driven behavior because Adaptive's Load term (weight 0.4) remains
active and can still mildly disfavor edge-a if it has accumulated relative load — a secondary,
smaller effect layered on top of the primary cache-affinity cause. This is a genuine case of Adaptive
losing to a simpler policy (EWMA) for a directly identified, mechanistically-explained, and now
causally-verified reason. Adaptive's own mean latency (30.51ms) sits worse than EWMA's (19.67ms) as
the direct, traceable consequence.

**What Would Falsify This**:

| Claim | Falsifier |
|---|---|
| Cache-affinity's fixed 0.1 weight, not something else, causes the 0% return rate | **Checked directly**: forcing `Weights.Cache = 0` raises the return rate from 0% to 54% — confirmed, not merely consistent |
| This is deterministic, not a seed artifact | Rerun with a different `Global` seed (not yet done — flagged for Program G) |

### B2 — Correlated Failure (signal scarcity): NOT CONFIRMED — a genuine negative result

**Setup**: 3 near-homogeneous targets (20/25/30ms); edge-a AND edge-b crash simultaneously at t=1.5s,
both recover at t=2.5s, leaving only edge-c as the sole available target for 1 second.

**Hypothesized mechanism**: with exactly one available target during the outage, no policy's selection
signal can matter (there is nothing to choose between); the interesting question was whether Adaptive's
heavier multi-signal decision process would behave WORSE once all three targets become simultaneously
available again (a burst of re-balancing decisions).

**Result**: adaptive was NOT worse here — if anything, it was the best-balanced of the three
(`max_share` 0.339 vs round-robin's 0.500 and EWMA's 0.732), with mean latency (24.93ms) between the
other two. **This specific adversarial construction did not make Adaptive lose.** Per this stage's own
discipline, this is reported as a real negative result, not redesigned repeatedly until a failure
appears. It suggests Adaptive's load-based signal (Section 7's "keeps utilization balanced" finding)
generalizes to the simultaneous-recovery case too, at least in the virtual engine, where load tracking
works correctly (Program F Section 10) — a genuinely different situation from Program F's real-engine
finding, where Adaptive's own load signal is currently uninstrumented.

## 13. Program C Results — Recovery and Adaptation Dynamics

**Experiment**: `cmd/experiment-011c`, artifact `experiments/011-research-validation/results/011C-recovery-dynamics.json`.
A 5-phase, 7.5s scenario (edge-a 15ms / edge-b 30ms / edge-c 45ms): phase 1 all up (edge-a best), phase
2 edge-a down (edge-b best-of-available), phase 3 edge-c ALSO down (only edge-b left — the "C fails"
phase, using compounding failure since the assignment's literal "capacity changes" can't be expressed
without time-varying `ServiceTime`, the same gap Program B disclosed), phase 4 edge-a recovers, phase 5
edge-c recovers too (full topology restored).

**Negative/methodological result, directly connected to Program A's root cause**: the originally-
planned `transition_p99` metric (p99 over a 300ms window after each phase boundary) is **identical**
to `steady_state_p99` for every single policy (ratio exactly 1.00x, all six policies). This is not
because adaptation is instant and costless — it's because **the virtual engine cannot express an
"adaptation cost" in p99 at all**, for the same reason Section 7 identified: with no queueing model,
the worst a temporarily-wrong routing decision can cost is exactly one fixed, bounded per-target
service time (there's no queue buildup, no compounding delay from a string of bad decisions). p99 is
therefore bounded above by the slowest target in `available` regardless of whether the policy is
"steady" or "adapting," so this specific metric — as specified in the assignment — is structurally
unable to distinguish the two in the current platform. This generalizes Section 7's finding: it isn't
only mean latency under load concentration that the no-queueing model can't penalize; the entire class
of "does a policy pay extra during a topology transition" claims is out of reach for percentile-based
metrics here.

**The finer-grained signal that IS visible — per-phase mean latency**:

| Policy | Phase 1 (A best) | Phase 2 (A down) | Phase 3 (A+C down) | Phase 4 (A back) | Phase 5 (all up) |
|---|---:|---:|---:|---:|---:|
| round-robin | 29.72 | 37.16 | 30.68 | 23.11 | 29.31 |
| weighted-round-robin | 24.58 | 34.95 | 30.54 | 20.81 | 24.08 |
| least-connections | 25.97 | 34.36 | 30.41 | 20.73 | 25.68 |
| ewma | 16.51 | 28.90 | **30.00** | 16.22 | **15.00** |
| p2c-load | 27.62 | 36.35 | 30.55 | 21.55 | 27.95 |
| adaptive | 27.90 | 35.32 | 30.54 | 20.81 | 27.92 |

Two things worth naming directly: **phase 3 converges to ~30.4-30.7ms for every non-EWMA policy** (and
EWMA lands at exactly 30.00ms) because only one target (edge-b) is available — every policy's
selection signal is moot with one candidate, an exact structural confirmation of Program B's B2
"signal scarcity" mechanism, now reproduced in a second, independently-designed scenario. Second,
**recovery is essentially immediate at this granularity**: no policy shows a lingering elevated mean in
the phase immediately after a recovery (phase 4/5 fall back in line with phase 1 for every policy),
which is itself informative — it means whatever health-detection lag `internal/health.Registry` has
was short enough, relative to this scenario's 1.5s phase length and 300ms transition window, not to
show up as a visible cost here. This scenario cannot rule out a detection-lag cost at a finer time
resolution than was measured.

**Consistent with, not contradicting, Program A**: EWMA has the lowest mean in every single phase,
including the two recovery phases, for the same reason established in Section 7 — pure latency-greedy
concentration, unpenalized by any queueing cost, wins on this metric in a heterogeneous topology.
Adaptive tracks close to weighted-round-robin/least-connections/p2c-load throughout, not because it
performs badly in an absolute sense, but because (per Section 7) it deliberately balances load in a way
this metric doesn't reward.

## 14. Consolidated Note: A Real Platform-Capability Gap Spans Programs A, B, and C

Three independently-designed experiments — Program A's regime map, Program B's H2 (declined), and
Program C's "capacity changes" phase — all ran into the same underlying limitation:
`internal/replay.TargetProfile.ServiceTime` is fixed for an entire `Scenario`, and `RunWorld` has no
queueing/contention model (a Stage 5 design choice, `docs/learning/005-virtual-time.md`). This is the
single most consequential platform-capability finding of Stage 11 so far: it doesn't just affect one
metric in one experiment, it structurally limits what the virtual engine can be asked about the
research questions this stage exists to answer (regime boundaries under real capacity pressure,
staleness/oscillation attacks, and adaptation-cost measurement all run into it). Adding a genuinely
time-varying per-target latency and/or a real queueing model would be a substantial platform change,
correctly out of scope for Stage 11 itself, but is now an evidence-backed candidate for a future
stage's actual design goal, not a speculative "nice to have."

## 15. Program D Results — Distribution Shift

**Experiment**: `cmd/experiment-011d`, artifact `experiments/011-research-validation/results/011D-distribution-shift.json`.
"Development" is one of Program A's own moderate-heterogeneity/constant/no-failure configurations.
"Evaluation-shifted" changes **five** generating parameters at once relative to development —
heterogeneity severity (2x spread → 6x spread), workload shape (constant → flash crowd), key skew
(50% hot → 85% hot), intensity (flat 75req/s → 30-150req/s spike), and adds a failure timed during the
flash-crowd peak (none → during-transition) — not merely a disjoint traffic seed. This directly avoids
the same-distribution mistake Stage 10's own audit found in Stage 8's Holdout set (`Stage10.md`'s own
callout, referenced in Section 3's H5).

**Result**:

| Policy | Dev mean | Dev margin behind best | Shifted mean | Shifted margin behind best |
|---|---:|---:|---:|---:|
| round-robin | 29.97ms | 47.1% | 37.06ms | 112.2% |
| ewma | 20.37ms | 0.0% (wins both) | 17.47ms | 0.0% (wins both) |
| adaptive | 27.46ms | 34.8% | 22.20ms | **27.1%** |

**H5 answered directly, not assumed: Adaptive's disadvantage relative to the best policy (EWMA)
narrowed under genuine distribution shift** (34.8% → 27.1%), rather than widening or reversing. The
mechanism is visible in `max_share`: EWMA's own concentration dropped from 0.977 (development) to
0.647 (shifted), while Adaptive's rose slightly (0.502 → 0.620) — under the flash-crowd's transient
dynamics plus a mid-run failure, EWMA can no longer lock onto a single target as completely as it does
under pure constant load (the interim failure forces at least one real redistribution, and the
flash-crowd's own arrival-rate swings change which target looks momentarily best more often than a
flat-rate stream does). EWMA's own advantage partially erodes under this specific kind of shift, which
narrows — without eliminating — the gap to Adaptive. Round-robin, having no adaptive mechanism at all,
got measurably worse in relative terms (47.1% → 112.2% behind best), the expected direction for a
policy with zero responsiveness to worsening conditions.

**Scope of this claim, stated precisely**: this demonstrates *a* genuine distribution shift narrows
Adaptive's disadvantage in *this* direction, for *this* specific combination of changed factors — it is
not evidence that distribution shift always favors Adaptive, nor a general claim about "robustness."
Only one shifted distribution was tested; Section 16 below inventories this as an explicit limitation
requiring more shifted distributions (varying which factors change, and by how much) before a general
claim about Adaptive's shift-robustness would be warranted.

## 16. Program G Results — Seed and Reproducibility Attack

**Experiment**: `cmd/experiment-011g`, artifact `experiments/011-research-validation/results/011G-reproducibility-attack.json`.
Programs A-D built scenarios with literal, manually-specified topologies (no randomness in target
count/names/service-times), so they don't exercise `SeedTree`'s `Topology`/`Failure` axes as generators
at all. Program G instead uses `internal/tuning.ScenarioSpace.Generate` — the actual, only generator in
this codebase that draws topology and failure windows from those two axes — for a genuine test.

**G1 (repeat-run reproducibility): CONFIRMED.** The identical `Experiment` run 3 times produced
byte-identical `Records`, `Completions`, and `RejectedCount` every time (full trace comparison via
`reflect.DeepEqual`, not just summary statistics, which could coincidentally match even if underlying
decisions differed).

**G2 (SeedTree axis independence): a genuine, previously-undetected reproducibility hazard found.**
Holding three axes fixed and varying one at a time:

| Axis varied | Targets changed | Arrivals changed | Failures changed | Isolation holds? |
|---|---|---|---|---|
| Traffic only | No | Yes | No | **Yes** |
| Topology only | Yes | No | **Yes** | **No** |
| Failure only | No | No | Yes | **Yes** |

Varying ONLY the Topology seed also changed the failure window — violating the isolation `Generate`'s
own doc comment claims ("topoRNG only ever affects target count/names/service-times"). **Root cause,
directly confirmed rather than inferred**: `Generate` draws target count `n` from `topoRNG`, then later
draws `failureRNG.Intn(n)` to pick which target fails — since `n` is topology-seed-dependent whenever
`MinTargets != MaxTargets` (true of `DefaultScenarioSpace`: 2-5), the *range* `failureRNG.Intn` draws
from shifts when only Topology changes, even though `failureRNG`'s own seed never did. Confirmed
directly: re-running with `MinTargets = MaxTargets = 3` (removing `n`'s topology-dependence) made the
failure window byte-identical across the same Topology-seed change — isolating the exact mechanism, not
just observing the symptom.

**Why this was never caught**: the existing `TestGenerate_IndependentAxisControl`
(`internal/tuning/scenario_test.go`) only ever checked ONE direction — that varying Failure leaves
Topology/Traffic unchanged (true, and still true) — never the reverse (that varying Topology leaves
Failure unchanged, which is false). This is exactly a previously-untested invariant, per this stage's
own charter for challenge-suite expansion.

**Scope of impact**: this does NOT affect any Stage 11 finding above (Programs A-D used literal,
non-generated topologies, never touching `ScenarioSpace.Generate`'s Topology/Failure interaction), but
it DOES mean any past or future claim resting on "the Development/Holdout tuning scenarios vary Failure
independently of Topology" (Stage 8's tuning work uses this same generator) should be treated with this
caveat. **Not fixed here** — a real fix (e.g. drawing the failure target from a fixed-size name pool
rather than `Intn(n)`, or drawing `n` after `failureRNG`) is a legitimate, scoped follow-up, not
attempted in this pass since no Stage 11 conclusion depends on it.

**Regression coverage added**: `TestGenerate_TopologySeedCanLeakIntoFailureSelection`
(`internal/tuning/scenario_test.go`) pins the current (leaky) behavior and directly confirms the
target-count mechanism via the fixed-`n` case, so a future change either preserves this documented
limitation deliberately or the test is updated to assert the improvement — it cannot silently regress
or silently "fix itself" unnoticed.

**G3 (policy-seed isolation): confirmed, with an explicit, policy-dependent nuance.** `seeds.Policy` is
never consumed at scenario-generation time — only at routing time, and only by `p2c-load` (its pair-
sampling randomness). Varying `seeds.Policy` alone, holding an identical `Scenario` fixed:

| Policy | Consumes `seeds.Policy`? | Outcome changed when `seeds.Policy` varies? | Matches expectation? |
|---|---|---|---|
| round-robin | No | No | Yes |
| ewma | No | No | Yes |
| adaptive | No | No | Yes |
| p2c-load | Yes | Yes | Yes |

All four match their expected behavior exactly — there is no unexpected leakage on this axis, but the
finding is worth stating explicitly rather than collapsing into one pass/fail: "does the Policy seed
matter" has a policy-dependent answer, not a universal one, and a caller expecting `AdaptivePolicy` to
produce different behavior under a different `seeds.Policy` (with everything else fixed) would be
mistaken — Adaptive has no consumer of that axis at all.

## 17. Program E Results — Mechanistic Attribution

**Experiment**: `cmd/experiment-011e`, artifact `experiments/011-research-validation/results/011E-mechanistic-attribution.json`.
Reuses Program A's flagship severe/constant/none scenario. For EWMA and Adaptive, at each of the 3
targets, computes: `Lambda` (throughput, req/s), `W` (mean sojourn time, ms), and — critically — an
**independently-measured** `L` (time-averaged in-flight request count), via direct event-timeline
integration over exact dispatch/completion timestamps (a step function: +1 at each dispatch, -1 at
each completion, area under the curve divided by horizon). This is a genuinely separate computation
from `Lambda*W`, not a restatement of it, so comparing the two via `internal/attribution.CheckLittlesLaw`
is a real (if, in this specific model, expected-to-pass) consistency check.

**Result**: relative error between measured `L` and predicted `Lambda*W` is ≈0 (at floating-point
precision) for all 6 target/policy combinations — e.g. EWMA/edge-a: `Lambda=72.75 req/s, W=15.00ms,
L(measured)=1.091, L(predicted)=1.091, relErr=0.0000`. `L(measured)` also equals `ρ` (utilization,
from `UtilizationFromWorld`) exactly in every row, since capacity is normalized to `1/ServiceTime`.

**What this attribution supports and does not — stated explicitly, per this stage's own instruction not
to overclaim**:

- **Supports**: internal consistency of the L/Lambda/W bookkeeping across two independently-computed
  paths (direct timeline integration vs. utilization-derived). This confirms there is no arithmetic or
  measurement inconsistency in how these three quantities are derived from the same underlying
  `WorldResult`.
- **Does NOT support**: any claim about real queueing or wait-time behavior. `W` here is simply the
  target's fixed `ServiceTime` — there is no wait component in this model at all (per Section 7/14's
  no-queueing-model finding), so Little's Law holding almost exactly is an expected mathematical
  consequence of the model's own construction (`L = Lambda * ServiceTime`, trivially, when nothing ever
  waits), not an independent empirical discovery about queueing dynamics. A claim like "Little's Law
  confirms this system behaves like a queue" would be an overclaim the model cannot support — the
  correct claim is narrower: the attribution engine's bookkeeping is internally consistent, and this
  scenario's utilization numbers (already used causally in Sections 7 and 12) are trustworthy as
  utilization numbers, not as evidence of queueing dynamics that were never simulated.
- This directly reuses and quantifies Section 7's own EWMA/edge-a ρ=1.09 (previously reported at 2
  significant figures from `UtilizationFromWorld` alone) and Section 12's Adaptive balancing numbers,
  now backed by an independent L measurement rather than resting on `UtilizationFromWorld` alone.

## 18. Statistical Robustness Check (Program A's Flagship Claim)

**Experiment**: `cmd/experiment-011h`, artifact `experiments/011-research-validation/results/011H-statistical-robustness.json`.
Every number in Programs A-F rests on **one seed per configuration** — necessary to keep the regime map
(162 runs) tractable, but exactly the "no single-run strong claims" risk this stage's own discipline
warns against. This experiment strengthens the single most load-bearing claim (Section 7: EWMA beats
Adaptive on mean latency under severe heterogeneity) with actual replication and effect-size reporting.

**Method, chosen for the actual question being asked**: the question is "is EWMA's advantage a real,
direction-consistent effect, or could it be noise" — a location-difference-with-effect-size question,
not a significance-test-by-habit one. Reran the severe/constant/none scenario across 12 independent
traffic seeds (`JitterFraction: 0.3` deliberately added — Program A's own constant-pattern default has
*zero* jitter, so varying its seed alone would silently produce byte-identical arrivals and prove
nothing about robustness). Used `internal/statistics.CliffsDelta` (a distribution-free effect size) and
`BootstrapDiffCI` (an uncertainty interval on the mean difference), both existing project tools, chosen
per-question rather than applied by default.

**Result**: EWMA had the lower mean latency in **12 of 12** seeds (15.90ms every time — invariant to
jitter, because once EWMA locks onto edge-a via its cold-start rule, service time is fixed regardless
of arrival timing, per Section 7/14's no-queueing finding). Adaptive's mean varied 26.12-27.13ms across
seeds. Cliff's Delta = **1.000 ("large")** — the maximum possible effect size, meaning EWMA beat
Adaptive in literally every paired comparison. The bootstrap 95% CI on (Adaptive mean − EWMA mean) is
**[10.55ms, 10.89ms]**, entirely positive and excluding zero by a wide margin.

**This is not single-run noise.** Program A's flagship finding — EWMA's mean-latency advantage over
Adaptive under severe heterogeneity — is a robust, seed-independent, direction-consistent effect. (This
checks *seed* robustness only, per Program D's own terminology distinction — it says nothing about
*distribution* generalization beyond what Program D already established.)

## 19. Reconciling Stage 11 with Stage 8's 62.5-70% Win-Rate Claim

Programs A-E/H all test `AdaptivePolicy()`, which uses `DefaultAdaptiveConfig()` (weights
Load=0.4/Latency=0.4/Cache=0.1/Cost=0.1) — the **hand-chosen** default, not Stage 8's own **tuned**
configuration (`Load=0.161, Latency=0.568, Cache=0.051, Cost=0.220`, `ReferenceLatency=192ms`,
`StaleAfter=3.74s`, per `Stage8.md`). Since Adaptive loses decisively to EWMA throughout Programs
A-D, and Stage 8 reported Adaptive winning 62.5-70% of its own scenarios, this needed direct
reconciliation rather than being left as an unexplained tension.

**Checked directly**: rerunning Program A's flagship severe/constant/none scenario with the Stage 8
tuned config instead of the default:

| Policy | Mean latency | Max share |
|---|---:|---:|
| ewma | 15.90ms | 0.973 |
| adaptive (default weights) | 27.38ms | 0.503 |
| adaptive (Stage 8 tuned weights) | **22.45ms** | 0.503 |

The tuned configuration is a real, meaningful improvement over the default (27.38ms → 22.45ms, ~18%
better) — consistent with Stage 8's own tuning result — but it still loses decisively to EWMA on raw
mean latency in this specific regime (22.45ms vs 15.90ms, ~41% worse). **Tuning narrows but does not
close this specific gap.**

**The remaining, larger reconciliation is a metric and scenario-distribution difference, not a
contradiction.** Stage 8's 62.5-70% figure is a win rate on a composite **utility/LatencyScore**
metric (`Stage8.md`: "driven by a clear lead on latency quality, `LatencyScore` 0.5607 vs. next-best
0.5310" — a normalized score, not raw mean-latency milliseconds), measured against **randomly-generated**
Development/Holdout scenarios (`internal/tuning.ScenarioSpace.Generate`, 2-5 targets, 5-200ms service
times, 50% chance of one failure). Program A instead measures **raw mean latency** on a **systematically
constructed** regime sweep, specifically including a severe-heterogeneity/no-failure/constant-load
corner Stage 8's random sampling may rarely or never construct in exactly this form. These are
legitimately different, complementary questions — not competing answers to the same one. Stage 8 asks
"does Adaptive win, by its own composite objective, across a broad random scenario distribution" (yes,
substantially); Program A asks "in this specific, controlled regime, does Adaptive beat EWMA on raw
mean latency" (no, decisively, even tuned). **This is precisely the kind of regime-boundary finding
Q1 sets out to discover, not a contradiction requiring one side to be wrong.**

**Consequence**: any future comparison across this project should state explicitly whether it is
measuring composite utility (Stage 8's metric, which folds in fairness and other factors) or raw mean
latency (Program A's metric) — the two can and do disagree about which policy "wins," and neither
number is wrong, they are answering different questions. This is now a documented, evidence-based
caveat rather than an implicit ambiguity.

---

*(Limitations, unresolved questions, and claims-supported/not-supported summaries are appended below to
close out Stage 11.)*
