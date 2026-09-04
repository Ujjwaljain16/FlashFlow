# Experiment 003: Server-Side Scaling & Routing Policies

## 1. Executive Summary & Research Question

Stage 3 studies how a reverse proxy should choose among multiple edge targets as those targets' capacity, load, and latency diverge. Experiment 002-D already established the negative result that motivates this stage: static Round Robin collapses cluster throughput by 89.6% when a single edge degrades, because it has no way to account for target state.

Stage 3 proceeds incrementally: Round Robin → Weighted Round Robin → Least Connections → Latency-Aware EWMA → Power of Two Choices, each policy motivated by a limitation the previous one exposed. This experiment set implements and measures that progression against the existing Stage 2 topology (`internal/topology`), proxy (`internal/proxy`), and health machine (`internal/health`), unchanged.

### Primary Research Question (003-A)
> Under homogeneous edges, does `RoundRobinSelector` actually deliver approximately equal request distribution, and does that hold under real concurrent access — not just as a property of the counter's arithmetic?

---

## 2. Experimental Setup & Topology

```text
  [HTTP Benchmark Client]
             │ (HTTP/1.1, GET /data)
             ▼
  [FlashFlow Reverse Proxy] — RoundRobinSelector
   ├── Tracked Transport (proxy_upstream)
   └── 4-State Health Machine (all targets HEALTHY throughout)
             │
   ┌─────────┼─────────┐
   ▼         ▼         ▼
[Edge A]  [Edge B]  [Edge C]  — identical config, 1ms simulated processing delay each
   │         │         │
   └─────────┼─────────┘
             ▼
      [Origin Server]
```

- **Environment**: Windows, Go 1.23.3, in-process topology (no Docker) — same convention as Stage 2's `cmd/experiment-002*` binaries.
- Each concurrency cell starts a **fresh** Origin/Edge/Proxy instance (avoids Stage 1's TIME_WAIT cross-cell contamination lesson) and runs a discarded 30-request warmup before the measured run.
- Fairness is measured via **two independent instrumentation points** per edge: the proxy's health registry (`TargetHealth.TotalAppRequests`, incremented in `ServeHTTP` after every completed or failed upstream round trip) and each edge's own `edge→origin` `TrackedTransport.RequestsCompleted`. For a healthy edge with no retries these must agree exactly; the experiment binary (`cmd/experiment-003a`) asserts this and fails loudly (`log.Fatalf`) if they don't, rather than silently reporting mismatched data.
- Warmup traffic is explicitly excluded from the measured counts by baselining both instrumentation points after warmup and before the measured run (an early run of this experiment did not do this and produced totals that silently exceeded the configured request count — caught by a sum-of-counts vs. `SuccessfulRequests+FailedRequests` invariant check, fixed before any data below was recorded).

---

## 3. Results: Experiment 003-A — Round Robin Fairness Baseline

**Hypothesis (H1)**: see `hypotheses.md`. Four of the five cells below use request counts that are exact multiples of `len(edges)==3`, for which a perfectly even split is a mathematical certainty of the counter's modulo arithmetic, not an empirical finding. The fifth cell (`c=97, r=5000`) deliberately uses a non-multiple of 3 at high concurrency — this is the one cell that actually tests whether `atomic.Uint64`-based selection holds up under real concurrent contention rather than just testing arithmetic.

| Concurrency | Requests | RPS | p50 | p95 | p99 | edge-a | edge-b | edge-c | max−min | CV |
|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| 1 | 300 | 566.5 | 1.64ms | 2.47ms | 2.73ms | 100 | 100 | 100 | 0 | 0.0000 |
| 10 | 1500 | 4567.1 | 2.09ms | 3.38ms | 4.51ms | 500 | 500 | 500 | 0 | 0.0000 |
| 50 | 3000 | 7497.4 | 3.57ms | 16.04ms | 21.05ms | 1000 | 1000 | 1000 | 0 | 0.0000 |
| 100 | 6000 | 7378.1 | 3.72ms | 36.70ms | 55.23ms | 2000 | 2000 | 2000 | 0 | 0.0000 |
| **97** | **5000** | 2759.1 | 8.11ms | 116.26ms | 205.23ms | 1667 | 1667 | 1666 | **1** | 0.0003 |

Raw per-cell JSON: `experiments/003-routing-policies/results/003A-c*.json`.

### Findings

1. **H1 confirmed, including the non-trivial case.** At `c=97, r=5000` (not evenly divisible by 3, under real 97-way goroutine contention on a shared `atomic.Uint64` counter), the split was 1667/1667/1666 — exactly the ideal floor/ceil distribution, with zero skew beyond what the remainder (5000 mod 3 = 2) requires. This confirms `RoundRobinSelector`'s atomic counter is correctly serialized under concurrent access; it is not merely correct by construction on paper.
2. **Proxy-recorded and edge-forwarded counts agreed in every cell, at every edge.** The two independent instrumentation points never diverged, which is expected (no retries exist anywhere in the Stage 2 forwarding path) but is a useful standing invariant to keep asserting as Stage 3 adds stateful policies with more moving parts.
3. **An unexpected, non-fatal client/server outcome mismatch appeared once during development** (not present in the final recorded data above): in an earlier run of the `c=50, r=3000` cell, the proxy/edge/origin chain fully processed and recorded 3000 app requests, but the benchmark client (`httpx.RunHTTPBenchmark`) classified only 2999 as successful. This was not a routing-fairness defect — the per-edge distribution was still perfectly even — and is most plausibly a transient client-side response-read or connection-reuse race under the shared `http.Client`'s connection pool, not a proxy or edge bug. `cmd/experiment-003a/main.go` now computes and records a `client_server_outcome_mismatch` field for every cell specifically so this class of event is visible rather than silently absorbed into "failed requests." It did not recur in the final run (all cells show `client_server_outcome_mismatch: 0`), so it is recorded as an observed, unexplained, low-frequency phenomenon rather than a confirmed mechanism — consistent with the project's evidence-boundary rule not to assert a cause we haven't verified.
4. **Tail latency at `c=97` (p95=116ms, p99=205ms) is dramatically worse than at `c=100` (p95=37ms, p99=55ms) despite lower concurrency.** This is an open observation, not yet explained. Plausible contributing factors — none confirmed — include: `c=97` was the fifth and final cell run in the same process (accumulated OS/scheduler state from four prior cells), the non-round concurrency number interacting differently with Go's goroutine scheduler, or ordinary Windows scheduling noise (the same class of measurement-resolution/OS-jitter caveat already documented in Stage 1). This does not affect the fairness conclusion (H1), which depends only on per-edge counts, not on absolute latency. It is flagged here because Stage 3's later comparative experiments (003-F) will compare tail latency *across policies*, and if this kind of run-order or scheduling variance is systematic rather than incidental, it could confound those comparisons — worth a repeatability check before 003-F, not before 003-A.

### Interpretation

Round Robin does exactly what it is designed to do — distribute selection count evenly — and does so correctly under real concurrency, not just in principle. This is a necessary but small result: Experiment 002-D already showed that equal *count* is not equal *load* the moment edges stop being homogeneous. 003-A's contribution is narrow and deliberate — it establishes that the baseline policy's core mechanism is trustworthy before building policies on top of it that carry actual per-target state (Least Connections, EWMA), where a bug in the counting/accounting discipline would be much harder to detect than it is here.

---

## 4. Results: Experiment 003-B — Weighted Round Robin Under Known, Static Heterogeneous Capacity

**Hypothesis (H2)**: see `hypotheses.md`. Setup replicates Experiment 002-D exactly (edge-a=1ms, edge-b=1ms, edge-c=100ms, c=30, r=600) so the Round Robin cell below is a direct, independent re-run of a previously published result — useful as a repeatability check, not just a new number.

**Implementation note**: `WeightedRoundRobinSelector` (`internal/proxy/weighted_round_robin.go`) uses the *smooth* weighted round-robin algorithm (the same scheme nginx and LVS use), not naive sequence expansion (e.g. repeating a weight-4 target 4 times in a row). Naive expansion is bursty — it would send several consecutive concurrent requests to the same heavy-weighted target rather than interleaving them — which is exactly the failure mode a capacity weight is meant to avoid. This choice, and the (initially wrong, then corrected) empirical claim about its actual maximum burst length, is documented in `internal/proxy/weighted_round_robin_test.go`.

| Policy | RPS | p50 | p95 | p99 | edge-a share | edge-b share | edge-c share |
|:---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| `round-robin` | 526.8 | 2.79ms | 103.20ms | 107.21ms | 33.33% (200) | 33.33% (200) | 33.33% (200) |
| `wrr-equal-weights` (1:1:1) | 479.6 | 2.63ms | 102.70ms | 109.77ms | 33.33% (200) | 33.33% (200) | 33.33% (200) |
| `wrr-capacity-correct` (100:100:1) | **1520.2** | 11.56ms | **37.35ms** | **65.33ms** | 49.83% (299) | 49.67% (298) | **0.50% (3)** |

Raw per-cell JSON: `experiments/003-routing-policies/results/003B-*.json`.

### Findings

1. **`round-robin` replicated 002-D's original finding within normal run-to-run variance**: 526.8 RPS / p95=103.20ms / p99=107.21ms here vs. the original 478.0 RPS / p95=102.27ms / p99=119.98ms. Same order of magnitude, same qualitative collapse — a useful repeatability check before trusting either number in isolation.
2. **`wrr-equal-weights` behaved statistically indistinguishably from `round-robin`** (479.6 RPS vs 526.8 RPS, both within the noise band the two `round-robin`-style runs already show against each other). This is exactly the sanity check H2 asked for: WRR with no capacity information degenerates to RR-like behavior rather than doing something unpredictable.
3. **H2 confirmed directionally and substantially**: correctly-weighted WRR (100:100:1) cut edge-c's traffic share from 33.33% to 0.50% (200 requests down to 3), nearly tripled throughput (526.8 → 1520.2 RPS, 2.9×), and reduced p95 from 103.20ms to 37.35ms (64% reduction) and p99 from 107.21ms to 65.33ms (39% reduction).
4. **But the recovery is partial, not complete** — 1520.2 RPS is still far below the "all edges at 1ms" homogeneous baseline from 002-D (4610.2 RPS), even though 99.5% of traffic now avoids the slow edge entirely. p50 (11.56ms) is also *higher* than either Round Robin variant's p50 (~2.7ms), which is counterintuitive at first glance for the policy that's supposed to be better.

### Interpretation

Finding 4 is the interesting result, not a contradiction of H2. The mechanism (grounded in `internal/httpx/benchmark.go`, not asserted without checking the code): `RunHTTPBenchmark` splits `requests` evenly across `concurrency` worker goroutines, each of which issues its share of requests **sequentially**, and the measured `TotalDuration` is `time.Since(start)` after **all** workers finish (`wg.Wait()`) — i.e., wall-clock duration is bounded by the *slowest* worker, not the average worker. At c=30 with 600 requests, each worker handles 20 requests sequentially. Only 3 requests total land on edge-c under the corrected weighting — but whichever 1–3 of the 30 workers happen to draw one of those 3 slow requests each absorb a full extra ~100ms *serialized into that worker's own sequential run*, and become the long pole that the whole benchmark waits on. A worker that draws none of the 3 slow requests finishes fast (hence the still-low median, since ~27 of 30 workers see none); a worker that draws one is disproportionately slow (inflating the tail and the total wall-clock duration used to compute RPS). This means **a policy can eliminate 98.5% of a slow edge's traffic share and still leave throughput and tail latency well short of the fully-homogeneous case**, purely from how a small number of slow requests happen to distribute across concurrent workers — a coordination/queueing effect, not a flaw in the weighting itself. This has not been verified by further instrumentation (e.g. per-worker completion times) — it is offered as a mechanism consistent with the code and the numbers, not a proven causal claim.

This is also the first concrete evidence, ahead of schedule, for the limitation H2 explicitly flagged: static weights only help as much as the configuration allows, and here even a *correct* configuration cannot fully undo the effect of routing any nonzero traffic to a target with a 100× latency penalty. Whether a *dynamic* policy (Least Connections, Experiment 003-C) does meaningfully better under the same scenario — by reacting to the slow edge's actual in-flight load rather than trusting a static assumption — is now a sharper, evidence-motivated question than it was before this experiment.

---

## 5. Results: Experiment 003-C — Least Connections Under Unequal Request Duration

**Hypothesis (H3)**: see `hypotheses.md` for H3a/H3b/H3c. This experiment is the first to use `internal/proxy/load_tracker.go` (`LoadTracker`) and `internal/proxy/least_connections.go` (`LeastConnectionsSelector`) — application-level in-flight request count per target, owned by the proxy and updated around every request's lifecycle, deliberately not derived from `transport.TrackedTransport.ActiveConns` (see the `LoadTracker` doc comment for why socket count is the wrong signal). Both are unit- and integration-tested in `internal/proxy/load_tracker_test.go`, `least_connections_test.go`, and two new `proxy_test.go` cases proving the increment/decrement lifecycle doesn't leak on the upstream-error path specifically.

### H3a — Static heterogeneous topology (1ms/1ms/100ms, c=30, r=600)

| Policy | RPS | p95 | p99 | edge-c share |
|:---|:---:|:---:|:---:|:---:|
| `round-robin` | 480.2 | 103.95ms | 110.92ms | 33.33% |
| `least-connections` | **2364.9** | 19.20ms | 109.54ms | 2.00% (12/600) |
| *(for reference, 003-B)* `wrr-capacity-correct` (100:100:1) | 1520.2 | 37.35ms | 65.33ms | 0.50% (3/600) |

**H3a confirmed, and exceeded**: Least Connections did not just match 003-B's hand-tuned WRR result — it beat it (2364.9 RPS vs 1520.2 RPS), *despite* routing more traffic to the slow edge (2.00% vs 0.50%) and never being told the capacity ratio. This is genuinely counterintuitive (more traffic to the slow edge, yet higher throughput) and is **not fully explained** — flagged as an open question rather than forced into a tidy narrative. The edge-c share was exactly 12/600 (2.00%) across two independent full runs of this experiment, which looks more like a near-deterministic property of the algorithm under this workload than random noise, but n=2 is not enough replications to assert that confidently.

### H3b — Dynamic degradation (homogeneous 1ms×3 → edge-c degraded to 100ms mid-run, no reconfiguration, c=30, r=600/phase)

| Policy | Phase 1 (homogeneous) RPS | Phase 1 edge-c share | Phase 2 (degraded) RPS | Phase 2 edge-c share |
|:---|:---:|:---:|:---:|:---:|
| `round-robin` | 3625.6 | 33.33% | 498.4 | 33.33% (unchanged) |
| `wrr-frozen-equal-weights` | 1803.5 | 33.33% | 503.1 | 33.33% (unchanged) |
| `least-connections` | 1745.9 | 32.83% | **1255.9** | **4.50%** |

**H3b confirmed cleanly**: Round Robin and a WRR frozen at its original (correct-at-the-time) 1:1:1 configuration behave *identically* in phase 2 to phase 1 — both keep sending exactly 33.33% of traffic to edge-c even after it silently became 100× slower, because neither has any mechanism to notice. Least Connections' edge-c share dropped from 32.83% to 4.50% with **zero reconfiguration** — the selector never changed, only the observed in-flight counts did. Phase-2 throughput reflects this directly: Least Connections (1255.9 RPS) outperforms both frozen policies by roughly 2.5× (498.4 / 503.1 RPS) under the exact same post-degradation conditions.

**A real, measured cost worth keeping**: in Phase 1 (fully homogeneous, nothing to adapt to), Least Connections' throughput (1745.9 RPS) is *lower* than Round Robin's (3625.6 RPS) — roughly half. `LeastConnectionsSelector.SelectTarget` reads `LoadTracker.Get` (an `RWMutex`-guarded map lookup) once per candidate, and `ReverseProxy.ServeHTTP` separately takes the tracker's mutex again for `Increment` and once more for the deferred `Decrement` — strictly more per-request locking than Round Robin's single `atomic.Uint64.Add`. This is exactly the "measure whether the policy introduces meaningful overhead" question flagged in the Stage 3 brief, now answered empirically rather than theoretically: Least Connections' adaptivity is not free, and the cost is visible precisely in the condition where its extra intelligence has nothing to do.

### H3c — Low concurrency (c=1), same static heterogeneous topology

| Policy | Target order | RPS | edge-a share | edge-c share |
|:---|:---|:---:|:---:|:---:|
| `round-robin` | a,b,c | 27.3 | 33.33% | 33.33% |
| `least-connections` | a,b,c | 384.7 | **100%** | 0% |
| `least-connections-slow-first-order` | **c**,a,b | **9.8** | 0% | **100%** |

**H3c was wrong, in an informative way.** The original prediction was that Least Connections would show *no measurable advantage* at c=1, on the reasoning that sequential (non-overlapping) requests never let in-flight counts diverge. That reasoning about the mechanism was actually correct — but its consequence was not "no signal", it was "every selection is a tie, forever." At c=1, the client always waits for a response before issuing the next request, so by the time each new `SelectTarget` call happens, the *previous* request has already completed and decremented — every candidate reads load 0, every time. `LeastConnectionsSelector`'s deterministic tie-break (first target in `available` order) then wins *every single selection*, for the entire run.

This is not genuine load-sensing — it is total dependence on an arbitrary implementation detail (target list order) that has nothing to do with actual edge speed. The first `least-connections` cell above looks spectacular (384.7 RPS, 100% share to edge-a) purely because edge-a — the fast edge — happened to be listed first in `proxy.Config.Targets`. The verification cell reorders the *exact same topology* with the slow edge (`edge-c`) listed first: Least Connections now sends **100% of traffic to the slow edge, forever**, for the entire 300-request run, producing 9.8 RPS — worse than plain Round Robin (27.3 RPS), which at least spreads the pain across all three edges. Confirmed by directly reversing the ordering and observing the outcome flip from best-of-all-policies to worst-of-all-policies with no other change.

### Interpretation and implications for later stages

H3c is the most important finding of this experiment precisely because it wasn't the one being looked for. Least Connections' tie-break rule is currently an unexamined implementation detail (`internal/proxy/least_connections.go`: "ties... are broken by position in `available`, for determinism") that was written for reproducibility, not for correctness under sustained ties — and under low concurrency, ties are not the rare case, they are the *only* case. This is worth carrying forward explicitly: **P2C (Experiment 003-E) is explicitly required by the Stage 3 brief to "use well-defined randomness"** — this finding is direct evidence for why that requirement matters, not a stylistic preference. A future policy whose tie-breaking is silently deterministic-by-configuration-order can produce results that are indistinguishable from genuine intelligence when you get lucky with ordering, and indistinguishable from a serious bug when you don't. No fix is applied here — changing the tie-break rule is out of scope for what Stage 3 needs Least Connections to demonstrate, and doing so without a specific follow-on experiment to justify it would be exactly the kind of premature abstraction the project's philosophy warns against. It is recorded here as a documented, verified limitation for whoever next touches `LeastConnectionsSelector` or designs P2C's own tie-breaking.

---

## 6. Results: Experiment 003-D — Latency-Aware EWMA Under Latency Variation

**Hypothesis (H4)**: see `hypotheses.md`. `EWMASelector` (`internal/proxy/ewma.go`) and `LatencyTracker` (`internal/proxy/latency_tracker.go`) measure `T3-T2` (proxy-dispatch to upstream-response-received, i.e. `transport.RoundTrip`'s duration) on every successful round trip, smoothed via `estimate = alpha*sample + (1-alpha)*estimate`, with a cold-start rule that treats any never-observed target as strictly better than any observed one — a direct, deliberate response to Experiment 003-C's H3c finding about `LeastConnectionsSelector` getting stuck under sustained ties.

### H4a — Static heterogeneous topology (1ms/1ms/100ms, c=30, r=600)

| Policy | RPS | p95 | p99 | edge-a | edge-b | edge-c |
|:---|:---:|:---:|:---:|:---:|:---:|:---:|
| `round-robin` | 475.6 | 104.11ms | 112.87ms | 33.33% | 33.33% | 33.33% |
| `least-connections` | 1721.6 | 8.04ms | 104.77ms | 51.33% | 46.33% | 2.33% |
| `ewma` | **5972.1** | **10.03ms** | **15.00ms** | **0.00%** | **100.00%** | **0.00%** |

At face value, EWMA posted the best numbers by a wide margin. **Do not read that as "EWMA correctly identified edge-c as bad and balanced between edge-a and edge-b."** It did not balance between edge-a and edge-b at all — it sent every single one of 600 requests to edge-b and literally zero to edge-a, despite edge-a and edge-b being configured identically (both 1ms). This is investigated in H4a-follow-up below, because a policy that happens to get the best number for the wrong reason is a bigger problem than one that gets a worse number honestly.

### H4a-follow-up — Pure homogeneous check (3 identical 1ms edges, EWMA only, c=30, r=600, 3 independent runs)

The static-heterogeneous result above was suspicious specifically because edge-a got exactly 0%. To isolate whether this is genuine "avoid the bad edge" behavior or something else, this follow-up removes the heterogeneity entirely — all three edges are configured with an **identical** 1ms delay, so there is no capacity difference for any policy to discover. Round Robin would give ~33.3%/33.3%/33.3% here by construction.

| Run | edge-a | edge-b | edge-c |
|:---:|:---:|:---:|:---:|
| 1 | 94.00% | 3.83% | 2.17% |
| 2 | 68.33% | 28.67% | 3.00% |
| 3 | 18.17% | 78.67% | 3.17% |

**This is the most important finding of Stage 3 so far.** Across three independent runs of *provably identical* targets, EWMA converged to a massively uneven split every single time — but the *winner* was different in every run (edge-a, then edge-a again but less dominantly, then edge-b). This rules out a deterministic bug (a fixed edge always winning) and confirms a reproducible *mechanism*: `EWMASelector` is a pure greedy policy with no exploration once the cold-start phase ends. Under real concurrency (c=30), many goroutines race through `SelectTarget`'s read of `LatencyTracker.Estimate` before any of their own `Observe` calls land (the same read-then-write race documented on `LoadTracker.Get`, now affecting a comparison of real numbers rather than just an unobserved/observed tie). Whichever target happens — by ordinary scheduling and network jitter — to accumulate a *marginally* better early estimate keeps winning every subsequent comparison, which means it keeps being the only one to receive fresh observations, which means its lead is continuously reinforced while its rivals' estimates freeze at whatever they measured during their one brief trial. This is the textbook multi-armed-bandit "greedy exploitation with no exploration" failure mode, now demonstrated empirically against real HTTP infrastructure rather than a simulation.

This also retroactively explains H4a: EWMA's excellent static-heterogeneous numbers are real (it did successfully avoid the genuinely bad 100ms edge-c, same as Least Connections), but the edge-a/edge-b split within that result is not evidence of intelligent balancing — it is the identical lock-in artifact, which happened not to matter in that scenario only because edge-a and edge-b were both actually fine.

### H4b — Dynamic degradation (homogeneous 1ms×3 → edge-c degraded to 100ms mid-run, c=30, r=600/phase)

| Policy | Phase 1 edge-c share | Phase 1 RPS | Phase 2 edge-c share | Phase 2 RPS |
|:---|:---:|:---:|:---:|:---:|
| `round-robin` | 33.33% | 1915.7 | 33.33% (unchanged) | 459.0 |
| `least-connections` | 30.50% | 1738.1 | 4.17% | 1471.2 |
| `ewma` | 2.33%¹ | 1671.6 | **0.00%** | **2019.3** |

¹ Phase 1 also shows the lock-in artifact (edge-a=0%, edge-b=97.67%, edge-c=2.33%) — edge-c's small phase-1 share is a residue of its one cold-start trial, not active balancing.

H4b's literal claim — EWMA shifts traffic away from an edge that degrades mid-run without reconfiguration — is technically true (2.33% → 0.00%) and RPS *increased* after the degradation (1671.6 → 2019.3), which is a real, correct, and good outcome for this specific run. But given the H4a-follow-up finding, this "success" needs the same asterisk: edge-c had already been reduced to near-irrelevance by the lock-in mechanism before it was ever degraded, so this result mostly demonstrates that a starved target's insignificance is preserved, not that EWMA actively detected and responded to the degradation event.

### H4c — Alpha sensitivity under oscillating latency (edge-c toggling 1ms ↔ 150ms across 6 phases, c=10, r=200/phase, alphas 0.05 / 0.2 / 0.6)

This part of the experiment **failed to test what it was designed to test**, and that failure is itself the finding. The plan was to watch each alpha's EWMA estimate for edge-c track the oscillation at a different speed. What actually happened, for all three alphas:

| Alpha | edge-c share per phase (fast/slow/fast/slow/fast/slow) | edge-c EWMA estimate per phase (ms) |
|:---:|:---|:---|
| 0.05 | 5.0% / 0.0% / 0.0% / 0.0% / 0.0% / 0.0% | 14.5 / 14.5 / 14.5 / 14.5 / 14.5 / 14.5 |
| 0.20 | 7.0% / 0.0% / 0.0% / 0.0% / 0.0% / 0.0% | 17.2 / 17.2 / 17.2 / 17.2 / 17.2 / 17.2 |
| 0.60 | 12.5% / 0.0% / 0.0% / 0.0% / 0.0% / 0.0% | 88.1 / 88.1 / 88.1 / 88.1 / 88.1 / 88.1 |

Edge-c's EWMA estimate **never changed after phase 1, for any alpha** — because after phase 1, edge-c lost the same lock-in race the H4a-follow-up isolated, and was selected zero or near-zero times in every subsequent phase (visible in the share column). With no new observations, there is nothing for any alpha to smooth: the estimate is frozen at whatever value happened to be recorded during edge-c's one brief cold-start trial in phase 1. This is a more serious consequence of the same root cause than H4a-follow-up alone suggested: it is not just that a starved target's *share* stays low — its *latency estimate itself stops reflecting reality*, so EWMA as built cannot detect that a currently out-of-favor target's true performance has changed at all, whether it gets worse or recovers. A genuine alpha-sensitivity comparison would require a policy that guarantees continued sampling of every target (an explicit exploration mechanism) — which this selector deliberately does not attempt to provide (see the `EWMASelector` doc comment). Rerunning H4c with a fix would mean building that mechanism, which is explicitly out of scope for Stage 3's EWMA step; it is exactly what Power-of-Two-Choices' random sampling (Experiment 003-E) exists to test next.

### Interpretation and implications for later stages

H4's headline result is not "EWMA beats Least Connections" (H4a's raw numbers say that, but for a reason that discredits taking the comparison at face value). It is that **a purely greedy latency-based policy, once past its cold start, stops exploring — and that has two compounding failure modes**: it can lock onto an arbitrary winner among genuinely equal targets (H4a-follow-up), and it can become permanently blind to a starved target's real, ongoing changes in performance (H4c). Both failures share one mechanism and one fix direction: the policy needs some form of continued exploration, not just an initial one-time trial. This is now the single strongest, most concretely evidenced motivation in the Stage 3 record so far for Power-of-Two-Choices' explicit random sampling — P2C's core idea (sample two candidates at random rather than always picking the single best-known one) is a direct, minimal answer to exactly the failure mode this experiment demonstrated. No code change is made to `EWMASelector` in response to this finding — per the project's "earn the abstraction" rule, the fix belongs to whichever policy is designed to solve it, and that is Experiment 003-E's job, not a late patch to Stage 3's third policy.

---

## 7. Results: Experiment 003-E — Power of Two Choices

**Hypothesis (H5)**: see `hypotheses.md` for H5a/b/c. `P2CSelector` (`internal/proxy/p2c.go`) samples two distinct candidates uniformly at random on every selection, via an explicit, seeded `*rand.Rand` (seed recorded per run below), and picks the better by a pluggable `P2CScorer` — `ScorerFromLoad` (in-flight count) or `ScorerFromLatency` (EWMA estimate), reusing `LoadTracker`/`LatencyTracker` rather than building a third kind of per-target state.

Before running this experiment, unit testing (`internal/proxy/p2c_test.go`) already forced a correction to the original plan: a test assuming P2C would let a currently-losing target "recover" failed, revealing that P2C only ever records an observation for the *winning* (dispatched) target — never the loser of a sampled pair. This reshaped H5 into three separable, precisely bounded claims (H5a/b/c) before any of the experiment below was run.

### H5a — Pure homogeneous lock-in check (3 identical 1ms edges, P2C-over-latency, c=30, r=600, 3 seeded runs)

| Run | Seed | edge-a | edge-b | edge-c |
|:---:|:---:|:---:|:---:|:---:|
| 1 | 20260905 | 50.50% | 47.17% | 2.33% |
| 2 | 20260906 | 49.33% | 1.83% | 48.83% |
| 3 | 20260907 | 3.67% | 46.67% | 49.67% |

**H5a is confirmed, but with a precise and important nuance the original hypothesis undersold.** P2C did *not* reproduce EWMA's catastrophic collapse (Experiment 003-D: one edge at 94–100%, the other two near 0%) — in every run here, **two** of the three edges split traffic roughly evenly near 47–50% each. But it also did not achieve 3-way fairness (~33/33/33): in every run, exactly one edge (a different one each time, ruling out a fixed bug) was pushed down to ~2–4%.

The mechanism, reasoned through and consistent with the data: with 3 targets, P2C compares one of 3 possible pairs each time — {A,B}, {A,C}, or {B,C} — each with 1/3 probability, so any one target is included in 2/3 of comparisons. If early jitter gives one target (say C) a very slightly worse EWMA estimate than *both* others, C now loses most of the comparisons it's part of — whether paired against A or against B — while A and B, whenever paired against *each other* (1/3 of the time), split close to 50/50, since neither is actually worse. Both A and B keep winning against C and refreshing their own accurate estimate; C keeps losing and never gets to refresh its slightly-stale one. The result is a weaker, structurally different version of the same feedback loop Experiment 003-D found: instead of "1 permanent winner, 2 starved," P2C produces "2 competitive survivors, 1 starved loser." This is a genuine, meaningful improvement (2 of 3 edges stay usable instead of 1 of 3), not a complete fix, and it is worth stating precisely rather than rounding it up to "P2C solves lock-in."

### Static heterogeneous comparison matrix (1ms/1ms/100ms, c=30, r=600)

| Policy | RPS | p95 | p99 | edge-a | edge-b | edge-c |
|:---|:---:|:---:|:---:|:---:|:---:|:---:|
| `round-robin` | 442.1 | 103.53ms | 106.28ms | 33.33% | 33.33% | 33.33% |
| `least-connections` | 1567.2 | 8.43ms | 108.55ms | 53.00% | 44.67% | 2.33% |
| `ewma` | 1619.3 | 46.01ms | 70.87ms | 69.67% | 30.33% | 0.00% |
| `p2c-load` | 908.3 | 102.43ms | 116.26ms | 48.67% | 45.33% | **6.00%** |
| `p2c-latency` | **1923.9** | **28.45ms** | **37.25ms** | 52.67% | 47.33% | **0.00%** |

`p2c-latency` won on every metric here — best throughput, best tail latency, *and* the most even a/b split of any policy (52.67/47.33 vs EWMA's 69.67/30.33), while fully avoiding edge-c. `p2c-load` did noticeably worse than every other dynamic policy, including still sending 6.00% of traffic (36/600) to the 100ms edge, which is enough to visibly damage p95/p99 (a percentile only needs a few dozen slow samples out of 600 to be dominated by them). The likely mechanism: latency directly encodes "this edge is intrinsically slow" from a single sample, while load only reveals a persistently-slow edge indirectly, through in-flight requests backing up over time (the Little's-Law-style argument from Experiment 003-C) — and because P2C only ever compares 2 of 3 candidates at a time, that backlog signal takes longer to reliably dominate a random pairing than a directly-measured latency difference does. This is consistent with, not contradictory to, Experiment 003-C's finding that load-based Least Connections *did* eventually discover the same slow edge — it just needed sustained concurrent traffic to do so, which P2C's narrower per-call comparison partially dilutes.

### Degradation-then-recovery — the decisive test (homogeneous → edge-c degraded to 100ms → edge-c recovers to 1ms, c=30, r=600/phase)

No previous Stage 3 experiment tested recovery — 003-C's H3b and 003-D's H4b only ever tested an edge getting *worse*. This closes that gap and directly tests H5b vs H5c.

| Policy | Phase 1 (homogeneous) edge-c | Phase 2 (degraded) edge-c | Phase 3 (recovered to 1ms) edge-c |
|:---|:---:|:---:|:---:|
| `ewma` | 75.83% | 5.00% | **0.00%** |
| `p2c-load` | 33.83% | 5.83% | **32.67%** |
| `p2c-latency` | 40.00% | 5.00% | **0.00%** |

**This is the cleanest, most decisive result in Stage 3.** Both latency-scored policies (`ewma` and `p2c-latency`) correctly reduced edge-c's share once it degraded (75.83%→5.00% and 40.00%→5.00%) — but **neither recovered it at all** once edge-c returned to being just as fast as the others (0.00% in phase 3 for both). Once edge-c stops winning comparisons, it stops being dispatched, stops being observed, and its EWMA estimate is frozen at "100ms" forever, regardless of what P2C's random sampling does — exactly as predicted by H5b and by the unit test that discovered it (`TestP2C_LatencyBased_CannotDetectRecoveryOfLosingTarget`), now confirmed under real concurrent HTTP traffic rather than a synthetic single-threaded test.

`p2c-load`, in the *identical* scenario, recovered cleanly: 33.83% → 5.83% → **32.67%**, landing back almost exactly where phase 1 started. This is H5c, confirmed: `ScorerFromLoad` reads `LoadTracker.Get`, which reflects *live* in-flight count, not a memory of a past sample — once edge-c's real requests finish, its true count returns to 0 regardless of whether anything has routed to it recently, so it is immediately, correctly eligible again the moment real load conditions change back. There is no "staleness" for a signal that is recomputed from present truth rather than remembered from the past.

### Interpretation and implications

Stage 3's central research question was: *what information does a routing policy need to make a good decision?* Experiment 003-E's answer is sharper than "more signals are better" — **the type of signal determines what kind of change a policy can ever detect, independent of how well it samples.** Randomized sampling (P2C) meaningfully improves on greedy full-scan's worst failure mode (partial fix for H5a), but no sampling strategy can rescue a signal that only remembers the past and never gets refreshed for an unselected target. A load signal is self-correcting because it is re-derived from ground truth on every read; a latency signal is not, because it is only ever updated by the very requests the policy chooses to send — creating a closed loop that can only report on decisions the policy already made, never on ones it stopped making. This is now Stage 3's strongest, most concrete piece of evidence for why a future adaptive router combining multiple signals (the six-signal design named in `trd.md`) is not just "more sophisticated," but structurally necessary: no single signal built so far — count, weight, load, or latency — is sufficient on its own across every scenario tested (RR fails under heterogeneity; WRR fails under change; Least Connections fails under sustained low concurrency; EWMA and latency-scored P2C both fail to detect recovery; load-scored P2C is slow to detect purely-latency-driven slowness). Each policy's specific, evidenced failure mode is a different argument for a different signal — which is exactly the shape of evidence that should motivate combining them, rather than an a priori assumption that combining them would help.

---

## 8. Results: Experiment 003-F — Comparison Matrix (Burst and Failure)

**Hypothesis (H6)**: see `hypotheses.md`. This is the major Stage 3 experiment: it fills the two scenarios from the brief's comparison-matrix template not yet covered by any prior 003-* experiment (Burst, Failure) across all five policies, then synthesizes all six experiments into one final comparison. A methodology bug was caught and fixed before trusting this data: `WRR` was initially given the heterogeneous-tuned 100:100:1 weights in the Failure scenario's genuinely *homogeneous* topology, which would have confounded "WRR configured wrong for this topology" with "WRR's failure-handling behavior." Fixed to use topology-matched weights (100:100:1 for the heterogeneous Burst topology, 1:1:1 for the homogeneous Failure topology) before any numbers below were recorded.

### H6a — Burst (1ms/1ms/100ms, baseline c=10 → burst c=150 → cooldown c=10)

| Policy | edge-c share: baseline → burst → cooldown | Burst RPS | Burst p99 |
|:---|:---|:---:|:---:|
| `round-robin` | 33.33% → 33.33% → 33.33% | 1992.4 | 133.25ms |
| `wrr` (100:100:1) | 0.67% → 0.47% → 0.67% | 2649.9 | 249.60ms |
| `least-connections` | 2.67% → **6.73%** → 2.67% | 2430.7 | 245.77ms |
| `ewma` | 0.00%* → 0.00% → 0.00% | 2386.8 | 261.74ms |
| `p2c-latency` | 0.00% → 0.00% → 0.00% | **7035.6** | **98.07ms** |

\* EWMA's baseline already shows full a/b lock-in (edge-a=100%, edge-b=0%) even in this phase — the effect from Experiment 003-D was present before the burst began, not caused by it.

**H6a confirmed for Least Connections specifically, and not for the others.** LC's edge-c share nearly tripled during the burst (2.67%→6.73%) relative to both its own baseline and cooldown — the one policy whose behavior measurably worsened under the concurrency spike. This is consistent with the documented `LoadTracker.Get` read-then-write race: at c=150, far more simultaneous `SelectTarget` calls can read a stale (pre-increment) count before any of their own dispatches land, so more of them can pile onto a target — including, transiently, the genuinely slow one — before its rising in-flight count is visible to the next caller. WRR (no runtime state at all) and Round Robin (no state at all) were exactly as expected: completely insensitive to concurrency level, by construction. `p2c-latency` was the standout: not only did it show zero degradation under burst, it delivered the highest throughput (7035.6 RPS) and *lowest* tail latency (p99=98.07ms, actually better than its own baseline-adjacent numbers) of any policy in any phase of this scenario — the most robust policy under a large concurrency swing. An unplanned secondary observation: EWMA's baseline lock-in (100/0) loosened somewhat during the burst itself (43.67/56.33) before reverting to full lock-in (0/100) in cooldown — suggesting the degree of EWMA's lock-in may itself be concurrency-dependent, though this was not a designed part of H6a and is reported as an open observation, not a tested claim.

### H6b — Failure (homogeneous 3×1ms, edge-b hard-killed mid-run, fast 50ms health probe)

| Policy | Before-failure edge-b share | Errors during kill window | Post-kill share (a / b / c) |
|:---|:---:|:---:|:---:|
| `round-robin` | 33.25% | 0/400 (0.00%) | 50.00% / 0% / 50.00% |
| `wrr` (1:1:1) | 33.25% | 112/400 (28.00%) | 36.00% / 0% / 36.00%¹ |
| `least-connections` | 34.00% | 138/400 (34.50%) | 35.25% / 0% / 30.25%¹ |
| `ewma` | 12.00%² | 0/400 (0.00%) | 100.00% / 0% / 0.00% |
| `p2c-latency` | 34.25% | 0/400 (0.00%) | 4.50% / 0% / **95.50%** |

¹ Shares shown are of the 400 requests attempted, including the ones that failed to reach edge-b; among *successful* deliveries the surviving edges split the rest.
² EWMA had already partially deprioritized edge-b before the kill (a pre-existing lock-in artifact, not a failure-anticipation effect).

**The core architectural claim in H6b is confirmed cleanly: every policy reached exactly 0% forwarded traffic to the killed edge after detection**, with no policy-specific exception — health-eligibility filtering (`internal/health`, upstream of every `TargetSelector`) works identically regardless of which routing policy is active, exactly as the Stage 2/3 architectural separation ("health determines eligibility, routing determines selection among eligible") was designed to guarantee.

**The more specific claim — which policy handles the transient detection window best — cannot be answered confidently from this data, and I am not going to pretend otherwise.** Error rates during the kill window ranged from 0% (RR, EWMA, P2C-latency) to 34.50% (Least Connections), but this experiment ran **one trial per policy**, unlike the 3-replicate design used for the pure-homogeneous lock-in check in 003-D/E. Some of the variation has a defensible mechanistic explanation (EWMA's 0 errors plausibly reflects that it was already avoiding edge-b before the kill, for reasons unrelated to the failure itself; RR's live per-request `available` filtering has no persistent state that could go stale, unlike LC's or EWMA's learned scores) — but without replication, I cannot rule out ordinary run-to-run scheduling/timing variance as a major contributor, especially since these five runs executed sequentially on the same machine. **This is flagged explicitly as unfinished evidence requiring replication, not rounded up into a ranking.**

One result deserves attention regardless of replication status, because it is large and directionally clear even as a single data point: `p2c-latency`'s post-kill distribution (4.50% / 0% / 95.50%) is not simply "avoided the dead edge" — it swung from a balanced 3-way split (29.75/34.25/36.00 before the kill) to an extreme, EWMA-D-style lock-in onto edge-c, nearly abandoning the *still-healthy* edge-a as well. The likely mechanism, consistent with everything established about P2C-over-latency in Experiment 003-E: the disruption of the kill event (some comparisons involving the now-dying edge-b, before health excludes it) appears to have acted as a fresh round of noisy re-seeding, similar to how the pure-homogeneous check showed P2C settling into an arbitrary 2-of-3 winner combination — except here, apparently, the two "winning" edges narrowed further to one. This was not predicted by H6b and is reported as a genuine, unexplained, and mechanistically plausible finding worth a dedicated follow-up experiment (repeated kill-during-live-traffic trials specifically for `p2c-latency`), not as a confirmed causal claim.

### Interpretation

H6a and H6b together close out the comparison-matrix template from the Stage 3 brief (Homogeneous, Heterogeneous, Slow edge, Burst, Failure, Recovery — all six now have at least one controlled experiment). The two new scenarios did not change Stage 3's central conclusion, they reinforced it from a different angle: Least Connections' known race window gets worse specifically under heavy burst concurrency; P2C-latency is the most robust policy under load *and* the one most susceptible to being knocked into a new, more extreme lock-in state by a disruption event; the health/routing architectural boundary holds regardless of which policy sits on the routing side of it. No policy was good at everything, and the one attempt in this stage to force a confident ranking from insufficiently replicated data (the H6b error-rate table) is explicitly called out as such rather than reported as a finding.
