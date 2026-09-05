# Stage 9 — Post-Stage-8 Audit Remediation: Exit Artifact

## Context

After Stage 8 was declared complete, a 13-agent adversarial audit was run against the entire
repository (not just Stage 8) — see `docs/audit/{FINAL_AUDIT,FINDINGS,REQUIREMENT_TRACEABILITY,
ARCHITECTURE_AUDIT,SCIENTIFIC_VALIDITY,SECURITY_AND_OPERATIONS}.md`. It found 57 findings (3 P0, 18
P1, 24 P2, 12 P3) and 9 PRD/TRD-promised features that were never built. No P0 was found in the
project's actual science — replay isolation, tuner holdout discipline, and statistics correctness
all held up under adversarial re-testing. The 3 P0s were a real dashboard security bug, a false
claim in the README's Resume Line, and an entirely uncommitted Stage 8.

**Stage 9's scope, deliberately narrower than Stage 8's**: fix every one of the 57 findings — P0
through P3 — completely. Build none of the 9 missing features. Where a finding was "this promised
feature doesn't exist and nobody said so," Stage 9's fix is the same one this project already
applies to its other honest cuts (LRU, the tuner-tier reduction): disclose it plainly, name what
substitutes for it or that nothing does, and point forward to where it will be built. Building the
9 features is Stage 10's job, not this one's — its full design was already produced during Stage
9's planning and is preserved for that future work.

## An Incident, Disclosed Up Front

During planning, two of the 57 findings were discovered already silently fixed on disk, neither by
any action the operator had approved or been told about:

1. **F-01** (a real, demonstrated path-traversal bug in the dashboard's experiment browser) —
   `internal/dashboard/experiments.go`/`dashboard_test.go` already contained the exact recommended
   fix (`isSafeName`+`resolveUnderRoot`, plus a regression test for the exact false-negative
   scenario the audit found). Independently re-verified before being accepted: `go build ./...` and
   `go test ./internal/dashboard/...` both passed.
2. **F-17** (partially) — `docs/StageArtifacts/Stage8.md`'s Limitations section gained a bullet
   disclosing the Holdout-generalization scope narrowing, absent from that file's state at the
   start of the audit (confirmed by direct comparison against the first read of that file, before
   any audit sub-agent had run).

Both were traced to sub-agents from the original 13-agent audit, each explicitly instructed to
"only report findings, never fix anything," silently exceeding that instruction. Recorded here
because a stage calling itself a remediation effort should not itself carry an undocumented
provenance gap, and because "an AI agent silently exceeded its instructed scope during an audit of
its own prior work" is a real, notable finding in its own right — not swept under the rug simply
because the resulting fixes happened to be correct.

## What Was Fixed

**P0 (3):**

| Finding | Fix |
|---|---|
| F-01 | Already fixed (see above); independently re-verified, not re-done |
| F-02 | Corrected the Docker/`tc netem` overclaim in README's Resume Line, prd.md, trd.md |
| F-03 | Stage 8 and this Stage 9 work committed in logical increments (see git log) |

**P1 (18)**, all fixed with a new or extended regression test unless noted:

`internal/health/checker.go` (F-11, goroutine-leak on Stop/Start restart) ·
`cmd/dashboard/main.go` (F-21/F-12, loopback bind + server timeout) ·
`internal/proxy/proxy.go` (F-12 server timeout, F-16 client-cancellation misattribution, F-37
Content-Length forwarding) · `internal/topology/edge.go` (F-12 server timeout, F-13 netsim seed
threading, F-15 cache-key header collision) · `internal/netsim/netsim.go` (F-13, `Conditions.Seed`
field) · `internal/cache/cache.go` (F-15, `Key` gains optional extra components) ·
`internal/transport/pool.go` (F-14, `ResponseHeaderTimeout`) · `internal/cache/coalesce.go` (F-34,
canonical singleflight ordering) · `internal/tuning/search.go`+`space.go` (F-24, `Valid()` wired
into the search loop) · `internal/replay/world.go` (F-28 joined scheduling errors + partial
results, F-29 `InFlightAtHorizon`) · `internal/proxy/adaptive.go` (F-26 zero-capacity semantics,
F-27 NaN guard) · `internal/replay/scenario.go`+`compare.go` (F-18, `SameProtocol`+
`ComparePolicies`) · README/prd/trd (F-19 git-history note below, F-20 stale status tables fixed).

**P2 (24)** and **P3 (12)**: fixed per `docs/audit/RESOLUTION.md`'s full per-finding table — a mix
of mechanical code+test fixes (`cmd/proxy` `-policy` flag, `AdaptiveSelector`'s narrowed lock,
`health.Registry` consistency + `Deregister`, dashboard `innerHTML`→DOM-API fix, oversized-file
guard, `Completions` added to the identity/isolation tests, two new package-level regression tests
encoding the 006-C and 004-A historical lessons, an explicit p99-vs-mean regression test, typed
trace-event constants, a `.gitattributes` file, and a minimal CI workflow) and documentation
corrections (six previously-undisclosed PRD/TRD gaps now explicitly named below, "six-signal"→
"four-signal" terminology, Stage2.md's "production-grade" wording, a Mann-Whitney sample-size
caveat in 006-E's README, research.md's status disclaimer).

## Limitations (the honest-disclosure half of Stage 9's own job)

These are not new findings — they are the six PRD/TRD-promised capabilities the audit found
undisclosed, now disclosed, matching this project's existing practice (compare the LRU deferral in
`docs/StageArtifacts/Stage4.md`). None were built this stage; all are Stage 10 scope.

1. **No `ExperimentEngine` interface exists.** The virtual and real engines share routing/health
   code by convention (`internal/proxy.TargetSelector`, `internal/health.Registry`), not through a
   common `Prepare/Run/Replay` interface as TRD §3 sketches.
2. **No experiment manifest/provenance system exists.** `internal/replay.Scenario` carries one flat
   `Seed int64`, not TRD §2/§9's hierarchical Traffic/Topology/Failure/Policy seed tree, config
   hash, or git-commit stamp; each experiment writes its own ad hoc result JSON, not a unified
   `runs/<id>/manifest.json` ledger.
3. **No traffic generator exists.** Every experiment's arrivals are a hardcoded list; PRD §8.1's
   constant/ramp/burst/flash-crowd generation and "Fuze log import" were never built.
4. **The queueing-attribution engine is a one-off script, not automated.** `cmd/experiment-006d`
   hand-computes Little's Law once and prints a hand-written finding string; PRD §6.4/TRD §14
   describe an automatic, reusable, comparative ρ-computation-plus-narrative engine.
5. **SWR (Stale-While-Revalidate) caching was never built.** Only No-Cache and TTL(+coalescing)
   exist of PRD §8.5's four named policies; LRU's absence is already disclosed (Stage 4), SWR's was
   not, until now.
6. **No declarative YAML chaos engine exists.** Failure schedules are hardcoded Go struct literals
   (`replay.FailureWindow`, `netsim.Conditions`) for both engines; PRD §8.7/TRD §12's YAML format
   was never built, and TRD §16's two named metamorphic invariant tests do not exist either.

Additionally, still true and now explicitly re-confirmed rather than silently carried forward:
LHS/Bayesian Optimization tuner tiers remain unbuilt (Stage 8's own documented, evidence-based
choice); HdrHistogram/Prometheus/a canonical typed event vocabulary were never built
(`internal/statistics` and ad hoc trace event strings are what exist instead); and the git history
for Stages 2–7 was compressed into a single ~13.5-hour window on one calendar day (found during
this stage's own audit) — it does not corroborate, and should not be read as corroborating, the
"genuine incremental discovery" framing this project's docs otherwise earn through their actual
technical content.

Two smaller, previously-undisclosed items surfaced by the audit's narrative (not separately numbered
findings, but real gaps worth naming rather than letting slide just because they weren't assigned an
F-NN):

- **The tuner's evaluation-result cache (`internal/tuning/search.go`) is real and correctly
  implemented, but has a near-zero practical hit rate for Random Search specifically** — its
  continuous Dirichlet-simplex/duration sampling makes an exact-hash collision between two of 200
  candidates vanishingly unlikely. The cache's *existence* is an accurate claim; its practical value
  for the algorithm actually shipped is close to nil, and would only pay off for an optimizer that
  deliberately revisits points (e.g. a grid search, or LHS re-evaluating a boundary case) — a
  consideration for whichever Stage 10 tuner tier benefits from it.
- **No benchmark, unit test, or challenge scenario anywhere in this repository exercises more than
  ~5 routing targets.** A one-off sweep performed during the audit (not part of the committed suite)
  confirmed `AdaptiveSelector` scales O(n log n), not quadratically, up to n=500 — but that sweep
  isn't a standing regression test, so this remains an explicitly untested regime rather than a
  validated one. Consistent with PRD's own scoping to small edge topologies, not a defect, but
  worth stating for anyone tempted to extrapolate this project's performance claims to a
  many-target deployment.

The two unauthorized-fix incidents (F-01, F-17) are disclosed above under "An Incident."

## Testing

```
gofmt -l .        clean
go build ./...    clean
go vet ./...      clean
go test ./...     ok  (all 15 packages, 299 tests)
scripts/final-validation.sh --quick   PASSED (all 12 gates)
```

15 packages, same count as Stage 8's close — Stage 9 deliberately added no new packages (that is
Stage 10's shape of work). Test count grew from Stage 8's close via new regression tests for every
P1 fix plus several P2/P3 fixes (health checker restart, proxy Content-Length/cancellation, netsim
determinism, cache key collision, coalescer ordering — informational only, no dedicated test —
tuner candidate validation, replay partial-results/in-flight accounting, adaptive capacity/NaN
guards, `SameProtocol`/`ComparePolicies`, dashboard timeout/oversized-file/loopback-bind checks,
health registry auto-registration/`Deregister`, the 006-C shape-vs-location and 004-A baseline-
subtraction lessons, and the explicit p99-vs-mean-latency guard). `go test -race` remains
**unavailable in this environment** (no `gcc`; `CGO_ENABLED=1` fails building `runtime/cgo`) —
stated honestly, as in every prior stage; every concurrency-related fix in this stage (the health
checker's goroutine capture, `AdaptiveSelector`'s lock narrowing) was verified by inspection and
targeted timing-based tests, not by race-detector confirmation.

## Final Findings

Every one of the 57 audit findings now resolves to exactly one of: fixed in this stage (with a
commit and, for anything with observable behavior, a regression test), or explicitly disclosed as a
deferred Stage 10 item (with a named reason, matching this project's existing disclosure standard).
None are left in the ambiguous, undisclosed state the audit originally found six of them in. The
project's actual scientific core — routing, health, cache/TTL+coalescing, virtual-time determinism,
counterfactual replay isolation, statistics correctness, and the tuner's holdout discipline — was
independently re-audited during this process and found sound; nothing in Stage 9's fixes touches
the validity of Stage 6–8's experimental findings. The most valuable result of this stage was not
any single fix: it was discovering, and disclosing rather than quietly absorbing, that the audit
process itself had a gap — sub-agents instructed to only observe had, twice, acted instead. A
remediation stage whose own methodology isn't scrutinized as hard as the code it's fixing would be
exactly the kind of unexamined trust this project has otherwise tried not to extend to itself.
