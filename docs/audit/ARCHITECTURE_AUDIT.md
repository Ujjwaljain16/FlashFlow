# Architecture, Abstraction, and Performance Audit

## Architectural separation — verified, not assumed

Each boundary below was checked by direct import-graph inspection (`grep` for cross-package
imports), not inferred from documentation:

- **`internal/proxy` (domain routing logic) has zero dependency on `internal/vtime` (the execution
  engine).** The two are connected only through the `clock.Clock` interface, which both the real
  `ReverseProxy` (wall-clock) and `replay.RunWorld` (via `vtime.Engine.Clock()`) satisfy
  independently. This is the correct dependency direction and is the load-bearing seam that keeps
  routing logic engine-agnostic, as TRD §2 intends.
- **`internal/health` has no dependency on `internal/proxy`; `internal/cache` has no dependency on
  `internal/dashboard`.** Both boundaries are clean.
- **The dashboard is a genuine read-only projection, not a second source of truth.** Every handler
  (`internal/dashboard/{experiments,playground,tuning}.go`) computes its result fresh per request
  from `experiments/*/results/*.json` or by calling `replay.RunWorld` live — no in-memory cache, no
  run history, nothing that could drift from the on-disk artifacts. This directly satisfies the
  "dashboard as projection" design goal and was verified, not merely asserted.
- **The tuner reuses the execution engine rather than duplicating it.** `internal/tuning/evaluate.go`
  and `robustness.go` both call `replay.RunWorld` directly — confirmed by reading the call sites,
  not inferred from the Stage 8 exit-artifact claim that repeats this. Every tuning evaluation path
  (aggregate, per-scenario, search) goes through the identical engine every other experiment since
  Stage 7 uses.

## Abstraction ledger

| Abstraction | Purpose | Consumers | Earned? | Notes |
|---|---|---|---|---|
| `clock.Clock` | Decouple domain logic from wall-clock vs. virtual time | `ReverseProxy`, `health.Registry`, `cache.Cache`, `netsim`, `topology.Edge`, replay (via `vtime.Engine.Clock()`) | Yes | Two real implementations, both genuinely exercised; the seam that keeps `internal/proxy` free of any `vtime` import |
| `proxy.TargetSelector` | Pluggable routing decision | 7 implementations, consumed identically by the live proxy, the virtual replay engine, and the dashboard | Yes | Textbook earned interface |
| `replay.PolicySpec` | Bundle a selector-constructor + name for per-run fresh selector/tracker construction | 6 constructors, consumed by `RunWorld`, tuning, dashboard | Yes | `seed`/`targets` parameters are each used by exactly one policy but threaded uniformly — cheap, reasonable uniformity |
| `replay.Instrumentation` | Feed dispatch/completion events to whatever endogenous tracker a policy needs | 2 implementations, both genuinely exercised by different policies | Yes | — |
| Tuning "Optimizer" interface | — | — | **Correctly absent** | No interface was built despite only Random Search existing — `TunerVersion` is a plain string constant, not a type-level abstraction. This is the *opposite* of a premature abstraction, matching the project's own earn-the-abstraction discipline: a second optimizer would introduce the interface then, not maintain an unused seam now. |

## Dead code / duplication

A repo-wide sweep for `deprecated|legacy|shim|backward.?compat|TODO|FIXME|XXX` across all non-test
`.go` files in `internal/` and `cmd/` returned **zero matches** — no compatibility shims or stale
markers found anywhere.

Three independent percentile implementations exist (`internal/statistics.Percentile`,
`internal/httpx.calculatePercentiles`, and a private copy in `cmd/experiment-005h`). The first two's
divergence is explicitly and correctly disclaimed in `internal/statistics/percentile.go`'s own
comment; the `experiment-005h` copy predates the shared package (verified via `git log
--follow --diff-filter=A`) and is undocumented but frozen, historical output — polish-level, see
[F-47](FINDINGS.md).

Every `cmd/experiment-*` binary (~50 of them, `001a` through `008h`) is referenced by name or ID in
some learning note or stage artifact — no orphaned experiment binaries found. The apparent gaps in
the numbering (`004b`, `004g`) are explicitly and deliberately descoped, with stated rationale, in
`docs/learning/004-caching-failures.md` and `Stage4.md` — not silent omissions.

## Real-vs-Virtual engine feature parity matrix

| Feature | Virtual (`internal/replay`) | Real (`cmd/proxy` + `internal/proxy`) | Divergence documented? |
|---|---|---|---|
| Round Robin / WRR / Least-Conn / EWMA / P2C / Adaptive | All six, via `PolicySpec` constructors | Same underlying selector types — genuinely the same Go code, confirmed by direct trace | N/A (identical); but `cmd/proxy`'s CLI only ever wires Round Robin — [F-30](FINDINGS.md) |
| Health checking | `health.Registry` driven by a virtual ticker feeding synthetic up/down booleans | Same `health.Registry`, driven by real HTTP probes (`health.Checker`) | Genuinely shared state-machine code; only the probe I/O differs, as expected. `RecordAppResult`'s error-rate DEGRADED path is real-engine-only (virtual has no app status codes) |
| Cache | Not modeled at all — `Scenario` has no cache/TTL dimension | `internal/cache` (TTL + coalescing) on `EdgeServer`, exercised by real-HTTP experiments | Yes, explicitly documented (Stage 8 learning notes) |
| Failure/chaos injection | `replay.FailureWindow` — Go struct literals | `netsim.Transport{Conditions{...}}` — also Go literals | PRD claims YAML for both; neither engine has it — [F-10](FINDINGS.md) |
| Network degradation | Not modeled in `RunWorld`/`Scenario` at all | `internal/netsim` — in-process latency/jitter/loss simulator | Yes, documented as a `tc netem` substitute at the Stage 4 level, but misrepresented as delivered `tc netem` in top-level docs — [F-02](FINDINGS.md) |

The headline "same policy code runs in both engines" claim independently checks out — this is not
a case of parallel, hand-duplicated routing logic behind a shared-sounding name.

## Performance and scale

- **No accidental O(n²) hot paths found.** A direct benchmark sweep of `AdaptiveSelector` from
  n=3 to n=500 targets showed cost growing consistently with O(n log n) (attributable to the
  deterministic `sort.Strings` tie-break), not quadratically. `RoundRobinSelector` is flat/O(1)
  across the same range.
- **`AdaptiveSelector.SelectTarget` holds a single mutex across its entire body** (sort + score
  loop + two map writes), fully serializing concurrent routing decisions — every other selector
  uses finer-grained or lock-free synchronization. Invisible at the project's tested scale (3–5
  targets) but a real, identifiable bottleneck if target count or request concurrency grows
  ([F-31](FINDINGS.md)).
- **Scale validation gap, not a defect**: no benchmark, unit test, or challenge scenario anywhere
  in the repo exercises more than ~5 targets. The n=500 sweep above was performed for this audit
  and is not part of the committed test suite. Given PRD's explicit scoping to small edge
  topologies, this is an untested regime rather than a broken one, but it is currently undisclosed
  as a scale boundary anywhere in the docs.
- **The tuner's evaluation-result cache is real and correctly implemented** but, given Random
  Search's continuous Dirichlet/duration sampling, has a near-zero practical hit rate — the claim
  "an evaluation-result cache exists" is true; its value for the shipped algorithm specifically is
  close to nil and undisclosed as such ([F-45](FINDINGS.md)).
- **Benchmark numbers cited in Stage 8 docs were independently reproduced** (4.29ns vs. documented
  4.4ns for Round Robin's selection cost; 246.5ns vs. documented 259ns for Adaptive) — consistent
  with normal machine variance, no fabrication found.
- **The open-loop load generator (`cmd/experiment-008g`) is genuinely open-loop**: each request
  computes an absolute dispatch time and sleeps via `time.Until`, rather than using a shared ticker
  or sequential `time.Sleep` — verified by reading the dispatch loop directly, avoiding the classic
  closed-loop coordinated-omission failure mode.
- **The dashboard's experiment browser does not scan the whole repository per request** — confirmed
  by reading `internal/dashboard/experiments.go`: one non-recursive `os.ReadDir` per group, no
  `filepath.Walk`. No caching exists, but none is needed at the project's current scale (9
  top-level experiment groups).
