# Hypotheses: Experiment 006 — Statistics, Distributional Analysis & Queueing Attribution

## H1 — The Statistical Primitives Behave Correctly on Known Synthetic Data

Before trusting any statistical claim about a real FlashFlow experiment, the methods making that claim need their own validation, independent of `internal/statistics`'s unit tests (which check internal correctness; this checks end-to-end behavior on realistic-shaped data).

- *Setup*: five synthetic scenarios, each with a known expected outcome: (a) two samples from the identical distribution, (b) two samples with a large, unambiguous shift, (c) the same practical effect size at two very different sample sizes, (d) a distribution contaminated with extreme outliers, (e) a bootstrap CI for a distribution whose true mean is known exactly.
- *Prediction 1*: identical distributions produce a large Mann-Whitney p-value and a negligible Cliff's Delta; clearly shifted distributions produce a small p-value and a large Cliff's Delta.
- *Prediction 2*: the same practical effect size (a fixed shift) produces a similar-magnitude Cliff's Delta regardless of sample size, but a *narrower* bootstrap CI and typically a smaller p-value at the larger sample size — sample size buys precision and power, not a larger effect.
- *Prediction 3*: Cliff's Delta (rank-based) is not dominated by a handful of extreme outliers the way a raw mean difference would be — outlier contamination should shift the mean noticeably more than it shifts the delta or the median difference.
- *Prediction 4*: a bootstrap CI for the mean of a large sample from a distribution with a known true mean brackets that true value.
- *Purpose*: this is the methodology gate every later 006 experiment depends on — if these five scenarios don't behave as expected, nothing built afterward on top of these primitives can be trusted, no matter how interesting the resulting numbers look.

## H2 — EWMA Lock-In Variability Across Controlled Seeds

Stage 3 observed extreme, apparently-random target imbalance among genuinely equal targets (94/4/2, 68/29/3, 18/79/3 across three real runs); Stage 5 reproduced a different split again. `EWMASelector` has no RNG of its own — its documented cold-start rule is "ties among unobserved targets fall back to `available` order." This hypothesis asks how variable the resulting lock-in actually is under a seed-controlled source of variation targeting that exact mechanism.

- *Setup, Cell 1*: 3 targets with identical nominal service time, a fixed deterministic 300-request arrival schedule (unchanged across runs), and the `available` slice's order permuted by a seeded shuffle — 50 seeds, 50 runs.
- *Original prediction (stated here even though it turned out wrong, per this project's practice of recording incorrect predictions rather than only the ones that held)*: the maximum-target-share statistic would vary meaningfully run to run, characterizable by a median, a percentile range, and a bootstrap CI.
- *What actually happened*: zero variance. Every one of 50 runs produced exactly the same 0.9667 max-share; only the winning target's identity varied, matching the permuted order 50/50 times. Investigated rather than reported as-is: permuting labels among truly identical targets under an identical workload is a pure relabeling — the underlying timing dynamics are isomorphic across every permutation, so severity is a deterministic structural constant of (spacing, service time, request count), not a random variable under this design at all.
- *Cell 2, added because of that discovery*: the same 50 seeds, plus a small (±2ms on a 20ms base) per-run service-time jitter — the actual kind of timing noise that produced Stage 3's original variability, which Cell 1's design never introduced.
- *Revised prediction for Cell 2*: genuine (if small) timing differences between targets should let real latency differences compete with tie-break order for control of the outcome.
- *Purpose*: an honest two-part story — first a null result that revealed a hidden assumption (permutation alone doesn't create genuine variability among literally-identical targets), then a targeted refinement that tests what actually varies once realistic noise exists.

## H3 — Cache/Coalescing Effect Consistency and Failure-Shape Attribution

Stage 4 established coalescing's core benefits (004-D: upstream requests collapse from C to 1; 004-F: failure shape shifts from partial to all-or-nothing) each from a single run per condition. This hypothesis asks whether those findings hold up under genuine replication, and — separately — demonstrates why the choice of statistical test matters as much as running one at all.

- *Setup, Part 1*: 15 independent real replicate runs per condition (coalesce on/off), each a fresh Edge+Origin pair, measuring upstream request count and p99 latency for a 30-request burst against a just-expired key.
- *Prediction 1*: upstream request count is perfectly consistent within each condition (near-zero variance) and completely separated between conditions — a structural property of whether anything deduplicates concurrent misses, not a probabilistic tendency.
- *Prediction 2*: p99 latency shows a real but more modest effect size, consistent with 004-C/004-D's documented Origin-model limitation (no queueing representation, so a stampede's tail-latency cost is understated).
- *Setup, Part 2*: 30 independent real replicate bursts per condition, each against a fresh never-cached key under 30% simulated packet loss, recording the per-burst failure count individually — granularity 004-F's aggregate-only recording never preserved.
- *Prediction 3*: coalescing produces a higher proportion of all-or-nothing bursts (0 or all N requests failing) than independent dialing, and this proportion difference should be measurable via a statistic that actually targets distributional *shape*, not central tendency — Mann-Whitney U is the wrong tool for this specific question and is included deliberately to show why, not to report its result as the answer.

## H4 — Queueing/Concurrency Attribution via a Real Finite-Capacity Bottleneck

`transport.TransportConfig.MaxConnsPerHost` already exists and is already wired into the real `http.Transport` (`internal/transport/pool.go`) but has never been used anywhere in this project to create actual finite capacity — every prior experiment left it at the default (unlimited). Setting it low creates a genuine, measurable bottleneck: Go's transport will actually block a request until a connection frees up. This hypothesis uses that real bottleneck for a controlled load sweep and a Little's Law check, on a topology deliberately stripped down to client→transport→Origin — no Edge, cache, or routing — so `L`, `λ`, and `W` all describe one unambiguous system boundary (the client's own view of dispatch-to-response), per the measurement-boundary-alignment requirement.

- *Setup*: Origin with a fixed 20ms service delay, client transport capped at 5 concurrent connections, a load sweep across offered concurrency {2, 4, 5, 8, 15, 30} (below, at, and up to 6× capacity), 3 replicates per level, 1-second closed-loop measurement windows. `L` is sampled independently (a live outstanding-request counter, time-averaged every 2ms) from `W` (mean per-request latency) and `λ` (completed requests / window duration), so the Little's Law check isn't circular.
- *Prediction 1*: `L ≈ λW` holds within a small mean relative error across the sweep, since these are stable, closed-loop measurement windows over one consistent boundary.
- *Prediction 2*: below capacity, throughput scales roughly linearly with offered concurrency and latency stays near the pure 20ms service time; above capacity, throughput plateaus near the analytically-predicted ceiling (connections / service time = 5 / 0.02s = 250 req/s) while latency grows substantially — the classic finite-server saturation signature.
- *Explicit non-claim*: this is evidence about the transport-layer connection limit specifically exercised here, not a claim that FlashFlow's edge or origin behave like a textbook M/M/c queue in general, and not a claim about queueing anywhere else in the system that wasn't measured.

## H5 — Tail Latency Attribution: Decomposing p99 Into Service and Waiting

Building directly on 006-D's real finite-capacity bottleneck: when p99 rises under load, is that rise attributable to waiting time specifically, distinguishable from service time — and does the tail simply shift with the rest of the distribution, or stretch disproportionately?

- *Setup*: 10 replicates at baseline concurrency (2, below the 5-connection capacity) and 10 replicates at elevated concurrency (30, 6× capacity), Origin's service delay fixed and identical (20ms) in both conditions, full per-request latency percentiles (not just the mean 006-D recorded) captured per replicate.
- *Prediction 1*: both p50 and p99 shift substantially between conditions, but the p99-minus-p50 spread also grows — the tail stretches further from the median under load, not just moving in lockstep with it.
- *Prediction 2*: using the baseline p99 (measured under no queueing) as an estimate of the fixed service+overhead component, the majority of the elevated p99 should be attributable to waiting time rather than service time, since Origin's configured delay never changes between conditions — this attribution is only trustworthy because the service component is independently known and held constant by construction, not inferred from the latency data itself.

## H6 — Real vs Virtual Statistical Comparison

Experiment 005-H compared exactly one real run against one virtual run per concurrency level and found upstream requests matching exactly while p99 diverged in a specific, explained direction. This hypothesis asks the question 005-H's single-pair design couldn't answer on its own: is that gap a stable, replicable property of the two engines, or could a single real run have landed anywhere?

- *Setup*: 15 independent real replicates of 005-H's exact stampede scenario (burst=30, TTL=50ms, service=100ms, no coalescing), plus 5 reconfirmation runs of the virtual side (expected, by 005-B/D/G's already-established determinism, to be byte-identical every time — not a source of new variation to characterize, just a cheap sanity check before trusting the comparison).
- *Prediction 1*: the upstream-request-count match holds with zero variance across all 15 real replicates, confirming it as a genuine structural property on the real side, not a coincidence specific to 005-H's one recorded run.
- *Prediction 2*: real p99 shows genuine run-to-run variability (unlike the virtual side), but a bootstrap CI on the real-vs-virtual gap stays clearly separated from zero across that variability — the gap 005-H measured is a stable property of the two engines' different latency models, not an artifact of which single pair of runs happened to be compared.
