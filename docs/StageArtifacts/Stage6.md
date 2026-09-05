# Stage 6 — Statistics, Distributional Analysis & Queueing Attribution: Exit Artifact

## What Was Built

| Component | File(s) | Description |
|---|---|---|
| Percentiles & descriptive stats | `internal/statistics/percentile.go` | `Mean`, `Median`, `StdDev`, `Min`, `Max`, `Percentile` (linear interpolation) — the foundation every other tool sits on |
| Mann-Whitney U | `internal/statistics/mannwhitney.go` | Two-sample rank-sum test, normal approximation with tie/continuity correction; documents its own applicability floor and unpaired-only assumption |
| Cliff's Delta | `internal/statistics/cliffsdelta.go` | Effect size in [-1,1], Romano et al. magnitude thresholds; answers "how large," which a p-value never does |
| Seeded bootstrap CI | `internal/statistics/bootstrap.go` | `BootstrapCI`/`BootstrapDiffCI`, explicit analysis-only RNG, never an experiment's own seed |
| Six experiment suites | `cmd/experiment-006a` … `006f` | Statistical validation, routing variability, cache/coalescing consistency, queueing attribution, tail latency decomposition, real-vs-virtual replication |
| Experiment documentation | `experiments/006-statistics-queueing/{hypotheses,README}.md` | H1–H6, methodology, and full results for all six experiments |
| Learning notes | `docs/learning/006-statistics-queueing.md` | Method selection, validation, per-domain findings, surprises, limitations, evidence-tiering, Stage 7 motivation |

**No existing domain package required modification.** `internal/statistics` is entirely new and self-contained; every 006 experiment consumes it as a library, exactly matching the stage's own statistics-vs-analysis separation (generic math in `internal/statistics`, FlashFlow-specific interpretation in the experiment binaries that call it).

One real discovery changed what this stage actually needed to build: `transport.TransportConfig.MaxConnsPerHost` already existed and was already wired into the real `http.Transport`, but no experiment across four prior stages had ever set it to a value that created actual finite capacity. 006-D and 006-E's queueing work needed no new capability at all — only using a knob that was always there.

---

## Methodology

Each statistical method was chosen for a specific, pre-identified question, not implemented first and matched to a question afterward:

- **Percentiles/descriptive stats**: every other tool needs them, and FlashFlow's own latency data (skewed, heavy-tailed) makes means alone misleading — established directly in 006-A's outlier-contamination scenario (a mean shift of ~4500 units from 3 outliers in 100 points; median shift of 0.13).
- **Mann-Whitney U**: for "do these samples come from systematically different populations" — appropriate because FlashFlow's data doesn't satisfy a t-test's normality assumption, and explicitly *not* used for questions about distributional shape, a distinction 006-C's own methodology section demonstrates by getting it wrong first and correcting course.
- **Cliff's Delta**: because every comparison in this stage needed "is there evidence of a difference" and "how large is it" answered as two separate questions, never conflated.
- **Bootstrap CI**: for quantities with no closed-form standard error (medians, percentile differences, proportions), always with an explicit, analysis-dedicated RNG — verified never to alter or depend on any experiment's own randomness.

---

## Synthetic Validation

006-A ran five scenarios with a known right answer before any primitive touched real FlashFlow data: identical distributions (no evidence of difference, negligible effect size — confirmed), a 3-sigma shift (p<0.000001, large effect — confirmed), the same effect size at n=20 vs n=400 (similar effect size, 4.5× narrower CI at the larger n — confirmed, with the honest caveat that the effect-size *estimate* itself was also less noisy at the larger n, not just its interval), an outlier-contaminated sample (mean distorted by ~4500 units, median and Cliff's Delta essentially unaffected — confirmed), and a bootstrap CI for a distribution with a known true mean (interval contained it — confirmed). A same-seed determinism check on the bootstrap itself also passed. All 6 checks passed; `internal/statistics` also carries 42 of its own unit tests (hand-verified tie cases, structural symmetry properties, edge-case rejection).

---

## Experiments

| # | Title | Central Finding |
|---|---|---|
| 006-A | Statistical Validation | All 5 synthetic scenarios plus a determinism check behaved exactly as predicted — the methodology gate every later experiment depends on |
| 006-B | Routing Policy Variability (EWMA Lock-In) | Permutation alone produced zero variance in lock-in severity (a discovered null result, not a design flaw); adding realistic timing jitter revealed the actual mechanism — tie-break order controls the outcome only when targets are exactly equal, collapsing to near-chance (36% vs 100%) the instant real timing differences exist |
| 006-C | Cache/Coalescing Effect | Upstream-request reduction reconfirmed with zero variance across 15 real replicates; a first Mann-Whitney attempt on failure-shape gave an unstable p-value across reruns (0.003–0.41) because it was the wrong test — the all-or-nothing proportion, measured directly, was stable and clearly nonzero every time |
| 006-D | Queueing/Concurrency Attribution | A real, previously-unused capacity knob (`MaxConnsPerHost`) produced a textbook saturation curve; Little's Law held within 5.0% mean error across an 18-point load sweep |
| 006-E | Tail Latency Attribution | 83% of elevated p99, in a controlled scenario with an independently-known constant service time, is attributable to waiting rather than service time; the p99−p50 spread itself grew (CI excludes zero), confirming disproportionate tail stretching, not a uniform shift |
| 006-F | Real vs Virtual Statistical Comparison | 005-H's single-pair gap confirmed stable: real p99 is genuinely noisy but the real-vs-virtual gap's 95% CI [5.32, 9.48]ms never approaches zero across 15 real replicates |

Full data, methodology, and per-experiment interpretation: `experiments/006-statistics-queueing/README.md` (7 sections, 6 JSON result files).

---

## Statistical Findings

The strongest results in this stage are the ones where a null or unstable result was investigated rather than reported at face value: 006-B's zero-variance permutation cell revealed that lock-in severity isn't a random variable at all under that design, and 006-C's unstable Mann-Whitney p-value revealed that the test itself was answering the wrong question. Both led to a more specific, more mechanistically-grounded finding than the original hypothesis predicted. Every effect size reported in this stage is accompanied by a bootstrap CI or an explicit sample-size statement — no result rests on a p-value alone.

---

## Queueing Findings

`MaxConnsPerHost=5` against a fixed 20ms Origin delay produced a real, measurable finite-capacity system: linear throughput and flat latency below capacity, throughput plateauing at ~250 req/s (matching the analytical prediction almost exactly) above it, latency growing six-fold. Little's Law held within 5.0% mean error using one consistent client-side measurement boundary for `L`, `λ`, and `W`. Decomposing the resulting tail latency against Origin's known, unchanging service time attributed 83% of the elevated p99 to waiting. Both findings are deliberately scoped to the transport-layer connection limit exercised — not a claim about queueing anywhere else in FlashFlow, since Origin itself remains an unbounded infinite-server model, as documented since Stage 4.

---

## Real vs Virtual

006-F closed the question Stage 5's 005-H could only pose: the structural match (upstream request count) is deterministic on both sides, confirmed across 15 real replicates with zero variance. The latency gap is not — real p99 varies genuinely run to run — but the *gap itself*, characterized with a bootstrap CI, never approaches zero. The honest framing carried through both stages: this is not evidence the virtual engine is wrong, it's a quantified description of a model boundary that was always going to be there (no queueing representation in the virtual service-time model), now stated as a number with an interval instead of a single before/after pair.

---

## Surprises

1. **006-B's null result was the stage's most valuable finding**, not a failed experiment — it revealed that permuting labels among literally identical things doesn't create genuine randomness, then motivated the fix that uncovered the real mechanism.
2. **A statistical test can be unstable in a way that itself is informative.** Mann-Whitney's p-value swinging from 0.003 to 0.41 across identical reruns wasn't noise to average away; it was direct evidence the test didn't fit the question.
3. **`MaxConnsPerHost` had been available and unused since early in this project** — the queueing question Stage 5's exit artifact flagged as open needed no new engineering, only someone to set an existing configuration value.
4. **Sample size improved effect-size estimate stability, not just interval width** (006-A) — a distinction the stage's own instructions anticipated but that still required demonstrating rather than assuming.

---

## Limitations

- Every queueing/attribution claim is scoped to the specific real bottleneck exercised (`transport.MaxConnsPerHost`); Origin's own infinite-server model, documented since Stage 4, remains unaddressed and is not claimed to behave like a finite queue.
- `internal/netsim`'s loss simulation has no seeded-RNG injection point through `EdgeConfig` — a real, discovered gap left unfixed because no 006 conclusion depends on it, recorded as a concrete opportunity for whichever future work needs reproducible network-loss experiments.
- No multiple-comparison correction exists, because no experiment in this stage ran a comparison matrix large enough to need one — deliberately not added speculatively.
- Tail-percentile precision at small sample sizes remains a stated limitation of `internal/statistics` itself, not something bootstrap resampling can manufacture past.
- 006-E's waiting/service decomposition method requires an independently-known, controlled service-time constant; it would not transfer as-is to a system (a real production origin, for instance) where that constant isn't directly available.

---

## Evidence Discipline

**Strong** (deterministic, replicated, mechanism identified): the upstream-request structural match in both engines (006-C, 006-F); Little's Law within 5% error (006-D); the p99 waiting-time attribution grounded in a controlled constant (006-E). **Suggestive, precisely quantified**: the real-vs-virtual p99 gap and the coalescing failure-shape difference — both real, both bounded by a stated confidence interval, neither overclaiming precision beyond 15–30 replicates. **Unresolved, explicitly flagged**: whether 006-B's jitter-driven win-rate collapse generalizes beyond the specific ±2ms perturbation tested.

---

## Testing

```
gofmt -l .        clean
go build ./...    clean
go vet ./...      clean
go test ./...     ok  (all 11 packages)
```

186 tests pass across the whole repository at Stage 6's close (144 at Stage 5's close + 42 new, all in `internal/statistics`). `go test -race` remains **unavailable in this environment** (no `gcc`; `CGO_ENABLED=1` fails building `runtime/cgo`) — stated honestly, as in every prior stage.

---

## Stage 7 Readiness

**READY**

> Stage 6 turned "EWMA sometimes locks onto one target" into a characterized, mechanistic, quantified claim: tie-break order controls the outcome only when targets are exactly equal, and collapses toward chance-level winner selection the instant any real timing difference exists — evidence about *when and why* a routing signal fails, not just that it sometimes does. It also built and validated the machinery (bootstrap CIs, effect sizes, rank-sum tests) an adaptive router or a fair policy comparison would need underneath it to distinguish a real improvement from noise. Stage 7's adaptive routing and counterfactual evaluation remain unbuilt, correctly — but the statistical and mechanistic foundation they need to be trustworthy, rather than merely plausible, now exists and has itself been validated against known synthetic cases before being trusted.
