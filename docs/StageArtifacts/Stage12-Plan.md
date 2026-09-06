# Stage 12 — Plan: Model Fidelity and Experimental Control

**Status at creation: plan only. Implementation follows this document; results are appended to
`Stage12.md`, not here.**

## 1. Stage 11 Findings Being Addressed

| Stage 11 finding | Stage11.md ref | Track |
|---|---|---|
| `RunWorld` has no queueing/contention model; EWMA's mean-latency win is a modeling artifact (unconstrained concentration costs nothing) | §7, §14 | D |
| `TargetProfile.ServiceTime` is fixed for an entire Scenario; H2 (oscillation/staleness attack) could not be tested | §12 | C |
| `RealEngine`'s latency signal was fixed, but its load (in-flight count) signal remains a post-hoc, non-concurrent approximation | §10, Limitation 3 | A |
| `internal/tuning.ScenarioSpace.Generate`'s Topology seed leaks into Failure target selection via `n`-dependent `Intn(n)` | §16, Limitation 4 | B |
| Only one genuine distribution shift was tested | §15, Limitation 5 | (re-test only, no new mechanism) |
| Adaptive's cache-affinity trap was confirmed causally, but only under the flat, queueing-free model | §12 | re-test under D |
| Virtual-vs-real validation is partial (EWMA confirmed, Adaptive's load-driven concentration not yet validated) | §10 | A, then re-test |

## 2. Scientific Motivation

Stage 11 found that several of its own conclusions were contingent on specific, known platform gaps
rather than genuine properties of the routing policies being studied. The central Stage 12 question is
narrower than "build a better simulator" — it is: **does fixing exactly the four gaps Stage 11 proved
consequential change what we believe about EWMA, Adaptive, and the virtual/real relationship?** Every
mechanism added below exists because a specific Stage 11 sentence said "this cannot be tested" or "this
is a modeling artifact, not a real effect" — not because a richer simulator is generically desirable.

## 3. Current Model Limitations (precise)

- `internal/replay/world.go`'s `RunWorld`: a dispatched request's completion is scheduled at
  `now.Add(serviceTimes[target])` unconditionally — no capacity check, no queue, ever.
- `internal/replay/scenario.go`'s `TargetProfile{Name, ServiceTime}`: `ServiceTime` is one `time.Duration`
  read once per `RunWorld` call via `scenario.serviceTimes()`; nothing in the type can vary over time.
- `internal/engine/real.go`: `policy.New` builds its own fresh `LoadTracker`/`LatencyTracker`, entirely
  separate from `proxy.ReverseProxy`'s own internal pair (which IS correctly updated at genuine
  dispatch/completion boundaries by `ServeHTTP`, lines 186-187 and 300). The selector never sees the
  proxy's real values.
- `internal/tuning/scenario.go`'s `Generate`: `failTarget := targets[failureRNG.Intn(n)].Name`, where
  `n` is drawn from `topoRNG` — couples the Failure axis's outcome to the Topology axis's draw.

## 4. Proposed Minimal Mechanisms

### Track A — Real load-signal sharing (no new abstraction, a wiring fix)
Change `PolicySpec.New`'s signature to accept an optional `Trackers{Load, Latency *proxy.LoadTracker/*LatencyTracker}`
struct (zero value == "construct fresh," preserving virtual-engine behavior exactly). `RunWorld` passes
`Trackers{}` (no change to virtual determinism). `RealEngine.run` constructs the `ReverseProxy` FIRST
(with a nil/placeholder selector), then builds the real selector via `policy.New(..., Trackers{Load:
pxy.LoadTracker(), Latency: pxy.LatencyTracker()})`, then `pxy.SetSelector(selector)`. This makes the
selector read the EXACT objects `ServeHTTP` increments/decrements at genuine dispatch/completion
boundaries — no post-hoc bridging, no back-to-back `OnDispatch`/`OnComplete` calls. The manual
`X-Selected-Edge`-header bridge added in Stage 11 becomes unnecessary and is removed.

### Track B — Seed-axis independence (structural fix, not a test change)
`Generate` draws the failure-target CANDIDATE from `failureRNG.Intn(len(targetNames))` — a fixed-size
draw (5, `ScenarioSpace`'s own name-pool size, never topology-seed-dependent) — instead of `Intn(n)`.
If the candidate index is `>= n` (the name isn't present in this topology instance), this scenario has
no failure (a defensible, explicitly-documented consequence, not a silent behavior change: smaller `n`
means a higher chance of "the RNG picked a target that doesn't exist here"). This makes the failure
target's identity, when valid, and the failure timing/duration draws completely independent of `n` —
whereas today changing `n` reliably changes the result of `Intn(n)` from the identical `failureRNG`
state.

### Track C — Time-varying target service time (discrete scheduled changes)
Add `ServiceTimeChange{At clock.VirtualTime, NewServiceTime time.Duration}` and a
`[]ServiceTimeChange` field on `TargetProfile`. `RunWorld` schedules one `e.Schedule` callback per
change, for every target, BEFORE scheduling any arrivals (so a `t=0` change always precedes a `t=0`
arrival under vtime's existing (timestamp, insertion-sequence) tie-break) — each callback simply
mutates the `serviceTimes` map entry RunWorld already reads current values from at dispatch/service-
begin time. A request already in service keeps its originally-scheduled completion untouched (Go
closures capture the duration value at scheduling time); only requests that begin service AFTER a
change observe the new value. No continuous function, no general framework — a fixed, small,
deterministic list of scheduled point-changes, matching what the H2 experiment actually needs (2-4
changes per target, not an arbitrary schedule DSL).

### Track D — Minimal finite-capacity contention model (the largest piece)
Add `Capacity int` to `TargetProfile` (`<= 0` means infinite — the existing flat-mode behavior,
preserved byte-for-byte with zero code changes to any pre-Stage-12 Scenario literal, since the field's
zero value is 0). When `Capacity > 0`, `RunWorld` maintains per-target `{busy int; queue
[]queuedRequest}` state (plain Go slices/ints inside RunWorld's own closure — no shared mutable state,
no goroutines, no locks; the virtual engine is already single-threaded and deterministic by
construction). On dispatch: if `busy < Capacity`, begin service immediately (identical to today); else
append to the target's FIFO queue. On completion: decrement `busy`; if the queue is non-empty, pop the
front request and begin ITS service now (so its total latency = wait + service, both attributable).
`Instrumentation.OnDispatch`/`OnComplete` fire at the same semantic boundaries as today (router decision
/ final completion) — a "load" signal already meant "how many requests currently assigned to this
target, in whatever state," which queueing doesn't change.

## 5. Invariants

1. **Flat-mode backward compatibility**: `Capacity <= 0` and empty `ServiceTimeChange` must reproduce
   byte-identical `WorldResult` traces to pre-Stage-12 code, for every existing Scenario literal in
   `cmd/experiment-*` and `internal/challenge`. Verified directly (Section 8).
2. **Determinism**: same `Scenario` + same `PolicySpec` → byte-identical `WorldResult`, with capacity
   and/or time-varying service time enabled. No wall-clock reads, no goroutines, no map-iteration-order
   dependence introduced anywhere in the new code paths.
3. **Exogenous/endogenous separation**: `Capacity` and `ServiceTimeChange` are `TargetProfile` fields —
   world physics, identical for every policy compared against one `Scenario`, exactly like
   `ServiceTime` already is.
4. **No double-counting**: a request's `Latency` in `CompletionRecord` must equal wait time plus service
   time exactly once each, never twice, never dropped.
5. **No negative/stranded state**: `busy` never goes negative; a queued request is never silently
   dropped; a target failing while requests are queued at it does not strand or duplicate that work
   (chosen semantic: queued work is unaffected by failure state, matching the existing precedent that
   `FailureWindow` only gates NEW routing eligibility, never already-scheduled completions).

## 6. Compatibility Strategy

- `PolicySpec.New`'s signature change is additive-parameter, not additive-method: every existing
  6 constructors in `policies.go` gain one parameter (`tr Trackers`, using `tr.Load`/`tr.Latency` if
  non-nil, else constructing fresh — a 2-line change per constructor). All 3 real call sites
  (`world.go`, `real.go`, `policies_test.go`) are updated in the same commit; no dangling old-signature
  callers can exist after the change (Go's compiler enforces this).
- `TargetProfile`'s two new fields (`Capacity`, `ServiceTimeChange`) are additive struct fields with
  zero values that reproduce old behavior — no existing literal needs updating.
- `Generate`'s fix changes `Intn(n)` to `Intn(len(targetNames))` — a one-line change plus a validity
  check; existing callers (`GenerateFromRoot`, `GenerateSet`, `NewSplit`) are unaffected at the call
  signature level, only at the value level (Section 10 measures exactly how much).

## 7. Experiments Affected

- **Program A** (regime map) — the flagship severe/constant/none finding is re-run under Track D
  (contention enabled) to test whether EWMA's concentration advantage survives a real capacity cost.
- **Program B** (adversarial) — B1 (cache-affinity trap) re-run under Track D; a new H2
  (staleness/oscillation) experiment becomes possible for the first time under Track C.
- **Program C** (recovery dynamics) — re-run under Track C+D to see whether the previously-flat
  transition/steady-state p99 ratio (exactly 1.00x, Stage11.md §13) changes now that a topology
  transition can carry a genuine capacity cost.
- **Program F** (virtual-vs-real) — re-run after Track A to test whether Adaptive's real-engine
  concentration now more closely matches its virtual behavior.
- **Program E** (attribution) — re-run under Track D to test whether Little's Law now decomposes into
  a genuine service/wait split, not just a bookkeeping-consistency check.
- **Stage 8's tuning result** — re-run via `tuning.NewSplit` + `tuning.RunRandomSearch` after Track B,
  compared against the original 008-B/008-C recorded numbers (mandatory, Section 10 of the brief).

## 8. Verification Strategy

After each track: `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./...`. After Track D
specifically: hand-constructed analytically-checkable cases (infinite capacity ≡ flat model; 1 slot;
2 slots; deliberate overload; symmetric equal targets; heterogeneous targets) verified against
hand-computed expected values, not just "it runs." After all tracks: `scripts/final-validation.sh`.
A benchmark comparison (flat vs. contention-enabled, at Program A's own request scale) records the
throughput cost of the new fidelity.

## 9. Expected Failure Modes

- A same-timestamp ordering bug between a service-time change and an arrival/completion at the exact
  same virtual time, silently depending on map iteration order rather than the vtime tie-break.
- Queue state leaking `busy` counts across `RunWorld` calls if any of it is accidentally hoisted out of
  the per-call closure (would break the "fresh state per call" counterfactual-isolation guarantee).
- The Track A signature change missing a call site (compiler will catch this; still explicitly checked).
- Track D changing Program A's numbers in a way that looks like a regression rather than a genuine,
  better-justified finding — explicitly not "fixed" by re-tuning the model until old numbers reappear.

## 10. Explicit Non-Goals

No packet-level simulation, no TCP congestion control, no stochastic queueing-theory (M/M/c) validation,
no general continuous-function service-time model, no new routing policies, no contextual bandits, no
DR-OPE, no Parquet, no advanced cache eviction policies, no topology-generation framework beyond the
minimal name-pool-independence fix Track B requires. Track D is a deterministic discrete-event capacity
model, not a stochastic simulator, unless the workload itself already introduces randomness (it does,
via existing traffic generation — the queue's OWN mechanics remain deterministic).
