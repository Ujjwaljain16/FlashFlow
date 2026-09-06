# Stage 16 — Final Research Synthesis, Reproducibility & Release

## Executive Summary

FlashFlow is a Go-based research laboratory for controlled experiments on distributed edge-routing
behavior. Across 16 stages, it built a dual-engine (deterministic virtual-time + real HTTP) platform,
six routing policies, finite-capacity queueing, a statistics and counterfactual-replay toolkit, and a
self-tuning parameter optimizer — then used that platform to run an actual, evolving research program.
The program's central discovery changed shape four times as evidence accumulated: from "does Adaptive
beat EWMA" (Stage 11), to "a finite-capacity reversal exists in one scenario" (Stage 12), to "ρ≈0.9
predicts a load-blind-vs-load-aware transition" (Stage 13), to that classification being directly
falsified by scale (Stage 14), to a measured, two-mechanism explanation — committed backlog for acute
collapse, sustained time-over-capacity for chronic collapse — that is precise about where it does and does
not hold (Stage 15). Stage 16 freezes this evidence, re-audits every strong claim against source, confirms
reproducibility from a clean checkout, and presents one flagship demonstration that shows the mechanism
directly rather than a leaderboard.

## Project Question

What measurable property predicts whether a routing policy escapes or becomes trapped in an
already-forming backlog, under heterogeneous edge conditions and finite serving capacity?

## What Was Built

A dual-engine (`internal/engine.VirtualEngine`/`RealEngine`) experimentation platform: TCP foundations,
an HTTP reverse proxy with health checking, six routing policies (round-robin, weighted-round-robin,
least-connections, EWMA, P2C, Adaptive — the last a four-signal Load/Latency/Cache/Cost combination), TTL
+ coalescing + stale-while-revalidate caching, a declarative YAML chaos/failure engine, an in-process
network-degradation simulator (`internal/netsim`, built in place of `tc netem`), a deterministic
virtual-time discrete-event engine with exogenous/endogenous counterfactual replay isolation, a
statistics toolkit (percentile, Mann-Whitney U, Cliff's Delta, bootstrap CI), three tuners (Random Search,
LHS, Bayesian Optimization) with disjoint development/holdout seed ranges, a queueing-theoretic
attribution engine, hand-rolled histogram/Prometheus-style telemetry, a local dashboard, and — the newest
addition, Stage 15 — `internal/backlog`, a queue-timeline reconstruction and committed-backlog
measurement package. Full detail: `docs/StageArtifacts/Stage16-ScopeFreeze.md`.

## What Was Actually Validated

Every item above is covered by passing tests; the platform-level claims (determinism, statistics
correctness, replay isolation, security fixes) are validated independently of the research findings. The
RESEARCH claims — which policy handles concentration well, why, and under what conditions — are validated
to the specific, bounded degree the `docs/StageArtifacts/Stage16-ClaimLedger.md` states for each one, no
further.

## Research Evolution

### Stage 11 → Stage 12

Stage 11's flat-model finding (EWMA sometimes beats Adaptive under heterogeneity) held, but a real-engine
load-tracking bug meant it couldn't yet be trusted at face value. Stage 12 fixed that bug and added a
genuinely new capability — finite per-target capacity — and re-running Stage 11's own scenario under it
produced a sharp reversal at Capacity=1 (EWMA: 15.90ms→131.06ms; Adaptive barely moved), confirmed across
12 seeds. Scoped explicitly to one scenario, not generalized.

### Stage 12 → Stage 13

Two independent sweep dimensions (scaling arrival rate with capacity; fixing capacity and varying rate)
both located the Capacity=1 reversal's true driver at offered ρ≈0.89-0.97 — a real, reproducible,
narrower-than-"any heterogeneity" mechanism. Extending to all six policies produced Stage 13's central
reframing: round-robin and EWMA (no live load signal) both collapsed; every policy with SOME load
signal — static or live — stayed nearly unaffected. "Load-blind vs. load-aware," not "EWMA vs. Adaptive."

### Stage 13 → Stage 14

Testing whether this survived beyond the one 3-target topology that produced it: the qualitative
phenomenon generalized (N=3/5/8, and a structurally different bimodal topology); the specific numbers did
not. Achieved rho DECREASED as target count grew even as severity WORSENED. Decisively: the full
six-policy sweep at N=8 found EWMA — load-aware by Stage 13's own classification — losing outright to
round-robin, confirmed across 10 independent seeds (Cliff's Delta=1.000). "Load-blind vs. load-aware" was
falsified as the deepest regime boundary.

### Stage 14 → Stage 15

Stage 14 proposed "concentration-proneness under already-committed queueing" as the replacement mechanism
but never measured it. Stage 15 built `internal/backlog` and measured committed backlog directly: it
achieved PERFECT rank agreement with severity across a target-count generalization test where peak rho
was badly misordered. It also revealed the mechanism was incomplete — round-robin's own canonical-scenario
failure had LOW committed backlog yet was chronically, permanently over capacity, a genuinely different
failure shape no single scalar metric captures.

## Final Mechanistic Model

### Acute Collapse

A policy locks onto a target via a signal that lags CURRENT state (EWMA's smoothed latency history;
Adaptive with its Load signal ablated), commits substantial work there between congestion onset and
material diversion, and that committed work explains the resulting tail-latency collapse. Best predictor:
committed backlog (perfect rank agreement across topology-size generalization; imperfect across workload
shape).

### Chronic Collapse

A policy never adapts its allocation at all (round-robin; weighted-round-robin when its weights go stale,
though not observed stale in this project's own tests) and permanently sends more work to a target than
that target's own capacity supports — present from the first request to the last, not triggered by any
single event. Best predictor: fraction-of-time-over-capacity.

### Concentration

Necessary for either collapse shape, not sufficient on its own — directly confirmed by intervention: a
policy reaching complete concentration (top1_share=1.000) at load far under capacity showed zero
congestion and zero committed backlog (`experiment-015b` F1).

### Committed Backlog

Operationally defined as dispatches to a target strictly between the moment it becomes congested
(depth/capacity exceeds an explicit threshold) and the moment a trailing window of routing decisions shows
that target's share drop below an explicit, target-count-scaled fraction. Measured via
`internal/backlog.AnalyzeDiversion`, built on nothing but existing dispatch/completion event data — no
changes to the core simulation model.

### Fraction Above Capacity

The fraction of the observed horizon during which a target's depth/capacity ratio exceeds 1.0. Captures
the CHRONIC failure shape committed backlog is blind to (round-robin: fraction=0.711, committed
backlog=4 — the two metrics correctly disagree because they measure different things).

## Policy Mechanisms

| Policy | Main Information Used | Typical Mechanism | Main Strength | Main Vulnerability |
|---|---|---|---|---|
| RR | None | Chronic fixed allocation | Perfectly predictable, no state to corrupt | Permanently overloads whichever target is slowest, regardless of moment-to-moment conditions |
| WRR | Static configured weights | Capacity-aware static split | Stable, even under a real burst, when weights are calibrated correctly | Cannot react to a change reality invalidates its weights for (not observed stale in this project, but structurally true) |
| LC | Current in-flight count | Anti-concentration, reacts to NOW | Diverts before a large backlog can form — the cleanest "unlock" mechanism tested | Only sees connection count, not actual latency or cost |
| EWMA | Smoothed latency history | History lock-in | Effective when conditions are genuinely stable | Its own past success becomes the reason it over-commits once a target degrades |
| P2C | Sampled comparison (2 random targets) | Bounded, structurally limited concentration | Never fully commits the way EWMA does — confirmed seed-independent (8/8 vs. 0/8) | Sampling itself provides no guarantee against a persistently unlucky comparison |
| Adaptive | Load + Latency + Cache + Cost (weighted) | Multi-signal, protected primarily by Load | Resistance to committed-backlog collapse traced specifically to its Load component | Can still over-commit if Load's own weight is reduced or under specific burst conditions — its own P99 was the worst of six policies in the canonical scenario |

## Adaptive Signal Evidence

Controlled ablation on the canonical scenario (`experiment-015c`), holding topology/workload/seed fixed
and zeroing one weight at a time: removing LOAD more than doubles committed backlog (86→206, worse than
EWMA's own 97) and shifts the bottleneck target from the slowest to the fastest, recreating an EWMA-style
lock-in. Removing LATENCY degrades things moderately (86→129) without changing which target locks up.
Removing CACHE is nearly a no-op (+4 backlog). Cost was not separately ablated because this project's own
usage of `AdaptivePolicyWithConfig` never populates capacity/cost `TargetWeights`, making Cost
structurally always-neutral already (confirmed by reading `internal/replay/policies.go` directly).

## P2C vs. EWMA Evidence

8 independent seeds with genuine arrival-stream jitter (`experiment-015b` F6): EWMA shows
committed_backlog>50 in 8/8 seeds; P2C-load in 0/8. A real, seed-independent structural distinction, not
a lucky draw in one scenario — sampling-based comparison structurally avoids the full commitment
smoothed-history comparison falls into.

## Cache-Affinity Unresolved Case

Stage 13's own finding (higher cache weight worsens interim latency but improves eventual recovery) was
tested directly against the committed-backlog mechanism in both the originally-hypothesized location (the
target absorbing hot-key traffic during a crash) and the natural alternative (a post-recovery rush back
to the just-recovered target). Neither explains it; the committed-backlog measurement itself becomes
unstable at the small scale involved. Left explicitly as an open question — not forced into either
candidate explanation.

## Virtual vs. Real Boundary

A validated real concurrency ceiling (`MaxConnsPerHost`, matched exactly to the virtual model's Capacity=1
assumption, itself validated via a raw atomic-counter probe before any policy comparison was trusted)
reproduces the concentration-proneness mechanism's direction at most tested stress levels. The
round-robin-beats-EWMA falsifier reproduces at below/near-ceiling levels and REVERSES at the highest
overload level tested — round-robin's fixed allocation eventually loses to EWMA's "wrong but at least
concentrated" lock-in once overload is extreme enough. A genuine, documented boundary, not smoothed over.

## Tuning Evidence

Random Search (Tuner v1) was sufficient for this project's own small (6-parameter) Adaptive weight space
and is the actual recommended/winning tuner (Stage 8: 62.5-70% composite-utility win-rate). LHS and
Bayesian Optimization (Tuner v2/v3) were both built and directly compared (`cmd/experiment-010a`); neither
meaningfully beat Random Search on this search space — the expected result of an already-converged small
space, not a wasted engineering effort. Development/Holdout separation is enforced by disjoint seed ranges
at construction time (`internal/tuning/scenario.go`), not a runtime check; Holdout was touched only after
winner selection, throughout.

## Reproducibility

### Levels

- **R0**: description only, no runnable artifact.
- **R1**: can be rerun manually with some manual setup.
- **R2**: deterministic given seed/config, but not automated into a script (or, for the real engine,
  reproducible in DIRECTION but not in exact numbers, due to genuine OS/timing variance).
- **R3**: automatically reproduced from a committed script; virtual-engine results are byte-identical
  except for a timestamp field.
- **R4**: independently rerun across multiple seeds, already demonstrated in this repository.

### Table

| Result | Level | Exact Command | Seed/Config | Evidence |
|---|---|---|---|---|
| Stage 15 committed-backlog cross-topology ranking | R3 | `./scripts/reproduce-stage15.sh` (runs `experiment-015e`) | Fixed seeds `14203`/`14205`/`14208`/`14300` | `Stage15.md` Predictor Search section |
| Stage 15 P2C vs. EWMA 8-seed separation | R4 | `go run ./cmd/experiment-015b` | Seeds 15600-15607, JitterFraction=0.3 | `Stage15.md`, this document's P2C/EWMA section |
| Adaptive Load-signal ablation | R3 | `go run ./cmd/experiment-015c` | Seed 15000 (identical to `015a`'s own canonical run) | `Stage15.md` Policy Mechanism Comparison |
| Flagship scenario (all 6 policies, 3 seeds) | R4 | `./scripts/reproduce-flagship.sh` | Seeds 16000/16001/16002, JitterFraction=0.3 | `Stage16-FlagshipDemo.md`; confirmed byte-identical (except timestamp) on independent rerun during this stage's own audit |
| Real-engine falsifier direction (`experiment-015f`) | **R2** | `go run ./cmd/experiment-015f` | `MaxConnsPerHost=1`, requests 30/75/400 | Direction reproduces at 1-2 of 3 levels depending on run — confirmed NOT byte-identical across independent reruns during this stage's own audit (18 of ~36 data lines changed; the falsifier reproduced in only 1/3 levels on one rerun vs. 2/3 originally). Real OS scheduling and wall-clock timing are the disclosed cause, not a bug |
| Stage 8 tuning win-rate | R3 | `go run -buildvcs=true ./cmd/experiment-008C` (holdout evaluation) | Fixed development/holdout seed ranges | `Stage8.md` |
| Statistics toolkit correctness | R4 | `go test ./internal/statistics/...` | N/A (hand-computed reference values, not randomized) | `Stage6.md`, `SCIENTIFIC_VALIDITY.md` |

A result is never called "fully reproducible" merely because its code path is deterministic — the real
engine's own experiments are the concrete counterexample proving this distinction matters, not a
hypothetical one.

### Seed / Provenance Audit

Hierarchical `SeedTree` (Global/Traffic/Topology/Failure/Policy axes) prevents the seed-leakage bug Stage
11 found (Topology and Failure sharing one seed). `internal/netsim`'s own RNG is threaded from a
configured seed, not wall-clock, after Stage 9's fix. Map iteration is never relied on for ordering in any
hot path that affects results (`internal/backlog.ComputeConcentration` sorts before use; dispatch/
completion event merges sort explicitly). Known, disclosed limitations, not solved this stage: most
experiment binaries after `010a` still write ad hoc result JSON rather than a full provenance manifest;
`ConfigHash`/`ScenarioSetHash` cover configuration identity, not full causal provenance of a specific run;
timestamps in result JSON make BYTE-identical artifacts impossible across reruns even when every
SEMANTIC field is identical (confirmed directly: the only diff in a virtual-engine rerun is the
`timestamp` field).

## Final Challenge Suite

`internal/backlog`'s own 9 hand-computed tests (8 from Stage 15, 1 new this stage —
`TestAnalyzeDiversion_CongestionButNeverDiverts`, guarding the exact "static policy never diverts" bug
this stage's own flagship experiment surfaced) join the existing `internal/challenge` metamorphic suite
(doubled-service-time and halved-arrival-count invariants, each confirmed to actually catch an injected
violation, not merely pass vacuously). No new synthetic certification suite was added — every test added
this stage exists because a specific defect was found, per this stage's own instruction against
manufacturing coverage.

## Final Security/Robustness Audit

Independently re-verified (not assumed) every issue Stage 9's own audit fixed, category by category:
path traversal, URL parsing, header forwarding/stripping, request cancellation, goroutine leaks, health-
checker shutdown, response timeouts, body closing, cache-key collisions, shared mutable state, dashboard
exposure, filesystem reads. **All 12 categories: still fixed, still covered by a passing regression
test.** One new, real issue was found and fixed this stage: `internal/topology/origin.go`'s
`OriginServer` — a fourth real, network-reachable HTTP server used by every real-engine experiment — had
neither `ReadHeaderTimeout` nor logged `Serve()` errors, the exact fix class Stage 9 gave the other three
servers (proxy, edge, dashboard) but never enumerated this file. Fixed identically to the existing
pattern. A second, low-severity gap was noted and left as a disclosed limitation, not fixed: neither the
proxy nor the edge server injects `X-Forwarded-For`/`X-Forwarded-Host` headers, so origin-side code has no
way to recover the original client's address — never a PRD requirement, but worth naming.

## Flagship Experiment

`cmd/experiment-016-flagship`, reusing Stage 15's own canonical scenario (5 heterogeneous targets,
Capacity=1, FlashCrowd workload, 8s horizon) unmodified in topology, run across 3 independent seeds. Full
walkthrough: `docs/StageArtifacts/Stage16-FlagshipDemo.md`. Chosen because it is the one scenario showing
all six policies' distinct mechanism classes side by side, including the negative result (Adaptive's own
worst-of-six P99), not because it produces the largest-looking number. Building it caught two real issues
before they could mislead: a diversion-share threshold calibrated for 3-target topologies was too lenient
at 5 targets (fixed by scaling to the target count, reproducing the existing 3-target value exactly), and
a static policy's "never diverted" case was defaulting to a misleading zero/false pair instead of an
explicit N/A.

## Negative Results

- Adaptive's own P99 was the worst of all six policies tested in the canonical scenario, in every one of
  3 independent seeds.
- Round-robin's failure mode has LOW committed backlog yet is the most chronically over-capacity of the
  six policies — committed backlog alone would have called round-robin "fine."
- Committed backlog's cross-workload rank agreement is imperfect (distance 2, not 0).
- The cache-affinity interim-latency effect remains unexplained by this mechanism in both locations
  tested.
- The real-engine falsifier reverses at extreme overload.
- No alpha value rescues EWMA at the main capacity boundary (unlike its real, causal role in H2).

## Claims Supported

See `docs/StageArtifacts/Stage16-ClaimLedger.md` for the full, evidence-linked list (32 rows). Headline
supported claims: the six routing policies and caching layer are correctly implemented; the virtual
engine is deterministic (same machine/toolchain); committed backlog is the strongest available predictor
of acute collapse, with perfect rank agreement across topology-size generalization; Adaptive's resistance
to collapse traces specifically to its Load signal; P2C and EWMA are mechanistically distinct in a
seed-independent way; a validated real ceiling reproduces the mechanism's direction in most tested
conditions.

## Claims Limited

Rho as a boundary predictor (real, but narrow-scenario); committed backlog across workload shape
(real, but imperfect); the real-engine falsifier's direction (holds at most, not all, tested levels);
health-registry exclusion latency (real, bounded by probe interval).

## Claims Retired

LRU cache eviction as implemented (never built); rho as a sufficient severity predictor at scale;
concentration alone as sufficient for collapse; "load-blind vs. load-aware" as the deepest regime
boundary; "Adaptive is safe" as an unqualified claim; alpha as a general explanation of the main
capacity-boundary collapse (its causal role is specific to H2).

## Unresolved Questions

Whether concentration-proneness is a fixed property of a policy's design or topology-dependent; whether a
compact combination of committed backlog and fraction-above-threshold predicts both collapse shapes at
once; what actually explains the cache-affinity interim-latency effect; whether the real-engine
extreme-overload reversal generalizes beyond the one topology tested; whether P2C's and WRR's shared
lock-in resistance reflects the same or different underlying mechanisms.

## Known Model Boundaries

FlashFlow's virtual model has no packet-level realism, no cross-hardware determinism guarantee, and a
finite-capacity model that is a genuine simplification (single FIFO queue per target, not a real
multi-resource scheduler). Its real engine is subject to genuine OS scheduling noise, disclosed and
measured, not hidden. Its cache-affinity mechanism remains unexplained. Its predictor (committed backlog)
is strong across topology size and weak-but-real across workload shape. It has never been run against a
real production traffic distribution.

## Final Release State

Working tree clean at commit (see verdict below); `go test ./...` passing across 23 packages;
`gofmt`/`go vet`/`go build` all clean; `scripts/final-validation.sh` passing; both reproduction scripts
independently confirmed to work from the current tree.

**Clean-checkout verification, performed as the mandatory last step before release** (Section 14 of this
stage's own charter): `git clone`d the repository into a completely separate directory (no shared
filesystem state, no untracked local files, no IDE artifacts) at the commit immediately preceding this
one. From that isolated clone: `go build ./...` succeeded; `go test ./...` passed across all 23 packages;
`./scripts/reproduce-flagship.sh` reproduced the flagship result byte-for-byte identical (except the
timestamp field) to every prior run reported in this document and in
`docs/StageArtifacts/Stage16-FlagshipDemo.md`. The clean checkout was deleted afterward — nothing about
this project's reproducibility depends on any file that isn't tracked in git.

## Conclusion

FlashFlow set out to compare routing policies and ended up discovering that the comparison itself was
underspecified — first by target count, then by the classification used to explain the results, and
finally by treating "collapse" as one phenomenon when it is at least two. That sequence of corrections,
each driven by a specific, falsifiable experiment rather than intuition, is this project's actual
deliverable. The final mechanism is not a universal law; it is a precisely-scoped, replicated, and
adversarially-tested explanation that says exactly where it stops working.

---

## Stage 16 Verdict

```text
STAGE 16 VERDICT:
READY WITH DOCUMENTED LIMITATIONS

PROJECT STATUS:
FlashFlow is a complete, internally consistent research laboratory whose 16-stage arc is preserved as a
sequence of increasingly precise corrections rather than a single tidy narrative. Every strong claim has
been re-audited against source and evidence; two real documentation overclaims and one real security gap
(OriginServer's missing timeout/error-logging) were found and fixed during this stage's own audit, not
before. The repository is reproducible from a clean checkout for every virtual-engine result and honestly
scoped for its one real-engine result.

FINAL RESEARCH QUESTION:
What measurable property predicts whether a routing policy escapes or becomes trapped in an
already-forming backlog under finite capacity?

BEST-SUPPORTED ANSWER:
Two distinct, complementary properties, not one: committed backlog (work already dispatched between
congestion onset and material diversion) for acute over-commitment, and fraction-of-time-over-capacity
for chronic, structural under-provisioning. Concentration is necessary for either but sufficient for
neither.

CENTRAL MECHANISM:
concentration -> capacity pressure -> (acute: committed backlog -> tail collapse) OR (chronic: sustained
time-over-capacity -> tail collapse).

ACUTE COLLAPSE:
Best explained by committed backlog; confirmed with perfect rank agreement across a target-count
generalization test (N=3/5/8, plus bimodal) where peak rho is badly misordered.

CHRONIC COLLAPSE:
Best explained by fraction-of-time-over-capacity; round-robin's own canonical-scenario failure is the
clean example (committed backlog=4, fraction-above-capacity=0.711, never drains).

CONCENTRATION:
Necessary, not sufficient -- directly confirmed by intervention (complete concentration at low load
produced zero congestion and zero committed backlog).

COMMITTED BACKLOG:
Operationally defined, measured, validated as the strongest predictor of acute collapse; imperfect (not
zero) across workload-shape generalization.

FRACTION ABOVE CAPACITY:
The correct complementary metric for chronic collapse, which committed backlog is blind to.

ADAPTIVE:
Multi-signal; resistance to collapse traced specifically to its Load component via controlled ablation
(removing it more than doubles committed backlog, worse than EWMA's own). Its own P99 was nonetheless the
worst of six policies in the canonical scenario, across all three independently-seeded reproductions --
"Adaptive is safe" is retired as an unqualified claim.

EWMA:
Smoothed-history lock-in; its own past success is the reason it over-commits once a target degrades.
Loses to round-robin at scale (Stage 14's falsifier), confirmed here across 8 independent seeds showing
committed_backlog>50 in 8/8.

P2C:
Sampling-based comparison structurally avoids full lock-in -- a real, seed-independent mechanistic
distinction from EWMA (committed_backlog>50 in 0/8 seeds), not a lucky scenario.

LC:
Reacts to current in-flight count; the cleanest, fastest "unlock" mechanism of the six tested, consistent
low committed backlog across every scenario in this project.

WRR:
Static, capacity-aware allocation; safe when weights match reality (as they do in every scenario tested
here). Never registers a "diversion" event at all, since its allocation never changes -- correctly
reported as N/A for committed backlog, not zero.

RR:
No adaptation at all; chronic, not acute, failure -- permanently overloads whichever target is slowest,
independent of any single event.

VIRTUAL VS REAL:
The mechanism's direction reproduces at most tested real-engine stress levels; reverses at the most
extreme overload level tested. A validated ceiling (matched to the virtual model's own capacity
assumption) was required before any reproduction could be trusted at all.

TUNING:
Random Search is sufficient for and remains the recommended tuner for this project's own small parameter
space; LHS and Bayesian Optimization were built, tested, and confirmed not to meaningfully improve on it
-- the expected result of an already-converged search space, documented rather than treated as wasted
work.

REPRODUCIBILITY:
Virtual-engine results reproduce byte-for-byte except a timestamp field, confirmed directly this stage
for both Stage 15's own experiments and the new flagship. The one real-engine experiment does NOT
reproduce byte-for-byte and is classified at a lower reproducibility level (R2, direction-only) -- this
distinction is now measured, not asserted.

FLAGSHIP RESULT:
The canonical 5-target, Capacity=1, FlashCrowd scenario, run across 3 independent seeds: EWMA and
Adaptive both show severe, often-non-draining acute collapse; round-robin shows a different, chronic
failure; weighted-round-robin, least-connections, and P2C-load stay comparatively mild in every seed.

STRONGEST POSITIVE RESULT:
Committed backlog's perfect rank agreement with severity across the target-count generalization test,
where peak rho is badly misordered.

STRONGEST NEGATIVE RESULT:
Adaptive's own P99 was the worst of all six policies tested in the canonical scenario, in every
independently-seeded reproduction -- mean latency alone would have hidden this.

MOST IMPORTANT FALSIFICATION:
"Load-blind vs. load-aware routing is the deepest regime boundary" (Stage 13's own central claim),
falsified by EWMA losing to round-robin at scale (Stage 14), confirmed across 10 independent seeds.

CLAIMS SUPPORTED:
Core platform correctness (routing policies, caching, health, virtual-time determinism, statistics,
counterfactual replay); committed backlog predicts acute collapse with perfect topology-size rank
agreement; Adaptive's Load signal is its specific protective mechanism; P2C/EWMA mechanistic distinction;
real-ceiling directional reproduction in most tested conditions; Random Search sufficiency for this
project's own tuning space.

CLAIMS LIMITED:
Rho as a boundary predictor (real, narrow-scenario); committed backlog across workload shape (real,
imperfect); real-engine falsifier direction (holds at most, not all, tested levels); health-registry
exclusion latency (bounded by probe interval).

CLAIMS RETIRED:
LRU cache eviction as implemented; rho as sufficient at scale; concentration alone as sufficient;
load-blind-vs-load-aware as the deepest boundary; "Adaptive is safe" unqualified; alpha as a general
explanation of the main-boundary collapse.

UNRESOLVED:
Whether concentration-proneness is policy-intrinsic or topology-dependent; a single combined predictor
for both collapse shapes; the cache-affinity interim-latency mechanism; generalization of the real-engine
extreme-overload reversal; whether P2C and WRR share one underlying lock-in-resistance mechanism.

KNOWN MODEL BOUNDARIES:
No packet-level realism; no cross-hardware determinism guarantee; a single-FIFO-queue-per-target
capacity model, not a real multi-resource scheduler; real-engine results subject to genuine OS timing
noise; never run against real production traffic.

FILES:
internal/backlog/{backlog.go,backlog_test.go} (Stage 15 + 1 new Stage 16 test);
internal/topology/origin.go (security fix); cmd/experiment-016-flagship/main.go;
docs/StageArtifacts/{Stage16.md,Stage16-ScopeFreeze.md,Stage16-ClaimLedger.md,
Stage16-ResearchSynthesis.md,Stage16-FlagshipDemo.md,Stage16-ArtifactIndex.md};
docs/learning/016-stage16-final-synthesis.md; scripts/{reproduce-stage15.sh,reproduce-flagship.sh};
README.md; prd.md; trd.md.

TESTS:
go test ./... -- 23 packages, all passing; internal/backlog now has 9 hand-computed tests (8 from Stage
15, 1 new); full existing suite (internal/proxy, internal/health, internal/dashboard, internal/challenge,
etc.) re-verified passing during this stage's own security re-audit.

REPRODUCTION COMMANDS:
./scripts/final-validation.sh; ./scripts/reproduce-stage15.sh; ./scripts/reproduce-flagship.sh.

FINAL COMMIT:
See `git log -1` at release time; this document is committed alongside the final release commit
("research: finalize FlashFlow study and release evidence").

FINAL RELEASE STATE:
Working tree clean; every source file, test, experiment command, result artifact, stage artifact,
learning note, and script required to understand and reproduce this project's research story is tracked
in git as of the final release commit.
```
