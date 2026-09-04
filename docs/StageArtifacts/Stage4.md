# Stage 4 — Edge Caching, Coalescing, Failures & Network Degradation: Exit Artifact

## What Was Built

| Component | File(s) | Description |
|---|---|---|
| TTL response cache | `internal/cache/cache.go` | Fixed-TTL, lazy-eviction cache; GET-only, 2xx-only; injected `clock.Clock` |
| Request coalescing | `internal/cache/coalesce.go` | `Coalescer` — leader/waiter deduplication of concurrent fetches for the same key; panic-safe, no abandoned in-flight entries |
| Network condition simulator | `internal/netsim/netsim.go` | `Transport` — `http.RoundTripper` wrapper injecting latency/jitter/packet loss, built in place of `tc netem` (Linux-only, unavailable on this project's Windows host) |
| Deterministic time | `internal/clock/clock.go` | `MockClock` promoted from a private test type to exported, for deterministic TTL-expiry tests and experiments |
| Peak concurrency tracking | `internal/topology/origin.go` | `OriginServer.ConcurrencyStats()` — lock-free active/peak concurrent-request counters |
| Edge wiring | `internal/topology/edge.go` | Cache, coalescer, and netsim all wired in as independent, opt-in `EdgeConfig` knobs (zero value = off, matching the existing `DefaultDelay` convention) |
| Five experiment suites | `cmd/experiment-004a`, `004c`–`004f` | No-Cache/TTL-Cache baseline, Cache Stampede, Coalescing before/after, Origin Outage & Recovery, Network Degradation |
| Experiment documentation | `experiments/004-caching-failures/{hypotheses,README}.md` | H1–H6, methodology, and full results for all five experiments |
| Learning notes | `docs/learning/004-caching-failures.md` | Full before/after, surprises, mechanism, and motivation-for-next-stage narrative |

**No changes were made to Stage 1–3 code.** `TargetSelector` and every Stage 3 routing policy are untouched — Stage 4 deliberately simplified the topology to `Client -> Edge -> Origin` (no `Proxy` layer) since caching and coalescing are edge-local concerns orthogonal to upstream routing, a deliberate scope decision stated up front rather than an oversight.

Two experiments planned at the stage's outset were deliberately not built: **004-B (cache capacity/LRU)** and **004-G (a combined scenario)**. Both are addressed in Known Limitations and in `docs/learning/004-caching-failures.md` §8 — the short version is that Stage 4's evidence already established its architectural lesson without them, and adding either now risked diluting a clean causal chain rather than strengthening it.

---

## Repository Tree (Stage 4 additions)

```text
flashflow/
├── cmd/
│   ├── experiment-004a/main.go   # No-Cache baseline + TTL-Cache comparison
│   ├── experiment-004c/main.go   # Cache Stampede
│   ├── experiment-004d/main.go   # Request Coalescing before/after
│   ├── experiment-004e/main.go   # Origin Outage & Recovery
│   └── experiment-004f/main.go   # Network Degradation
├── docs/
│   └── learning/004-caching-failures.md
├── experiments/004-caching-failures/
│   ├── hypotheses.md              # H1-H6
│   ├── README.md                  # Full methodology + results, sections 1-11
│   └── results/                   # 16 JSON result files
├── internal/cache/
│   ├── cache.go / cache_test.go
│   └── coalesce.go / coalesce_test.go
├── internal/netsim/
│   └── netsim.go / netsim_test.go
├── internal/clock/
│   └── clock.go                   # modified: MockClock exported
└── internal/topology/
    ├── edge.go                    # modified: cache + coalescer + netsim wiring
    ├── origin.go                  # modified: ConcurrencyStats
    └── topology_test.go           # modified: +24 tests
```

---

## Tests Written (36 new, on top of Stage 3's 72)

| Area | Representative tests | Covers |
|---|---|---|
| Cache | `TestCache_ExpiredEntryIsLazilyEvicted`, `TestCache_ConcurrentAccess`, `TestKey_ExcludesHeadersIncludesQuery` | Lazy eviction verified at the map level, concurrency safety, correct cache-key shape |
| Coalescer | `TestCoalescer_ConcurrentCallsShareOneFetch`, `TestCoalescer_FailurePropagatesToAllWaiters`, `TestCoalescer_NoAbandonedEntryAfterFailure`, `TestCoalescer_PanicIsRecoveredAndSharedAsError` | The central leader/waiter promise, shared failure, and the "no abandoned in-flight entry" invariant under every exit path |
| Edge/Cache integration | `TestEdgeServer_Cache_MissThenHit`, `TestEdgeServer_Cache_ExpiredEntryRefetches`, `TestEdgeServer_Cache_DoesNotCacheErrorStatus` | Real HTTP hit/miss cycle, deterministic TTL expiry via `MockClock`, never-cache-failure rule |
| Edge/Coalescing integration | `TestEdgeServer_Coalesce_ConcurrentMissesShareOneUpstreamFetch`, `TestEdgeServer_Coalesce_LeaderCancellationDoesNotAbortWaiters` | 20-way real burst collapsing to one upstream fetch; the leader's own client disconnecting doesn't cancel work its waiters still need |
| Edge/Real-failure integration | `TestEdgeServer_Coalesce_FailureCleansUpAndRecovers` | A genuinely stopped Origin (not a mocked error) still fails cleanly, shares the failure, and recovers with no stuck state |
| Origin | `TestOriginServer_ConcurrencyStats_TracksPeak` | Peak concurrent-request tracking against 20 real simultaneous requests |
| Netsim | `TestTransport_LossRateOneAlwaysDropsWithoutCallingBase`, `TestTransport_ContextCancellationDuringDelayReturnsPromptly`, `TestTransport_EndToEndOverRealServer` | Loss never reaches the base transport, delay is interruptible by context cancellation, real end-to-end behavior against `httptest.Server` |
| Edge/Netsim integration | `TestEdgeServer_NetworkConditions_LatencyOnlyAffectsMisses`, `TestEdgeServer_NetworkConditions_LossFailsMissesButNotHits` | Cache insulation holds under simulated degradation, not just a total outage |

108 tests pass across the whole repository at Stage 4's close (72 at Stage 3's close + 36 new).

## Test Results

```
gofmt -l .        clean
go build ./...    clean
go vet ./...      clean
go test ./...     ok  (all 9 packages)
```

`go test -race` remains **unavailable in this environment** (no `gcc`; `CGO_ENABLED=1` fails building `runtime/cgo`) — stated honestly, as in every prior stage, not claimed as passed. Concurrency safety is argued explicitly in code (`Coalescer`'s mutex-guarded map, `netsim.Transport`'s serialized `rand.Rand` access) and exercised by dedicated concurrent tests, run repeatedly during development with no observed failures — including one real flakiness bug caught and fixed during development (`TestEdgeServer_Coalesce_FailureCleansUpAndRecovers` needed a wider artificial delay window to reliably coalesce a burst against a near-instant connection-refused failure; see `docs/learning/004-caching-failures.md` §5).

---

## Experiment Inventory & Key Results

| # | Title | Central Finding |
|---|---|---|
| 004-A | No-Cache baseline vs. TTL-Cache | TTL cache cut upstream requests 98.77% (3000→37) and raised throughput 11.6×; an unplanned stampede signal (30 concurrent misses, 0 hits in warmup) foreshadowed 004-C before it was designed |
| 004-C | Cache Stampede | Upstream requests equal burst size *exactly* at every level tested (10/10, 30/30, 100/100) — real, unbounded duplicate work with no sign of a ceiling; Origin's infinite-server-like model kept p99 from showing the queueing blowup a real backend would |
| 004-D | Request Coalescing | The identical burst collapses to exactly 1 upstream request and peak concurrency of 1 at every level — not reduced, eliminated; p99 improvement is modest, explained directly by 004-C's Origin-model caveat rather than treated as a shortfall |
| 004-E | Origin Outage & Recovery | A warm key succeeds 20/20 times through a total outage (pure cache insulation); a 30-way burst on a never-cached key collapses to 1 failed dial, shared identically, with no stuck state and immediate clean recovery |
| 004-F | Network Degradation | **Headline finding**: under 30% simulated loss, coalescing turns burst outcomes from 49/50 *partial* (independent dialing) to 50/50 *all-or-nothing* — aggregate failure rate barely moves (32.0% vs 29.2%), but who fails together changes completely; a real, previously invisible cost of coalescing that only a partially (not totally) degraded link could surface |

Full data, methodology, and per-experiment interpretation: `experiments/004-caching-failures/README.md` (11 sections, 16 JSON result files).

---

## Known Limitations (carried forward, not fixed in Stage 4)

1. **`OriginServer` behaves closer to an infinite-server queue than a real, capacity-constrained backend** (one goroutine + `time.Sleep` per request, no bound on concurrency). This is documented explicitly in 004-C and 004-D's own interpretation sections — it's the reason a 100-way duplicate stampede didn't produce the sharp nonlinear tail-latency blowup queueing theory predicts, and the reason coalescing's own p99 win looked modest rather than dramatic. Not fixed here, because doing so is a topology-model change orthogonal to what this stage's cache/coalescing/failure questions needed.
2. **`internal/netsim` is a request-level, in-process substitute for `tc netem`, not a validation that `tc netem` itself was used or even available.** It cannot reproduce packet-level effects (reordering, partial-write corruption, kernel qdisc queueing behavior) — only added latency and probabilistic loss at the HTTP-request granularity, which is what this stage's specific hypotheses (H6) needed and no more. Documented in the package's own doc comment, not left implicit.
3. **004-B (cache capacity/LRU) and 004-G (a combined scenario) were not built.** Both were deliberately descoped, not overlooked: 004-B answers a genuinely different research question (behavior under constrained capacity) that isn't necessary to establish this stage's architectural lesson, and 004-G would risk becoming an integration smoke test rather than a clean, individually interpretable experiment before this project has the deterministic execution machinery (Stage 5) to make a combined scenario reproducible.
4. **Getting clean, controllable results this stage required a growing set of real-time workarounds** — `clock.MockClock` for deterministic TTL expiry, a pre-reserved fixed local port for deterministic Origin restart, and an artificial edge-side delay whose only purpose was widening a coalescing race window against near-instant failures. None of these are bugs; together they are the concrete evidence motivating Stage 5, laid out in `docs/learning/004-caching-failures.md` §8.

None of these are "bugs" in the sense of violating a stated invariant — each is either an explicit modeling choice with a documented tradeoff, or a deliberate scope decision made from a position of already having sufficient evidence, consistent with this project's "earn the abstraction" rule.

---

## Gate-by-Gate Verdict

| Gate | Status | Notes |
|---|---|---|
| **1 – Implementation** | ✅ PASS | Cache, Coalescer, and netsim all implemented; wired into `EdgeServer` as independent opt-in knobs with zero-value-means-off semantics |
| **2 – Concurrency Safety** | ✅ PASS (by design + tests, not `-race`) | All shared state mutex- or atomic-guarded; dedicated concurrent tests including a real (not mocked) origin-outage scenario; `-race` unavailable in this environment (documented, not glossed over) |
| **3 – Cache/Coalescing Correctness** | ✅ PASS | GET-only, 2xx-only caching; no abandoned in-flight entries on success, failure, or panic; leader cancellation proven not to abort waiters, at both the unit and real-HTTP-integration level |
| **4 – Testing** | ✅ PASS | 108 tests pass repo-wide, `gofmt`/`go vet` clean |
| **5 – Empirical Research** | ✅ PASS | 5 experiment suites, 16 JSON result files, covering no-cache/TTL-cache, stampede, coalescing, real failure, and simulated network degradation |
| **6 – Evidence Discipline** | ✅ PASS | A real measurement bug (cache stats not baseline-subtracted, 004-A) and a real test-design flaw (too-narrow coalescing window against a near-instant failure) were both caught and fixed before results were trusted; `internal/netsim`'s limitations relative to `tc netem` are stated in its own doc comment, not glossed over; 004-B/004-G were scoped out explicitly with reasons on the record, not silently dropped |
| **7 – Documentation** | ✅ PASS | `hypotheses.md` (H1-H6), `README.md` (11 sections), `docs/learning/004-caching-failures.md`, this exit artifact |

---

## Stage 5 Readiness

**READY**

> Stage 4's strongest result — 004-F's discovery that coalescing preserves aggregate failure rate while fundamentally changing failure *correlation* — is exactly the kind of second-order behavior this project exists to uncover, and it required real, controlled network degradation to find. Producing it, and every other clean result this stage, meant paying a steadily growing tax in one-off concessions to real wall-clock time and real OS networking: a mock clock to force deterministic expiry, a fixed local port to make an outage-and-recovery cycle repeatable, an artificial delay whose only job was widening a race window, and — the largest one — an entire simulator built from scratch because the tool this project's own design assumed (`tc netem`) simply isn't available on the host running these experiments. Each was reasonable alone. Stacked together, they are the concrete case for Stage 5, not an abstract one:
>
> ```text
> Stage 3: Routing alone is insufficient.
>         ↓
> Stage 4: Edge-local state fundamentally changes system behavior.
>         ↓
> Caching reduces upstream work, but expiration creates coordinated misses.
>         ↓
> Coalescing removes duplicate work, but changes failure correlation.
>         ↓
> Network degradation exposes behavior application-level delay can't fully represent.
>         ↓
> Real-time experiments become increasingly difficult to control and reproduce precisely.
>         ↓
> Stage 5: Deterministic Virtual-Time Engine
> ```
>
> This is not a claim that Stage 4's own mandate is incomplete — the cache/coalescing/failure/degradation causal chain is fully evidenced on its own terms, which is exactly why 004-B and 004-G were left unbuilt rather than added to make the stage look larger. It's a claim that continuing to answer *this class* of question against real time and real networking has a rising, now-measured cost, and that Stage 5's deterministic virtual-time engine is the first architecture in this project actually earned by that cost, not assumed from the roadmap.
