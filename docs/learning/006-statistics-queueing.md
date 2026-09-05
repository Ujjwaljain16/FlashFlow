# Stage 6 Learning Notes — Statistics, Distributional Analysis & Queueing Attribution

## Before Stage 6

Stage 5 gave FlashFlow deterministic, reproducible execution: the same manifest and seed now produce the same event trace, every time, proven by repeated-run comparisons across 005-B, 005-D, and 005-G. That answered "did this experiment run the way it was defined to run." It did not answer whether an observed difference between two experiments — two policies, two configurations, two engines — was real, how large it was, or what mechanism produced it. Stage 5's own exit artifact named this gap directly: 005-H quantified a real-vs-virtual latency difference from a single pair of runs, and 005-E reproduced Stage 3's EWMA lock-in at different exact proportions with no way to say whether that variation was noise or something systematic.

## The Statistical Gap

A raw trace, however large and reproducible, is not an analysis. Three concrete examples from this project's own history made the gap specific rather than abstract:

1. Stage 3's EWMA lock-in varied across three real runs (94/4/2, 68/29/3, 18/79/3) with no tool to say whether that was three samples of one underlying distribution or three unrelated accidents.
2. 005-H compared exactly one real run to one virtual run and found a latency gap — suggestive, but a single pair can't rule out that a differently-timed pair would have shown something else.
3. Stage 4's cache-stampede and coalescing findings (004-C, 004-D, 004-F) were each measured from a single run per condition; 004-F's failure-correlation finding in particular only ever recorded aggregate counts, never a per-burst sample fine-grained enough to run a real statistical test against.

## Method Selection

The project's own instruction was explicit: identify real questions first, then pick the smallest tool that answers each one, validated on synthetic data before touching real evidence. In practice that produced four primitives, each earned by a specific need:

- **Percentiles and descriptive stats** (`Mean`, `Median`, `StdDev`, `Percentile`) — the foundation every other tool sits on, and the thing FlashFlow already needed since Stage 1's latency reporting, just never had as independently-tested, reusable functions.
- **Mann-Whitney U** — for "do these two samples come from systematically different populations," appropriate for the skewed, heavy-tailed latency and count data this project actually produces, where a t-test's normality assumption doesn't hold.
- **Cliff's Delta** — because Mann-Whitney's p-value answers "is there evidence of a difference," not "how large is it," and every comparison in this stage needed both questions answered separately.
- **Seeded bootstrap confidence intervals** — for quantities without a closed-form standard error (medians, percentile differences), with the RNG always explicit and separate from any experiment's own seed, so running an analysis can never alter the evidence it analyzes.

## Validation

006-A tested all four primitives against five scenarios with a known right answer *before* any of them touched a FlashFlow experiment: identical distributions (expect no evidence of a difference), a clear 3-sigma shift (expect strong evidence), the same effect size at two very different sample sizes (expect similar effect size, narrower interval at the larger n), an outlier-contaminated sample (expect the rank-based statistics to resist it where a mean wouldn't), and a bootstrap CI for a distribution with a known true mean (expect the interval to contain it). All six checks passed, including a same-seed determinism check on the bootstrap itself. One honest nuance surfaced rather than smoothed over: sample size didn't just narrow the confidence interval as expected, it also made the Cliff's Delta point estimate itself less noisy — precision and estimate stability turned out to be related but not quite identical claims.

## Routing Findings

006-B set out to characterize EWMA lock-in's variability across seeds and got a null result first: permuting three genuinely identical targets' tie-break order across 50 seeds produced *zero* variance in lock-in severity — every run converged to exactly the same 0.9667 max-share, only the winner's identity changed. Investigated rather than accepted at face value: relabeling truly identical targets under an identical fixed workload is a pure symmetry operation, so severity was never a random variable under that design at all. Adding a second cell — the same 50 seeds, plus a small ±2ms per-run service-time jitter, the actual kind of noise that drove Stage 3's real variability — revealed a second mechanism entirely: severity barely moved, but the first-in-order target's win rate collapsed from 100% to 36% (close to the 33% chance baseline for three targets). Tie-break order controls the outcome only when targets are exactly, unrealistically equal; the moment any real timing difference exists, EWMA's actual comparison rule takes over instead. That gives Stage 3's original variability a mechanistic explanation neither Stage 3 nor Stage 5 could state.

## Cache/Coalescing Findings

006-C reconfirmed 004-C/004-D's upstream-request finding with real replication behind it for the first time: 15 independent real runs per condition produced literally zero variance in upstream count within each condition, complete separation between them. The more consequential result was a caught mistake: a first attempt at comparing per-burst failure counts under coalescing via Mann-Whitney produced an unstable p-value across identical reruns (0.003 to 0.41, crossing 0.05 in both directions) — because Mann-Whitney tests for a location shift, and the actual phenomenon (coalescing concentrating failure onto whole bursts, all-or-nothing, versus spreading it across partial failures) is a *shape* difference, not a central-tendency one. Measuring the all-or-nothing proportion directly and bootstrapping its difference gave a stable, clearly-nonzero answer every time. This also surfaced a real, undone limitation: `internal/netsim`'s loss simulation has no seeded-RNG injection point through `EdgeConfig`, making that specific data source non-reproducible run to run — recorded rather than silently worked around.

## Queueing Analysis

006-D found that `transport.TransportConfig.MaxConnsPerHost` — already present, already wired into the real `http.Transport`, never once used in four prior stages to create actual finite capacity — is exactly the real bottleneck Stage 5's exit artifact said the virtual engine couldn't yet provide. Set to 5 against a fixed 20ms Origin delay, an 18-point load sweep produced a textbook saturation curve: linear throughput and flat latency below capacity, throughput plateauing at almost exactly the analytically predicted 250 req/s ceiling above it, while latency grew six-fold. Little's Law held within 5.0% mean error, measured with `L`, `λ`, and `W` all sharing one client-side system boundary so the check wasn't measuring three different things and calling it one law. 006-E then decomposed the resulting tail latency using that same real bottleneck: 83% of the elevated p99, in that specific controlled scenario, was attributable to waiting time rather than service time — an attribution trustworthy only because Origin's service time was an independently known, unchanging constant, not something inferred from the same data being explained.

## Real vs Virtual

006-F replicated 005-H's single-pair comparison 15 times on the real side (plus 5 reconfirmation runs on the virtual side, which stayed byte-identical as already established). The structural match — both engines produce exactly the stampede's burst size in upstream requests — held with zero variance across every real replicate, confirming it as genuinely deterministic on both sides, not a coincidence of 005-H's one recorded run. Real p99 turned out to be genuinely noisy (a real several-millisecond spread across replicates) where virtual p99 is a fixed constant — but the gap between them never approached zero across that noise, closing 005-H's open question: the gap is a stable property of the two engines' different latency models, not an artifact of which pair happened to be compared.

## Surprises

1. **006-B's zero-variance null result was the most informative finding of the stage**, not a failed experiment. It revealed a hidden assumption (permuting labels among literally identical things creates genuine randomness) that turned out to be false, and the fix it motivated (adding realistic jitter) uncovered a second, more important mechanism.
2. **Mann-Whitney's instability on 006-C's failure-shape question was caught only by rerunning the same setup five times and watching the p-value cross 0.05 in both directions.** A single run would have looked like a normal, if unremarkable, result.
3. **`MaxConnsPerHost` had been sitting in the codebase since Stage 2/3, fully wired, completely unused for its actual purpose** — four stages of experiments ran with it at its default (unlimited) value, and the queueing question the project cared about most only needed someone to actually set it.
4. **The Cliff's Delta point estimate itself was noisier at small sample sizes than expected** (006-A) — precision from replication turned out to apply to the effect-size estimate, not just the interval around it.

## Limitations

- Every statistical claim in this stage is scoped to the specific controlled scenario that produced it. 006-D and 006-E's queueing findings are about the transport-layer connection limit specifically exercised, not a general claim about queueing anywhere else in FlashFlow — Origin itself remains, as documented since Stage 4, an unbounded infinite-server model.
- `internal/netsim`'s loss simulation lacks a seeded-RNG injection point through `EdgeConfig`, a real gap 006-C surfaced and left unfixed, since fixing it wasn't necessary for that experiment's own conclusions.
- No multiple-comparison correction was implemented, because no experiment in this stage ran the kind of large comparison matrix (item 35's "RR vs WRR vs LC vs EWMA vs P2C, all pairs") that would need one — a deliberate absence, not an oversight, per the stage's own instruction not to add correction methodology before the experiment design calls for it.
- Tail-percentile estimates from small samples remain inherently imprecise — stated in `internal/statistics`'s own doc comments, and visible directly in 006-D's growing relative error (1% to 12%) as concurrency, and therefore variance, increased.
- This stage's queueing attribution method (comparing against an independently-known, controlled service-time constant) doesn't transfer directly to a system where that constant isn't available — a real production origin, for instance — without first establishing an equivalent independent baseline.

## Evidence Discipline

Findings from this stage sort cleanly into three tiers. **Strong** (deterministic, replicated, mechanism identified): the upstream-request structural match in both engines (006-C, 006-F), Little's Law holding within 5% error (006-D), the p99 waiting-time attribution grounded in a controlled constant (006-E). **Suggestive, precisely quantified**: the real-vs-virtual p99 gap (006-F) and the coalescing failure-shape difference (006-C) — both real and bounded by a confidence interval, neither claiming more precision than 15-30 replicates support. **Unresolved, explicitly flagged**: whether EWMA's jitter-driven win-rate collapse (006-B, Cell 2) generalizes beyond the specific ±2ms perturbation tested, and whether the netsim reproducibility gap matters for any conclusion not yet drawn.

## Stage 7 Motivation

Stage 6 turned "EWMA sometimes locks onto one target" into "under controlled seed variation, EWMA's lock-in mechanism is a hard tie-break rule that only survives when targets are exactly equal — and reproducibly collapses toward chance-level winner selection the instant any real timing difference exists." That is precisely the evidence a routing decision needs before adapting: not "sometimes this signal fails," but a characterized, quantified description of *when* and *why* it fails, and how large the effect is. Stage 6 also demonstrated, concretely, that FlashFlow can now measure whether a proposed change is a real improvement (bootstrap CI excludes zero) or noise (CI straddles it) — the exact machinery a router that adapts based on observed signals, or an evaluation that compares policies fairly, needs underneath it. Stage 7's adaptive routing and counterfactual evaluation are not yet built, and shouldn't be — but the statistical and mechanistic foundation they'd need to be trustworthy, rather than merely plausible, now exists.
