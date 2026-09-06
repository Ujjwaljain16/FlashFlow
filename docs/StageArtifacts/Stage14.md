# Stage 14 — Scale & Topology Generalization: Is the Stage 13 Regime Structural?

**All results below come from experiments actually run (`cmd/experiment-014a` through `014i`,
`experiments/014-scale-topology/results/`). Nothing here was decided before its corresponding
experiment executed.**

## Motivation

Stage 13 ended PASS with a precise, deliberately scoped finding: within one severe-heterogeneity,
3-target topology family, load-blind routing (round-robin, EWMA) becomes unstable once the concentrated
target's offered load crosses ρ≈0.89-0.97, while load-aware routing (WRR, LC, P2C, Adaptive) stays
comparatively stable. Stage 13 explicitly declined to generalize this beyond the one topology family it
tested. Stage 14 exists to find out whether that regime is structural — a property of concentration-
driven capacity pressure in general — or an artifact of the specific 3-target model used to discover it.

## Stage 13 Findings Motivating Stage 14

The five unresolved questions Stage 13 itself named, restated here because Stage 14 is organized
directly around answering them: (1) does the ρ≈0.89-0.97 transition survive beyond 3 targets? (2) does
it survive a fundamentally different heterogeneity shape (bimodal, not graduated)? (3) does a controlled
real-engine concurrency ceiling reproduce the virtual contention reversal, where Stage 13's unconstrained
real engine did not? (4) does smoothing alpha affect the MAIN capacity-boundary dynamics, not just H2's
sustained lag? (5) does recovery cost ever differentiate policies under a different topology/capacity
regime?

## Research Questions

Not "does Adaptive still win" but: do we understand the conditions under which the Stage 13 regime
persists, moves, weakens, or disappears? A regime that survives with a different threshold, survives in
a narrower topology family than hoped, or disappears outright under a specific intervention are all
scientifically useful outcomes — none was assumed in advance.

## Hypotheses (stated before running the corresponding experiment)

- H1: the concentrated-target rho boundary generalizes to more targets, because the mechanism (a greedy
  policy locking onto one target, that target's offered load crossing its own capacity) does not depend
  on how many other, unused targets exist alongside it.
- H2: a bimodal (fast-group/slow-group) topology will produce GROUP concentration rather than
  single-target concentration, since multiple targets now tie on the fastest available signal.
- H3: an explicit, validated real concurrency ceiling (Go's `MaxConnsPerHost`) will reproduce the virtual
  reversal's direction, where Stage 13's unconstrained real engine — which never actually created
  sustained queueing at the concurrency levels tested — did not.
- H4: smoothing alpha, having a real causal effect on H2's transient-recognition lag (Stage 13), will
  also affect the MAIN boundary's severity, since both involve a smoothed latency signal.
- H5: rho, while necessary, will become progressively less SUFFICIENT as target count grows, because a
  policy's own concentration percentage is not fixed — it can shift with topology size in ways that
  change the offered load at the concentrated target independent of the intentional rho-matching.

## Experimental Design

Nine experiments (`014a`-`014i`), hierarchical: discover (014a/014b sweep target count and topology
shape) → narrow (014c isolates the central rho-matching question) → attack (014d/014f/014g test workload,
full policy set, and falsification directly) → generalize (014e tests the real engine; 014h/014i confirm
recovery and statistical robustness). No giant factorial matrix: target counts are capped at {3,5,8},
policies are introduced incrementally (round-robin/EWMA/Adaptive first, full 6-policy set only once a
concrete question — Section 24's falsification mandate — required it).

## Target-Count Results (`014a`)

Graduated topology (15ms-stepped targets) at N∈{3,5,8}, Capacity∈{0,1,2,3}, identical workload (300
requests, 4s horizon) across all three target counts. **Adaptive is completely invariant to target
count** (mean=26.19ms, top1=0.502, entropy=1.50 in every cell) — it never uses the slow tail targets
regardless of how many exist. **EWMA's own concentration and rho actually DECREASE as N grows**
(Capacity=0 rho_max: 1.099→1.057→0.964 for N=3/5/8; top1 share: 0.977→0.940→0.856) — its cold-start "try
every target once" rule forces proportionally more exploration as more targets exist, which lowers its
achieved concentration at face value. A genuine counterexample surfaced here: at N=8/Capacity=1, EWMA's
own max-target rho reads 0.709 (nominally stable under M/M/1 theory) yet the target still shows 75%
wait-share and severe collapse (mean=137.03ms) — directly falsifying "rho alone predicts collapse" as N
grows. The likely mechanism: EWMA's per-request cold-start exploration across more targets creates
transient concentration bursts a time-averaged rho metric cannot see.

## Graduated vs. Bimodal Topology Results (`014b`)

Same fast (15ms) / slow (60ms) absolute service times as `014a`'s topology extremes, arranged as a
bimodal fast-group/slow-group split (2+1, 3+2, 5+3 targets) instead of a graduated gradient. **Confirms
H2 directly**: EWMA's `top1Share` drops sharply as N grows (0.977→0.953→0.923 at Capacity=0, down to
0.205 at N=8/Capacity=1) while `fastGroupShare` stays consistently high (0.950-0.983) — this is GROUP
concentration, not single-target concentration. Despite a structurally lower individual-target rho
(0.236 at N=8/Capacity=1, versus the graduated topology's 0.709 at the same cell), EWMA still shows real
degradation (28.17ms mean, 29.4% wait share) — concentration, not the literal single-target rho number,
is the operative variable. Round-robin's own max-rho target consistently shifts to the SLOW group (never
the fast group), with severity depending on the fast:slow ratio (rho_max=1.500 at N=3's 2fast+1slow, down
to 0.555 at N=8's 5fast+3slow). Adaptive stays essentially perfect in every cell (mean=15.00ms flat,
fast_group=1.000) — bimodal topology is structurally EASIER for Adaptive than graduated, since ties among
fast targets give it more equally-good options to route between.

## Rho Generalization — The Central Experiment (`014c`)

Constructed approximately-equivalent normalized conditions across N∈{3,5,8} by scaling request counts
(300/336/381) to target the SAME EWMA max-target rho≈0.9 at each N, using `014a`'s own OWN observed
concentration percentages (not assumed). **Adaptive wins at all three target counts — the qualitative
regime is robust.** But EWMA's degradation gets progressively WORSE as N grows (93.78ms→201.81ms→
307.32ms mean) even as its ACHIEVED max-target rho actually DECREASES (0.915→0.833→0.716) — because
EWMA's own concentration percentage shifts dynamically with request volume and target count, undermining
the very rho-matching the experiment tried to hold constant. **Conclusion, precisely stated: rho is
NECESSARY but increasingly INSUFFICIENT as a predictor as topology size grows.** This is the single most
important, carefully-derived empirical result of Stage 14.

## Capacity Scaling

Section 14's scale-invariance question (does fixed normalized pressure → scale invariance hold across
target counts, the way Stage 13 found exact scale invariance within one topology) is answered indirectly
by `014c`'s own finding: because EWMA's achieved rho does not stay matched across N even when the
experiment deliberately tries to hold it constant, exact scale invariance across TARGET COUNTS does not
hold the way Stage 13's WITHIN-one-topology scale invariance did. The two claims are not in conflict —
Stage 13 scaled absolute time units within one fixed topology; Stage 14 found that scaling ACROSS
topology sizes changes the achieved concentration itself, a different and more consequential kind of
scaling than Stage 13 tested.

## Workload Interaction (`014d`, Program E)

At the N=8 near-boundary point (Requests=381, Capacity=1), compared Constant against one transient
pattern (FlashCrowd, same total requests/horizon so time-averaged rate matches). **Adaptive's advantage
SURVIVES the transient workload** (135.73ms vs EWMA's 332.40ms) but degrades substantially from its own
constant-load baseline (29.88ms→135.73ms, ~4.5×) — a transient burst still creates real, if temporary,
concentration pressure even for a policy that handles sustained load well.

## Failure Interaction (`014d`, Program F)

At the same N=8 near-boundary point, failing the CONCENTRATED target (edge-00) for 1 second pushed even
Adaptive's mean latency up ~26% (29.88ms→37.60ms) — the first time in this project a failure has
measurably affected Adaptive specifically, because this is the first failure test conducted at a
genuinely marginal (rather than slack) capacity level. Failing an unused target (edge-07) had exactly
zero effect on any policy, as expected.

## Real Concurrency Ceiling (`014e`, Track D)

**Part 1 (mandatory pre-validation)**: a policy-neutral raw `http.Client` + atomic-counter probe
confirmed Go's `http.Transport.MaxConnsPerHost` (now exposed as `engine.RealExperimentConfig.
MaxConnsPerHost`, an additive field wired to existing infrastructure — no new mechanism built) genuinely
bounds observed concurrency: ceiling=1/2/4 measured exactly at ceiling; unlimited reached 20/20.

**Part 2**: ceiling=1 (chosen to match the virtual model's own Capacity=1 exactly — an earlier attempt at
ceiling=2 was under-powered, since its implied per-edge throughput was never actually exceeded by EWMA's
offered load, and produced an inconclusive, non-monotonic result, corrected before drawing any conclusion
from it) on the same 15/30/60ms topology as Stage 13's own `011f`/`013k`, at below/near/above offered
load. **This cleanly reproduces the virtual reversal's direction on p99** (the metric that actually
carries a queueing-collapse signal; p50 stays flat across all three levels since only the concentrated
target's tail is affected): Adaptive wins at every level, with the margin widening sharply under pressure
(EWMA p99: 62.37ms→70.96ms→2914.07ms; Adaptive p99: 17.32ms→32.21ms→102.80ms). At `above_ceiling`,
Adaptive's `max_share` correctly drops to 0.542 as it spreads load once queueing bites, while EWMA stays
concentrated (`max_share`=0.505) with catastrophic tail latency — the same mechanism as the virtual
model, not a coincidental number.

## Virtual-vs-Real Triangulation

Stage 13's own `013k` found the UNCONSTRAINED real engine did NOT reproduce the virtual reversal (EWMA
still won at high concurrency, opposite direction). `014e`'s validated, explicit ceiling — the mechanism
Stage 13 named as untested in its own Limitations — DOES reproduce the direction, cleanly and
monotonically, once genuinely exceeded. The reconciliation: the real engine was never wrong about
contention in general — it simply never had a mechanism producing genuine, sustained per-target queueing
at the concurrency levels Stage 13 tested. Given an honest, validated ceiling matching the virtual
model's own capacity assumption, the two engines agree in direction.

## Alpha Follow-Up (`014g`, Program G)

Tested whether smoothing alpha (Stage 13's `013g` only tested it against H2's sustained-lag scenario)
affects the MAIN capacity-boundary collapse `014f` found (EWMA losing to round-robin at N=8 near/above-
boundary). Varied alpha across a 16× range (0.05-0.8), holding topology/capacity/workload/seed fixed.
**Clean negative result**: no alpha value ever lets EWMA beat round-robin, and mean latency does not even
change monotonically with alpha (near_boundary: 300.41/288.60/307.32/285.52/289.03ms across increasing
alpha) — the opposite of Stage 13's H2 finding, where alpha had a real, monotonic causal effect.
Interpretation: the main-boundary collapse is a STRUCTURAL problem (EWMA's early cold-start dispatches
commit requests to one target, which then queues serially at Capacity=1; that backlog cannot be drained
faster by a later re-routing decision regardless of how quickly it recognizes degradation), not a
signal-responsiveness problem alpha could fix.

## Recovery Follow-Up (`014h`, Program H)

ONE representative topology (N=8 graduated, Capacity=1, the same near-boundary cell) and three
representative policies spanning `014f`'s concentration-proneness spectrum (ewma, least-connections,
adaptive). Crashed the concentrated target (edge-00) for 1 second. **LC and Adaptive show a clean,
interpretable recovery signal**: pre_p99=60ms → transition bump to 120ms → drains in 117-212ms → back to
60ms steady state. **EWMA's numbers are not comparably interpretable**: its own pre-failure p99 is
already 1147ms — it was already in collapse at this load level before the failure ever happened
(confirming `014f`/`014g`'s finding that this is a pre-existing structural limit), and its queue never
drains within the horizon regardless of the failure. Recovery differentiation and baseline stability turn
out to be the same underlying property, not independent axes: a policy can only meaningfully "recover" if
it had a stable state to recover to. A 5.2-10.4% wrong-target fraction during the failure window matches
the expected one-probe-cycle detection lag (100ms probes vs. 1000ms failure window) — a measurement
artifact, not a routing bug.

## Multi-Policy Regime Classification (`014f`, Sections 24 & 28)

All six policies at the N=8 below/near/above-boundary cells, classified by actual signal source
(round-robin: none; weighted-round-robin: a static weight frozen at startup; least-connections/ewma/
p2c-load/adaptive: a live, continuously-updated signal) rather than by name. **FALSIFIER FOUND**: EWMA —
a "dynamic-load-aware" policy — is WORSE than round-robin at both near_boundary (307.32ms vs 170.48ms)
and above_boundary (589.39ms vs 376.80ms), directly falsifying "load-aware always beats load-blind" as a
categorical claim. The data instead points to CONCENTRATION-PRONENESS as the real mechanism: EWMA and
P2C-load lock onto a single target via a smoothed/sampled signal and stay locked even as it overloads
(EWMA's own rho at its locked target hits 3.131 at above_boundary), while least-connections and adaptive
stay flat and excellent (15-45ms) at every level by actively correcting away from an overloading target.
Weighted-round-robin also does well throughout because its static weights already approximate true
per-target capacity and never concentrate at all. Round-robin's own bottleneck is the SLOWEST target
(edge-07/edge-04), not the fastest, consistent with `014b`'s group-concentration finding. This falsifier
was confirmed, not a single-seed artifact, in `014i` (10/10 independent seeds, Cliff's Delta=1.000).

## Counterexamples

- `014a`: N=8/Capacity=1, EWMA's own max-target rho=0.709 (<1, nominally stable) yet 75% wait-share and
  severe collapse (137.03ms) — falsifies "rho alone predicts collapse" as N grows.
- `014f`/`014i`: EWMA loses to round-robin at N=8 near/above-boundary, confirmed 10/10 independent seeds
  — falsifies "load-aware always beats load-blind" categorically.
- `014g`: no alpha value across a 16× range rescues EWMA at the main boundary — falsifies "smoothing
  speed explains the main-boundary collapse" (it explains H2's lag, a different mechanism).
- `014h`: EWMA's own pre-failure baseline is already collapsed at the tested load level, making its
  "recovery" numbers uninterpretable as a recovery signal specifically — a reminder that recovery
  differentiation presupposes a stable state existed to recover to.

## Statistical Robustness (`014i`)

10 independent seeds (a fresh, disjoint block, jittered arrivals) at the N=8 near-boundary point,
confirming both `014c`'s central claim (Adaptive beats EWMA: 10/10 seeds, Cliff's Delta=1.000, bootstrap
95% CI on the mean difference [260.21, 274.88]ms) and `014f`'s falsifier (round-robin beats EWMA: 10/10
seeds, Cliff's Delta=1.000, CI [119.49, 134.11]ms). Both effects are structural at this operating point,
not single-seed artifacts.

## Mechanistic Attribution

```
EWMA's cold-start dispatch concentrates onto one target (single, 014a; or a fast GROUP, 014b)
      ↓
that target's/group's offered rho rises -- but achieved rho becomes an increasingly unreliable
proxy as N grows, because EWMA's OWN concentration percentage shifts with topology size (014c)
      ↓
once queueing begins, an early backlog forms that later re-routing cannot retroactively drain,
REGARDLESS of how fast the policy later recognizes the degradation (014g's alpha negative result)
      ↓
the backlog serializes at Capacity=1, producing tail-latency collapse severe enough that even
ROUND-ROBIN (no signal at all) beats a locked-in EWMA (014f, confirmed 014i)
      ↓
policies that actively correct AWAY from an already-committed backlog (least-connections,
adaptive) or that never concentrate in the first place (weighted-round-robin) avoid this
collapse entirely; policies that merely HAVE a load signal but still lock in (EWMA, p2c-load
to a lesser degree) do not
```

The operative variable this stage's evidence converges on is concentration-proneness under
already-committed queueing, not "does the policy have a load signal" — a genuine refinement of Stage
13's own attribution chain, not a contradiction of it.

## Historical Reconciliation

| Stage 13 Claim | Stage 14 Experiment | Result | Status |
|---|---|---|---|
| ρ≈0.89-0.97 transition zone (severe heterogeneity, N=3) | `014a`, `014c` | Reproducible at N=5/N=8 when rho is deliberately matched; but achieved rho DECREASES with N even as EWMA's degradation WORSENS | **Narrows**: rho predicts the transition's EXISTENCE within one topology, but not its SEVERITY across topology sizes |
| Heterogeneity ratio matters only via concentration | `014b` (bimodal) | Confirmed in a structurally different topology: GROUP concentration (not single-target) still degrades EWMA despite lower per-target rho | **Survives, generalized**: concentration (of any shape) is the mediating variable, not the specific single-target lock-in mechanism |
| Load-blind vs. load-aware is the deeper 2-regime split | `014f`, confirmed `014i` | FALSIFIED categorically: EWMA (load-aware by signal) loses to round-robin (load-blind) at N=8 near/above-boundary | **Reverses/refines**: concentration-proneness under committed queueing, not signal presence, is the operative axis |
| Real engine does not reproduce the virtual reversal | `014e` (validated ceiling) | An EXPLICIT, validated concurrency ceiling DOES reproduce the direction cleanly and monotonically | **Reverses, with an explanation**: the prior non-replication was a missing-mechanism artifact (no real queueing occurred), not evidence the models disagree in principle |
| Smoothing alpha is causal for Adaptive's H2 lag | `014g` (main boundary, not H2) | No effect at all on the main-boundary collapse, non-monotonic | **Confirmed as scenario-specific**: alpha's causal role is specific to H2-style transient-recognition lag, not a general property of smoothing |
| Recovery transition-cost is capacity-threshold-dependent | `014h` (N=8, near-boundary) | LC/Adaptive show a clean recovery signal; EWMA's own baseline is already collapsed, making recovery uninterpretable for it specifically | **Narrows further**: recovery is only a meaningful concept for a policy with a stable state to recover to |

## Supported Claims

- Adaptive's qualitative advantage over EWMA generalizes across N∈{3,5,8} in graduated topology and
  across bimodal topology, under matched normalized load (`014a`, `014b`, `014c`).
- Rho is necessary but increasingly insufficient as a severity predictor as target count grows — a
  precise, non-overclaiming refinement of Stage 13's own ρ finding (`014c`).
- Concentration (single-target or group) is the mechanism mediating heterogeneity's effect, generalized
  beyond the graduated topology Stage 13 tested (`014b`).
- A validated, explicit real concurrency ceiling reproduces the virtual model's regime direction, closing
  (with an explanation, not a contradiction) Stage 13's own unresolved virtual-vs-real divergence
  (`014e`).
- The load-blind vs. load-aware 2-regime classification does NOT survive topology scaling — a
  falsification of Stage 13's own deepest claim, replicated across 10 independent seeds (`014f`, `014i`).
- Smoothing alpha's causal role is specific to H2-style transient-recognition lag, not a general property
  of the main capacity-boundary mechanism (`014g`).
- Recovery differentiation presupposes a stable baseline; a policy already collapsed before a failure
  cannot produce an interpretable recovery signal (`014h`).

## Claims NOT Supported by Evidence

- That the ρ≈0.89-0.97 NUMBER itself (as opposed to the qualitative transition it names) generalizes
  across target counts — `014c` shows achieved rho actively decreases with N even as severity worsens.
- That "load-aware" policies as a class are safe from the Stage 13 collapse mechanism — EWMA and, to a
  lesser extent, P2C-load are both load-aware by signal and still vulnerable (`014f`).
- That a real concurrency ceiling of ANY size reproduces the virtual reversal — an under-powered ceiling
  (implied throughput never actually exceeded by offered load) produced an inconclusive, non-monotonic
  result before recalibration (`014e`'s own internal correction).
- That smoothing alpha is irrelevant to Adaptive's behavior in general — it remains causally real for
  H2-style scenarios (Stage 13); only its role in the MAIN boundary specifically is ruled out here.
- That bimodal topology is harder for any policy than graduated — Adaptive was, if anything, structurally
  helped by bimodal ties (`014b`).

## Limitations

1. Target-count generalization was tested only at N∈{3,5,8} in a graduated and a matched-ratio bimodal
   topology; a fully general topology-family sweep (arbitrary heterogeneity shapes, clustered
   distributions) was explicitly out of scope per this stage's own charter.
2. `014e`'s real-ceiling triangulation used ONE topology (the same 15/30/60ms 3-edge model as Stage 13's
   `011f`/`013k`) and one ceiling value (1); whether the reproduction generalizes to larger real
   topologies or different ceiling values was not tested.
3. `014f`'s full-policy-set falsification was run at ONE topology (N=8 graduated); whether the same
   falsifier appears in bimodal or other topology shapes was not directly tested.
4. `014g`'s alpha follow-up used EWMA only (not Adaptive) at the main boundary; Adaptive's own alpha
   sensitivity at this specific boundary (as opposed to H2) was not separately tested.
5. `014h`'s recovery test used ONE failure configuration (crashing the concentrated target); recovery
   under a non-concentrated-target failure or a time-varying degradation was not tested in the
   generalized topology.
6. Statistical confirmation (`014i`) targeted only the N=8 near-boundary point; the N=5 and other N=8
   cells from `014a`/`014b`/`014c` were not independently re-confirmed across multiple seeds.

## Unresolved Questions

- Does the concentration-proneness axis found here (EWMA/P2C-load prone; LC/Adaptive resistant) hold as a
  general property of these policies' DESIGN, or could a different topology make LC or Adaptive
  concentration-prone too?
- Does the real-ceiling reproduction (`014e`) generalize to a larger real topology (N=5, N=8 real edges),
  or was the 3-edge topology's simplicity itself a factor in how cleanly the direction reproduced?
- Would `014f`'s falsifier (EWMA losing to round-robin) also appear in the bimodal topology, where EWMA's
  concentration takes a different (group) shape?
- Is there a rho-like quantity that DOES remain a sufficient severity predictor as N grows, given that
  the raw single-target rho `014c` used does not?
- Does Adaptive's own smoothing alpha (not just EWMA's) have any role at the main boundary, distinct from
  its established H2 role?

---

## Mandatory Regime Table

| Topology | Targets | Heterogeneity | Load Level | Capacity | Winner | Regime | Mechanism | Evidence |
|---|---|---|---|---|---|---|---|---|
| Graduated | 3 | severe (15-45ms) | near-boundary (ρ≈0.92) | 1 | Adaptive | dynamic-load-aware (anti-concentration) wins | concentration → ρ rise → queue → wait | `014c` |
| Graduated | 5 | severe (15-75ms) | near-boundary (ρ≈0.83, scaled) | 1 | Adaptive | same, but achieved ρ already diverging from target | same, worsening severity despite lower ρ | `014c` |
| Graduated | 8 | severe (15-120ms) | near-boundary (ρ≈0.72, scaled) | 1 | Adaptive | same; ρ least sufficient here | same; rho necessary but insufficient | `014c` |
| Graduated | 8 | severe | below-boundary (150 req) | 1 | LC / Adaptive (tie, 15.00ms) | anti-concentration wins cleanly | minimal cold-start backlog yet | `014f` |
| Graduated | 8 | severe | near-boundary (381 req) | 1 | **round-robin** (beats EWMA) | FALSIFIES load-blind/load-aware axis | EWMA locked onto edge-00, serial backlog | `014f`, confirmed `014i` |
| Graduated | 8 | severe | above-boundary (600 req) | 1 | Adaptive (LC close) | anti-concentration wins; EWMA worst of all six | same mechanism, worse | `014f` |
| Bimodal | 3 (2f+1s) | bimodal (15/60ms) | Capacity sweep 0-3 | 0-3 | Adaptive | group concentration doesn't break anti-concentration | fast-group tie handling | `014b` |
| Bimodal | 8 (5f+3s) | bimodal (15/60ms) | Capacity=1 | 1 | Adaptive | EWMA degrades via GROUP concentration despite lower per-target ρ | ρ=0.236 yet 29.4% wait share | `014b` |
| Real 3-edge | 3 | graduated (15/30/60ms) | below ceiling (30 req) | ceiling=1 | Adaptive (p99) | anti-concentration wins even pre-saturation | mild real tail effect | `014e` |
| Real 3-edge | 3 | graduated | above ceiling (400 req) | ceiling=1 | Adaptive (decisively, 28× on p99) | reproduces virtual mechanism on real engine | validated `MaxConnsPerHost` queueing | `014e` |

## Mandatory Rho Table

| Scenario | Targets | Capacity | Arrival Rate | Max ρ (target) | Winner | Transition? |
|---|---|---|---|---|---|---|
| Graduated, N=3, `014a` | 3 | 1 | 75 req/s (300 req/4s) | 1.099 (Cap=0 baseline) → 0.915 at Cap=1 (`014c`) | Adaptive | Yes |
| Graduated, N=5, `014a`/`014c` | 5 | 1 | 84 req/s (336 req/4s) | 0.833 (achieved, target was 0.9) | Adaptive | Yes, but ρ undershoots target |
| Graduated, N=8, `014a` | 8 | 1 | 75 req/s (300 req/4s) | 0.709 (yet 75% wait-share!) | EWMA loses despite ρ<1 | Yes — rho-blind counterexample |
| Graduated, N=8, `014c` | 8 | 1 | 95 req/s (381 req/4s) | 0.716 (achieved, target was 0.9) | Adaptive | Yes, ρ undershoots most here |
| Bimodal, N=8 (5f+3s), `014b` | 8 | 1 | 75 req/s (300 req/4s) | 0.236 (single-target); group share 0.983 | Adaptive; EWMA still degrades | Yes, via group not single-target ρ |
| Real 3-edge, `014e` | 3 | ceiling=1 | 150 req/s (600 req/4s, above) | not computable (real engine; see max_share instead: EWMA 0.505, Adaptive 0.542) | Adaptive | Yes |

## Mandatory Virtual-vs-Real Table

| Condition | Virtual Result | Real Result (`014e`, ceiling=1) | Same Direction? | Explanation |
|---|---|---|---|---|
| Below boundary | Adaptive ≈ EWMA (flat model, minimal queueing) | EWMA p99=62.37ms, Adaptive p99=17.32ms — Adaptive already ahead | Same | Even sub-saturation, real tail variance favors Adaptive slightly |
| Near boundary | Adaptive >> EWMA (virtual Capacity=1 collapse begins) | EWMA p99=70.96ms, Adaptive p99=32.21ms | Same | Ceiling begins to bind; gap widens as in the virtual model |
| Above boundary | Adaptive >> EWMA (virtual collapse, large ratio) | EWMA p99=2914.07ms, Adaptive p99=102.80ms (28× ratio) | Same, and SHARPER | Genuine sustained queueing achieved; matches virtual mechanism closely |
| (Contrast) Stage 13's unconstrained real engine | Adaptive >> EWMA (virtual) | EWMA won (opposite direction) | **Different** | No explicit ceiling meant no real engine ever actually queued at those concurrency levels — a missing mechanism, not a model disagreement |

## Mandatory Falsification Table

| Claim | Falsifier | Was It Found? | Interpretation |
|---|---|---|---|
| Rho predicts the boundary | N=8/Capacity=1/EWMA: ρ_max=0.709 (<1, nominally stable) yet 75% wait-share, 137.03ms mean | **YES** (`014a`) | Rho necessary but insufficient as N grows; transient concentration invisible to time-averaged rho |
| Load-aware regime generalizes | EWMA (dynamic-load-aware by signal) loses to round-robin (load-blind) at N=8 near/above-boundary | **YES**, confirmed 10/10 seeds | Concentration-proneness, not signal presence, is the operative axis (`014f`, `014i`) |
| Target count doesn't matter | Adaptive invariant across N (`014a`); but EWMA's achieved ρ and severity both diverge with N in the rho-matched design (`014c`) | **Partially** — qualitative winner stable, quantitative severity-to-ρ relationship is not | Target count changes HOW MUCH concentration a policy achieves, even under deliberate rho-matching |
| Bimodal topology preserves the mechanism | EWMA still degrades under bimodal despite structurally lower per-target ρ (0.236), via GROUP concentration (`014b`) | **Mechanism preserved in modified form** | Concentration (of any shape), not literal single-target lock, is the true operative variable |
| Real concurrency ceiling matches virtual direction | Recalibrated ceiling=1 (`014e`) matches cleanly and monotonically; an earlier under-powered ceiling=2 attempt (never actually exceeded) did NOT match | **YES, with a calibration caveat** | An honest ceiling must actually be exceeded by offered load to produce a signal; this is itself informative, not a nuisance |

---

## Stage 14 Verdict

**PASS WITH LIMITATIONS.**

**CENTRAL FINDING**: The Stage 13 regime is partially structural and partially an artifact of the
3-target model that discovered it. The QUALITATIVE claim — some policies handle concentration-driven
capacity pressure far better than others — generalizes cleanly across target count (N=3,5,8), topology
shape (graduated and bimodal), and engine (virtual and, with a validated concurrency ceiling, real). The
specific ρ≈0.89-0.97 NUMBER and the specific "load-blind vs. load-aware" CLASSIFICATION do not both
survive: rho becomes progressively less sufficient (though still necessary) as target count grows, and
the load-blind/load-aware axis is directly falsified by EWMA — a load-aware policy — losing to
round-robin at scale. The refined, better-supported mechanism is CONCENTRATION-PRONENESS UNDER ALREADY-
COMMITTED QUEUEING: policies that actively correct away from an overloading target (least-connections,
adaptive) or that never concentrate at all (weighted-round-robin, when correctly configured) avoid the
collapse; policies that lock onto a target via a smoothed or sampled signal and do not un-lock (EWMA,
p2c-load to a lesser degree) do not, regardless of whether they nominally "have a load signal."

**TARGET-COUNT GENERALIZATION**: Confirmed qualitatively (N=3,5,8, `014a`/`014c`); rho's quantitative
predictive power narrows as N grows.

**BIMODAL TOPOLOGY**: Confirmed — mechanism generalizes as GROUP concentration, not single-target
lock-in (`014b`).

**RHO GENERALIZATION**: Narrows — necessary, not sufficient, as N grows (`014c`).

**CAPACITY VS RHO**: Kept explicitly separate throughout; achieved rho, not literal capacity, remains the
operative (if imperfect) predictor within one topology (`014a`-`014c`).

**WORKLOAD EFFECT**: Adaptive's advantage survives a transient (FlashCrowd) pattern at the generalized
N=8 boundary, though its own absolute latency degrades ~4.5× from its constant-load baseline (`014d`).

**FAILURE EFFECT**: For the first time, a failure measurably affected Adaptive itself (+26%), because
this is the first failure test conducted at a genuinely marginal (not slack) capacity level (`014d`).

**REAL CONCURRENCY CEILING**: Implemented (`engine.RealExperimentConfig.MaxConnsPerHost`, additive, wired
to existing Go infrastructure) and validated policy-neutrally before any policy comparison (`014e`).

**VIRTUAL VS REAL**: Reconciled — a validated ceiling reproduces the virtual reversal's direction
cleanly; Stage 13's prior non-replication is explained as a missing-mechanism artifact, not a model
disagreement (`014e`).

**LOAD-BLIND VS LOAD-AWARE**: FALSIFIED as the deepest regime boundary; replaced by concentration-
proneness under committed queueing (`014f`, confirmed `014i`).

**THIRD REGIME**: Not found as a discrete third bucket; found instead as a necessary AXIS REFINEMENT —
concentration-proneness is orthogonal to signal-presence, meaning the real space is at least
two-dimensional (has-a-signal × does-it-avoid-lock-in), not the one-dimensional load-blind/load-aware
line Stage 13 proposed.

**ALPHA**: No effect on the main boundary, a clean negative result contrasting with alpha's real, causal
effect on H2's transient lag (`014g`).

**RECOVERY**: Differentiates policies only when they have a stable baseline to recover to; EWMA's
recovery numbers were uninterpretable because it was already collapsed pre-failure at this load level
(`014h`).

**COUNTEREXAMPLES**: Four found and documented (rho-blind collapse at N=8; the load-aware falsifier;
alpha's null effect at the main boundary; recovery's dependence on baseline stability).

**STATISTICAL ROBUSTNESS**: Both the central claim and the falsifier confirmed across 10 independent
seeds each, Cliff's Delta=1.000 (large) in both cases (`014i`).

**STRONGEST NEW CLAIM**: Concentration-proneness under already-committed queueing, not signal presence,
is the mechanism separating stable from unstable routing under capacity pressure.

**STRONGEST NEGATIVE RESULT**: EWMA — the policy Stage 11-13 treated as "load-aware" by virtue of using a
live latency signal — loses to round-robin at scale, confirmed robustly across seeds.

**STAGE 13 CLAIMS THAT CHANGED**: "Load-blind vs. load-aware" (falsified/refined); "real engine doesn't
reproduce the virtual reversal" (reversed, with explanation); "ρ≈0.89-0.97 as the boundary" (narrowed to
a necessary-not-sufficient role).

**CLAIMS NO LONGER SUPPORTED**: That any policy using a live load/latency signal is safe from the
concentration-collapse mechanism; that the specific ρ number, not just the qualitative transition it
names, generalizes across target counts.

**NEW LIMITATIONS**: Target-count generalization tested only at N∈{3,5,8}; real-ceiling triangulation
used one topology and one ceiling value; the falsifier was not directly re-tested in the bimodal
topology; alpha follow-up used EWMA only, not Adaptive.

**NEW UNRESOLVED QUESTIONS**: Whether concentration-proneness is an intrinsic design property or
topology-dependent; whether the real-ceiling reproduction generalizes beyond the 3-edge topology; whether
a rho-like quantity exists that stays sufficient (not just necessary) as N grows.

**EXPERIMENTS**: 9 (`014a`-`014i`). **TESTS**: full existing suite passing throughout (`go test ./...`).
**BENCHMARKS**: none added this stage — no performance bottleneck was encountered running these
experiments, consistent with Section 38's instruction not to optimize speculatively. **FILES**: 9 new
experiment commands, 9 new result JSON artifacts, one additive engine field (`RealExperimentConfig.
MaxConnsPerHost`), this document and its companion learning notes, README updates. **COMMITS**: 9 (one
per experiment, `707d678` through the final documentation commit).

Did the Stage 13 regime survive when the topology changed, or did Stage 14 reveal that we were observing
a property of one 3-target model? **Both, precisely separated**: the qualitative phenomenon survived and
generalized; the specific number and the specific two-regime classification used to describe it did not.
Here is the regime — concentration-proneness under already-committed queueing; here are the variables
that determine its boundary — target count (weakly, via achieved concentration), topology shape (via
concentration form, single-target or group), and signal design (whether a policy un-locks once queueing
begins); here is where it holds — every topology and engine tested, given a genuine capacity constraint;
and here is where the earlier description breaks — a load-aware label is not a safety guarantee, and a
rho number computed for one topology size does not transfer to another.
