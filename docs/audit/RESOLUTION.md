# Audit Resolution — Stages 9–10

Maps every finding in [FINDINGS.md](FINDINGS.md) to exactly one disposition:
**Fixed** (code/test change) · **Disclosed** (documentation-only) · **Already fixed** (found
silently applied during planning, pre-dating Stage 9 — see `docs/StageArtifacts/Stage9.md`'s "An
Incident, Disclosed Up Front") · **Deferred** (P2/P3 item explicitly not addressed, with reason).
Stage 9 fixed every P0-P2 finding it could without building new features, and explicitly disclosed
the 9 PRD/TRD features F-04 through F-10 (plus the LHS/Bayesian tuner tiers named in F-22 and the
telemetry stack named in F-23) as deferred to a dedicated build stage rather than fixed by
documentation alone. Stage 10 (`docs/StageArtifacts/Stage10.md`) then built all 9 of those features;
this ledger has been updated in place to reflect that rather than left to describe a stale,
Stage-9-only state.

## P0

| ID | Disposition | Detail |
|---|---|---|
| F-01 | Already fixed | Dashboard path-traversal fix (`isSafeName`+`resolveUnderRoot`) pre-existed on disk; independently re-verified (`go build`, `go test ./internal/dashboard/...`), kept |
| F-02 | Fixed | README/prd.md/trd.md Docker/`tc netem` overclaims corrected |
| F-03 | Fixed | Stage 8 + Stage 9 committed in logical increments |

## P1

| ID | Disposition | Detail |
|---|---|---|
| F-04 | Fixed (Stage 10, §10.8) | `internal/engine.ExperimentEngine` interface added; `VirtualEngine`/`RealEngine` both satisfy it (compile-time-checked); see `docs/StageArtifacts/Stage10.md` |
| F-05 | Fixed (Stage 10, §10.3) | `Scenario.Seed` widened to `replay.SeedTree` (Global/Traffic/Topology/Failure/Policy); `internal/provenance` adds `Manifest`/`ConfigHash`/`GitCommit`; `FinalResearchReport.md`'s manifest wording now matches what exists |
| F-06 | Fixed (Stage 10, §10.1) | `internal/traffic` adds `Generate` (Constant/RampUp/RampDown/Burst/FlashCrowd) and `ImportCombinedLog`/`ArrivalsFromLog` ("Fuze log" concretized as NCSA combined format) |
| F-07 | Fixed (Stage 10, §10.4) | `internal/challenge/metamorphic_test.go`: doubled-service-time (latency must not decrease) and halved-arrival-count (utilization must not increase) invariants, both against real `RunWorld`/`attribution` output |
| F-08 | Fixed (Stage 10, §10.2) | `internal/attribution` adds `CheckLittlesLaw`/`Utilization`/`UtilizationFromWorld`/`Explain`/`Compare`; `cmd/experiment-006d` refactored onto it |
| F-09 | Fixed (Stage 10, §10.5) | `cache.Cache.GetSWR`/`Config.StaleWindow` added; `topology.EdgeConfig.StaleWindow` wired into the real request path; real end-to-end test in `topology_test.go` |
| F-10 | Fixed (Stage 10, §10.7) | `internal/chaos`: hand-rolled flat-schema YAML parser, `ToFailureWindows` (virtual), `ToRealSchedule`/`RunReal` (real, via new `EdgeServer.SetDown`) |
| F-11 | Fixed | `internal/health/checker.go`: `stopCh` captured locally, not read as a struct field; `TestChecker_StartStopStart_OnlyOneActiveLoop` |
| F-12 | Fixed | `ReadHeaderTimeout` added to `internal/proxy/proxy.go`, `internal/topology/edge.go`, `cmd/dashboard/main.go` servers |
| F-13 | Fixed | `netsim.Conditions.Seed` field added, threaded through `EdgeServer`; `TestEdgeServer_NetworkConditions_SeededIsReproducible` |
| F-14 | Fixed | `TransportConfig.ResponseHeaderTimeout` added; `TestTrackedTransport_ResponseHeaderTimeout_BoundsHungBackend` |
| F-15 | Fixed | `cache.Key` gains optional extra components; edge.go folds in `X-Override-Status`/`X-Artificial-Delay-Ms`; `TestEdgeServer_Cache_OverrideStatusHeaderDoesNotCollide` |
| F-16 | Fixed | `proxy.go` checks `r.Context().Err()` before recording an app-level failure; `TestProxy_ClientCancellation_DoesNotRecordAppError` |
| F-17 | Already fixed (partial) + Disclosed | Stage8.md's same-distribution-holdout caveat pre-existed; the narrower "in-distribution generalization only" framing is the accepted, permanent scope (not a Stage 10 build item) |
| F-18 | Fixed | `Scenario.SameProtocol` + `ComparePolicies` added; 4 new tests in `compare_test.go` |
| F-19 | Disclosed | Git-history compression noted plainly in Stage9.md; not fixable (history is what it is) |
| F-20 | Fixed | README Build Sequence/Experiments tables corrected to show Stages 1–9 accurately |
| F-21 | Fixed | `cmd/dashboard` defaults to `127.0.0.1:7070`; `TestDefaultAddr_BindsLoopbackOnly` |

## P2

| ID | Disposition | Detail |
|---|---|---|
| F-22 | Fixed (Stage 10, §10.9) | `internal/tuning` gains a `Tuner` interface; `LHSTuner`/`BayesOptTuner` (hand-rolled GP+EI) added alongside `RandomSearchTuner`, all running through the identical `RunSearch` loop; `cmd/experiment-010a` reports honestly that neither beats Random Search on this project's own scenarios, matching Stage 8's own convergence finding |
| F-23 | Fixed (Stage 10, §10.6) | `internal/telemetry` adds a hand-rolled `Histogram` (HdrHistogram-style, logarithmic buckets) and `WriteText` (Prometheus text-exposition format); `cmd/proxy -metrics-addr` serves it live |
| F-24 | Fixed | `evaluateCandidate` gates the search loop on `ConfigSpace.Valid`; `TestEvaluateCandidate_RejectsOutOfSpaceConfigWithoutCallingEvaluate` |
| F-25 | Fixed (doc) | "six-signal"→"four-signal (six tunable parameters)" in README, prd.md, trd.md; appended correction in Stage7.md |
| F-26 | Fixed | `scoreUtilization` distinguishes absent-from-map vs. explicit-zero capacity; `TestAdaptiveSelector_ZeroCapacityIsPenalizedNotAveraged` |
| F-27 | Fixed | `scoreLatency` guards `ref<=0`; `SelectTarget` treats non-finite scores as disqualifying; `TestAdaptiveSelector_ZeroReferenceLatencyDoesNotProduceNaN` |
| F-28 | Fixed | `world.go` joins all scheduling errors (`errors.Join`), returns partial results; `TestRunWorld_MultipleSchedulingFailures_AllReportedAndPartialResultsKept` |
| F-29 | Fixed | `WorldResult.InFlightAtHorizon` added; `TestRunWorld_HorizonTruncation_InFlightRequestsAreCounted` |
| F-30 | Fixed | `cmd/proxy` gains a `-policy` flag wired to the same selector constructors every experiment shares |
| F-31 | Fixed | `AdaptiveSelector` scores under `RLock`, writes under `Lock` (was one `Mutex` for the whole call) |
| F-32 | Fixed | `.github/workflows/ci.yml` added (gofmt/vet/build/test on push/PR) |
| F-33 | Fixed | `.gitattributes` added (LF normalization) |
| F-34 | Fixed | `Coalescer.Do` signals (`wg.Done()`) before deleting the in-flight entry (canonical singleflight order); no dedicated timing test (documented as impractical to test reliably) |
| F-35 | Fixed | `RecordAppResult` now auto-registers, matching `RecordProbeResult`; `TestRegistry_RecordAppResult_AutoRegistersLikeRecordProbeResult` |
| F-36 | Fixed | `Registry.Deregister` added; `TestRegistry_Deregister_ResetsStateOnReRegister` |
| F-37 | Fixed | `outReq.ContentLength` copied from the inbound request; `TestProxy_ForwardsContentLength_NotAlwaysChunked` |
| F-38 | Fixed (doc) | Stage2.md's "production-grade" wording corrected |
| F-39 | Fixed (doc) | 006-E README notes the n=10/side Mann-Whitney sample-size caveat |
| F-40 | Fixed | New regression tests: `TestMannWhitneyU_LocationShiftOnly_DoesNotFlagAPureShapeDifference` (006-C), `TestStats_AreCumulativeSinceConstruction_NotPerPhase` (004-A) |
| F-41 | Fixed | `app.js`'s two remaining `innerHTML` interpolations replaced with DOM-API construction; verified live in-browser, no console errors |
| F-42 | Fixed | `ReadResultFile` bounds read size (`maxResultFileBytes`); `TestReadResultFile_RejectsOversizedFile` |
| F-43 | Fixed (doc) | README states `scripts/*.sh` require Git Bash/WSL |
| F-44 | Fixed | `Completions` added to `TestRunWorld_IdentityDeterministic` and `TestRunWorld_Isolation` |
| F-45 | Disclosed | Tuning eval-cache's near-zero practical hit rate for Random Search — noted in Stage9.md; no code change (cache is correct, just rarely useful for this algorithm) |

## P3

| ID | Disposition | Detail |
|---|---|---|
| F-46 | Fixed | Leftover `deployments/nginx-bench/nginx.conf;C` directory deleted |
| F-47 | Fixed (doc) | `internal/statistics/percentile.go` and `cmd/experiment-005h/main.go` cross-reference the third percentile implementation |
| F-48 | Fixed (doc) | trd.md §19 notes `Clock`'s `SleepUntil`→`Schedule` design change |
| F-49 | Fixed (doc) | research.md gains a status-note disclaimer distinguishing proposal from as-built |
| F-50 | Fixed | Trace event-type strings promoted to typed constants in `internal/replay/world.go` |
| F-51 | Fixed (doc) | `vtime.Trace`'s unbounded-memory design documented as a known, currently-inactive limit |
| F-52 | Fixed (doc) | `netsim.Transport.Conditions`'s unsynchronized-mutation contract documented explicitly |
| F-53 | Fixed | `Serve()` errors (other than `http.ErrServerClosed`) now logged in `proxy.go` and `edge.go` |
| F-54 | Fixed | `netsim.Transport.RoundTrip` uses `time.NewTimer`+`Stop()` instead of `time.After` |
| F-55 | Fixed | `cmd/dashboard/main.go` gains `signal.Notify`+`srv.Shutdown` |
| F-56 | Deferred | 008-D/008-F's implicit re-derivation-equals-persisted-ledger assumption — not hardened this stage (would require touching experiment binaries' file-I/O assumptions); documented as a known, low-priority gap |
| F-57 | Fixed | `TestComputeScores_UsesMeanLatencyNotP99` added — explicit, not incidental, regression protection |

## Summary

- **P0**: 3/3 resolved (1 already fixed, 2 fixed in Stage 9)
- **P1**: 18/18 resolved (7 built in Stage 10 — F-04 through F-10 — 1 partially-already-fixed, 10 fixed in Stage 9)
- **P2**: 24/24 resolved (2 built in Stage 10 — F-22, F-23 — 3 disclosed/deferred, 19 fixed in Stage 9)
- **P3**: 12/12 resolved (11 fixed in Stage 9, 1 deferred — F-56)

No finding is left in an undisclosed or unaddressed state, and as of Stage 10 no finding is left in
a merely-disclosed state either where building the feature was the actual ask (F-04 through F-10,
F-22, F-23) -- every one of those now has real, tested code. See `docs/StageArtifacts/Stage9.md` /
`docs/learning/009-stage9-audit-remediation.md` for the audit-remediation narrative and
`docs/StageArtifacts/Stage10.md` / `docs/learning/010-stage10-features.md` for the feature-build
narrative.
