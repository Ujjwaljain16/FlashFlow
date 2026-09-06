# Stage 16 — Final Artifact Index

Everything an external reviewer needs to locate evidence for any claim this project makes, organized by
stage. Every "Evidence Level" uses the same scale defined in `docs/StageArtifacts/Stage16.md`'s
reproducibility section (R0-R4).

## Stages 1-9: Foundation

| Stage | Artifact | Source Packages | Key Experiments | Evidence Level |
|---|---|---|---|---|
| 1 | `docs/StageArtifacts/Stage1.md`, `docs/learning/001-tcp-foundations.md` | `internal/tcp` | `experiments/001-tcp-connection-lifecycle/` (120-run benchmark) | R2 |
| 2 | `docs/StageArtifacts/Stage2.md`, `docs/learning/002-http-reverse-proxy.md` | `internal/proxy`, `internal/health` | `experiments/002-http-reverse-proxy/` | R2 |
| 3 | `docs/StageArtifacts/Stage3.md`, `docs/learning/003-routing-policies.md` | `internal/proxy/{round_robin,weighted_round_robin,least_connections}.go` | `experiments/003-routing-policies/` | R2 |
| 4 | `docs/StageArtifacts/Stage4.md`, `docs/learning/004-caching-failures.md` | `internal/cache` | `experiments/004-caching-failures/` | R2 |
| 5 | `docs/StageArtifacts/Stage5.md`, `docs/learning/005-virtual-time.md` | `internal/vtime`, `internal/clock` | `experiments/005-virtual-time/` | R3 |
| 6 | `docs/StageArtifacts/Stage6.md`, `docs/learning/006-statistics-queueing.md` | `internal/statistics` | `experiments/006-statistics-queueing/` | R3 |
| 7 | `docs/StageArtifacts/Stage7.md`, `docs/learning/007-adaptive-routing-replay.md` | `internal/proxy/{ewma,p2c,adaptive}.go`, `internal/replay` | `experiments/007-adaptive-replay/` | R3 |
| 8 | `docs/StageArtifacts/Stage8.md`, `docs/learning/008-tuning-final-validation.md` | `internal/tuning` | `experiments/008-tuning-validation/` | R3 |
| 9 | `docs/StageArtifacts/Stage9.md`, `docs/learning/009-stage9-audit-remediation.md`, `docs/audit/*` | Security/robustness fixes across `internal/proxy`, `internal/health`, `internal/netsim`, `internal/cache`, `internal/dashboard` | Commits `bd98246`, `014824b`, `45791c1`, `8f224c0`, `5200c1b` | R2 (regression-tested, re-verified in Stage 16) |

## Stage 10: Feature Completion

| Artifact | Source Packages | Key Experiments | Evidence Level |
|---|---|---|---|
| `docs/StageArtifacts/Stage10.md`, `Stage10-Plan.md`, `Stage10DemoValidation.md`, `docs/learning/010-stage10-features.md` | `internal/traffic`, `internal/chaos`, `internal/provenance`, `internal/tuning/{lhs,bayesopt}.go`, `internal/telemetry`, `internal/attribution`, `internal/engine`, `internal/challenge` | `experiments/010-stage10-features/`, `cmd/experiment-010a` | R3 |

## Stage 11: Research Validation

| Artifact | Source | Key Experiments | Evidence Level |
|---|---|---|---|
| `docs/StageArtifacts/Stage11.md`, `docs/learning/011-stage11-research-validation.md` | — | `experiments/011-research-validation/` (Programs A-H, `cmd/experiment-011a`-`011h`), `INDEX.json` | R3 |
| Commit | `2072d31` (close-out) | | |

## Stage 12: Model Fidelity

| Artifact | Source | Key Experiments | Evidence Level |
|---|---|---|---|
| `docs/StageArtifacts/Stage12.md`, `Stage12-Plan.md`, `docs/learning/012-stage12-model-fidelity-and-control.md` | `replay.TargetProfile.Capacity`/`ServiceTimeSchedule`, `replay.Trackers` | `experiments/012-model-fidelity/`, `cmd/experiment-012a`-`012f` | R3 (12-seed statistical confirmation, `012e`) |
| Commit | `069e454` (close-out) | | |

## Stage 13: Regime Discovery

| Artifact | Source | Key Experiments | Evidence Level |
|---|---|---|---|
| `docs/StageArtifacts/Stage13.md`, `docs/learning/013-stage13-regime-discovery.md` | — | `experiments/013-regime-discovery/`, `cmd/experiment-013a`-`013k` | R3-R4 (10-seed confirmation, `013j`) |
| Commit | `fdc9377` (close-out) | | |

## Stage 14: Scale & Topology Generalization

| Artifact | Source | Key Experiments | Evidence Level |
|---|---|---|---|
| `docs/StageArtifacts/Stage14.md`, `docs/learning/014-stage14-scale-and-topology.md` | `engine.RealExperimentConfig.MaxConnsPerHost` | `experiments/014-scale-topology/`, `cmd/experiment-014a`-`014i` | R4 (10-seed confirmation, `014i`) |
| Commit | `e7e2c65` (close-out) | | |

## Stage 15: Mechanism Identification

| Artifact | Source | Key Experiments | Evidence Level |
|---|---|---|---|
| `docs/StageArtifacts/Stage15.md`, `Stage15-Plan.md`, `docs/learning/015-stage15-mechanism-identification.md` | **`internal/backlog`** (new package: `Timeline`, `ComputeConcentration`, `FindFirstCongestionOnset`, `FindPeakEpisodeCongestionOnset`, `AnalyzeDiversion`), 8 hand-computed unit tests | `experiments/015-mechanism-identification/`, `cmd/experiment-015a`-`015f` | R3 (virtual, `015a`-`015e`, byte-identical reruns confirmed in Stage 16); R2 (real engine, `015f`, direction-only) |
| Commit | `43dea50` (close-out) | | |

## Stage 16: Final Synthesis, Reproducibility & Release

| Artifact | Source | Key Experiments/Scripts | Evidence Level |
|---|---|---|---|
| `docs/StageArtifacts/Stage16.md` (main artifact), `Stage16-ScopeFreeze.md`, `Stage16-ClaimLedger.md`, `Stage16-ResearchSynthesis.md`, `Stage16-FlagshipDemo.md`, `Stage16-ArtifactIndex.md` (this file), `docs/learning/016-stage16-final-synthesis.md` | `internal/topology/origin.go` (security fix: `ReadHeaderTimeout`, logged `Serve()` errors) | `cmd/experiment-016-flagship`, `experiments/016-final-synthesis/`, `scripts/reproduce-stage15.sh`, `scripts/reproduce-flagship.sh` | R3 (flagship, virtual engine, byte-identical reruns confirmed) |

## Source Packages Reference (all 23)

`attribution`, `backlog` (Stage 15), `cache`, `challenge`, `chaos`, `clock`, `dashboard`, `engine`,
`health`, `httpx`, `netsim`, `provenance`, `proxy`, `replay`, `statistics`, `tcp`, `telemetry`,
`topology`, `traffic`, `transport`, `tuning`, `vtime` — all under `internal/`.

## Full Test Suite

`go test ./...` — 23 packages, all passing as of this stage's HEAD commit (see `docs/StageArtifacts/Stage16.md`'s
"Final Release State" for the exact commit hash and test count at freeze time).

## Scripts

| Script | Purpose |
|---|---|
| `scripts/final-validation.sh` | Full release-readiness gate (formatting, vet, tests, deterministic replay, statistical/tuning validation, challenge suite) |
| `scripts/reproduce-stage15.sh` | Reruns every Stage 15 experiment in order |
| `scripts/reproduce-flagship.sh` | Reruns the Stage 16 flagship demonstration |
| `scripts/demo-stage10.sh` | Stage 10 feature demo |
| `scripts/nginx-reference-benchmark.sh` | External reference-point benchmark (not a FlashFlow claim) |

## Audit Trail

| Document | Purpose |
|---|---|
| `docs/audit/ARCHITECTURE_AUDIT.md`, `FINAL_AUDIT.md`, `FINDINGS.md`, `REQUIREMENT_TRACEABILITY.md`, `RESOLUTION.md`, `SCIENTIFIC_VALIDITY.md`, `SECURITY_AND_OPERATIONS.md` | The post-Stage-8 adversarial audit that drove Stage 9 (remediation) and Stage 10 (feature completion) |

An external reviewer with no prior context can start at `README.md`, follow it to
`docs/StageArtifacts/Stage16.md` for the final verdict, then use this index to locate the exact source,
test, or experiment backing any specific number or claim.
