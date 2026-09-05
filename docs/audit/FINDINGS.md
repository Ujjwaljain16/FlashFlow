# FlashFlow Stage 8 Final Audit — Findings Register

Adversarial, evidence-driven audit conducted after Stage 8 was declared complete. 13 independent
review passes covered requirements traceability, architecture, concurrency/determinism, state
machines, counterfactual replay, statistics, the adaptive router/tuner, cache/health/network/proxy,
dashboard/security/reproducibility, experiment provenance/git history, and documentation honesty.
Every `gofmt`/`go vet`/`go build`/`go test ./...` claim was independently re-run (all 15 packages
pass clean) rather than trusted from prior stage artifacts.

Findings are numbered `F-<NN>`. Where multiple independent passes found the same issue from
different angles, this is noted explicitly as corroboration — it materially increases confidence
the finding is real, not an artifact of one reviewer's misreading.

Severity key: **P0** blocker · **P1** serious, fix before public release · **P2** important,
fix when practical · **P3** polish/non-blocking.

---

## P0 — Blockers

### F-01 — P0 — Dashboard experiment browser has a real, HTTP-reachable path-traversal bug; the project's own "unit-tested, path-traversal-safe" claim is false

**Area:** Security / Dashboard
**Location:** [internal/dashboard/experiments.go:24](../../internal/dashboard/experiments.go) (`safeName` regex), consumed by `ListResultFiles`/`ReadResultFile`; reachable via [cmd/dashboard/handlers.go](../../cmd/dashboard/handlers.go) `handleExperimentPath`.

**Observed:** `safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)`. The comment claims this excludes `..`, but `.` is an allowed character with unbounded repetition, so the literal string `".."` fully matches. `filepath.Join("experiments", "..", "results", file)` cleans to a path one level *above* the experiments root. Verified live against a running `cmd/dashboard` instance:
```
GET /api/experiments/../proof.json       -> 404 (net/http's mux cleans a literal "..")
GET /api/experiments/%2e%2e/proof.json   -> 200 {...}   <- bypasses the mux's cleaning
GET /api/experiments/..%2fproof.json     -> 200 {...}
```
The existing regression test (`internal/dashboard/dashboard_test.go` `TestListResultFiles_RejectsPathTraversal`) passes today only because no top-level `results/` directory happens to exist in this repo — it is a false-negative test, not a real guard.

**Expected:** `docs/StageArtifacts/Stage8.md` states the handlers are "path-traversal-safe (unit-tested)." A regex that literally admits the string `..` does not meet that bar, and the test that's supposed to prove it doesn't actually exercise the failure mode.

**Why it matters:** This is a real, demonstrated deviation from a specific safety claim the project makes about itself, reachable over HTTP with no auth (see F-15). Blast radius is bounded — `/` is excluded from the character class, so only a single `..` segment can apply, landing the escape at `<CWD>/results/<file>`, not an arbitrary path — but the control is broken, not merely narrow, and would silently start leaking content the moment any directory of that shape exists next to the experiments root.

**Reproduction:** As above; a synthetic `results/proof.json` at the repo root is served back through `/api/experiments/%2e%2e/proof.json`.

**Root cause:** Traversal protection implemented as a character-class regex instead of an absolute-path-prefix check.

**Recommended fix:** Reject `name == "."`/`name == ".."` explicitly, and independently verify (defense-in-depth) that `filepath.Abs(resolved)` has `filepath.Abs(ExperimentsRoot)` as a prefix.

**Regression test:** A test that asserts the literal string `".."` is rejected by checking the returned error/type directly, independent of whether a matching directory happens to exist on disk. Add an HTTP-level test hitting the URL-encoded form (`%2e%2e`) since the raw string is caught earlier by `net/http`'s own mux cleaning.

**PRD/TRD impact:** None directly (dashboard is not a named PRD/TRD security requirement), but directly falsifies a specific, written Stage 8 completion claim.

---

### F-02 — P0 — The "Docker / `tc netem` real-emulation engine" claim in the README's Resume Line, and in prd.md/trd.md, was never built — contradicted by the project's own Stage 4 documentation

**Area:** Documentation honesty
**Location:** [README.md](../../README.md) (architecture diagram + Resume Line), [prd.md:14,163,189](../../prd.md), [trd.md:27,135-139](../../trd.md), vs. [internal/netsim/netsim.go:1-13](../../internal/netsim/netsim.go) and [docs/StageArtifacts/Stage4.md:9,99,125](../StageArtifacts/Stage4.md).

**Observed:** Four independent review passes confirmed zero occurrences of `netem`/`qdisc`/`NET_ADMIN`/`exec.Command`-shelling-to-Docker anywhere outside prose comments. What exists is `internal/netsim`, an in-process Go `http.RoundTripper` wrapper injecting latency/jitter/loss — explicitly documented in its own package comment as a substitute for `tc netem`, adopted because netem is Linux-only and the project was developed on Windows. `docs/StageArtifacts/Stage4.md` is scrupulously honest about this pivot. But the README's **Resume Line** — text explicitly meant to be copied onto an actual resume — still reads *"a real-world Docker emulator (`tc netem`)"*, and `prd.md`/`trd.md` still describe the same undelivered architecture without a corrective note.

**Why it matters:** Of every claim audited across the whole documentation set, this is the one most likely to actively mislead a technical reviewer who takes it at face value — it names a specific, checkable technology, in the one sentence of the whole project explicitly written for external consumption, and it did not ship.

**Evidence:** Confirmed independently by the documentation-honesty, cache/health/network, real-vs-virtual/performance, and dashboard/security audit passes.

**Recommended fix:** Rewrite the README Resume Line and prd.md/trd.md's Emulation Engine sections to state the actual, disclosed substitution ("in-process HTTP-level network degradation (`internal/netsim`), built in place of `tc netem`") — matching the honesty already present in `docs/StageArtifacts/Stage4.md`.

**PRD/TRD impact:** PRD §1/§7, TRD §6 both describe this engine as delivered; it is not.

---

### F-03 — P0 — Stage 8 is entirely uncommitted; a clean checkout cannot reproduce it

**Area:** Reproducibility / Git history
**Location:** repository-wide; `git status`, `git ls-tree -r HEAD --name-only`.

**Observed:** `HEAD` is at `cf60268 docs(stage7): add Stage 7 learning notes and exit artifact`. None of `cmd/dashboard/`, `internal/dashboard/`, `internal/tuning/`, `internal/challenge/`, `cmd/experiment-008*`, `scripts/`, `deployments/nginx-bench/`, `docs/StageArtifacts/Stage8.md`, `docs/FinalResearchReport.md`, or `docs/learning/008-tuning-final-validation.md` exist in git history — all of it is untracked working-tree state. In addition, `internal/replay/policies.go` and `internal/replay/world.go` (Stage 7, supposedly already committed) are locally modified relative to `HEAD`, so even Stage 7's committed state no longer matches disk.

**Why it matters:** This is a direct, demonstrated failure of the reproducibility-from-clean-checkout test this audit explicitly performs (spec §20/§48/§69): `git clone` of this repository today reproduces Stages 1–7 only. "Stage 8 (final) is declared complete" is true of one machine's working directory, not of the repository. It also means the git history cannot be used as evidence for the project's own "genuine multi-stage discovery and correction" narrative for the one stage under audit.

**Compounding context (F-19):** the entire committed history for Stages 2 through 7 — 80 commits — was made in a single ~13.5-hour window on one calendar day, with some stages landing in commits seconds apart. This doesn't invalidate any individual technical finding, but it means the commit history itself provides no independent corroboration of the incremental, error-and-correction development story the docs repeatedly invoke.

**Recommended fix:** Commit Stage 8 in logical, reviewable increments (mirroring how Stages 2–7 are structured in the docs, whatever their actual authoring timeline) before treating it as final. Resolve the uncommitted `internal/replay` diff first — see F-20 for its content.

**PRD/TRD impact:** TRD §9's provenance/ledger goal is undermined at the meta-level: even a complete manifest system would be moot if the code that produced the ledger isn't itself committed.

---

## P1 — Serious (fix before public release)

### F-04 — P1 — `ExperimentEngine` interface (TRD §3) was never built

TRD §3 specifies a shared `Prepare/Run/Replay` interface unifying the virtual and real engines. Zero hits for `ExperimentEngine` in the Go source. Disclosed once, honestly, at Stage 5 (`docs/learning/005-virtual-time.md:85`: "left unbuilt... future stage's packaging") but never revisited or formally closed in Stage 8's own Limitations section, despite Stage 8 being final. The two engines are unified only by convention (shared `TargetSelector`/`health.Registry` code), not by an enforced interface boundary. **PRD/TRD impact:** TRD §1/§3 named architectural contract, absent with a stale disclosure trail.

### F-05 — P1 — Experiment manifest / provenance / hierarchical-seed model (TRD §2, §9) never built; `FinalResearchReport.md` overstates what exists

TRD promises a `runs/<id>/manifest.json` carrying `GlobalSeed/TrafficSeed/TopologySeed/FailureSeed/PolicySeed`, `ConfigurationHash`, `GitCommit`. Zero hits for any of those field names or for a `manifest.json` file anywhere in the repo; `internal/replay.Scenario` has exactly one seed field (`Seed int64`); no `internal/provenance` package exists despite being named in TRD §1's own repository map. Disclosed once at Stage 5, never revisited at project close. Materially worse: `docs/FinalResearchReport.md:25` states *"the same manifest and seed reproduce the same event trace, byte for byte"* — language that reads as confirming the TRD's manifest design when no manifest file of any kind exists. **Recommended fix:** either build a minimal manifest (seed + git commit + config hash per run) or correct `FinalResearchReport.md`'s wording and add an explicit Stage 8 Limitations entry. **PRD/TRD impact:** PRD §8.8 ("Added" as a headline v3.1 item), TRD §2/§9.

### F-06 — P1 — Traffic Generator (PRD §8.1) was never built, with no disclosure trail anywhere

Unlike LRU, the tuner-tier reduction, and `tc netem`→netsim — all of which have an explicit deferral statement somewhere in the learning notes — the constant/ramp/burst/flash-crowd traffic generator and "Fuze log import" have **zero mentions anywhere outside prd.md itself**. Actual traffic in every experiment is a hardcoded `[]replay.Arrival` list per scenario. This is the strongest candidate in the whole audit for "silently dropped scope," as distinct from the many honestly-disclosed cuts elsewhere in the project.

### F-07 — P1 — Metamorphic invariant testing (TRD §16) does not exist anywhere, and its absence is undisclosed

TRD §16 names two specific invariants ("2x delay → latency must not decrease," "halved load → utilization ρ must monotonically decrease"). Zero hits for "metamorphic" anywhere in test code; `internal/challenge` — Stage 8's own purpose-built coverage-gap-filler, whose learning note describes a deliberate coverage survey — does not mention this invariant class at all. Unlike other cuts, this is not surfaced in Stage 8's Limitations section alongside the six items it does list. **Recommended fix:** implement the two invariants (straightforward given the existing deterministic Scenario/RunWorld machinery) or add an explicit disclosed scope boundary.

### F-08 — P1 — The "automated queueing-theoretic attribution engine" (PRD §6.4 "core, not optional"; TRD §14) is a one-off script, not a reusable engine, and this gap is undisclosed against the spec

PRD/TRD describe automatic ρ-computation and generated causal-explanation text triggered by tail-latency spikes. No `Attribution`-named code, no `ρ`/`rho` variable, no M/M/1 formula exists anywhere; what exists is `cmd/experiment-006d/main.go`, a single hand-written experiment that measures Little's Law once and prints one hardcoded finding string. Confirmed independently by both the statistics and requirements-traceability passes. **Positive note:** the actual measurement (006-D) is itself methodologically sound — correct, non-circular, single-boundary Little's Law verification with honestly-caveated error growth — the gap is entirely between the PRD's "automated, reusable, comparative" framing and the narrower thing that shipped, not a defect in what was built.

### F-09 — P1 — SWR cache policy missing, undisclosed (distinct from the LRU gap, which is disclosed)

PRD §8.5/TRD §8 require 4 cache policies: No Cache, TTL, LRU, Stale-While-Revalidate. Only fixed-TTL + coalescing exist. LRU's absence is well-reasoned and documented (`docs/StageArtifacts/Stage4.md:19,100`). SWR's absence is **never mentioned anywhere** in any learning note or stage artifact — confirmed independently by two review passes.

### F-10 — P1 — Declarative YAML chaos engine (PRD §8.7, TRD §12) never built

No YAML parsing exists anywhere in the Go source. Both engines' "declarative" failures are hardcoded Go struct literals (`replay.FailureWindow`, `netsim.Conditions`), not data-driven configuration. This compounds F-02 (the same architecture section also promised real `tc netem` translation). Disclosed at the Stage 4 level for the netem half; the YAML half has no disclosure anywhere.

### F-11 — P1 — `internal/health.Checker`'s Stop/Start cycle has a latent goroutine leak and unsynchronized field access

**Location:** [internal/health/checker.go](../../internal/health/checker.go) `Start`/`Stop`/`runLoop`.
`Start()` writes `c.stopCh = make(chan struct{})` under lock, then spawns `go c.runLoop()`; `runLoop` reads the *field* `c.stopCh` directly on each iteration rather than a captured local value. If `Stop()` then `Start()` happen before the old goroutine observes the close, the old goroutine reads the *new* (open) channel on its next iteration and never exits — two loops probing every target forever, plus an unsynchronized read/write on `stopCh` itself. **Currently latent:** no code path in the repo restarts a `Checker` (each `ReverseProxy`/`RunWorld` lifetime starts and stops one exactly once) — race-detector confirmation is unavailable in this environment (no gcc). **Recommended fix:** capture `stopCh` as a local variable before spawning `runLoop`, pass it as a parameter. **Regression test:** Start→Stop→Start immediately, assert only one active loop.

### F-12 — P1 — None of the three real `net/http.Server` instances set read/write/idle timeouts

`internal/proxy/proxy.go`, `internal/topology/edge.go`, and `cmd/dashboard/main.go` all construct bare `http.Server`/call `http.ListenAndServe` with no `ReadHeaderTimeout`/`WriteTimeout`/`IdleTimeout`. A slow or silent client can hold a per-connection goroutine open indefinitely (Slowloris-shaped). Requires an adversarial or badly-behaved client to trigger, not normal-operation growth. **Recommended fix:** set at minimum `ReadHeaderTimeout` on all three.

### F-13 — P1 — `internal/netsim`'s RNG falls back to a wall-clock seed in every real experiment code path, breaking reproducibility

**Location:** [internal/netsim/netsim.go:72-77](../../internal/netsim/netsim.go), called with `nil` from [internal/topology/edge.go:104](../../internal/topology/edge.go). `EdgeConfig`/`netsim.Conditions` expose no seed field at all. The two real experiments that exercise this path — `cmd/experiment-004f` and `cmd/experiment-006c` — both hit the `nil` branch, meaning their per-request loss/jitter sequences are not reproducible run-to-run, directly at odds with the project's stated determinism discipline. All unit tests, by contrast, pass an explicit seeded `*rand.Rand`, so this path is untested. **Recommended fix:** add a `Seed`/`*rand.Rand` field to `EdgeConfig`, thread it through; add a test asserting two identically-seeded `EdgeServer`s produce identical drop/delay sequences.

### F-14 — P1 — `ReverseProxy` has no upstream response timeout; a hung backend can block a proxied request indefinitely

**Location:** [internal/proxy/proxy.go:85,248](../../internal/proxy/proxy.go). Unlike `EdgeServer` (which wraps its origin client in a 10s timeout), `ReverseProxy.transport.RoundTrip` is called on a bare `TrackedTransport` with no `ResponseHeaderTimeout`/client `Timeout` anywhere in `TransportConfig`. A target that accepts a TCP connection but never writes a response hangs the request until the *original client's* own deadline, if any. This is a realistic chaos/failure mode this project's own health-check design otherwise anticipates. **Recommended fix:** add a `ResponseHeaderTimeout` to `TransportConfig` or wrap in an `http.Client` with a bounded `Timeout`.

### F-15 — P1 — Cache key excludes headers that Origin's own debug endpoints use to vary behavior, allowing response collisions

**Location:** [internal/cache/cache.go:23-33](../../internal/cache/cache.go) vs. [internal/topology/origin.go:111-131](../../internal/topology/origin.go)'s `X-Artificial-Delay-Ms`/`X-Override-Status` handling, forwarded unmodified by the edge. Two requests to the identical path+query with different override headers collide on one cache key; whichever reaches the edge first is served to both. Currently latent — no experiment happens to combine caching with these debug headers — but unguarded by any test. **Recommended fix:** exclude these headers from cache-fronted forwarding, or fold them into the key when present, or document the limitation explicitly.

### F-16 — P1 — Client-side cancellation is misattributed as an upstream failure, corrupting health telemetry

**Location:** [internal/proxy/proxy.go:218,248-274](../../internal/proxy/proxy.go). The upstream request is built with the inbound client's own context; when a client disconnects mid-flight, `RoundTrip` returns an error wrapping `context.Canceled`, which the code treats identically to a genuine upstream failure and unconditionally calls `registry.RecordAppResult(target, http.StatusBadGateway)`. No check anywhere distinguishes "client hung up" from "target is failing." A burst of impatient/timing-out clients can push a perfectly healthy target into `DEGRADED` purely from client-side behavior. **Recommended fix:** check `r.Context().Err() != nil` before recording an app-level failure against the target. **Regression test:** cancel the client context mid-flight against a slow test upstream; assert `TotalAppErrors` does not increment.

### F-17 — P1 — Holdout scenarios are not distribution-shifted; the "generalization" claim is narrower than the TRD's own rule, and the gap document

TRD §11's own Generalization Rule gives "shifted Zipf parameters or unannounced failures" as the kind of shift Holdout should test. `NewSplit` generates both Development and Holdout from the identical `ScenarioSpace` (same failure probability, same target-count and service-time ranges) — the *only* difference is the seed range. Stage 8's docs use strong, unqualified language ("the evidence for generalization is real") for what is actually the narrower claim "generalizes to fresh random draws of the same distribution," which is real and useful (008-E's adversarial test specifically demonstrates it catches sampling-noise overfitting) but is not evidence of out-of-distribution robustness. This gap is not listed among Stage 8's own six stated Limitations. **Recommended fix:** either add a distribution-shifted second holdout set, or explicitly narrow the claim in the docs.

### F-18 — P1 — The exogenous/endogenous boundary for counterfactual comparisons is enforced by convention, not by the type system

**Location:** [internal/replay/scenario.go:61-73](../../internal/replay/scenario.go) (`UseHealthRegistry`, `ProbeInterval`, `Horizon`). These are exogenous inputs but are experiment-protocol knobs, not world physics — nothing prevents constructing two Scenarios for comparison that differ in `Horizon` alone, which `FirstDivergence` would then report as a policy-caused divergence when it's actually a run-length artifact. Every current caller gets this right by hand (copy-then-mutate-one-field); it is discipline, not a guarantee. Compounding: `FirstDivergence`'s positional trace diff is only safe today because policy code structurally cannot call `Engine.Record` — an unenforced, untested invariant that a future policy extension could silently break. The project's own `docs/learning/007-adaptive-routing-replay.md` already states this exact boundary "has been demonstrated for a health-failure intervention specifically; it has not been separately checked for every other kind of exogenous change" — the gap is self-acknowledged but not closed. **Recommended fix:** split `Scenario` into world-physics fields and protocol-control fields, or add an explicit equality-precondition check before comparing two runs.

### F-19 — P1 — Git history: all Stage 2–7 commits (80 of them) landed in a single ~13.5-hour window on one day

See F-03 for the reproducibility consequence. Independently worth stating: some stage-closing commits are seconds to low-minutes apart, and Aug-19 vs. Sep-5 commits show a stark shift from terse to long narrative messages. This does not invalidate any individual technical finding (every headline number this audit traced back to raw JSON matched exactly), but it means the git history itself cannot corroborate the "genuine, incremental discovery and correction" framing the project's docs repeatedly invoke, and the final report should say so plainly rather than let commit messages imply a multi-day/week timeline.

### F-20 — P1 — README's Build Sequence and Experiments tables are stale, showing only Stage 1 complete

Independently confirmed by three review passes. `README.md`'s status table marks Stages 2–8 as not started (🔲) despite all 8 `docs/StageArtifacts/` existing, `experiments/002` through `008-tuning-validation` all populated, and 15 tested packages in `internal/`. This is the starkest and most visible cross-document contradiction in the whole audit — unusually, an *under*-claim rather than an overclaim, but one that actively contradicts the same README's own header/architecture-diagram prose describing the complete system in the present tense, and the Stage 8 additions (dashboard, tuner, final validation) are not documented in README at all — a new reader has no path from `git clone` to running the dashboard.

### F-21 — P1 — Dashboard binds to all interfaces, not localhost, undermining its own stated "local dev tool" threat model

`cmd/dashboard/main.go` uses a bare `:7070` (wildcard bind), not `127.0.0.1:7070`. Combined with F-01's traversal bug and the complete absence of authentication, this means the (bounded) traversal exposure is reachable from anything else on the same network, not just the operator's own machine, contrary to the "local research tool" framing under which the no-auth design is otherwise reasonable. **Recommended fix:** default to `127.0.0.1`, require an explicit flag to bind wider.

---

## P2 — Important (fix when practical)

- **F-22** — Auto-Tuner progression (Random→LHS→Bayesian, PRD §6.2 "core, not optional") shipped only Random Search v1; well-justified in Stage 8 docs, but `prd.md` itself was never revised to reflect the accepted final scope.
- **F-23** — Telemetry stack (HdrHistogram, Prometheus, canonical typed Event Stream — TRD §13/PRD §8.9) never built; substituted by plain-slice percentile computation and ad hoc string-literal trace event names per call site (no centralized vocabulary).
- **F-24** — Tuner's search loop never calls `ConfigSpace.Valid()` on sampled candidates — safe today only because the Dirichlet sampler happens to always produce valid points; no defense-in-depth if a different sampling strategy is ever substituted.
- **F-25** — Project calls its router a "six-signal adaptive router" in the learning notes/README; the code implements four signals (health is a pre-filter, capacity folds into load) — six is the count of *tunable parameters*, correctly stated only in `space.go` itself.
- **F-26** — `scoreUtilization`'s zero/negative-capacity guard collapses "unconfigured" and "explicitly disabled" into the same default-weight-1 behavior; an operator setting capacity=0 to pull a target from rotation would not get that effect.
- **F-27** — Latent NaN path in `scoreLatency` if `ReferenceLatency` reaches 0 (unreachable via any current config path, but if triggered, Go's NaN comparison semantics would permanently lock routing onto the alphabetically-first candidate) — independently found by two review passes from different code paths.
- **F-28** — `RunWorld` discards all partial results on a single scheduling error and overwrites earlier error text with the latest one, rather than fail-fast or aggregating — costly for debugging a large experiment run.
- **F-29** — Requests in-flight at a Scenario's `Horizon` cutoff are silently dropped from all counters (`Records` won't reconcile with `Completions + Rejected`).
- **F-30** — `cmd/proxy`'s shipped binary can only ever run Round Robin — no CLI flag exposes WRR/LeastConn/EWMA/P2C/Adaptive, despite the underlying selector code being genuinely shared with every experiment binary and the virtual engine.
- **F-31** — `AdaptiveSelector.SelectTarget` holds a single mutex across its entire scoring+sort+map-write body, serializing all concurrent routing decisions (invisible at the project's tested scale of 3–5 targets; a spot-check to n=500 found no accidental O(n²), but full serialization is a real bottleneck if concurrency/target-count ever grows).
- **F-32** — No CI configuration anywhere (`gofmt`/`vet`/`build`/`test`/`final-validation.sh` are all developer-run manually).
- **F-33** — No `.gitattributes`; `git diff` already shows LF→CRLF warnings on several files, risking spurious whole-file diffs for contributors with different `core.autocrlf` settings.
- **F-34** — `Coalescer.Do` deletes its in-flight map entry *before* signaling waiters (`wg.Done()`), a narrow window (not the canonical singleflight ordering) that could cause one redundant fetch; no correctness bug, since waiters already registered still get the correct value.
- **F-35** — `health.Registry.RecordAppResult` and `RecordProbeResult` handle an unregistered target inconsistently (one auto-registers, the other silently no-ops) — latent, since nothing in the repo calls either before registration today.
- **F-36** — No `Deregister`/reset API on `health.Registry`; a hypothetical future remove-then-re-add of a target would silently inherit stale health state. Not reachable today (no code path removes a live target).
- **F-37** — Reverse proxy always sends `Transfer-Encoding: chunked` upstream regardless of a known `Content-Length`, since `outReq.ContentLength` is never copied from the inbound request (unlike stdlib's own `httputil.ReverseProxy`, which does this explicitly).
- **F-38** — `Stage2.md` calls its binaries "production-grade," the one place in the doc corpus that uses that language uncritically; every other instance in the project explicitly disclaims production readiness.
- **F-39** — Mann-Whitney is used at n=10/side in experiment 006-E, right at the documented ~8-observation floor for the normal approximation, with no caveat in that experiment's own README (low practical risk since the finding leans on Cliff's Delta/CI, not the p-value).
- **F-40** — The 006-C statistical-method correction and the 004-A cache-stats baseline-subtraction fix each live only inside their one-off experiment binaries, with no dedicated package-level regression test guarding against reintroduction in a future experiment script.
- **F-41** — Dashboard's `app.js` builds `innerHTML` from disk-sourced experiment/file names without escaping — not remotely exploitable under the stated local-only threat model, but a real defense-in-depth gap that compounds with F-01 (the directory being listed is itself HTTP-influenced).
- **F-42** — Dashboard server has no response-size bound (`os.ReadFile` on arbitrary trace files) and no `ReadTimeout`/`WriteTimeout`/`MaxHeaderBytes` — low risk given single-user local operation.
- **F-43** — `scripts/final-validation.sh` and `scripts/nginx-reference-benchmark.sh` require actual bash (arrays, `BASH_SOURCE`) with no README statement that Git Bash/WSL is required; a plain-`cmd.exe`/PowerShell user gets an undocumented failure.
- **F-44** — New Stage 8 `WorldResult.Completions` field (the data source for tuned p99/mean latency) is not covered by the existing identity/isolation determinism tests, which predate it.
- **F-45** — `internal/tuning`'s evaluation-result cache is real and correctly implemented, but nearly never hits in practice for Random Search's continuous sampling — the claim is technically true but its practical value is close to zero and undisclosed as such.

---

## P3 — Polish / non-blocking

- **F-46** — `deployments/nginx-bench/nginx.conf;C` is a confirmed literal leftover artifact of the Windows/Git-Bash path-mangling bug the project's own docs describe as "fixed" — the fix (an env-var guard on future runs) never cleaned up the file the bug had already produced. Untracked, so `git status` never surfaces it for cleanup.
- **F-47** — A third, undocumented percentile implementation exists in the pre-`internal/statistics` `cmd/experiment-005h` binary (predates the shared package; the shared package's own doc comment already disclaims byte-identical agreement with `internal/httpx`'s separate convention, but doesn't name this third copy).
- **F-48** — `clock.Clock`'s actual interface (`Now() VirtualTime` only) has drifted from TRD §2's sketch (`Now()`+`SleepUntil(t)`); the actual design is a coherent, documented improvement (event-driven `Schedule` replaces blocking sleep), but TRD itself was never updated to match.
- **F-49** — `research.md` describes substantially more than what PRD/TRD scoped or what shipped (Parquet, OpenTelemetry, DR-OPE/CausalSim tensor completion, LinUCB/Thompson Sampling bandits, TinyLFU/ARC caching) — correctly triaged out by PRD's own Non-Goals section, but a reader conflating research.md with a requirements doc would see phantom gaps. Its own "Critical Novelty Assessment" table's claims (Bayesian optimization, bandits, OPE) are aspirational, not descriptive, and the document doesn't consistently flag which rows are "Proposed" vs. delivered.
- **F-50** — Event/telemetry vocabulary is real but informal (snake_case string literals invented per call site) rather than the "canonical," implicitly-fixed vocabulary TRD §13 implies; `LinkDegraded`-class events don't exist since network impairment was never wired into the virtual engine.
- **F-51** — `Trace` is an unbounded in-memory slice with no cap; non-issue at the project's actual experiment scale (hundreds to ~100K events) but a real architectural gap if experiments ever grow much larger.
- **F-52** — `netsim.Transport.Conditions` is an exported, unsynchronized field; no current caller mutates it post-construction, but it's a latent data race if a future "flapping conditions mid-experiment" feature is added.
- **F-53** — Swallowed `Serve()` error in both real HTTP servers' listener goroutines (`_ = p.server.Serve(ln)`) — no leak, just no operator-visible signal if the server stops unexpectedly.
- **F-54** — Bounded `time.After` timer leak in `netsim.Transport.RoundTrip` on the context-cancellation branch (should use `time.NewTimer`+`Stop()`).
- **F-55** — `cmd/dashboard` has no graceful-shutdown wiring (no `signal.Notify`/`srv.Shutdown`), unlike `cmd/proxy`/`cmd/edge` which both do this correctly; low impact since the dashboard spawns no background goroutines to leak.
- **F-56** — `008-D`/`008-F` re-derive the tuning winner by re-running the full search rather than reading `008-B`'s persisted ledger and asserting hash equality — a reasonable, deliberate choice (008-C treats the re-derivation itself as a determinism check) but rests on an implicit rather than asserted equality.
- **F-57** — Objective function's p99→mean-latency regression protection (`objective_test.go`) is accidental (works because of an unrelated zero-value default), not a test explicitly named/designed to catch that specific historical bug.

---

## Confirmed correct — positive findings

These were specifically hunted for as potential P0s and were **not found**, or were verified as sound.
Recorded here so the audit's negative results are as visible as its positive ones.

- `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./...` all pass clean across all 15 packages, independently re-run by this audit (not merely trusted from Stage 8's own claim), including with the current uncommitted `internal/replay` changes present.
- No counterfactual replay state leakage found. `TestRunWorld_IdentityDeterministic`, `_DivergenceOnlyAfterInterventionPoint`, and `_Isolation` are genuine, non-trivial tests (full-trace comparison, not summary stats; an interleaved unrelated run to catch shared global state) and all pass, including under `go test -count=5 -shuffle=on`.
- No holdout leakage in the tuner. Every call site touching Holdout data was traced; it is used exactly once, after a winner is already selected from Development alone. RNG separation between optimizer, per-scenario generation, and P2C's per-run randomness is real (independent seed spaces, not derived from a shared source).
- Every headline number in `docs/StageArtifacts/Stage8.md` (utility scores, winning weights, the full policy win-rate table, the NGINX comparison) was traced back to its raw JSON result file and matches exactly — no orphaned or unsupported claims found.
- All four `internal/statistics` implementations (percentile, Mann-Whitney, Cliff's Delta, bootstrap) are algorithmically correct, including tie-handling, degenerate-input guards, and defensive non-mutation — verified against hand-computed reference values in their own test suites, not "no-panic" theater.
- The virtual-time engine's event-queue tie-break is a real `(timestamp, insertion-sequence)` heap comparator — no map-iteration or wall-clock dependence found anywhere in `internal/vtime`. The virtual `Ticker` is purely engine-recursive, not backed by a real `time.Ticker`.
- The Adaptive router's four real signals are all direction-correct and provably bounded to [0,1]; tie-breaking is deterministic (explicit `sort.Strings`, not map order); all-unhealthy/single-target/cold-start paths are handled without panics and are tested.
- The dashboard is a genuine read-only projection over `experiments/*/results/*.json` with no second mutable state store, no in-memory run history, and no caching that could drift from disk.
- The tuner genuinely reuses `replay.RunWorld` for every evaluation path (aggregate, per-scenario robustness, and search) rather than duplicating the execution engine.
- No dead code, TODO/FIXME markers, or compatibility shims were found anywhere in `internal/` or `cmd/` (repo-wide grep, zero hits).
- No accidental O(n²) hot paths found; a direct benchmark sweep to n=500 targets confirmed the Adaptive selector's cost grows O(n log n), not quadratically.
- The open-loop load generator (`cmd/experiment-008g`) genuinely uses absolute-time-scheduled dispatch per request, not a shared ticker or naive sleep loop — verified to avoid the classic closed-loop coordinated-omission failure mode.
- No secrets/credential leakage and no shell-injection surface found in Go code (zero `os/exec` usage in the entire `internal`/`cmd` tree); shell scripts invoke Docker/Go with hardcoded arguments only.
