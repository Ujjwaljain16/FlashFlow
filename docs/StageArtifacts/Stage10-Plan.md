# Stage 10 Plan — Building the 9 Missing PRD/TRD Features (not yet executed)

## Status

**Not started.** This document is a design, not an exit artifact — there is no "What Was Built"
section here because nothing in this stage has been built yet. It exists so the design work already
done isn't lost or re-derived from scratch whenever Stage 10 actually begins.

## Context

The post-Stage-8 adversarial audit (`docs/audit/`) found 9 PRD/TRD-promised features that were
never built, most with no disclosure trail. Stage 9 (`docs/StageArtifacts/Stage9.md`) deliberately
scoped these out — its job was to fix every audit finding and turn each of these 9 gaps into an
*honestly disclosed* limitation (see Stage9.md's Limitations section and
`docs/audit/RESOLUTION.md`), not to build them. Building them is this stage's entire scope.

The 9 items: a traffic generator, a generalized queueing-theoretic attribution engine, an
experiment manifest/provenance system with hierarchical seeds, two metamorphic invariant tests, an
SWR (Stale-While-Revalidate) cache policy, HdrHistogram + Prometheus telemetry, a declarative YAML
chaos engine, a formal `ExperimentEngine` interface unifying the virtual and real engines, and
LHS + Bayesian Optimization tuner tiers.

## Confirmed design decisions (locked in, do not re-litigate)

1. **Hand-roll the two hardest pieces** (the HdrHistogram-style histogram in §10.6, the
   Bayesian-optimization GP/Cholesky math in §10.9) rather than add external dependencies —
   preserves FlashFlow's zero-dependency `go.mod`. This was a deliberate choice, weighed against
   using `hdrhistogram-go`/`gonum`, in favor of matching this project's existing pattern of
   hand-rolling statistical/mathematical code (percentiles, Mann-Whitney, Cliff's Delta) rather than
   importing it.
2. **"Fuze log import" (§10.1) = NCSA/Apache combined access-log format.** This PRD term was never
   defined anywhere in the repository; this is the concretization settled on, chosen for being the
   most standard, recognizable access-log shape.
3. **Provenance seeds (§10.3): widen `Scenario` to a real `SeedTree{Global, Traffic, Topology,
   Failure, Policy}`** — genuine independent-axis control (e.g., sweep Failure timing while holding
   Traffic fixed) — not a derive-only convenience that would leave `Scenario` carrying one flat
   seed. This is the more invasive of two options that were weighed; a `DeriveSeeds(root int64)
   SeedTree` helper keeps every existing single-seed call site to a one-line change.

## Build order

**Track A (sequential)**: §10.1 → §10.2 → §10.3 → §10.4 → §10.5 → §10.6 → §10.7 → §10.8.
**Track B (parallel, any time)**: §10.9, with its own internal order (Tuner refactor + LHS, then
Bayesian Optimization).

Rationale: §10.1/§10.2/§10.3/§10.6 are self-contained and directly reusable by later items —
building them first maximizes how much easier later items get. §10.4 sits right after its two
dependencies (§10.1, §10.2) rather than at the end, since it's cheap once they exist. §10.7 and
§10.8 are sequenced last on Track A because they compose smaller pieces rather than inventing from
a blank page. §10.9 is fully isolated by file/package and risk profile, so it runs as an independent
stream.

**Run the full `go test ./...` suite immediately after §10.3** — everything downstream depends on
the widened `Scenario` shape being correct, and it is the highest-churn item in the whole plan.

---

## 10.1 Traffic Generator — `internal/traffic/`

New package: `generator.go`, `fuzelog.go` + tests. Pure functions producing `[]replay.Arrival` —
zero changes to `Scenario`/`RunWorld`.

```go
type Pattern string // Constant, RampUp, RampDown, Burst, FlashCrowd
type Params struct { Requests int; Horizon time.Duration; BaseRate, PeakRate float64
    BurstAt, BurstWidth time.Duration; JitterFraction float64; KeyFunc func(i int) string }
func Generate(pattern Pattern, p Params, seed int64) ([]replay.Arrival, error)
func HotColdKeys(hotWeight float64) func(i int) string
func ScheduleReal(arrivals []replay.Arrival, start time.Time, dispatch func(key string))
```
Rate curves via closed-form inversion of each pattern's cumulative-rate integral (deterministic by
construction). `ScheduleReal` reuses the absolute-time-dispatch pattern already confirmed sound in
`cmd/experiment-008g` (open-loop, no coordinated omission).

Fuze log import (confirmed: combined/NCSA access-log format):
```go
type FuzeLogEntry struct { Timestamp time.Time; Method, Path, Query string }
func ImportCombinedLog(r io.Reader) ([]FuzeLogEntry, error)
func ArrivalsFromLog(entries []FuzeLogEntry, compress float64) []replay.Arrival
```
Hand-parsed with one regexp per line — no dependency.

**Tests**: per-pattern table-driven (exact count, monotonic timestamps, hand-computed reference
grid for `Constant`, bucketed-density check for ramps), determinism, `ImportCombinedLog` against an
embedded fixture string.

## 10.2 Automated queueing-theoretic attribution engine — `internal/attribution/`

New package (not `internal/statistics` — that package's doc comment insists on zero domain
knowledge): `littleslaw.go`, `utilization.go`, `finding.go` + tests.

```go
type Sample struct { L, Lambda, W float64 }
func CheckLittlesLaw(s Sample) (ErrorMetrics, error)                    // generalizes 006-D's exact check
func Utilization(lambda float64, serviceTime time.Duration) float64
func UtilizationFromWorld(result replay.WorldResult, targets []replay.TargetProfile,
    horizon time.Duration) (map[string]float64, error)                 // feeds 10.4
type Finding struct { Target string; Rho float64; ComparedRho *float64; ComparedName, Text string }
func Explain(target string, rho float64) Finding       // fixed severity-band template
func Compare(nameA string, rhoA float64, nameB string, rhoB float64) Finding
```

Refactor `cmd/experiment-006d/main.go` onto `CheckLittlesLaw`/`Explain` instead of its current
inline math + hand-written string — the concrete fix for F-08, and incidentally gives 006-D a
package-level regression test it doesn't have today.

**Honesty to preserve in docs**: `UtilizationFromWorld`'s ρ comes from exogenous `ServiceTime` and
observed completion counts — `RunWorld` has no queueing/contention model, so this measures
offered-load-vs-capacity in the idealized sense useful for §10.4's monotonicity check, not a claim
that `RunWorld` models queueing delay.

**Tests**: hand-computed round numbers (λ=8, μ=10 → ρ=0.8); `UtilizationFromWorld` against a small
hand-built `WorldResult` fixture; `Explain`/`Compare` severity-band boundary tests at 0.7/0.9/1.0.

## 10.3 Provenance / manifest / hierarchical seeds — `internal/provenance/` + `Scenario` widening

Highest-churn item because of the confirmed "widen, don't derive" choice. Sequenced third within
Stage 10 (after §10.1/§10.2 land on the current `Scenario` shape) so later items build against the
new shape once, cleanly.

**`internal/replay/scenario.go`** — `Scenario.Seed int64` → `Scenario.Seeds SeedTree`:
```go
type SeedTree struct { Global, Traffic, Topology, Failure, Policy int64 }
func DeriveSeeds(global int64) SeedTree // sha256("label:root")-derived -- the compatibility path
```
Every existing literal `Seed: 42` becomes `Seeds: replay.DeriveSeeds(42)` — mechanical, not
semantic, for every site that doesn't need independent control.

**Cascading changes**:
- `internal/replay/policies.go`: `PolicySpec.New(clk, seeds replay.SeedTree, targets []TargetProfile)`
  — P2C reads `seeds.Policy` (was the flat `seed`).
- `internal/replay/world.go`: `RunWorld`'s call to `spec.New(e.Clock(), scenario.Seeds, ...)`.
- `internal/tuning/scenario.go`: `ScenarioSpace.Generate(seeds SeedTree) replay.Scenario` uses
  `seeds.Topology` for target count/names/service-times, `seeds.Traffic` for arrivals/jitter,
  `seeds.Failure` for failure-window presence/timing/target, threads `seeds.Policy` through.
  `GenerateFromRoot(global int64) replay.Scenario` = `Generate(DeriveSeeds(global))` — keeps
  `GenerateSet`/`NewSplit`/`ScenarioSetHash`/the Development-Holdout seed ranges byte-identical to
  today, since the derivation is deterministic and this is purely an internal restructuring.
- `internal/challenge/golden.go` and every `_test.go` in `internal/replay`/`internal/tuning`/
  `internal/challenge` building `Scenario{..., Seed: N}` directly: mechanical `Seed: N` →
  `Seeds: replay.DeriveSeeds(N)` (grep `Seed:` across these packages' test files first to get the
  exact list — expect roughly a dozen sites).

**`internal/provenance/`**: `seed.go`, `manifest.go`, `hash.go`:
```go
type Manifest struct { ExperimentID, Name string; Seeds replay.SeedTree
    ConfigurationHash, GitCommit string; GitDirty bool
    TunerVersion string `json:"tuner_version,omitempty"`; CreatedAt time.Time }
func ConfigHash(v any) (string, error)       // json.Marshal -> sha256 -> 16 hex chars
func GitCommit() (commit string, dirty bool) // runtime/debug.ReadBuildInfo() -- no os/exec
func (m Manifest) Write(runsRoot string) error // runs/<experiment-id>/manifest.json
```

**Scope cut, state explicitly**: TRD §9 also lists `config.yaml`/`events.jsonl`/`metrics.csv`/
`summary.json`/`statistics.json`/`replay.json` under `runs/<id>/`. This builds exactly what F-05's
recommended fix asked for (seed + git commit + config hash, via a real `SeedTree`); pointing
existing `vtime.Trace.WriteJSONLFile`/`tuning.SearchResult` output at `runs/<id>/` is a natural
follow-up, not required to close F-05.

**Tests**: `DeriveSeeds` determinism + pairwise-distinctness; `GenerateFromRoot(N)` vs
`Generate(DeriveSeeds(N))` equivalence (proves behavior-preserving refactor); independent-axis-
control demonstration (same Traffic seed, different Failure seed → identical arrivals, different
failure window); `ConfigHash` stability; `GitCommit` soft-checked; `Manifest.Write` round-trip.

## 10.4 Metamorphic invariant tests — `internal/challenge/metamorphic_test.go`

Depends on §10.1 and §10.2.

**(a) Doubled service time → latency must not decrease.** Baseline + `doubled` copy (every
`TargetProfile.ServiceTime *= 2`, `Arrivals` fixed). Compute `Horizon` generously from the doubled
max service time, identical for both runs (otherwise Stage 9's `InFlightAtHorizon` accounting fix
would corrupt the comparison for an unrelated reason). Primary assertion under Round Robin; a
secondary, explicitly-weaker assertion under `AdaptivePolicy()`, written up as a finding either way.

**(b) Halved arrival rate → utilization must not increase.** Halve the arrival **count** within the
same fixed Horizon (not "same count, double Horizon" — that just relabels the measurement window).
`traffic.Generate(Constant, ...)` for baseline (300) and halved (150) arrivals over identical
Horizon/Targets; assert `attribution.UtilizationFromWorld(halved) <= UtilizationFromWorld(baseline)`
per target.

## 10.5 SWR cache policy — `internal/cache/cache.go` (additive)

```go
type Config struct { TTL time.Duration; StaleWindow time.Duration } // StaleWindow==0 == today
func NewWithConfig(clk clock.Clock, cfg Config, coalescer *Coalescer) *Cache
type GetResult int // Miss, Fresh, Stale
func (c *Cache) GetSWR(key string, revalidate func() (Entry, error)) (*Entry, GetResult)
```
Stale hit: serve immediately, fire one background revalidation via the existing `Coalescer.Do`.
`topology.EdgeConfig` gains `StaleWindow time.Duration` (zero-means-off). Disclosed limitation:
real-engine-only, since `replay.Scenario` has no cache/TTL model.

## 10.6 HdrHistogram + Prometheus telemetry — `internal/telemetry/`

Hand-rolled per the confirmed decision.
```go
type Histogram struct { /* fixed range covering ~1µs-10s */ }
func (h *Histogram) Record(latencyNs int64); func (h *Histogram) ValueAtPercentile(p float64) int64
type Metrics struct { RequestsTotal map[string]uint64; LatencySeconds map[string]float64
    CacheHits, CacheMisses, CacheFills, CoalesceLeads, CoalesceShared uint64
    HealthState map[string]string; Histogram *Histogram }
func SnapshotFromProxy(p *proxy.ReverseProxy) Metrics   // pure aggregation of existing Snapshot() calls
func SnapshotFromEdge(e *topology.EdgeServer) Metrics
func WriteText(w io.Writer, m Metrics) error            // hand-rolled Prometheus text-exposition format
```
`/metrics` route on `ReverseProxy.Handler()`'s existing mux; `-metrics` flag on `cmd/proxy`.
Explicitly NOT a replacement for `internal/statistics.Percentile`.

## 10.7 Declarative YAML chaos engine — `internal/chaos/`

Hand-rolled parser (flat 4-key schema, no dependency earned). One companion addition:
`topology.EdgeServer.SetDown(bool)`.
```go
type Action string // crash, recover, latency
type Event struct { At time.Duration; Target string; Action Action; Delay time.Duration }
type Schedule []Event
func ParseYAML(r io.Reader) (Schedule, error)
func (s Schedule) ToFailureWindows() ([]replay.FailureWindow, error) // errors, never silently drops
func (s Schedule) ToRealSchedule(edges map[string]*topology.EdgeServer) []ScheduledAction
func RunReal(actions []ScheduledAction, start time.Time)
```
crash/recover → `SetDown`; latency → `SetArtificialDelay` (already exists) — no `netsim.Conditions`
mutation needed. Disclosed asymmetry: virtual engine can't express mid-run latency events.

## 10.8 `ExperimentEngine` interface — `internal/engine/`

Built last — composes §10.1/§10.3/§10.7's building blocks.
```go
type Experiment struct { ID, Name string; Scenario replay.Scenario; Policy replay.PolicySpec
    Real *RealExperimentConfig }
type RunResult struct { Engine string; WorldResult *replay.WorldResult; Real *RealMetrics }
type ExperimentEngine interface {
    Prepare(exp Experiment) error; Run(exp Experiment) (RunResult, error)
    Replay(exp Experiment, policy replay.PolicySpec) (RunResult, error)
}
```
`VirtualEngine.Run`/`Replay` = `replay.RunWorld` calls, giving already-correct counterfactual
replay a named front door. `RealEngine` starts Origin/Edges/Proxy, drives `traffic`-generated load.
`Prepare`/`Replay` should also call Stage 9's `Scenario.SameProtocol` check (F-18) now that a single
gate exists. Disclosed asymmetry: `RunResult.WorldResult`/`.Real` are mutually exclusive.

## 10.9 LHS + Bayesian Optimization tuner tiers (independent track, any time)

`internal/tuning/search.go` (refactor, non-breaking), new `lhs.go`, `bayesopt.go`, `linalg.go`.
```go
type TrialResult struct { Config proxy.AdaptiveConfig; Utility float64; Valid bool }
type Tuner interface { Suggest(previous []TrialResult) proxy.AdaptiveConfig; Name() string }
func RunSearch(tuner Tuner, evaluations int, scenarios []replay.Scenario,
    weights ObjectiveWeights) SearchResult   // identical shape -- zero changes needed downstream
```
`RunRandomSearch` becomes a thin wrapper over `RunSearch(NewRandomSearchTuner(...))`.

**LHS**: stratified, per-dimension-permuted sampling over the existing `ConfigSpace`.
**Bayesian Optimization**: hand-rolled GP (squared-exponential kernel, fixed length-scale) + EI
over a random candidate pool. Feasible in-house: 5 continuous dims, ~200 evaluations.

**Framing to lead with**: Stage 8 already showed Random Search converges at eval #24/200 and
plateaus for the rest — there's no evidence this space needs a better optimizer. Built to honor
PRD §6.2's promise, not because evidence demands it; report honestly if BO doesn't beat Random
Search.

**Build order within this item**: `Tuner`+`RandomSearchTuner`+LHS first (low risk), Bayesian
Optimization last (highest implementation risk in the whole Stage 10 plan — first linear algebra
this codebase has done).

---

## Critical files for Stage 10 implementation

- `internal/replay/scenario.go` and `world.go` — the exogenous/`RunWorld` contract every item
  (§10.3, §10.4, §10.7, §10.8) must plug into without breaking.
- `internal/proxy/adaptive.go` and `internal/tuning/space.go` — the `AdaptiveConfig`/`ConfigSpace`
  shape §10.9 and §10.2/§10.4 must respect exactly.
- `internal/cache/cache.go` and `coalesce.go` — what §10.5 extends and must reuse, not duplicate.
- `internal/topology/edge.go` — real-engine integration point for §10.8, §10.5, and §10.7.
- `internal/tuning/search.go` and `objective.go` — the pipeline §10.9's three `Tuner`
  implementations must share without duplicating.

## Verification plan (mirrors Stage 9's discipline)

After every numbered item: `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./...` must stay
clean across all packages (this project had 15 at Stage 9's close; expect ~20 once §10.1–§10.2,
§10.3's `internal/provenance`, §10.6's `internal/telemetry`, §10.7's `internal/chaos`, and §10.8's
`internal/engine` all exist). Re-run `scripts/final-validation.sh` after §10.4 (new determinism/
challenge checks) and again once all of Track A is done. Update `docs/audit/RESOLUTION.md` to
reflect each of F-04 through F-10 moving from "disclosed, deferred to Stage 10" to "built, see
commit X" as each lands — do not leave the resolution ledger stale once this stage starts closing
items.
