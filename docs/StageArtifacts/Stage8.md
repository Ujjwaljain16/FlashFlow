# Stage 8 — Auto-Tuning, Experiment Operations & Final Validation: Exit Artifact

## What Was Built

| Component | File(s) | Description |
|---|---|---|
| Tunable config space | `internal/tuning/space.go` | The 6 real tunable `AdaptiveConfig` parameters, searched as a 3D weight simplex (a confirmed, not assumed, redundancy elimination) plus 2 durations; sampling, validity checking, stable hashing |
| Scenario generator & split | `internal/tuning/scenario.go` | Bounded to what `internal/replay.Scenario` can execute; the sacred Development(40)/Holdout(20) seed-disjoint split |
| Objective function | `internal/tuning/objective.go` | `Metrics` → bounded `Scores` (Latency/Reject/Fairness) → both a documented `Utility` scalar and `ParetoFrontier`; corrected mid-stage from p99 to mean latency (see Tuning Results) |
| Random Search v1 | `internal/tuning/search.go` | Full search ledger (nothing discarded), evaluation-result cache, explicit optimizer/experiment/analysis RNG separation, convergence/plateau detection |
| Sensitivity & robustness | `internal/tuning/sensitivity.go`, `robustness.go` | Parameter perturbation analysis; per-scenario utility distribution (mean/median/worst/stddev), not just the pooled aggregate |
| 8 experiment suites | `cmd/experiment-008a` … `008h` | Machinery validation, Random Search, holdout/generalization, sensitivity, adversarial test, final policy evaluation, performance/load sweep, NGINX reference |
| Permanent challenge suite | `internal/challenge/` | Golden scenario with structural invariants; adversarial cases across routing, health, cache, network, replay, tuning (23 tests, gap-filling only — existing coverage was surveyed first) |
| Go benchmarks | `internal/vtime`, `internal/proxy`, `internal/replay`, `internal/tuning` `*_bench_test.go` | Event throughput, routing decision cost, replay-pipeline throughput, tuner evaluation rate |
| Open-loop load generator | `cmd/experiment-008g` | Real HTTP sweep against 006-D/E's exact bottleneck, absolute-time-scheduled dispatch (not a shared ticker) |
| NGINX reference benchmark | `cmd/experiment-008h`, `scripts/nginx-reference-benchmark.sh`, `deployments/nginx-bench/` | Docker-orchestrated, content-verified comparison |
| Final validation script | `scripts/final-validation.sh` | One command, 12-17 gates, a printed pass/fail matrix |
| Dashboard | `cmd/dashboard`, `internal/dashboard` | Go HTTP backend + embedded vanilla-JS frontend: Playground (live run/compare), experiment browser, tuning view |
| Experiment documentation | `experiments/008-tuning-validation/{hypotheses,README}.md` | H1–H8, methodology, and full results for all 8 experiments plus the two tooling deliverables |
| Learning notes | `docs/learning/008-tuning-final-validation.md` | Method selection, the objective-function discovery, per-domain findings, surprises, limitations, evidence-tiering |

---

## Optimization Methodology

**Algorithm**: Random Search v1 only. Latin Hypercube Sampling and Bayesian Optimization were not built — the search space (a 3D simplex plus 2 durations) showed no coverage limitation Random Search couldn't handle, and 200 evaluations converged well before exhausting budget, then plateaued. Building a more sophisticated algorithm without evidence it was needed would have violated the master context's own earn-the-abstraction instruction directly.

**Objective**: `Utility = 0.6·LatencyScore + 0.3·RejectScore + 0.1·FairnessScore`, an explicit evaluation preference (not a claim of optimality), each component a bounded [0,1] transform. `LatencyScore` was originally built from p99 latency and was corrected to mean latency mid-stage — see Tuning Results.

**Scenario split**: 40 Development scenarios (seeds 1-40), 20 Holdout scenarios (seeds 100,001-100,020) — disjoint by construction, generated once, never regenerated. The tuner used Development only; Holdout was touched exactly once, after a winner was already selected.

**Stopping criterion**: a fixed evaluation budget (200), with convergence tracked (best-so-far vs. evaluation index) rather than simply trusted — the run's own ledger shows it last improved at evaluation #24 and was flat for the remaining 176.

---

## Tuning Results

| | Development | Holdout |
|---|---:|---:|
| Baseline (hand-chosen default) utility | 0.6441 | 0.6607 |
| Winner (tuned) utility | 0.6594 | 0.6782 |
| Improvement | +0.0153 (2.37%) | +0.0175 |

**Generalization gap: −0.0022, 95% bootstrap CI [−0.0112, +0.0065] — includes zero.** The pooled point estimate says the improvement grew on Holdout rather than shrank (the stronger of the two possible outcomes rule 10 describes), but an adversarial audit caught that this headline number had never been given a confidence interval, unlike every other quantitative claim in this project since Stage 6. Adding one (a 95% bootstrap CI on the per-scenario paired dev-minus-holdout improvement, `internal/statistics.BootstrapDiffCI`, 5000 resamples) shows the gap's *sign* is not distinguishable from noise at this scenario-set size (Holdout per-scenario utility stddev ≈ 0.086–0.090, against a gap of −0.0022). The honest claim: the tuned configuration improves utility on both Development and Holdout — that holds — but "the improvement grew rather than shrank on Holdout" is not statistically supported as stated. Winning configuration: weights load=0.161, latency=0.568, cache=0.051, cost=0.220 (vs. the hand-chosen default's 0.4/0.4/0.1/0.1); `ReferenceLatency`=192ms, `StaleAfter`=3.74s. Sensitivity analysis (±10% weights, ±100ms durations, all 12 perturbations) moved utility by under 0.5% of baseline in every case on both Development and Holdout — a robust basin. Challenge-scenario performance (008-F): the tuned configuration won the extreme-capacity-ratio and failure-recovery challenges outright, matched the field on the identical-targets negative case.

Two further scope notes, added to `008C-holdout-validation.json` after the same audit: Development and Holdout are drawn from the *identical* `ScenarioSpace` distribution, differing only by seed range — this experiment tests generalization to unseen same-distribution samples, not to a distributionally different traffic shape (008-F's hand-crafted challenges are the distribution-shift check). And Holdout was technically scored twice across the stage's lifetime (once under the original p99-based objective, again under the corrected mean-latency objective) — recorded explicitly per master context rule 9, along with the fact that the correction was motivated entirely by Development-side evidence, never by inspecting Holdout first.

---

## Policy Comparison

Tuned Adaptive vs. Round Robin, Weighted Round Robin (given the single most favorable case it can be given), Least Connections, EWMA, and P2C, across Development, Holdout, and 3 challenge scenarios (008-F):

| Policy | Dev win rate | Dev non-inferiority | Holdout win rate | Holdout non-inferiority |
|---|---:|---:|---:|---:|
| Round Robin | 35.0% | 42.5% | 10.0% | 30.0% |
| Weighted Round Robin | 2.5% | 57.5% | 5.0% | 35.0% |
| Least Connections | 0.0% | 52.5% | 5.0% | 40.0% |
| EWMA | 0.0% | 20.0% | 5.0% | 25.0% |
| P2C-load | 0.0% | 55.0% | 5.0% | 35.0% |
| **Adaptive (tuned)** | **62.5%** | **80.0%** | **70.0%** | **85.0%** |

Not universal dominance, and reported as such: Round Robin genuinely wins over a third of Development scenarios, a legitimate outcome the master context explicitly requires this project to allow (rule 43) rather than suppress. Adaptive's advantage traces to a clear latency-score lead (0.5607 vs. next-best 0.5310); its tuned fairness score (0.2296) is honestly the lowest of all six, lower even than EWMA's established lock-in problem — a real, stated tradeoff, not a free improvement.

---

## Robustness

Per-scenario utility distributions (008-C), not just pooled means: the winning configuration's worst-case Holdout scenario matched the baseline's exactly (no regression at the tail); its standard deviation (0.0899) is close to, not tighter than, the baseline's own spread (0.0863) — the earlier claim of a "tighter" stddev did not survive a second look and is corrected here. Sensitivity analysis (008-D) found no perturbation direction that came close to a 10%-of-baseline fragility threshold — the search landed in a genuinely stable region, not an isolated lucky point.

---

## Dashboard

`cmd/dashboard` (backed by `internal/dashboard`) — a Go HTTP server serving an embedded, dependency-free frontend (plain JS/CSS, no build step, no framework; this project has no Node toolchain and one wasn't earned for a single internal tool). Three views, all verified working against real data in an actual browser session:

- **Playground**: run any of the 6 policies against one canonical scenario (3 heterogeneous edges, a real mid-run health failure and recovery, hot/cold key rotation) live, via the exact `internal/replay.RunWorld` every experiment since Stage 7 uses — a topology visualization sized by real traffic share, a filterable event timeline over the real trace, and derived latency metrics. A counterfactual comparison runs two policies against the byte-for-byte identical scenario and surfaces the first point of divergence (rule 31) via the same `FirstDivergence` every counterfactual experiment already uses.
- **Experiment browser**: a three-level drill-down (stage → result file → content) reading `experiments/*/results/*.json` directly, with path-traversal-safe handlers (unit-tested) — never a second, dashboard-owned data store (rule 34).
- **Tuning view**: the search ledger's best-so-far curve and Development-vs-Holdout utility, Holdout kept visually distinct by color, never blended into one number (rule 32).

The CLI remains fully first-class (rule 35): every `cmd/experiment-*` binary runs identically whether or not the dashboard is running, and the dashboard reads the same artifacts those binaries already produce.

---

## Validation

`scripts/final-validation.sh` runs, in order: gofmt, go vet, `go test ./...` (all 15 packages), deterministic replay identity/divergence/isolation, statistical validation (006-A, 007-A, 007-F, 008-A, 008-E — each with a real, internal correctness assertion, not just "did it run"), the golden scenario + full challenge suite, the core Go benchmark suite, and (in full mode) informational reruns of 008-B/C/D/F/G including the real-engine load sweep. Prints a final pass/fail matrix; both quick mode (~15s) and full mode (~2 minutes) were run and passed completely during this stage.

---

## External Comparison

A minimal NGINX reference benchmark (008-H, rule 55): FlashFlow's real `cmd/proxy` vs. NGINX, both fronting the byte-for-byte identical `cmd/http-origin` backend, at light load (200 requests, concurrency 10, plus a 20-request discarded warm-up). Result: FlashFlow proxy 21.1ms mean / NGINX 25.4ms mean, both close to Origin's own 20ms baseline. Explicitly framed, per the master context's own required language, as a reference point at light load through a single backend — not a claim that FlashFlow replaces NGINX or matches its production maturity. The benchmark's own first run caught and fixed a real bug (a Windows/Git-Bash path-mangling issue that silently broke the Docker mount, making NGINX serve its own default page instead of proxying); a later adversarial audit caught a second issue — `cmd/proxy` was left running with its default debug-header injection, meaning response bytes were not actually identical between the two systems as claimed — fixed with `-debug-headers=false`, plus an added warm-up phase and matched connection-pool sizing. All three issues are documented in full in the learning notes.

---

## AI Capabilities

None implemented. The optional AI-agentic layer (research assistant, skeptic loop, counterexample search) described in the master context was deliberately not built this stage: the deterministic core (tuner, holdout validation, dashboard, benchmarks) was the priority, and this entire multi-stage project has already been conducted through an interactive AI-assisted research process — adding a second, in-repo AI layer on top would not have demonstrated a new capability distinct from what building FlashFlow itself already required.

---

## Limitations

- All findings use `DefaultObjectiveWeights` (Latency 0.6, Reject 0.3, Fairness 0.1) — an explicit, stated preference, not a claim of optimality; a different weighting could favor a different winner.
- Holdout is a same-distribution sample (identical `ScenarioSpace`, disjoint seed range only) — it establishes the tuned configuration isn't overfit to its specific training scenarios, not that it holds up under a qualitatively different traffic shape. Distribution-shift robustness is what the hand-crafted challenge suite (008-F) checks instead.
- Only Random Search was built; LHS and Bayesian Optimization remain correctly unbuilt pending evidence they'd help.
- The challenge suite's coverage is deliberately not exhaustive against every category example in the master context (a true mid-scenario service-time reversal remains unimplementable without extending the replay engine's Scenario model, which no finding required).
- The NGINX comparison is one light-load run with no statistical replication — appropriate for a reference point, not this project's own standard of evidence.
- Load-sweep CPU/memory metrics are heap allocation and peak goroutine count, not a true CPU-utilization profile (no portable, dependency-free way to read that in Go).
- The dashboard has no authentication or multi-user concerns — appropriate for a local development tool, not a claim of production readiness.

---

## Testing

```
gofmt -l .        clean
go build ./...    clean
go vet ./...      clean
go test ./...     ok  (all 15 packages)
scripts/final-validation.sh          PASSED (quick mode, ~15s)
scripts/final-validation.sh (full)   PASSED (~2 minutes, all 17 gates)
```

15 packages now carry tests (12 at Stage 7's close + `internal/tuning`, `internal/challenge`, `internal/dashboard`). `go test -race` remains **unavailable in this environment** (no `gcc`; `CGO_ENABLED=1` fails building `runtime/cgo`) — stated honestly, as in every prior stage.

---

## Final Findings

Stage 8 answered its own central question directly: yes, FlashFlow can automatically find a useful adaptive-routing configuration that improves utility on both Development and Holdout, and a robust sensitivity profile and an adversarial methodology check (a Development-set tie that broke catastrophically on Holdout) both hold up under scrutiny. One claim does not: an adversarial audit found the generalization gap's headline point estimate (−0.0022, "improvement grew on Holdout") had never been given a confidence interval, and once bootstrapped, that interval includes zero — the gap's sign is not distinguishable from noise at this scenario-set size. The honest claim is narrower than what was originally written: the tuned configuration generalizes (no evidence of overfitting), not that it demonstrably generalizes *better* than it trained. The stage's most valuable discovery was not about the router at all: the original tuning objective measured the wrong thing (p99 latency, uninformative at this sample size) and was corrected mid-stage, exactly the caliber of methodological self-correction this project has practiced since 006-C. Every load-generation or evaluation tool built this stage — the tuner's own search, the open-loop sweep, the NGINX benchmark script, and (per the same audit) the generalization-gap analysis itself — needed at least one real correction before its output could be trusted, a pattern worth stating plainly rather than smoothing over: a measurement tool is part of the system under test, and a plausible-looking successful number is not the same as a correct one.
