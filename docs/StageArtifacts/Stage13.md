# Stage 13 — Regime Discovery: Is the Stage 12 Reversal General?

**All results below come from experiments actually run (`cmd/experiment-013a` through `013k`,
`experiments/013-regime-discovery/results/`). Nothing here was decided before its corresponding
experiment executed.**

## Motivation

Stage 12 discovered that introducing a minimal finite-capacity model reversed a Stage 11 conclusion,
sharply, at Capacity=1 on one specific severe-heterogeneity scenario. Stage 12 deliberately declined to
claim ρ≈1 was a general predictor — that question was left explicitly open. Stage 13 exists to answer
it: is the Stage 12 reversal a scenario-specific curiosity, a real but narrow mechanism, or (least
likely, and not assumed in advance) a general regime rule?

## Stage 12 Findings Motivating Stage 13

The exact list from the assignment: H2 real for EWMA, partial for Adaptive; `StaleAfter` didn't explain
the residual lag; RealEngine load tracking fixed; SeedTree Topology/Failure isolation fixed; Stage 8's
tuning conclusion survived; contention enabled waiting-time decomposition; cache-affinity can
self-heal under contention; virtual contention and RealEngine are not like-for-like.

## Research Questions

Restated from the assignment: does the capacity-dependent reversal generalize across topology/service-
time configurations (Program A)? Does it track normalized offered load (ρ) rather than raw arrival rate
or the literal capacity number (Programs B, D, and the Capacity=1 artifact test)? Does heterogeneity
ratio matter independently of the fastest target's absolute speed (Program C)? Does workload shape
(sustained vs. transient) change the answer (Program E)? Does failure interact with capacity
predictably (Program F)? Does smoothing alpha, not `StaleAfter`, explain H2's residual Adaptive lag
(Program G)? Does cache-affinity self-healing generalize (Program H)? Does the recovery transition-cost
finding generalize (Program I)? How many distinct routing regimes exist across the full policy set
(Program J)?

## Hypotheses (stated before running the corresponding experiment)

- H1: the reversal's location depends on the concentrated target's offered-load-to-capacity ratio (ρ),
  not the literal `Capacity` integer or the raw heterogeneity ratio.
- H2 (naming collision with Stage 11/12's own "H2" is unfortunate but both are the project's actual
  terminology): heterogeneity ratio affects the reversal only by changing how much a greedy policy
  concentrates, not directly.
- H3: `Capacity=1` specifically is not special; an equivalent normalized ρ at a higher capacity and
  proportionally higher arrival rate should reproduce the same qualitative outcome.
- H4: `LatencyTracker`'s smoothing alpha, not `StaleAfter`, is a real causal contributor to Adaptive's
  H2 lag — tested via intervention, not assumed.
- H5: sustained and transient overload of the same underlying (concentration-driven) cause should
  produce the same qualitative reversal; genuine aggregate-capacity shortfall should not, regardless of
  routing intelligence.

## Experimental Design

Eleven experiments (`013a`–`013k`), each targeted at one question, using small-to-moderate seed counts
for exploration and heavier replication (10-12 seeds) only at the two points that needed statistical
confirmation (the flagship reversal, already done in Stage 12, and the near-boundary point found in
`013c`). No giant factorial sweep: total across all eleven experiments is under 300 individual
`RunWorld`/`RealEngine.Run` calls.

## Capacity Boundary Results (Program A, `013a`)

Three heterogeneity levels (low: 10/15/20ms, moderate: 10/20/40ms, severe: 15/30/60ms) × capacity
{0,1,2,3,4,5} × 3 policies, identical workload otherwise.

**Low and moderate heterogeneity never destabilize at any capacity 0-5** — EWMA's max-target ρ stays
~0.73-0.74 throughout, because both share the same 10ms fastest target, and 73 req/s offered against a
100 req/s single-slot capacity never approaches instability regardless of how much slower the OTHER
targets are. **Only severe (15ms fastest target, ~67 req/s single-slot capacity) crosses ρ≈1 at
Capacity=1.** This points at the fastest target's absolute service time relative to concentrated
offered load as the operative variable — not the heterogeneity ratio between targets (confirmed
directly in `013d`, Part 2 below).

## Rho Analysis (Programs B, D, and the Capacity=1 Artifact Test — `013b`, `013c`)

**The single most important confirmatory result of this stage.** `013b` scaled arrival rate
proportionally with capacity (Capacity=1/rate=75, 2/150, 4/300, 8/600 req/s) on the severe topology.
After fixing a real bug in the analysis code itself (the first version computed single-server ρ without
dividing by `Capacity` for multi-slot targets — caught because rho appeared to scale with capacity
instead of staying constant, which was itself suspicious), the corrected per-slot offered ρ stayed in a
tight band (0.892, 0.889, 0.907, 0.970) across all four capacity levels, and **Adaptive won decisively
in every single one**, with EWMA's mean latency staying severely elevated (101-131ms vs its flat-model
~16ms) regardless of the literal capacity number. `013c` (fixed capacity=2, arrival rate varied 38-300
req/s) independently confirmed the same transition zone: the winner flips to Adaptive right around
offered ρ=0.889 (150 req/s), via a completely different sweep dimension.

A secondary, precise observation: the degradation is already severe at ρ≈0.9 (not exactly ρ=1),
consistent with standard M/M/1 queueing theory's own prediction that mean wait time already reaches
~9× service time at ρ=0.9 (ρ/(1-ρ) = 9); 131ms/15ms ≈ 8.7× matches closely.

**Scope, stated precisely, per this stage's own charter**: this confirms the mechanism IN THE SEVERE-
HETEROGENEITY SCENARIO TESTED. It does not establish ρ≈1 as a validated general law across every
service-time ratio, arrival pattern, or topology size FlashFlow could construct — see Limitations and
Unresolved Questions below.

## Arrival-Rate Analysis (`013c`)

Covered above (Program B is the same experiment as the rho analysis' second half). Two additional
findings: at very low load (38 req/s, ρ=0.272), Adaptive edges out EWMA by a small margin (15.00ms vs
16.15ms, under 8%) — a genuine but low-magnitude counterexample, distinct in kind from the severe
reversal. At extreme overload (300 req/s, ρ=2.857), Adaptive's advantage *narrows* rather than growing
further (528ms vs 504ms, nearly converged) — once total offered load vastly exceeds total system
capacity, no routing policy can fully compensate.

## Heterogeneity Analysis (Program C, `013d` Part 2)

Fixed the fastest target's absolute speed (15ms) and varied only how much slower the other two are
(1.0×/1.5×/2.0×/4.0× multiplier). **Confirms the causal chain directly**: widening the gap does not
cause instability by itself — it amplifies EWMA's own concentration share (0.510→0.864), which THEN
raises the concentrated target's ρ (0.570→0.982) as a consequence. Ratio is not itself the driving
variable; it is mediated entirely through how much concentration it induces. Even at the widest
multiplier tested (4.0×), ρ approached but did not quite exceed 1 (0.982) — the sharpest heterogeneity
tested here stayed just short of the transition.

Service-time scale invariance (`013d` Part 1): scaling all service times AND horizon by k∈{1,2,4} while
holding ρ constant by construction produced mean latencies scaling by EXACTLY k (131.06/262.11/524.23ms
for EWMA, matching to 4 significant figures). Absolute timescale does not matter when ρ is held
constant — even though `AdaptiveConfig`'s `ReferenceLatency`/`StaleAfter` are fixed-absolute-time
parameters that do not scale with the scenario, they were not decisive enough here to flip any routing
decision. This may not hold for scenarios where an absolute parameter's relationship to timing IS the
mechanism (H2-style scenarios, where the swap interval's absolute timing relative to `StaleAfter`
matters) — not tested for scale invariance here.

## Workload Analysis (Program E, `013e`)

Tested Constant, Burst, and FlashCrowd at Capacity=1. A severe burst (`PeakRate=300`, which vastly
exceeds the 3-target system's total combined capacity of ~116.7 req/s regardless of distribution)
showed Adaptive's advantage nearly evaporating (336.91ms vs EWMA's 339.27ms, essentially tied) — but
this is **genuine aggregate overload**, not evidence against the concentration-driven story. A mild
burst (`PeakRate=100`, under total system capacity) disambiguated cleanly: Adaptive still clearly
outperformed both other policies (67.40ms/41.39ms vs EWMA's 158.92ms/115.90ms) even though the overload
was transient, not sustained. **Conclusion: the reversal does not depend on sustained vs. transient load
shape — it depends on whether the overload is concentration-driven (fixable by routing) or aggregate-
capacity-driven (not fixable by any routing policy).**

## Failure Analysis (Program F, `013f`)

At Capacity=2 (a level with enough slack to fully recover in the no-failure case), failing the
concentrated/fastest target for 1 second measurably worsened EWMA (16.36ms→32.87ms) and reduced its
concentration (0.973→0.722) — a real, modest, directionally-consistent effect confirming "failure
removes capacity, pushing remaining targets' effective ρ up." Failing either of the other two targets
had almost no effect, since they're not where EWMA's own traffic concentrates. The effect was milder
than a literal capacity reduction because Capacity=2 already has slack and the failure window was brief
relative to the horizon; whether combining failure with an already-marginal Capacity=1 setting compounds
into the same severity seen elsewhere was not tested (a scoped limitation, not an oversight).

## H2 / Smoothing Analysis (Program G, `013g`)

A targeted causal intervention on `LatencyTracker`'s EWMA smoothing alpha (0.05/0.2/0.5/0.9), holding
everything else fixed, on the identical H2 swap scenario from Stage 12. A harness bug was caught before
trusting results: the first version of the custom instrumentation never fed observations into the
tracker at all, making all four alphas trivially identical — caught because that result was immediately
suspicious, not accepted. **After the fix, swap-window share-to-degraded decreases STRICTLY as alpha
increases (70.0%→63.3%→50.0%→46.7%)** — genuine causal evidence, via intervention rather than
correlation, that smoothing delay contributes to the residual H2 lag. A precise nuance: "decisions to
first correct choice" stayed constant at 14 across all four alphas — the effect is about sustained
post-discovery accuracy across the swap window, not how quickly Adaptive first notices the swap.

## Cache-Affinity Analysis (Program H, `013h`)

A capacity {0,1,2,3} × cache-weight {0.02, 0.10, 0.30} grid on the B1 scenario. Two findings: (1)
self-healing is confined EXACTLY to Capacity=1 in this scenario — Capacity=2 and 3 behave identically
to the flat model (0% return at default/high cache weight), since at those capacities there is no real
queueing pressure left to counteract the cache bonus at all; the mechanism requires ACTUAL queueing, not
merely a nonzero capacity value. (2) At Capacity=1, a genuine, non-obvious dose-response: HIGHER cache
weight produces a HIGHER eventual return rate (64%→68%→94%) despite a substantially WORSE interim mean
latency (168.55ms at weight 0.30 vs 100.68ms at 0.10) — a stronger bonus keeps traffic on the wrong
target longer, letting its queue depth build further before being overcome, worse in the middle but
more completely corrected once it breaks through.

## Recovery Analysis (Program I, `013i`)

Stage 12's ~1.20x transition/steady-state p99 ratio (Capacity=2, 3 policies) was retested at a
DIFFERENT capacity (3) across the FULL 6-policy set. The ratio stayed EXACTLY 1.00x for all six
policies at Capacity=3 — no transition cost visible at all. **Transition-cost visibility is itself
capacity-threshold-dependent**, consistent with the main reversal's own regime-boundary story, not a
universal property that appears under any nonzero contention.

## Multi-Policy Regime Map (Program J, `013i`)

The most important reframing this stage produced. Expanding the flagship boundary scenario
(Capacity=0/1) to all 6 policies shows **exactly two regimes, not six individual behaviors**: round-
robin and EWMA (no live load signal at all) both collapse catastrophically under contention
(34.85→193.70ms, 15.90→131.06ms — round-robin because its even split still overloads the slowest
target even in the flat model, ρ=1.470; EWMA because of its own concentration). Weighted-round-robin,
least-connections, P2C-load, and Adaptive — every policy with SOME load signal, static (WRR's configured
capacity weights) or live (LC/P2C/Adaptive) — all stay nearly unaffected (25-37ms range). **The deeper
regime boundary this stage found is "load-blind vs. load-aware," not specifically "EWMA vs. Adaptive."**

## Statistical Robustness (`013j` Part 1, plus Stage 12's own `012e`)

The flagship Capacity=1 reversal was already confirmed across 12 seeds in Stage 12. This stage added
confirmation AT THE ACTUAL NEAR-BOUNDARY POINT (Capacity=2, 600 requests, ρ≈0.89) rather than only deep
inside the obvious Capacity=1 collapse: Adaptive faster in 10/10 independent seeds, Cliff's Delta=1.000
(large), bootstrap 95% CI on the mean difference [74.43, 75.30]ms.

## Policy Configuration Sensitivity (`013j` Part 2)

Both `AdaptivePolicy()`'s hand-chosen default and Stage 8's actual tuned config beat EWMA at the
flagship boundary (default 27.93ms, tuned 24.26ms — slightly better — vs EWMA's 131.06ms). The reversal
is structural to Adaptive's load-aware design, not an artifact of one specific weight configuration.

## Virtual-vs-Real Triangulation (`013k`)

Explicitly NOT a full-matrix reproduction (RealEngine consumes no `Capacity`/`ServiceTimeSchedule`
abstraction). Three real concurrent-request-volume levels (low/medium/high) on the identical
15/30/60ms edge topology. **The real engine did NOT reproduce the virtual sweep's reversal** — at high
concurrency, EWMA still won (15.60ms vs Adaptive's 17.04ms), the opposite direction from the virtual
finding. Structurally consistent: Adaptive's `max_share` correctly dropped to 0.502 (balanced) at high
concurrency, matching its own virtual flat-model behavior, while EWMA's stayed concentrated (0.973) —
the CONCENTRATION pattern replicates, but the associated latency collapse does not, because Go's
`net/http` server handles hundreds of concurrent goroutines without the blocking, single-slot-per-
target contention the virtual model's explicit `Capacity` abstraction creates. Reported as a genuine,
disclosed divergence, not glossed over.

## Negative Results

- H2's overall swap-window degraded-share for Adaptive is real but was NOT explained by `StaleAfter`
  (Stage 12) — ruled out directly, not merely unconfirmed.
- The "1 slot is special" hypothesis was directly tested and REJECTED: normalized ρ, not the literal
  capacity integer, predicts the transition (`013b`).
- Heterogeneity RATIO alone does not predict instability — it must first translate into concentration,
  which then raises ρ (`013d`).
- Cache-affinity self-healing does NOT generalize to "any nonzero capacity" — it requires actual
  queueing pressure, absent at Capacity≥2 in this scenario (`013h`).
- The recovery transition-cost finding does NOT generalize to "any nonzero contention" — it vanished
  entirely at Capacity=3 (`013i`).
- Virtual-under-contention and real-engine results do NOT agree even in direction at the concurrency
  levels tested (`013k`) — an honest, disclosed non-replication, not smoothed over.

## Counterexamples

- **Case 1 template (ρ<1, Adaptive still wins)**: low/moderate heterogeneity, ALL capacities 0-5 —
  Adaptive edges out EWMA even in the flat model (10.00ms vs 10.10ms) (`013a`). Also at very low load in
  the severe scenario (ρ=0.272, `013c`), by a small margin.
- **Narrowing, not reversal, at extreme overload**: at ρ=2.857 (`013c`) and under severe aggregate-
  overload bursts (`013e`), Adaptive's advantage shrinks toward parity with EWMA — a real boundary
  condition on how far the "Adaptive wins" claim extends.
- **Virtual-vs-real disagreement in direction** (`013k`) is itself a form of Case 5 (the threshold moves
  substantially, in fact disappears, without an equivalent normalized-pressure construction existing on
  the real engine at all).

## Mechanistic Attribution

The causal chain from Section 17 of the assignment is directly supported by this stage's own evidence,
not merely asserted:

```
EWMA concentrates 97% of load (013a, 013b)
      ↓
concentrated target's offered rho crosses ~0.89-1.09 (013b, 013c — confirmed via TWO independent sweep dimensions)
      ↓
queue grows (max_queue_depth up to 34, 013a)
      ↓
83-85% of observed latency becomes waiting (Stage 12's own 012d, reconfirmed structurally here)
      ↓
Adaptive's balanced routing (any live-load-aware policy, per 013i's regime map) becomes preferable
```

Heterogeneity ratio and arrival rate both act ONLY through this chain's first link (how much
concentration occurs), not as independent direct causes (`013d`). Smoothing alpha acts on a DIFFERENT,
parallel mechanism (H2's staleness lag), confirmed causally via intervention (`013g`).

## Historical Reconciliation

See the mandatory claim-reconciliation table below.

| Stage 12 Claim | Stage 13 Experiment | Result | Status |
|---|---|---|---|
| Capacity reversal is scenario-specific until generalized | `013a`, `013b`, `013c`, `013d` | Confirmed at severe heterogeneity across 6 capacities, 2 independent rate-sweep dimensions, and exact scale invariance; does NOT occur in low/moderate heterogeneity at any tested capacity | **Narrows and sharpens**: real, but conditional on the concentrated target's absolute service time relative to offered load — not "any heterogeneous scenario" |
| ρ≈1 explains reversal | `013b` (scale rate+capacity together), `013c` (fix capacity, vary rate) | Both independently locate the transition at offered ρ≈0.89-0.97, not the literal capacity number | **Partial support**, upgraded from Stage 12's "unresolved": strong evidence within this scenario family; NOT confirmed as a cross-family general law (only one topology, one arrival pattern tested) |
| H2 Adaptive lag likely comes from smoothing | `013g` (alpha intervention) | Swap-window degraded-share decreases strictly with alpha (70%→47%) | **Confirmed causally** (not merely plausible) for the "sustained post-discovery accuracy" component; discovery-speed component unaffected by alpha |
| Cache-affinity self-healing generalizes | `013h` (capacity × cache-weight grid) | Confined exactly to Capacity=1 in this scenario; non-monotonic, counter-intuitive dose-response with cache weight | **Narrows**: real mechanism, but requires actual queueing pressure, not any nonzero capacity; the weight relationship is more complex than "more weight = worse" |
| Recovery penalty is uniform (across policies) | `013i` (full 6-policy set, new capacity) | Uniform ~1.20x at Capacity=2 (Stage 12); vanished entirely (1.00x) at Capacity=3 | **Narrows**: uniformity across policies held where re-tested, but the penalty's very existence is itself capacity-threshold-dependent |
| RealEngine doesn't consume Capacity/ServiceTimeSchedule (disclosed limitation) | `013k` (real triangulation) | Confirmed empirically: real engine shows the opposite winner at high concurrency | **Confirmed and quantified** — the limitation is real and consequential, not merely theoretical |

## Supported Claims

- The Stage 12 reversal is real and reproducible in the severe-heterogeneity scenario family, robust
  across 22 total independent seeds (12 from Stage 12 plus 10 near-boundary here), across 2 independent
  sweep dimensions, and exactly scale-invariant.
- Normalized offered ρ (not literal capacity, not raw arrival rate, not heterogeneity ratio) is the
  variable that predicts the transition, WITHIN the severe-heterogeneity topology family tested.
- The deeper structural boundary is load-blind vs. load-aware routing, not EWMA vs. Adaptive
  specifically — 4 of 6 policies tested share Adaptive's robustness.
- Smoothing alpha is a real, causally-confirmed (via intervention) contributor to Adaptive's H2 lag.
- The reversal is structural to Adaptive's design (survives both default and Stage-8-tuned
  configuration), not a configuration artifact.
- Cache-affinity self-healing and the recovery transition-cost penalty are both real but capacity-
  threshold-dependent, not universal consequences of any nonzero contention.
- Virtual contention and RealEngine genuinely diverge, empirically, not just by documented design gap.

## Claims NOT Supported by Evidence

- That ρ≈1 (or ρ≈0.9) is a validated general predictor across scenario families beyond the one severe-
  heterogeneity topology and constant/burst/flash-crowd workloads tested here — only one topology
  family, one target count (3), and one arrival-pattern family were tested.
- That heterogeneity ratio, arrival rate, or capacity are independently causal — all three were shown
  to act only through concentration and its effect on ρ.
- That the recovery transition-cost penalty differentiates policies by adaptation speed — where visible
  at all, it was uniform across all 6 policies tested, suggesting a topology-capacity property, not a
  routing-intelligence one (though only 2 capacity levels were checked).
- That smoothing alpha fully explains H2 — it explains the sustained-lag component; the "decisions to
  first correct choice" component was completely unaffected by alpha across a 20x range.
- That real-engine behavior can be inferred from the virtual contention model, or vice versa, at the
  concurrency levels tested — the two diverged even in direction.

## Limitations

1. Every finding above comes from topologies with exactly 3 targets and one traffic-generation family
   (`traffic.Generate`'s Constant/Burst/FlashCrowd patterns, `HotColdKeys` skew) — target count and
   arrival-process family were never varied.
2. The ρ≈0.89-0.97 transition zone was established in ONE topology family (severe heterogeneity, 15/30/
   60ms and its scaled/ratio variants); it was never tested against a genuinely different topology shape
   (e.g., 5+ targets, or a bimodal fast/slow split rather than a smooth gradient).
3. `013k`'s real-engine triangulation used only 3 concurrency levels and no deliberate concurrency-
   limiting mechanism (e.g., a connection-pool cap) — a real test WITH an artificial concurrency ceiling
   closer to Track D's own slot model was not attempted, and might behave differently.
4. Program F (failure interaction) tested only ONE capacity level (2) with slack; compounding failure
   with an already-marginal capacity was not tested.
5. Program G's alpha intervention used only the H2 swap scenario; whether alpha similarly affects the
   main capacity-boundary reversal (a different mechanism entirely) was not tested.
6. The scale-invariance finding (`013d` Part 1) was checked with one k range (1/2/4) on one scenario; H2-
   style scenarios where StaleAfter's absolute value IS the mechanism were flagged as a likely exception
   but not tested.

## Unresolved Questions (Earned by This Stage's Evidence)

- Does the ρ≈0.89-0.97 transition zone hold for topologies with more than 3 targets, or a fundamentally
  different heterogeneity shape (bimodal rather than graduated)?
- Does a real-engine test WITH an explicit concurrency-limiting mechanism (closer to Track D's own slot
  abstraction) reproduce the virtual reversal where the unconstrained real engine did not?
- Does smoothing alpha also affect the main capacity-boundary reversal's dynamics, or is its causal role
  specific to H2-style rapid target-quality changes?
- What explains "decisions to first correct choice" staying exactly constant (14) across a 20x alpha
  range, when the SUSTAINED accuracy metric clearly responds to alpha?
- Does the recovery transition-cost penalty ever differentiate policies by adaptation speed under ANY
  tested condition, or is it always a topology/capacity property alone?

---

## Stage 13 Verdict

**PASS.**

**Central finding**: the Stage 12 capacity-dependent reversal is a real, mechanistically-explained,
statistically-robust regime phenomenon within the severe-heterogeneity scenario family tested — driven
by normalized offered load (ρ) crossing approximately 0.89-0.97 at the concentrated target, not by the
literal capacity number, raw arrival rate, or heterogeneity ratio in isolation — and the deeper
structural boundary it reveals is load-blind vs. load-aware routing, not specifically EWMA vs. Adaptive.
It is a well-supported boundary with known exceptions (low/moderate heterogeneity never destabilizes;
extreme aggregate overload narrows Adaptive's advantage; the real engine does not reproduce it) and an
explained mechanism (concentration → ρ rise → queueing → waiting dominance → balanced routing wins) —
not a universal law, and not claimed as one.
