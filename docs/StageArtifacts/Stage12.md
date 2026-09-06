# Stage 12 — Model Fidelity and Experimental Control: Findings

**Companion to `docs/StageArtifacts/Stage12-Plan.md` (design plan, written first). This document
records what actually happened when the plan was executed — nothing here was decided before the
corresponding experiment ran.**

## 1. Motivation

Stage 11 answered FlashFlow's central research question with real evidence, but four of its own
findings pointed at the same conclusion: several of Stage 11's conclusions were properties of platform
gaps, not properties of the routing policies being studied. Stage 12's charter was narrow and specific
— make the minimum changes necessary to represent exactly the dynamics Stage 11 proved mattered, then
find out which previous conclusions survive contact with a more faithful model. This was never a
license to build a general-purpose network simulator; every mechanism below traces to a specific Stage
11 sentence that said "this cannot be tested" or "this is a modeling artifact."

## 2. Stage 11 Findings Being Addressed

See `Stage12-Plan.md` §1 for the full table. In summary: (1) `RunWorld` had no queueing/contention
model, letting EWMA's mean-latency win go unpenalized; (2) `TargetProfile.ServiceTime` was fixed for an
entire Scenario, blocking H2; (3) `RealEngine`'s load signal remained a disconnected, never-updated
tracker pair even after Stage 11's own partial latency fix; (4) `internal/tuning.ScenarioSpace.Generate`
leaked the Topology seed into Failure-target selection.

## 3. Model Changes

**Track C — Time-varying service time**: `TargetProfile.ServiceTimeSchedule []ServiceTimeChange`
(`{At, NewServiceTime}`). `RunWorld` schedules each change as its own vtime event, for every target,
before any arrival is scheduled — a request already in service is never retroactively affected; one
that begins service after a change observes the new value. Confirmed by 5 tests
(`internal/replay/servicetime_schedule_test.go`), all passing on first implementation.

**Track D — Minimal finite-capacity contention**: `TargetProfile.Capacity int` (`<= 0` = infinite, the
exact pre-Stage-12 behavior). When `Capacity > 0`, `RunWorld` maintains per-target `{busy, queue}` state
and implements deterministic FIFO queueing: a request begins service immediately if a slot is free,
else queues and begins service when the target's next completion frees one.
`CompletionRecord.Latency` transparently includes wait time. Confirmed by 7 tests
(`internal/replay/contention_test.go`), including hand-derived exact expected latencies for 1-slot and
2-slot cases, all correct on first implementation.

Both fields are additive with backward-compatible zero values — the complete pre-existing test suite
(Programs 001-011, the challenge suite, tuning) passes byte-for-byte unchanged with these fields
present, confirmed directly rather than assumed.

## 4. Experimental-Control Changes (Track B)

`internal/tuning/scenario.go`'s `Generate` drew the failure target via `failureRNG.Intn(n)`, where `n`
(target count) is topology-seed-dependent under `DefaultScenarioSpace`. Fixed by drawing the candidate
from `Intn(len(targetNames))` — the fixed, topology-independent name-pool size. When the candidate index
falls within the current topology's actual count, the failure window is now genuinely independent of
Topology; when it doesn't, the scenario simply has no failure (a disclosed, principled consequence, not
reintroduced coupling). Six axis-ownership tests now cover all four SeedTree axes symmetrically
(`internal/tuning/scenario_test.go`).

**Mandatory historical impact investigation**: reran the Stage 8 tuning pipeline
(`tuning.NewSplit` + `RunRandomSearch` + Holdout validation) under the corrected generator. The **same**
winning configuration was found (`config_hash 814c4f656afdbfce`, unchanged — the search itself doesn't
depend on scenario generation), with small utility drift (Development 0.7191→0.7218, Holdout
0.6947→0.6956) and a slightly narrower generalization gap (0.0106→0.0085). **The qualitative conclusion
is unchanged.** Secondary finding, disclosed not fixed: `ScenarioSetHash` hashes only seed tuples, not
resolved scenario content, so a hash match no longer guarantees byte-identical generated scenarios once
`Generate`'s logic changes, as this fix just demonstrated.

## 5. Real-Engine Changes (Track A)

`RealEngine.run` built each dynamic policy's selector via `policy.New`'s own fresh `LoadTracker`/
`LatencyTracker`, entirely disconnected from `proxy.ReverseProxy`'s own internal pair — the one
`ServeHTTP` actually increments/decrements at genuine dispatch/completion boundaries (`proxy.go` lines
186-187, 300; already directly proven correct at the proxy level by the pre-existing
`TestProxy_P2C_EndToEnd_AvoidsBusyEdge`). Stage 11 fixed only the latency signal via a post-hoc
response-header bridge, explicitly disclosing that load tracking remained unfixed.

Fixed properly: `PolicySpec.New` gained an additive `Trackers{Load, Latency}` parameter (zero value =
construct fresh, so `RunWorld`'s call site and virtual-engine determinism are untouched).
`RealEngine.run` now constructs the `ReverseProxy` FIRST (nil selector), builds the real selector using
the proxy's OWN `LoadTracker()`/`LatencyTracker()`, then attaches it via `SetSelector` — genuine,
concurrency-correct signals, not an approximation. The now-unnecessary post-hoc bridge was removed
entirely (would have double-counted).

## 6. New Experiments

| ID | What | Key artifact |
|---|---|---|
| 012-A | Program A's flagship finding rebuilt under contention (Capacity 0/1/2/3 sweep) | `012A-program-a-under-contention.json` |
| 012-B | H2 (staleness/oscillation) tested for the first time via Track C | `012B-h2-staleness-oscillation.json` |
| 012-C | B1 cache-affinity trap re-run under contention | `012C-cache-affinity-trap-under-contention.json` |
| 012-D | Attribution re-evaluated with genuine wait-time decomposition | `012D-attribution-with-waiting.json` |
| 012-E | Statistical robustness of the contention-reversal finding (12 seeds) | `012E-contention-reversal-robustness.json` |
| 012-F | Recovery dynamics re-run under contention | `012F-recovery-dynamics-under-contention.json` |
| (011-F rerun) | Virtual-vs-real final confirmation after Track A | `011F-virtual-vs-real.json` |
| (008-B/C rerun) | Stage 8 tuning pipeline under the corrected generator | `008B-search-ledger.json`, `008C-holdout-validation.json` |

Two new benchmarks (`BenchmarkRunWorld_{Adaptive,RoundRobin}Policy_Contention`,
`internal/replay/world_bench_test.go`) measure the contention model's throughput cost directly against
the pre-existing flat-model benchmarks.

## 7. Before/After Results

### 7.1 The central finding: Program A's flagship reversal (012-A)

| Capacity | EWMA mean | Adaptive mean | Winner |
|---|---:|---:|---|
| 0 (flat, = Stage 11) | 15.90ms | 27.38ms | EWMA |
| 1 | **131.06ms** | **27.93ms** | **Adaptive** |
| 2 | 16.36ms | 27.38ms | EWMA |
| 3 | 16.04ms | 27.38ms | EWMA |

**A sharp, capacity-threshold-dependent reversal, not a uniform one.** At Capacity=1, EWMA's
concentration onto edge-a pushes its utilization to ρ≈1.09 (already computed as over the queueing-
stability boundary in Stage 11's own flat-model accounting) — genuine, unbounded queue growth results
(mean latency 8x worse). Adaptive, which never over-concentrated, is essentially unaffected. At
Capacity≥2, doubling capacity halves edge-a's utilization to ~0.55 (comfortably stable) and EWMA
recovers almost completely. **Adaptive's advantage is real but concentrated in the specific regime
where EWMA's own strategy pushes a target past the stability boundary — not a broad range of realistic
capacity settings.**

Robustness (012-E): Adaptive faster in 12/12 independent traffic seeds at Capacity=1, Cliff's Delta =
1.000 (maximal), bootstrap 95% CI on the mean difference [88.67ms, 107.58ms] — entirely positive, not
single-run noise, and a larger, more decisive effect than the original flat-model finding's own CI.

### 7.2 H2 — tested for the first time (012-B)

| Policy | Overall mean | Swap-window[1.0-1.3s] mean | Share still to degraded target |
|---|---:|---:|---:|
| ewma | 64.31ms | 141.25ms | **100%** |
| adaptive (any `StaleAfter`: 100ms/1s/3s) | 30.13ms | 90.77ms | 63.33% |

EWMA gets **completely and permanently stuck** on the now-degraded target during the swap window — a
severe, total lock-in failure. Adaptive lags less severely but is not immune (63% of decisions still
wrong). `StaleAfter`'s specific value made **zero measurable difference** across three orders of
magnitude — the residual lag more likely comes from `LatencyTracker`'s own EWMA-smoothing delay (needing
several fresh observations to update its estimate) than the explicit staleness-reset mechanism this
experiment was designed to probe. **H2 confirmed for EWMA (severe); partially confirmed for Adaptive,
via a different mechanism than hypothesized.**

### 7.3 Cache-affinity trap under contention (012-C)

| Capacity | Policy | Mean | Post-recovery return to A |
|---|---|---:|---:|
| 0 | adaptive-default | 30.51ms | 0% |
| 1 | adaptive-default | 100.68ms | **68%** |

Under contention, the trap **partially self-heals**: the overloaded "wrong" target's real queueing cost
eventually outweighs the fixed 0.1 cache-affinity bonus — negative feedback the flat model could not
express. Not a full cure (68% is still substantial residual misrouting), but a real, mechanistically
distinct improvement over the flat model's complete 0% lock-in. Mean latency confirms §7.1's finding
generalizes to this scenario: round-robin/EWMA explode under contention (254ms/247ms) while every
Adaptive variant stays far lower (100-144ms). Separately, Stage 8's own tuned config (Cache weight
0.051, much lower than the default 0.1) already neutralizes the trap under the flat model (94% return,
matching EWMA) via its own weight balance, without any deliberate ablation.

### 7.4 Attribution with genuine waiting (012-D)

| Policy | Target | Service | Observed | Wait | Wait share |
|---|---|---:|---:|---:|---:|
| ewma | edge-a | 15.00ms | 91.86ms | 76.86ms | **83.7%** |
| ewma | edge-c | 60.00ms | 411.94ms | 351.94ms | 85.4% |
| adaptive | edge-a | 15.00ms | 15.00ms | 0.00ms | 0.0% |
| adaptive | edge-c | 60.00ms | 60.00ms | 0.00ms | 0.0% |

A direct, quantified decomposition of §7.1's mechanism: under EWMA, observed latency is 83-85% pure
waiting; under Adaptive, essentially none of it is. Little's Law's relative error remains ~0 across all
6 rows even with substantial real wait components now present — a genuinely meaningful internal-
consistency check under actual queueing, unlike Stage 11's necessarily-trivial version.

### 7.5 Recovery dynamics under contention (012-F)

| Capacity | Transition/steady-state p99 ratio (all 3 policies) |
|---|---|
| 0 (flat, = Stage 11) | exactly 1.00x |
| 2 | ~1.20x |

A real, measurable adaptation cost is now observable for the first time, confirming Stage 11's own
diagnosis of why it couldn't see one. The ~1.20x penalty is roughly uniform across all three policies in
this scenario — suggesting this specific transition's cost comes from the topology change itself (losing
a target reduces total capacity) rather than differentiating one policy's adaptation speed from
another's here.

### 7.6 Virtual-vs-real, final (011-F rerun)

| Policy | Virtual max_share | Real max_share (pre-Track-A) | Real max_share (post-Track-A) |
|---|---:|---:|---:|
| ewma | 0.973 | 0.973 | 0.973 |
| adaptive | 0.503 | 1.000 | **0.500** |

Outcome A from the assignment's own list: real Adaptive now closely resembles virtual Adaptive.

## 8. Stage 11 Claim Re-Evaluation and Claim-Reconciliation Table

| Earlier claim | Stage 12 intervention | New evidence | Status |
|---|---|---|---|
| EWMA wins raw mean latency under heterogeneity (flat model) | Track D (finite capacity) | §7.1: reverses at Capacity=1 (ρ>1), survives at Capacity≥2 (ρ<1) | **Narrows to a specific regime — the flat-model claim was correct FOR THAT MODEL, but does not generalize once real capacity exists at the threshold where concentration becomes unstable** |
| Adaptive's cache-affinity trap causes 0% post-recovery return | Track D (contention) | §7.3: rises to 68% under contention — partial self-healing via negative feedback | **Narrows — the trap is real but less absolute once capacity pressure exists** |
| H2 untestable (no time-varying latency) | Track C | §7.2: EWMA 100% stuck, Adaptive 63% stuck, StaleAfter irrelevant | **Now tested. Confirmed for EWMA (severe); confirmed-but-different-mechanism for Adaptive** |
| RealEngine's Adaptive load signal not validated | Track A | §7.6: real max_share 1.000 → 0.500, matching virtual's 0.503 | **Resolved — validated, matches virtual closely** |
| Topology/Failure axes independent (claimed, one direction only tested) | Track B | §4: full symmetric independence now tested and holds (with a disclosed suppression case) | **Fixed and confirmed** |
| Stage 8 tuning result (config, utility, generalization gap) | Track B (generator fix) | §4: same winning config, utility drift <0.3pp, gap narrows slightly | **Unchanged qualitatively — small numerical change only** |
| Little's Law validates only bookkeeping, not queueing (no wait existed) | Track D | §7.4: 83-85% wait share now decomposed and Little's Law still holds | **Extended — now a genuine consistency check under real queueing, not merely a restatement** |
| transition_p99 == steady_state_p99 (flat model could not express adaptation cost) | Track C+D | §7.5: ratio rises to ~1.20x under contention | **Confirmed the diagnosis — mechanism now exists, cost is visible** |

## 9. Negative Results

- H2's `StaleAfter` sweep (100ms/1s/3s) produced **zero measurable difference** in Adaptive's swap-window
  behavior — the hypothesized mechanism (explicit staleness reset) is not what's driving the residual
  lag in this scenario; the more likely cause (EWMA-smoothing delay) was not exhaustively isolated
  further, per this stage's own discipline against indefinite pursuit of one experiment.
- Program C's recovery-dynamics penalty (~1.20x) was **uniform across all three policies tested**, not
  differentiated by policy — the mechanism now works, but this specific scenario doesn't reveal a
  policy-specific adaptation-speed story the way it might have.
- Capacity=2 and Capacity=3 show EWMA's flat-model dominance **fully restored** — the reversal is not a
  general property of "adding any contention," only of contention severe enough to cross the stability
  boundary a policy's own concentration creates.

## 10. Mechanisms (Only What the Experiments Support)

- EWMA's flat-model win is explained by unconstrained load concentration; its contention-model loss is
  explained by that same concentration crossing the ρ=1 queueing-stability boundary, directly confirmed
  by the wait-time decomposition (§7.4) and the capacity sweep (§7.1).
- The cache-affinity trap's partial self-healing under contention is explained by the "wrong" target's
  rising real queueing cost eventually outweighing the fixed cache-affinity score bonus — inferred from
  the return-rate change correlated with capacity, not independently isolated signal-by-signal (a
  legitimate limitation, not a proven decomposition).
- H2's Adaptive lag is NOT explained by the hypothesized `StaleAfter` mechanism (ruled out directly);
  the EWMA-smoothing-delay explanation is plausible and consistent with available evidence but was not
  independently confirmed via a dedicated ablation (an honestly-flagged gap, not a claimed proof).

## 11. Performance Cost (Section 29)

~7-8% virtual-engine throughput reduction from the contention machinery (Adaptive: 366k→339k
virtual-requests/sec; RoundRobin: 436k→402k), measured directly via `go test -bench`, not estimated.
A modest cost for three previously-unanswerable research questions (the flagship reversal, H2, and
wait-time-decomposed attribution).

## 12. Limitations

1. **RealEngine's contention is not comparable to Track D's explicit model** — real physical servers
   have their own OS-level concurrency behavior, never reading `Capacity`/`ServiceTimeSchedule` at all.
   Virtual-under-contention vs. real is not a like-for-like comparison the way the flat-model one was.
2. **Capacity is uniform per experiment, not derived from anything physical** — Capacity=1 was chosen
   as the simplest, most interpretable assumption, not calibrated against any real server's actual
   concurrency limit. The regime boundary identified (§7.1) is specific to this scenario's arrival
   rate/service-time combination, not a universal capacity threshold.
3. **H2's residual Adaptive lag mechanism was not conclusively isolated** (Section 9's first negative
   result) — a real, disclosed gap in mechanistic explanation, not a claimed-and-unproven finding.
4. **Program C's contention re-run used only one capacity level (2) and one scenario** — the "uniform
   penalty across policies" finding has not been checked for generality.
5. **The FIFO queue discipline is the only one implemented** — no priority queueing, no per-policy queue
   ordering, matching Track D's own explicit non-goal (a minimal model, not a general one).
6. Every limitation from Stage 11 (§20) not explicitly addressed above still stands: only one genuine
   distribution shift was ever tested; most individual regime-map cells still rest on a single seed.

## 13. New Unresolved Questions (Earned by This Stage's Evidence)

- Where exactly is the capacity threshold for other scenario shapes (different arrival rates, different
  heterogeneity levels) — is ρ=1 a universal predictor of when EWMA-style concentration collapses, or
  scenario-specific?
- Does isolating `LatencyTracker`'s smoothing alpha (rather than `StaleAfter`) actually explain H2's
  residual Adaptive lag, as hypothesized but not tested?
- Does Program C's uniform-across-policies transition penalty hold for other topology-change shapes
  (e.g., a target recovering rather than failing, or a capacity change rather than a failure)?
- Would a priority-aware or per-policy queue discipline change any of §7's findings, or is FIFO's
  simplicity sufficient for every research question this project currently asks?

## 14. Claims Supported by Evidence

- Stage 11's flat-model EWMA-wins finding was model-dependent — not a vague "maybe it's different under
  a richer model" but a specific, checked mechanism: in the one tested severe-heterogeneity scenario,
  the reversal appeared exactly at the capacity level where EWMA's own concentration pushed a target's
  utilization past ρ=1 (the textbook queueing-stability boundary), and vanished exactly one level below
  it. This confirms the mechanism IN THIS SCENARIO precisely; it does not establish ρ=1 as a general
  predictor of when EWMA-style concentration collapses across other service-time ratios, arrival
  patterns, capacities, or topology shapes — that generalization question is explicitly unresolved
  (§13) and untested.
- H2 is real for EWMA (severe, total lock-in) and real-but-bounded for Adaptive.
- RealEngine's load defect is fully fixed and validated end-to-end (Adaptive real max_share now matches
  virtual within 0.6%).
- SeedTree's Topology/Failure axes are now genuinely, symmetrically independent (with one disclosed,
  principled exception), and this did not materially change Stage 8's own conclusions.
- Little's Law's bookkeeping consistency now extends to a genuine, substantial (83-85%) wait-time
  component, not merely a trivial zero-wait restatement.

## 15. Claims NOT Supported by Evidence (Explicitly)

- That Adaptive is now unconditionally better than EWMA — the reversal is regime-specific (Capacity=1
  in this scenario); EWMA fully recovers its flat-model dominance at Capacity≥2.
- That the cache-affinity trap is "fixed" by contention — it is measurably less severe (68% vs 0%
  return) but far from eliminated.
- That H2's mechanism for Adaptive is fully understood — the `StaleAfter` hypothesis was ruled out, but
  the alternative (smoothing delay) was not independently confirmed.
- That virtual-under-contention and real-engine results are directly comparable — they are not (Section
  12, Limitation 1); only the flat-model virtual-vs-real comparison (Section 7.6) is apples-to-apples.
- That Program C's uniform transition penalty generalizes beyond the one scenario/capacity level tested.
- **That ρ=1 is a validated, general predictor of when EWMA-style concentration collapses in
  FlashFlow.** ρ=1 is textbook queueing theory's stability boundary, and it correctly located the
  reversal in the ONE scenario tested — that is a real, mechanistically-sound result, not a coincidence.
  But one scenario, one service-time ratio, one arrival pattern, and one topology shape is not a
  generalization test. Whether ρ≈1 reliably predicts this reversal under a different heterogeneity
  ratio, a bursty rather than constant arrival process, or a different topology size remains explicitly
  open (§13) and must not be read as settled.

---

## Stage 12 Verdict

**PASS.**

Every one of the four tracks was completed, tested, and validated against real evidence, not assumed.
The central Stage 12 question — does making FlashFlow more faithful to the dynamics Stage 11 proved
important change what we believe about its routing policies — has a clear, evidence-backed answer:
**yes, sharply and specifically, in the scenario tested.** Stage 11's flagship finding (EWMA beats
Adaptive on raw mean latency under heterogeneity) reverses completely once genuine capacity constraints
push EWMA's own concentration past the ρ=1 stability boundary, and recovers completely one capacity
level more forgiving — a precise, mechanistically-explained, statistically-robust result for this
scenario, not a vague "it's more complicated now." It is deliberately NOT generalized further than
that: whether ρ≈1 is a reliable predictor across other service-time ratios, arrival patterns, or
topology shapes is untested and stays an open question (§13), not a claimed law. Two real bugs
(RealEngine's load signal, the Topology/Failure seed leak) were fixed
and validated end-to-end, with a mandatory historical-impact check confirming Stage 8's own conclusions
survive materially unchanged. H2, explicitly declined in Stage 11 as untestable, was built and tested
for the first time, with an honest negative result (the hypothesized `StaleAfter` mechanism) alongside
the confirmed positive one (EWMA's severe lock-in). Every reversal, narrowing, and confirmation is
attributed to a specific, checked mechanism — not asserted and not tuned to produce a preferred answer.
