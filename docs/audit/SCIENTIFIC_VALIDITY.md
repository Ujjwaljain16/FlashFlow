# Scientific Validity Audit — Statistics, Replay, Adaptive Router, Tuner

This is the core of whether FlashFlow's headline result (an auto-tuned adaptive router that beats
baselines and generalizes to holdout) is trustworthy. It was treated as the highest-risk area of
the whole audit and received two dedicated adversarial passes (counterfactual replay; adaptive
router + tuner) plus a third on statistics proper. **No P0 was found in any of the three.**

## Statistics (`internal/statistics`)

All four implementations were validated against their standard algorithms and hand-computed
reference values in their own tests, not just "does it run":

- **Percentile**: linear interpolation (R-7/NumPy convention), correctly documented as such;
  defensively sorts a copy (verified non-mutating); correct on empty/single-element input; rejects
  NaN/Inf before arithmetic.
- **Mann-Whitney U**: standard U-statistic formula; tied ranks are correctly averaged (the single
  most common implementation bug in this test — confirmed absent via a hand-verified tie-group
  test); standard tie-correction term in the variance; degenerate all-tied case returns p=1 instead
  of dividing by zero; doc comment correctly caveats the ~8-observation floor for the normal
  approximation and states it is unpaired-only.
- **Cliff's Delta**: `(greater-less)/(nA·nB)`, ties correctly excluded from dominance in either
  direction (verified with an explicit tie-containing hand-computed test); range provably in
  [-1,1] by construction.
- **Bootstrap CI**: correct with-replacement resampling and percentile-CI construction; RNG is a
  required, non-nil caller-supplied parameter (never internally time-seeded) — every real call
  site passes a fixed integer seed, and determinism was confirmed against the current on-disk
  `006A-statistical-validation.json` (only the timestamp differs from the committed version; every
  statistical value is byte-identical).

Test quality across these four files is genuinely strong — no "no-panic"-only tests found.

**One real gap** ([F-08](FINDINGS.md)): the "automated queueing-theoretic attribution engine" PRD/TRD
describe (automatic ρ computation, generated causal-explanation text on spike detection) does not
exist. What was built — `cmd/experiment-006d`'s one-off Little's Law verification — is itself
methodologically sound (single, consistent measurement boundary; no waiting-time/service-time
mixing; honestly caveated error growth at higher concurrency) but is a hand-run script producing a
hand-written finding string, not the reusable, automatic engine the spec describes. No stage
document discloses this as a scope reduction against the named PRD/TRD feature.

**Minor**: Mann-Whitney used at n=10/side in 006-E, right at the documented reliability floor, with
no caveat in that experiment's own writeup ([F-39](FINDINGS.md)); low practical risk since the
finding leans on Cliff's Delta/CI rather than the p-value. The 006-C shape-vs-location correction
and the 004-A cache-stats baseline-subtraction fix each have no dedicated package-level regression
test ([F-40](FINDINGS.md)).

**No evidence of scientific self-deception was found.** Scenario generation for tuning is a pure
function of a seed, with no access to evaluation results or optimizer state — it cannot see which
policy wins. All five call sites that generate the Development/Holdout split use one code path,
always the same seed ranges. The 008-E adversarial-tuner test is reported honestly even where it
produced a tie rather than the originally planned strict domination — the project investigated why
rather than adjusting the scenario to force the planned result.

## Counterfactual Replay (`internal/replay`) — the highest-risk subsystem

**No P0 found.** This was verified, not merely inspected: `go test ./internal/replay/... -count=5
-shuffle=on` was run directly and passed consistently.

- **The identity test is a genuine byte-for-byte claim.** `TestRunWorld_IdentityDeterministic` runs
  the RNG-dependent P2C policy twice against an identical Scenario and compares the *full* trace via
  `FirstDivergence` plus `reflect.DeepEqual` on records and completion counts — not a summary-stats
  comparison, which would have been a materially weaker (and misleading) claim.
- **A genuine causality check exists, stronger than plain identity**:
  `TestRunWorld_DivergenceOnlyAfterInterventionPoint` asserts two traces are byte-identical *before*
  an intervention and diverge only at or after it — this would catch a class of bug (state computed
  from post-intervention information) that identity-alone testing would miss.
- **Isolation is genuinely exercised**: `TestRunWorld_Isolation` runs an unrelated interleaved
  `RunWorld` call between two runs of the target scenario and asserts no cross-contamination —
  exactly the test that would catch an accidentally shared or global mutable object.
- **RNG is correctly isolated per call.** P2C constructs its own `rand.New(rand.NewSource(seed))`
  from the Scenario's own (exogenous) seed on every `PolicySpec.New` call; no package-level
  `math/rand` global is used anywhere in `internal/replay` or the P2C selector.
- **`health.Registry`, the event queue, and the trace are all freshly constructed per `RunWorld`
  call** — nothing is retained or derived from `Scenario` across calls that could leak.
- **The new `WeightedRoundRobinPolicy` (currently uncommitted) was specifically checked for
  `Scenario.Targets` mutation** and found to only read the slice, never write or retain it — though
  this safety is currently incidental (a plain slice reference, not a defensive copy), not enforced
  ([F-18](FINDINGS.md) territory, see below).

**Real, structural risks (P1/P2, not P0):**

- **The exogenous/endogenous boundary is enforced by convention, not by the type system**
  ([F-18](FINDINGS.md)). `Scenario.UseHealthRegistry`/`ProbeInterval`/`Horizon` are protocol knobs,
  not world physics, but nothing prevents constructing two Scenarios for comparison that differ on
  one of these alone — `FirstDivergence` would then report a "divergence" that is actually a
  run-length or protocol artifact, not a policy effect. Every current caller gets this right by
  hand; there is no guardrail if a future caller doesn't. The project's own
  `docs/learning/007-adaptive-routing-replay.md` already states this exact gap ("demonstrated for a
  health-failure intervention specifically... not separately checked for every other kind of
  exogenous change") — it is self-acknowledged, not hidden, but not closed either.
- **`FirstDivergence`'s positional trace comparison is safe today only because policy code cannot
  currently call `Engine.Record`** (the `Instrumentation` interface doesn't expose it) — an
  emergent property of the current code shape, not a documented or tested invariant. A future
  change that let policy code emit its own trace events would silently break this without any test
  catching it.
- **The suspicious-looking "only timestamp changed" diffs** in the currently-modified
  `006A`/`007A`/`007F` result JSONs were investigated directly: tracing the actual code paths
  (007-A and 007-F don't touch the parts of `internal/replay` that changed; both are deterministic
  given the confirmed absence of global RNG state) makes a legitimate re-run at least as plausible
  an explanation as a hand-edit. The repository currently has no way to *prove* the distinction
  either way — a real, if modest, provenance gap.

Race-detector confirmation is unavailable in this environment (no gcc/CGO); the replay engine's own
design (no goroutines anywhere in `internal/vtime`) makes this a lower-risk gap than it would be in
a genuinely concurrent system, but it is stated here rather than glossed over.

## Adaptive Router and Tuner — the other highest-risk subsystem

**No P0 found.** Specifically hunted for and not found: holdout leakage, non-monotonic signal
direction, and a NaN- or unhealthy-target selection bug reachable via any current code path.

- **Holdout leakage**: every call site touching `split.Holdout` across all five 008-series
  experiments was traced. `RunRandomSearch` is called with Development scenarios only; Holdout is
  touched exactly once, after `Best()` has already selected a winner. The evaluation-result cache
  is a fresh, per-call local map that cannot leak between a Development run and a later Holdout
  evaluation.
- **RNG separation** between the optimizer (seeded independently), per-scenario generation, and
  P2C's per-run randomness is real — genuinely independent seed spaces, not derived from a shared
  source.
- **Direction correctness**: all four real Adaptive signals (Utilization, Latency, Cache, Cost) are
  provably bounded to [0,1] and monotonic in the correct direction under every input the codebase
  can currently construct; the objective function's three sub-scores are likewise all monotonic in
  the intended direction, hand-verified against known corner cases in `objective_test.go`.
- **Every headline number in `Stage8.md` was independently re-traced to raw JSON and matched
  exactly** — dev/holdout utility, winning weights, the full six-policy win-rate/non-inferiority
  table, and the challenge-scenario results. No orphaned or unsupported claim was found.
- **Sensitivity and robustness analyses are real, not relabeled aggregates**: perturbation of each
  of the 6 tunable parameters and per-scenario (not pooled) utility distributions were both
  confirmed by reading the actual computation, matching the Stage 8 narrative precisely.

**Real findings (P1/P2, not P0):**

- **The tuner's search loop never validates sampled candidates against `ConfigSpace.Valid()`**
  ([F-24](FINDINGS.md)) — safe today only because the Dirichlet sampler happens to always produce
  valid simplex points; no defense-in-depth if a different sampler is substituted later.
- **The "generalization" claim is narrower than its own framing implies**
  ([F-17](FINDINGS.md)): Holdout scenarios are drawn from the *identical* distribution as
  Development (same failure probability, same target-count and service-time ranges) — the only
  difference is the seed range. This is real, useful evidence against sampling-noise overfitting
  (008-E's adversarial test specifically demonstrates the tuner can be caught overfitting to
  specific draws, and that Holdout catches it) — but it is not evidence of robustness to a shifted
  traffic distribution, which is what TRD's own Generalization Rule describes as the target. This
  narrower scope is not disclosed anywhere.
- **Terminology**: the router is called "six-signal" throughout the learning notes and README; the
  code implements four signals (Health is a pre-filter, Capacity folds into Load). "Six" correctly
  describes the count of *tunable parameters* in `space.go` — the two documents' language was
  conflated.
- **A zero/negative configured capacity is silently treated as "unconfigured" (average) rather than
  "avoid this target"** ([F-26](FINDINGS.md)) — outside the tuned parameter space, so it doesn't
  affect the tuning results, but a real router misconfiguration-tolerance gap.
- **A latent NaN path exists in `scoreLatency`** if `ReferenceLatency` reaches exactly 0
  ([F-27](FINDINGS.md)), independently found by two separate review passes from different angles.
  Unreachable via any current config path (the tuner's own bounds and the default config both
  prevent it), but if ever triggered, Go's NaN comparison semantics (`NaN < x` and `x < NaN` are
  both false) would permanently lock routing onto the alphabetically-first candidate regardless of
  its actual quality — a severe failure mode for a currently-latent bug.
