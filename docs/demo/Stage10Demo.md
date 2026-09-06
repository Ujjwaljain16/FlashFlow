# Stage 10 Demo — FlashFlow: A Controlled Systems Experiment, Not Just a Dashboard

## Demo Objective

Show, in under 5 minutes, that FlashFlow is a research laboratory for studying adaptive edge
routing — not a load balancer with a pretty UI. The demo runs one real, controlled experiment
(heterogeneous edges, a real workload, an injected failure), compares two routing policies under
byte-for-byte identical exogenous conditions, proves the resulting difference is real (not noise) via
counterfactual replay, explains the mechanism via queueing attribution, and reproduces the whole
thing on demand via a real provenance manifest.

Every number in this document was produced by actually running `cmd/demo-stage10` on this machine —
not hand-written, not estimated. Re-run it yourself with the commands below; the numbers will match.

## Prerequisites

- Go 1.23.3 (or compatible)
- No Docker, no external services, no network access required for the primary demo
- Git Bash or WSL if using `scripts/demo-stage10.sh` (a bash script); the underlying `go run` command
  works from any shell
- Working directory: repository root (`D:\Projects\Hack\FlashFlow` in this environment)

## Environment

Confirmed, not assumed, during preparation of this demo:

- `go build`/`go vet`/`go test ./...`/`scripts/final-validation.sh` all pass on the current tree
  before and after adding this demo (see Final Technical Verification below)
- The primary demo is 100% virtual-time — no real HTTP servers, no ports, no Docker
- The secondary/backup demo (Scene-equivalent, real engine) uses ports `8000`/`8081`/`9090` by
  default in the exact commands below — pick different ports with `-addr`/`-metrics-addr` if those
  are in use
- `demo/output/` is this demo's only generated-artifact directory; nothing under `experiments/` (this
  project's real historical research data) is touched by anything in this document

## Setup

```bash
git status          # confirm you're not about to lose uncommitted work when resetting demo/output/
```

No build step is required — `go run` compiles and runs in one step, matching every other command in
this project's own README.

## Reset Procedure

```bash
scripts/demo-stage10.sh          # resets demo/output/ and runs once
# or, without resetting prior output:
scripts/demo-stage10.sh --no-reset
```

The reset removes **only** `demo/output/` (this demo's own manifest directory) and recreates it
empty. It never touches `experiments/`, git-tracked files, or anything outside `demo/output/`. Run it
as many times as needed — every run is independent and leaves the repository in the same state it
started in, aside from the freshly-written `demo/output/stage10-demo/manifest.json`.

## Primary Scenario

**Story, exactly as observed by running the experiment (not decided in advance):**

```text
Problem:
Three edge targets with genuinely different capacities (20ms / 15ms / 60ms service time).
edge-c's own fixed service time alone makes it a bottleneck under load, independent of any failure.

Experiment:
300 requests over a 3-second window (internal/traffic.Generate, Constant pattern, 50% hot-key
concentration) while edge-b -- the FASTEST target when healthy -- crashes at t=1s and recovers
at t=2s (internal/chaos, a plain 4-line YAML schedule).

Question:
Under these identical conditions, does Adaptive actually route around the resulting overload
better than Round Robin?

Run:
Round Robin (baseline) vs Adaptive (counterfactual), via internal/engine's ExperimentEngine
Run/Replay against the byte-for-byte identical Scenario.

Observation (actual, from a real run):
  round-robin   mean=34.35ms  p99=60.00ms  rejected=0
  adaptive      mean=28.73ms  p99=60.00ms  rejected=0
  -> Adaptive's mean latency is 16.4% lower.
  -> The two policies' traces are IDENTICAL through event #8, then diverge --
     proof this is a real routing-decision effect, not two runs that quietly saw
     different conditions.

Explanation (actual, from internal/attribution):
  edge-c: rho=1.989 (round-robin) vs rho=1.337 (adaptive) -- overloaded under BOTH,
  but meaningfully less so under Adaptive.
  edge-b: rho=0.287 (round-robin) vs rho=0.429 (adaptive) -- Adaptive shifts more
  load here once it recovers, since it now has spare capacity.
  Mechanism: Adaptive doesn't eliminate edge-c's overload (no routing policy can,
  once offered load exceeds a fixed target's capacity) -- it shifts a meaningful
  share of the excess onto targets with headroom, lowering mean latency.

Reproducibility:
Same Scenario + same seeds -> zero trace divergence across 3 separate process runs
(confirmed: two runs with the demo's own seed, one after a full demo/output/ wipe).

Takeaway:
The value isn't "Adaptive won" -- it's that the exogenous conditions were held fixed,
the resulting difference was proven real (not noise), the mechanism was explained
from real telemetry, and the whole experiment reproduces byte-for-byte on demand.
```

## Exact Commands

```bash
# Primary demo (virtual engine, ~2 seconds, no network/Docker required)
go run -buildvcs=true ./cmd/demo-stage10

# Or, using the reset helper:
scripts/demo-stage10.sh
```

## Expected Output

The full, real output is reproduced in "Primary Scenario" above (Scenes 4-6's numbers). The complete
console transcript runs to about 70 lines across 7 clearly-labeled scenes; total wall-clock time is
1.6-1.9 seconds despite simulating 3.5 seconds of scenario time (virtual time, not real time).

## Dashboard Steps (optional visual support)

The dashboard's own canonical Playground scenario (`internal/dashboard.PlaygroundScenario`) is **not
literally the same run** as `cmd/demo-stage10` — same story (3 heterogeneous edges, edge-b fails and
recovers, RR vs Adaptive), different traffic-generation code path and a different key rotation. Use
it as **visual reinforcement of the same concept**, not as "the same experiment" — say so explicitly
if using it live, to avoid the exact kind of overclaim this project's own Stage 9 audit was built to
catch.

```bash
go run ./cmd/dashboard    # http://127.0.0.1:7070
```

Steps: Playground tab → policy=`round-robin` → Run → note the topology/metrics → policy
dropdowns → Compare (`round-robin` vs `adaptive`) → observe the real "First point of divergence"
banner. This is the same `replay.FirstDivergence` mechanism the CLI demo's Scene 4 uses, on a
closely analogous scenario.

## Evidence Locations

- `demo/output/stage10-demo/manifest.json` — real provenance manifest from the most recent run
  (seed tree, configuration hash, git commit/dirty state)
- Console output itself — every number is printed as it's computed, not read back from a file
- `internal/engine/consistency_test.go`, `internal/attribution/*_test.go`,
  `internal/chaos/*_test.go`, `internal/traffic/*_test.go` — the underlying capabilities' own
  regression tests, independently re-verifiable
- `docs/StageArtifacts/Stage10DemoValidation.md` — the adversarial demo-readiness audit this demo's
  design was informed by

## Recording Script

Target: **3-5 minutes**, terminal-only (no window-switching required for the primary path; the
dashboard is optional and separate).

| Timestamp | Screen action | Command / interaction | What is shown | What to say | Why it matters |
|---|---|---|---|---|---|
| 0:00-0:20 | Terminal, idle prompt | (none yet) | Empty terminal | "I wanted to know whether FlashFlow's adaptive router actually routes around a real problem — an overloaded, occasionally-failing target — better than plain round robin. Not benchmark it once and eyeball it — prove it, explain it, and reproduce it." | Opens with the research question, not the tool. |
| 0:20-0:35 | Run the command | `go run -buildvcs=true ./cmd/demo-stage10` | Scene 1 prints: 3 targets, service times | "Three edges, deliberately different speeds — one is 3-4x slower than the others. That slow one alone is going to be a bottleneck under enough load." | Establishes the controlled variable (topology) before anything runs. |
| 0:35-1:00 | (same run continues) | — | Scene 2: traffic generation | "This isn't a hardcoded request list — it's generated: 300 requests over 3 seconds, half hitting one cache key so we can also see the router's cache-affinity signal." | Shows the traffic generator is real, not decorative. |
| 1:00-1:20 | (same run continues) | — | Scene 3: chaos YAML printed, then compiled | "The failure is declarative — four lines of YAML saying edge-b crashes at 1 second and recovers at 2. edge-b happens to be the *fastest* target when healthy, so losing it removes the one thing that could otherwise absorb the overflow." | Shows the environmental change as data, not a hidden code branch. |
| 1:20-2:10 | (same run continues) | — | Scene 4: policy comparison table + divergence proof | "Same scenario, same seeds, same failure — only the policy changes. Adaptive's mean latency comes out 16% lower. And here's the proof it's real: the two policies' event traces are byte-identical up to event 8, then diverge — so this isn't two runs that quietly saw different conditions." | The counterfactual-replay proof moment — the core research-methodology claim. |
| 2:10-2:50 | (same run continues) | — | Scene 5: attribution utilization comparison | "Why? edge-c is overloaded either way — rho above 1 means it's offered more work than it can serve, and no routing policy fixes that for a fixed-capacity target. What Adaptive does is shift load onto edge-a and edge-b, which still have headroom, so the overload is less severe, not gone." | Explanation grounded in real queueing attribution, not asserted. |
| 2:50-3:30 | (same run continues) | — | Scene 6: manifest + rerun | "This writes a real provenance record — the seed tree, a config hash, the actual git commit. And re-running the identical experiment gives zero trace divergence from the first run — same inputs, same result, every time." | The reproducibility proof moment. |
| 3:30-3:50 | (same run continues) | — | Scene 7: takeaway | "The point isn't that Adaptive won. It's that I could hold everything else fixed, prove the difference was real, explain why, and reproduce it on demand." | Closing statement — restates the actual contribution. |
| 3:50-4:30 *(optional)* | Switch to browser | `go run ./cmd/dashboard`, click Playground → Compare | Dashboard's own topology + divergence banner on an analogous scenario | "The dashboard shows the same kind of comparison visually — same mechanism under the hood, `FirstDivergence`, just a different scenario instance." | Visual reinforcement; explicitly flagged as analogous, not identical, to avoid overclaiming. |

## On-Screen Captions (no-voiceover version)

| Scene | Caption |
|---|---|
| 1 | "3 heterogeneous edges — one is 3-4x slower by design." |
| 2 | "Workload generated, not hardcoded: 300 requests / 3s, 50% hot-key." |
| 3 | "Failure as data: a 4-line YAML schedule, not a hidden branch." |
| 4 | "Same conditions, two policies. Adaptive: 16% lower mean latency. Traces diverge at event #8 — proven real, not noise." |
| 5 | "Why: edge-c is overloaded either way. Adaptive shifts load to targets with headroom." |
| 6 | "Real provenance manifest written. Re-run: zero divergence. Same inputs → same result." |
| 7 | "Not 'Adaptive won.' Controlled, proven, explained, reproducible." |

## Fallback Procedure

**Primary path**: `go run -buildvcs=true ./cmd/demo-stage10`, live.

**Recovery path** (if the live run fails or misbehaves):
1. `go build ./... && go vet ./...` — confirm the tree itself is healthy.
2. `scripts/demo-stage10.sh` — full reset, then rerun.
3. If still failing, `git status` to check for an uncommitted, in-progress edit that broke something,
   and consider `git stash` only after confirming what's being stashed.

**Evidence fallback** (if a live rerun genuinely cannot happen during recording): use
`demo/output/stage10-demo/manifest.json` and the console transcript already captured in this
document's "Primary Scenario" section — but **say explicitly on screen or in narration that this is a
previously-generated artifact being shown, not a live run**. Never present a prior run's output as if
it just happened.

## Known Limitations

- The dashboard's Playground scenario is analogous to, not identical to, `cmd/demo-stage10`'s
  scenario (different traffic-generation code path). Say so if showing both.
- `cmd/demo-stage10`'s `ConfigurationHash` covers the topology/horizon/chaos-schedule, not an
  `AdaptiveConfig` weight variant (there's only one Adaptive configuration in this demo, the
  project's own default) — the hash's role here is "prove this run's inputs," not "distinguish
  between tuned configurations," which is a different concern from `cmd/experiment-010a`'s use of the
  same manifest type.
- `go run`'s default (`-buildvcs=auto`) omits git commit info in this environment; the demo commands
  above already specify `-buildvcs=true`.
- The demo is entirely virtual-time; it makes no claim about real-world wall-clock latency numbers
  (20ms/15ms/60ms are configured service times, not measured real-network latencies).

## Claims Audit

Every claim the recording script makes, checked against its actual source before inclusion:

| Demo Claim | Evidence | Safe? |
|---|---|---|
| "3-4x slower" (edge-c vs edge-a/edge-b) | 60ms/20ms=3, 60ms/15ms=4, from the literal `TargetProfile.ServiceTime` values in `cmd/demo-stage10/main.go` | Yes |
| "300 requests over a 3-second window, 50% hot-key" | Printed directly from the real `traffic.Params{Requests:300, Horizon:3s}` and `HotColdKeys(0.5)` passed to `traffic.Generate` | Yes |
| "edge-b is the fastest target when healthy" | 15ms < 20ms < 60ms, from the same literal `TargetProfile` values | Yes |
| "16.4% lower mean latency" | Computed at runtime from `statistics.Mean` over real `WorldResult.Completions`; reproduced identically across 3 separate process runs | Yes |
| "traces diverge at event #8" | Real `replay.FirstDivergence` output on the two real `WorldResult.Trace` slices | Yes |
| "this proves the difference is a real routing effect, not two runs seeing different conditions" | Both runs use the identical `Scenario` value (same object); `FirstDivergence`'s own pre-divergence-identity guarantee is exercised, not assumed | Yes |
| "edge-c is overloaded under both policies (rho > 1)" | Real `attribution.UtilizationFromWorld` output: 1.989 and 1.337, both computed at runtime | Yes |
| "Adaptive shifts load to targets with headroom" | Same utilization output: edge-a/edge-b's rho rises while edge-c's falls, under Adaptive vs Round Robin | Yes |
| "reproducible byte-for-byte" | A real rerun of the identical `Experiment` produces zero `FirstDivergence`, confirmed live | Yes |
| "real provenance manifest, real git commit" | `demo/output/stage10-demo/manifest.json`, inspected directly; `git_commit` matches the actual `git log` HEAD | Yes |
| "the failure is declarative data, not a hardcoded branch" | The YAML string is parsed at runtime by the real `chaos.ParseYAML`, not pre-compiled into a `FailureWindow` literal | Yes |
| "Adaptive always wins" / "is better in general" | Contradicted by Stage 8's own broader finding (62.5-70% win rate, not 100%, plus a fairness tradeoff) | **No — do not claim** |
| "this proves the real system behaves this way" | This is a virtual-time-only result; no real HTTP execution occurred in the primary demo | **No — do not claim** |
| "the overload was fixed" | Contradicted by the demo's own printed mechanism explanation (reduced, not eliminated) | **No — do not claim** |
| "the dashboard is running the same experiment" | Dashboard's `PlaygroundScenario` uses different traffic-generation code; analogous, not identical | **No — say "analogous" explicitly** |

## Safe Claims

- "Adaptive achieved 16.4% lower mean latency than Round Robin under this specific controlled
  scenario" — directly measured, reproduced across 3 separate runs.
- "The divergence between the two policies' behavior is proven, not assumed" — via
  `replay.FirstDivergence` on real traces.
- "The experiment is byte-for-byte reproducible" — confirmed via a real rerun producing zero trace
  divergence.
- "The failure schedule is declarative data, not a hardcoded branch" — the YAML is parsed by
  `internal/chaos.ParseYAML` at runtime, visibly, on screen.
- "The explanation is grounded in a real utilization computation, not asserted" — via
  `internal/attribution.UtilizationFromWorld`.

## Claims to Avoid

- "Adaptive always wins" / "Adaptive is better in general" — this demo shows one scenario; Stage 8's
  own broader evaluation found Adaptive wins 62.5-70% of scenarios, not all of them, and trades
  fairness for latency.
- "This proves the real system behaves identically" — this is a virtual-time result; the real engine
  demo (backup, below) is a separate, non-identical demonstration.
- "The overload was fixed/eliminated" — the demo's own Scene 5 explicitly says the opposite: overload
  is reduced, not eliminated, since no routing policy can give a fixed-capacity target more capacity.
- Any claim that the dashboard's Playground run is "the same experiment" as `cmd/demo-stage10` — it
  is analogous, not identical.

---

## Secondary (Backup) Demo — Real Engine + Live Telemetry

Use if a technically deeper, real-HTTP demonstration is wanted, or as the fallback if the primary
demo's narrative doesn't land in a particular audience. Independently re-verified working during
this demo's preparation (see commands below, run live, output confirmed).

```bash
go run ./cmd/http-origin -addr :8000 -delay-ms 20 &
go run ./cmd/proxy -addr :8081 -targets http://127.0.0.1:8000 -metrics-addr :9090 -check-interval-ms 200 &
curl http://127.0.0.1:9090/metrics   # baseline: flashflow_target_health{...,state="HEALTHY"} 1
curl http://127.0.0.1:8081/          # send one real request
curl http://127.0.0.1:9090/metrics   # flashflow_requests_total now reads 1, latency_seconds populated
```

**What to say**: "This is the real engine — actual HTTP servers, not a simulation — with live
Prometheus-format metrics. The same chaos schedule from the primary demo can crash and recover this
real edge, and you'd see `flashflow_target_health` flip live." (See
`internal/chaos/real_test.go`'s `TestToRealSchedule_And_RunReal_EndToEnd` for the exact
crash/recover pattern against a real `EdgeServer`, or `internal/topology/topology_test.go`'s
`TestEdgeServer_SetDown_CrashesAndRecovers`.)

**Evidence**: the `/metrics` output shown above, captured live during this document's own
preparation.
