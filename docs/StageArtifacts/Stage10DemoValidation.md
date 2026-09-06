# Stage 10 — Demo Readiness Validation

Adversarial, hands-on validation performed against the actual Stage 10 implementation (not against
`docs/StageArtifacts/Stage10-Plan.md`'s or `Stage10.md`'s own claims about itself). Every claim below
was checked by direct execution — running real binaries, hitting real HTTP endpoints, exercising a
real browser session, and deliberately mutating code to confirm a test would catch a real violation
— not by re-reading source and trusting it does what it says.

## Executive Verdict

**DEMO READY.**

Every one of Stage 10's 9 capabilities works correctly and robustly when exercised directly — no
demo theater, no fake data, no hardcoded results were found anywhere in the codebase. Four real gaps
were found during this audit and all four were fixed and re-verified: a completely unwired
provenance package, a badly stale README, `ExperimentEngine`'s missing Scenario/Real consistency
check, and `prd.md`/`trd.md`'s stale "not built" claims. The `go run` VCS-stamping gap turned out to
need a flag (`-buildvcs=true`), not a code change. The single remaining risk to a "clean checkout"
demo is environmental, not technical: all Stage 10 code (60+ files) is currently uncommitted — left
as-is per explicit instruction.

**Update**: a dedicated hero-demo binary (`cmd/demo-stage10`) was subsequently built, executed, and
verified — see `docs/demo/Stage10Demo.md` for the full recording script. It supersedes
`cmd/experiment-010a` (the tuner comparison) as the **primary** demo, per the explicit guidance that
a tuning result is a weaker visual demo than a controlled policy-comparison experiment; the tuner
comparison remains a valid, real, honestly-reported secondary demonstration.

## What Was Tested

Every item in the following list was **executed**, not merely read: `internal/traffic`'s 5 patterns
plus log import plus 6 edge cases; `internal/provenance`'s `SeedTree` axis-independence across 4
independent-variation combinations (including confirming `P2C` actually consumes `seeds.Policy`, not
just carries it); `internal/attribution`'s severity bands at every boundary plus 6 edge cases;
**both** metamorphic invariants, deliberately mutated twice to confirm they catch real violations,
then reverted; `internal/cache`'s SWR stale-hit/revalidation/coalescing plus a direct goroutine-leak
measurement; a live `cmd/proxy -metrics-addr` process scraped before/after traffic and under 10
concurrent requests; `internal/chaos`'s YAML parser against 6 real files on disk (not string
readers) plus a live crash/recover/latency schedule against a running `EdgeServer`;
`internal/engine`'s `VirtualEngine`/`RealEngine` including a deliberately-inconsistent
Scenario-vs-Real probe; all three tuners (`RandomSearchTuner`/`LHSTuner`/`BayesOptTuner`) via a real
200-evaluation, 3-way comparison run twice, plus Bayesian Optimization's duplicate-point/singular-matrix/NaN-utility
edge cases; the live dashboard in an actual browser session, including 5 raw-HTTP path-traversal
variants against the Stage 9 security fix; a Stage 1 TCP client/server smoke test; the full
`go build`/`go vet`/`go test ./...`/`scripts/final-validation.sh` suite.

## What Passed

- **Traffic generator**: exact arrival counts, deterministic seeding, correct pattern shapes (hand-verified
  density checks), graceful handling of extreme burst width (10x the horizon), 1-nanosecond horizon,
  requests=1, and 1,000,000 requests — no panics, no out-of-range arrivals in any case.
- **Provenance/SeedTree**: independent-axis control is real, not aspirational — verified experimentally
  that varying only Failure, only Policy, or only Traffic changes exactly what it should and nothing
  else, and that `P2C`'s selector actually consumes `seeds.Policy` (confirmed via divergent selection
  traces, not just "the field is passed through").
- **Attribution**: severity bands correct at 0.7/0.9/1.0 boundaries; zero-service-time, zero-horizon,
  zero-completions, and unknown-target inputs all handled without NaN/Inf/crash; generated text never
  overclaims that the virtual engine models queueing delay.
- **Metamorphic invariants**: both **actually catch real violations** — confirmed by deliberately
  breaking `doubleServiceTimes` (halving instead of doubling) and the halved-arrival-count parameter
  (increasing instead of decreasing), watching both tests correctly fail with clear diagnostic
  output, then reverting cleanly (verified via `grep` that no mutation marker survived).
- **SWR**: real end-to-end HTTP test (stale serve → background revalidate → fresh on next request);
  20 concurrent stale hits coalesce into exactly 1 real revalidation; 50 stale-hit cycles leave
  goroutine count unchanged (2 before, 2 after).
- **Telemetry/`/metrics`**: live process, correct `200`/`text/plain` response, `flashflow_requests_total`
  reads 0 before traffic and 5 after 5 real requests, histogram count matches exactly, 10 concurrent
  scrapes all return 200 with no panic.
- **Chaos YAML**: real file I/O (valid/unsorted/malformed/missing-target/unknown-action/same-timestamp
  cases) all produce correct, specific errors or correct parses; a live schedule against a running
  `EdgeServer` correctly flips it to 503 and back.
- **Tuning**: all three tuners produce only valid candidates across 50+ sequential rounds; Bayesian
  Optimization survives duplicate training points (confirmed the underlying singular-matrix case is
  caught by `choleskyLower`'s positive-definite check, and that the production jitter term prevents it
  from even triggering in the realistic case) and a NaN training utility without panicking.
- **Dashboard**: clean page load (zero console errors), new `010-stage10-features` experiment group
  correctly discoverable and its real JSON content correctly displayed, and — the most important
  security check — **5 different path-traversal encodings** (`%2e%2e`, double-encoded, backslash,
  mux-cleaned plain `..`, nested-in-filename) **all correctly rejected with 404** against the live
  server.
- **Regression**: `go test ./...` green across all 22 packages; Stage 1's TCP client/server smoke test
  still works end to end; Stage 9's three security fixes (dashboard loopback default, `ReadHeaderTimeout`
  on all three real HTTP servers) all confirmed present in the current code.
- **No demo theater found**: a repo-wide search for `TODO`/`FIXME`/`HACK`/hardcoded-success patterns,
  seed-specific special-casing, and demo-only branches in every Stage 10 package returned nothing.

## What Failed (found, then fixed during this audit)

1. **`internal/provenance` was completely unwired.** `Manifest`/`ConfigHash`/`GitCommit` existed as a
   fully tested library with **zero callers** outside its own package — no experiment, no
   `ExperimentEngine`, nothing produced a real `manifest.json`. This directly blocked the
   evidence-chain and provenance-demo requirements (Sections 7 and 26) — you cannot "inspect a
   generated manifest" that nothing generates. **Fixed**: wired into `cmd/experiment-010a`, which now
   writes one real manifest per tuner run to `experiments/010-stage10-features/runs/`. Verified: all
   three manifests share an identical `configuration_hash` (correct — same `ConfigSpace`/weights),
   each carries its own distinct `SeedTree`, and `git_commit`/`git_dirty` populate correctly with
   `go run -buildvcs=true` (see the VCS-stamping note below).
2. **`README.md` was badly stale.** It stated Stage 10 was "planned but not started" and explicitly
   claimed `ExperimentEngine`, Prometheus/HdrHistogram telemetry, and the provenance manifest did not
   exist — all now false. Since README is this project's own documented "Test From a Fresh User
   Perspective" entry point, this would have made all of Stage 10 undiscoverable to a cold viewer
   reading only the front door. **Fixed**: updated the implementation-status paragraph, the Build
   Sequence table (added a real Stage 10 row), the Experiments table (added 010-A), and added a
   "Running Stage 10 Features" section with real, copy-pasteable commands.

## What Failed (found, then also fixed — second pass)

3. **`ExperimentEngine` did not verify `Scenario` and `Real` describe the same experiment.**
   Constructed one `Experiment` with a `Scenario` describing 3 targets at 5/50/500ms and a `Real`
   config describing a completely different single 1ms edge; both `Prepare` calls succeeded and both
   `Run` calls executed without warning. **Fixed**: added `engine.ValidateConsistency`, which requires
   the exact same set of names between `Scenario.Targets` and `Real.Edges` (both directions, count
   included), wired into `RealEngine.Prepare`. Verified: the exact original probe now correctly fails
   `Prepare` (`internal/engine/consistency_test.go`'s
   `TestValidateConsistency_CatchesTheExactMismatchTheAuditFound`), and all pre-existing `RealEngine`
   tests still pass after updating their shared fixture (`realTestExperiment`) to use matching
   names, which is itself a natural, correct requirement to impose going forward. This does not (and
   cannot, without much more invasive bookkeeping) guarantee identical *behavior* between the two
   engines' runs — a real edge's latency depends on real scheduling the virtual engine's fixed
   `ServiceTime` never models — but it does guarantee the two configurations describe the same named
   topology, closing the silent-divergence gap.
4. **`prd.md`/`trd.md` contained multiple "Not built — Stage 10 scope" rows** for features Stage 10
   now ships. **Fixed**: both documents' status tables/sections rewritten to state what Stage 10
   actually built, matching `README.md`'s and `RESOLUTION.md`'s corrections.

## The `go run` VCS-Stamping Gap Needed a Flag, Not a Code Fix

Investigated further after the initial finding: `go run`'s *default* (`-buildvcs=auto`) silently
omits VCS stamping in this environment, but `go run -buildvcs=true ./cmd/experiment-010a` populates
`git_commit`/`git_dirty` correctly — confirmed on a fresh run with the prior manifests deleted first,
not just re-reading a stale file. No code change was needed; `internal/provenance.GitCommit`'s
existing "return empty rather than fail" behavior was already correct for the case where VCS info
genuinely isn't available. **Fixed via documentation**: `README.md`'s Stage 10 commands and this
report's demo commands now specify `-buildvcs=true`.

## What Was Flaky

Nothing. Every test run (including 3 separate full `go test ./...` passes and 2 separate 200-evaluation
tuner comparison runs at different points in this audit) produced identical qualitative results. The
metamorphic and provenance-axis tests are seed-deterministic by construction and were re-run multiple
times with identical output.

## What Required Manual Intervention

- Killing a backgrounded proxy/origin process pair after a `/metrics` test exceeded its command
  timeout (an artifact of this audit's own tooling, not of FlashFlow itself).
- Choosing a scratchpad-relative path instead of `/tmp` for chaos-YAML file fixtures, since this is a
  native Windows Go binary and `/tmp` is a Git-Bash-only path alias it cannot resolve — a real,
  documented environment quirk worth knowing before recording (see Environment Requirements).

## Security Checks

- Path traversal (the Stage 8/9 finding, F-01): **confirmed intact** against 5 distinct encodings via
  raw HTTP requests to a live server, not just the existing unit test.
- Dashboard loopback-only default bind (`127.0.0.1:7070`): confirmed present in `cmd/dashboard/main.go`.
- `ReadHeaderTimeout` on all three real HTTP servers (`proxy.go`, `edge.go`, `dashboard/main.go`):
  confirmed present.
- Dashboard `innerHTML` usage: the actually-risky, HTTP/disk-driven paths (experiment group/file
  names, file content) correctly use `textContent`/DOM APIs. Several other `innerHTML` calls remain
  for internally-generated, schema-fixed data (policy names, numeric metrics, fixed enum strings) —
  low/no risk under the current design. One soft spot: `renderTimeline`'s `ev.fields`/`ev.entity` are
  interpolated into `innerHTML` via template literals; not currently exploitable (Playground's
  scenario is fixed, no user input reaches trace event fields), but a latent gap if the Playground is
  ever parameterized with user-supplied scenario data. **P3 — not a blocker, worth a defense-in-depth
  fix eventually.**

## Reproducibility Checks

- Same-seed tuner runs: `TestLHSTuner_Determinism`, `TestBayesOptTuner_Determinism`,
  `TestGenerate_Determinism` (traffic) all pass — re-run during this audit, not just inspected.
- `TestGenerateFromRoot_EquivalentToGenerateDeriveSeeds` confirms the Stage 10 seed refactor is
  behavior-preserving for its own compatibility path.
- **Real caveat found, then resolved**: `go run`'s default (`-buildvcs=auto`) does not stamp Go's VCS
  build info in this environment, leaving `git_commit`/`git_dirty` empty in a manifest generated that
  way. `go run -buildvcs=true ./cmd/experiment-010a` populates both correctly — confirmed on a fresh
  run with prior manifests deleted first. No code change needed; the demo commands below already use
  the flag.

## Browser Checks

Real browser session (not just `curl`): clean load, zero console errors, correct tab switching,
correct real-data rendering in Playground/Experiments, and the path-traversal payloads correctly
normalized/blocked. Not separately tested in this audit: browser back/forward, concurrent tabs, and
an intentionally-malformed on-disk JSON artifact (time constraints) — recommended as a quick
pre-recording check, not expected to fail given the code review above.

## CLI Checks

Every Stage 10 CLI-relevant flag exercised directly: `cmd/proxy -metrics-addr` (live, scraped),
`cmd/experiment-010a` (run twice, byte-consistent qualitative result). `cmd/proxy -policy` and
existing flags unaffected (Stage 9 regression, reconfirmed).

## Engine Checks

`VirtualEngine.Prepare/Run/Replay` and `RealEngine.Prepare/Run/Replay` both individually correct and
well-tested (invalid-config rejection, real HTTP execution, chaos-schedule integration).
`RealEngine.Prepare` now additionally calls `ValidateConsistency` (fixed during this audit — see
finding #3), cross-checking that `Scenario.Targets` and `Real.Edges` name the same topology before
either engine ever runs.

## Experiment Checks

`cmd/experiment-010a` re-run twice in this audit at different points, both under `go run` and (once)
as a compiled binary; qualitative result (no tuner meaningfully beats Random Search) identical both
times. Dashboard's Experiment browser correctly surfaces its output; the dashboard's dedicated
"Tuning" tab does **not** show it (that view is hardcoded to read `008B`/`008C` files specifically) —
use the generic Experiments tab to show 010-A in a demo, not the Tuning tab.

## Evidence-Chain Check

Traced end to end for the tuner-comparison hero flow, after fixing finding #1 above:

```
ConfigSpace + ObjectiveWeights (source config)
    → provenance.ConfigHash (identical "e505d0cdb9691cb7" across all 3 tuner runs -- verified directly)
    → replay.DeriveSeeds(20260908) (real SeedTree, distinct Traffic/Topology/Failure/Policy per manifest)
    → tuning.RunSearch (real 200-evaluation execution per tuner)
    → experiments/010-stage10-features/runs/010a-<tuner>/manifest.json (real, inspected)
    → experiments/010-stage10-features/results/010A-tuner-comparison.json (real, inspected)
    → dashboard Experiments tab (confirmed displaying the real file content, live in-browser)
```

Every arrow above was independently verified, not assumed. This is the strongest, most fully-traceable
evidence chain in the current Stage 10 surface — recommended as the demo's spine.

## Performance Sanity

No accidental O(n²) found (Stage 9's own audit already confirmed `AdaptiveSelector` scales O(n log n)
to n=500; nothing in Stage 10 touches that hot path). SWR's background-revalidation goroutines
measured directly: 2 before, 2 after 50 cycles — no leak. Bayesian Optimization's per-`Suggest` cost
(500-candidate pool × up to 200 training points) completed the full 200-evaluation run in ~12-14
seconds, comparable to Random Search's ~11 seconds and LHS's ~13 seconds — no runaway cost observed.

## Environment Requirements

- Go 1.23.3, Windows (this environment) — confirmed working; scripts use Git Bash/WSL-only bash
  syntax (`scripts/*.sh`), already documented in README.
- Docker: **not currently running in this environment** (it was available earlier in this session).
  The NGINX reference benchmark degrades gracefully without it (exits early with a clear message,
  confirmed by its own design) — not required for any Stage 10 capability, but confirm it's running
  before a demo that includes the NGINX comparison.
- `/tmp`-style Unix paths do not resolve for a native Windows Go binary — use a real Windows path
  (or the session's own scratchpad directory) for any file-based demo step (chaos YAML files,
  imported access logs).
- Ports used across this audit's testing: 7070 (dashboard), 8000/8081/9090 range and 28000/28081/29090
  (ad hoc test instances) — no fixed port conflicts found; every server binds an explicit `-addr`.

## Primary Hero Demo

**Superseded** — see `docs/demo/Stage10Demo.md` for the current primary demo
(`cmd/demo-stage10`): a controlled heterogeneous-edge experiment with an injected failure, Round
Robin vs. Adaptive compared under identical exogenous conditions via `internal/engine`'s
`Run`/`Replay`, a counterfactual-divergence proof, a real `internal/attribution` explanation, and a
real provenance manifest with a reproducibility rerun — chosen over the tuner comparison per the
explicit guidance that a policy-comparison experiment is a stronger visual demo than a tuning result.

```bash
go run -buildvcs=true ./cmd/demo-stage10
```

**Expected observable outcome**: ~2 seconds total runtime; Adaptive's mean latency measured 16.4%
lower than Round Robin's under the identical scenario; a real counterfactual divergence at trace
event #8; a real provenance manifest at `demo/output/stage10-demo/manifest.json`; a rerun confirming
zero trace divergence from the original run.

## Secondary Demo — Tuner Comparison

**The tuner comparison + provenance evidence chain** (`cmd/experiment-010a`) remains a valid,
real, honestly-reported secondary demonstration — kept secondary per the explicit guidance that
"click → wait → number changes" is a visually weaker demo than a live policy-comparison experiment,
not because the result itself is any less real.

```bash
go run -buildvcs=true ./cmd/experiment-010a
cat experiments/010-stage10-features/runs/010a-bayesopt-v1/manifest.json
cat experiments/010-stage10-features/results/010A-tuner-comparison.json
```

**Expected observable outcome**: ~35 seconds total runtime; a printed table showing all three tuners
converging to ~0.719 utility; a real manifest.json per tuner with a shared configuration hash and
distinct seed trees; a result JSON whose `findings` field states honestly that neither LHS nor
Bayesian Optimization meaningfully beats Random Search, with the reasoning why that's the expected
(not disappointing) result.

## Backup Demo (technically deep, real engine)

**Live chaos injection against a running real edge + telemetry.**

```bash
go run ./cmd/http-origin -addr :8000 -delay-ms 20 &
go run ./cmd/proxy -addr :8081 -targets http://127.0.0.1:8000 -metrics-addr :9090 -check-interval-ms 200 &
curl http://127.0.0.1:9090/metrics   # baseline: HEALTHY
# (construct and run a chaos.Schedule crash/recover against a real EdgeServer -- see
# internal/chaos/real_test.go's TestToRealSchedule_And_RunReal_EndToEnd for the exact pattern)
```

**Expected observable outcome**: `/metrics`' `flashflow_target_health` flips from `HEALTHY` to
whatever state the health checker assigns during the injected outage and back, live, on screen.

## Optional Visual Demo

Dashboard: Playground → Run (round-robin) → Compare (round-robin vs adaptive) → observe the real
first-divergence point → Experiments tab → drill into `010-stage10-features` → show the real
tuner-comparison JSON. All steps confirmed live in-browser during this audit.

## Known Demo Risks

- All Stage 10 code is uncommitted (60+ files at the time of this audit, left as-is per explicit
  instruction). A demo depending on a fresh `git clone` of `origin/main` will show **none** of Stage
  10. This is an operational fact, not a bug — flagged here because it directly affects the "clean
  checkout" go/no-go question below.
- Docker was not running at audit time; confirm it is started before any demo including the NGINX
  reference comparison (not required for any Stage 10 capability itself).

## Fixes Required Before Recording

**None remaining.** All four issues originally found here (unwired provenance, stale README,
`ExperimentEngine` consistency, stale `prd.md`/`trd.md`) were fixed and re-verified during this
audit. Docker needs to be started manually before any demo segment that uses it — an environment
step, not a code fix.

## Things That Should NOT Be Claimed in the Demo

- "The virtual engine models queueing delay" — it does not, and `internal/attribution`'s own
  generated text is careful never to imply this; don't contradict it live.
- "Every experiment now has a provenance manifest" — only `cmd/experiment-010a` does; say "the
  provenance system exists and is wired into the tuner-comparison experiment" instead.
- Any claim implying the NGINX comparison ran in this exact session without checking Docker is
  actually running first.
