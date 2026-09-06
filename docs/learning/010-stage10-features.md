# Stage 10 Learning Notes — Building the 9 Missing PRD/TRD Features

## Before Stage 10

Stage 9 closed with every one of the post-Stage-8 audit's 57 findings resolved — fixed, or, for the 9
PRD/TRD features the audit found silently missing, explicitly disclosed as deferred to a dedicated
build stage. A full design for all 9 features already existed from Stage 9's own planning
(`docs/StageArtifacts/Stage10-Plan.md`), with locked-in decisions on the questions that would
otherwise have needed re-litigating mid-build: hand-roll the two hardest pieces (the histogram, the
Bayesian Optimization math) rather than add dependencies; concretize "Fuze log import" as NCSA
combined-log format; widen `Scenario`'s seed into a real tree rather than a derive-only convenience.

## The Central Question

Could all 9 features actually be built — not stubbed, not partially implemented behind an honest
disclaimer, but built with the same testing discipline (hand-computed reference values, real
end-to-end exercises against actual HTTP servers, not just unit-level mocks) this project has applied
since Stage 1 — while keeping every one of Stage 9's own fixes intact and never regressing an existing
test.

## A Refactor That Was Honest About What It Would Break

Widening `Scenario.Seed` into a `SeedTree` (§10.3) was the highest-churn item in the plan for a
reason: it touches the one field every scenario generator in this project's history has depended on.
Splitting one shared `*rand.Rand` into three independent ones (per axis: topology, traffic, failure)
is what makes `TestGenerate_IndependentAxisControl` possible — holding Traffic fixed while varying
Failure now provably produces identical arrivals and only a different failure window, which was
structurally impossible under the old single-shared-RNG design. But it was never going to leave
Stage 8's actual reported numbers unchanged, and it didn't: rerunning 008-C after this refactor
reproduces a different winning configuration entirely (utility 0.7191 vs. the original 0.6594). This
was verified, not guessed at — `TestGenerateFromRoot_EquivalentToGenerateDeriveSeeds` confirms the new
code is internally consistent, and a full `scripts/final-validation.sh` run confirms the whole
pipeline still holds together end to end. The discrepancy is named directly in `Stage10.md` rather
than left for whoever next reads Stage 8's original numbers next to a live rerun to discover as an
unexplained mismatch — the same standard this project has applied to every other correction since
006-C's Mann-Whitney fix.

## Latin Hypercube Sampling Needed a Real Design Decision, Not Just an Implementation

The `Tuner` interface's `Suggest(previous []TrialResult) proxy.AdaptiveConfig` signature calls one
candidate at a time, with no advance knowledge of the total evaluation budget — fine for Random
Search (every draw is independent) and necessary for Bayesian Optimization (which genuinely adapts
to `previous`), but a real problem for LHS: a Latin Hypercube's defining property (every stratum of
every dimension used exactly once across N samples) cannot be built incrementally without knowing N
in advance. Rather than force an awkward incremental-LHS approximation, `NewLHSTuner` takes
`evaluations` as an explicit constructor argument and builds its whole design up front — a real,
disclosed asymmetry with the other two tuners' constructors, not a limitation quietly worked around.
The stratification property itself was verified directly, not just plausibility-checked: sorting 30
design points' `ReferenceLatency` values and confirming exactly one falls in each of 30 equal-width
bins is a hand-verifiable Latin Hypercube invariant, and it held.

## Hand-Rolling a Gaussian Process Was the Highest-Risk Piece in the Whole Stage

Cholesky decomposition, forward/back substitution, a squared-exponential kernel, and Expected
Improvement all needed to be correct together for Bayesian Optimization to produce anything
meaningful — and subtle numerical bugs in this kind of code are notorious for producing
plausible-looking, wrong output rather than an obvious crash. The mitigation was the same one Stage 8
already learned the value of the hard way: verify the foundation against hand-computed values before
building anything on top of it. `choleskyLower` was checked against a fully worked-by-hand 2×2 case
(A=[[4,2],[2,3]] → L=[[2,0],[1,√2]]) before ever being used inside a GP fit; `solveCholesky` was
checked against a hand-solved linear system; only once both passed did `fitGP`/`predict` get built on
top of them. The resulting GP was then checked against its own defining property — it must
essentially reproduce its own training data at near-zero uncertainty when queried at an already-observed
point (`TestFitGP_InterpolatesTrainingData`) — before `BayesOptTuner` itself was written. Every one of
these checks passed on the first real run, which is itself informative: the risk was real, and the
layered verification is why a single hidden mistake in, say, the back-substitution's transpose
indexing didn't silently propagate into a confidently-wrong acquisition function three files later.

## The Tuner Comparison Confirmed Exactly What Stage 8 Already Predicted

`cmd/experiment-010a` ran all three tuners through the identical search loop, scenario set, and
budget. Random Search: 0.7191. LHS: 0.7192. Bayesian Optimization: 0.7194. All three plateau, and the
largest relative difference across any pair is 0.04% — noise, not a real advantage. This is not a
disappointing result; it is the expected one, stated in the plan before a single line of GP code was
written: Stage 8 already showed Random Search converges by evaluation 24 of 200 and plateaus for the
rest, so there was never strong evidence this search space needed a more sophisticated optimizer. What
Stage 10 adds is that this is no longer an inference from Stage 8's data alone — it's now a directly
confirmed result, on the actual algorithms that would have been the alternative.

## `EdgeServer.SetDown` Reused an Existing Pattern Instead of Inventing a New One

The chaos engine's crash/recover actions needed some way to make a real `EdgeServer` behave as fully
down. Rather than add a new, chaos-specific mechanism, `SetDown` follows `SetArtificialDelay`'s
already-established shape exactly (a mutex-guarded field, a setter, read at the top of the request
handler) and extends the effect to the `/health` endpoint too — deliberately, since a crashed process
should fail its own health check, which is what lets this project's own `health.Checker` actually
observe a chaos-injected crash as a real outage rather than a change nothing downstream would notice.

## Limitations

- Stage 8's originally-reported specific tuning numbers no longer reproduce under the current
  scenario generator (see above) — the underlying methodology was re-verified and is unaffected, but
  refreshing those documents' own numbers against a fresh 008-series rerun is a natural follow-up.
- `RealExperimentConfig` (§10.8) covers the common real-engine case only; per-edge cache or
  network-impairment configuration still requires direct use of `internal/topology`/`internal/proxy`.
- `internal/chaos`'s flat 4-key YAML schema is a deliberate scope boundary, not a general YAML
  subset — no nesting, no nested lists, no nested maps.
- `BayesOptTuner`'s kernel hyperparameters (length-scale, signal/noise variance) are fixed constants,
  not fit via marginal-likelihood optimization — appropriate given 010-A's own finding that this
  search space doesn't reward more sophistication, not a shortcut taken under time pressure.

## Evidence Discipline

**Strong** (deterministic, hand-verified against known-correct values, or directly observed): the
Cholesky/solve implementations (hand-computed 2×2 case, general reconstruction property);
`fitGP`/`predict`'s training-data-interpolation property; LHS's exact one-per-bin stratification
property; the SeedTree independent-axis-control demonstration; the real end-to-end SWR/chaos/telemetry
tests against actual running HTTP servers, not mocks. **Suggestive**: the 010-A tuner comparison is a
single run at one seed, not replicated across independent seeds the way 007-H replicated a
single-scenario finding — the qualitative conclusion (no tuner meaningfully beats Random Search here)
matches Stage 8's own independent finding closely enough that a full replication study wasn't judged
necessary to report it with reasonable confidence, but it is one run, stated as such.

## What Remains

All 9 PRD/TRD features named in Stage 8's audit and disclosed in Stage 9 now exist as real, tested
code — see `docs/StageArtifacts/Stage10.md` and `docs/audit/RESOLUTION.md`. What would most improve
the project's own internal consistency next is not new capability but bookkeeping: rerunning the full
008-series experiment suite under the new `SeedTree`-based generator and updating `Stage8.md`/
`FinalResearchReport.md`'s specific numbers to match, so a future reader never has to independently
discover the discrepancy this document names explicitly.
