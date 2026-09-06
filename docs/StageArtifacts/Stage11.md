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

---

*(Negative results, adversarial findings (Program B), recovery/adaptation dynamics (Program C),
distribution-shift findings (Program D), mechanistic attribution (Program E), virtual-vs-real
validation (Program F), reproducibility verification (Program G), statistical methods, limitations,
unresolved questions, and claims-supported/not-supported summaries are appended below as each program
actually executes.)*
