# Stage 4 Learning Notes — Edge Caching, Coalescing, Failures & Network Degradation

## 1. Initial Assumptions

Before Stage 4, our assumptions based on Stage 3's findings and the stage's own master context were:

1. **Caching would be a "boring win."** We expected a TTL cache to reduce upstream load roughly in proportion to hit ratio, with the interesting work being the routing decisions made once data lives in more than one place — not the caching mechanism itself.
2. **Request coalescing would be a strict improvement.** "Deduplicate identical concurrent work" sounded like a mechanism with no real downside — something that could only help, never change behavior in a way worth measuring.
3. **A real origin-outage test would mostly confirm what unit tests already covered.** We expected 004-E to be a validation exercise for invariants already proven with synthetic errors, not a source of new information.
4. **Network degradation would be a smoke test, not a finding.** We expected 004-F to answer "does the system still basically work under a bad link," not to surface a qualitatively new behavior.
5. **`tc netem` would be used as the TRD specifies**, without needing to design around its absence.

Two of these were wrong in ways that turned out to matter more than anything we expected to find.

## 2. What We Built

```text
internal/cache/
├── cache.go          # Cache — fixed-TTL, lazy-eviction response cache
├── cache_test.go
├── coalesce.go        # Coalescer — leader/waiter deduplication of concurrent fetches
└── coalesce_test.go

internal/netsim/
├── netsim.go          # Transport — http.RoundTripper wrapper simulating latency/jitter/loss
└── netsim_test.go

internal/clock/
└── clock.go            # MockClock promoted from a private test type to an exported one

internal/topology/
├── edge.go             # EdgeServer: cache, coalescer, and netsim all wired in as opt-in knobs
├── origin.go            # OriginServer: ConcurrencyStats (active/peak concurrent request tracking)
└── topology_test.go     # +24 tests across cache, coalescing, real-failure, and netsim scenarios
```

Five experiment binaries (`cmd/experiment-004a`, `004c`–`004f`; `004b`/`004g` deliberately not built — see §8), 16 recorded JSON results under `experiments/004-caching-failures/results/`, and 36 new tests (108 pass across the whole repository at Stage 4's close, up from 72 at Stage 3's).

## 3. What Experiments We Ran, and What Question Each One Asked

| Experiment | Question |
|---|---|
| 004-A (No-Cache) | With no cache at all, does every request reach Origin exactly once, and is a hot key treated any differently from a cold one? |
| 004-A (TTL-Cache) | Does a TTL cache meaningfully reduce upstream load and improve latency against that same fixed baseline? |
| 004-C | What happens when a warm cache entry's TTL elapses and a burst of concurrent requests for that exact key arrives right after? |
| 004-D | Does deduplicating concurrent misses for the same key (coalescing) actually eliminate the stampede's wasted work? |
| 004-E | What does the cache and coalescer actually do when Origin isn't slow or overloaded, but genuinely, completely down? |
| 004-F | What does a degraded-but-not-dead link (added latency, partial packet loss) do to cache insulation — and does coalescing behave differently under partial loss than it did under 004-E's total outage? |

## 4. What Happened

The full numbers, per experiment, are in `experiments/004-caching-failures/README.md`. The arc:

- **No-Cache baseline** confirmed its own precondition exactly: 3000/3000 client successes produced 3000 upstream requests, p50 sat almost exactly at Origin's configured delay, and hot/cold keys were indistinguishable — the deliberately boring fixed point everything else in this stage is measured against.
- **TTL-Cache** cut upstream requests by 98.77% (3000 → 37) and raised throughput 11.6× (1870 → 21652 RPS), with p50 collapsing and p99 barely moving — exactly the asymmetric shape predicted, since the tail is dominated by the misses a cache can't avoid. It also surfaced, unplanned, a stampede signal in its own warmup phase (30 concurrent first-touch requests to one path produced 30 misses, 0 hits) before Experiment 004-C existed to study that phenomenon on purpose.
- **Cache Stampede (004-C)** confirmed that signal deliberately and precisely: at burst sizes 10/30/100 against one just-expired key, upstream requests equaled the burst size *exactly* at every level, with Origin's peak concurrency scaling identically — real, unbounded duplicate work, growing linearly with no sign of a ceiling.
- **Coalescing (004-D)** eliminated it completely: the identical burst, at every concurrency level, produced exactly 1 upstream request and peak concurrency of 1 — not "reduced," eliminated. The only place the result was modest rather than dramatic was p99, and that gap was itself explained rather than left as an unexplained shortfall (see §5).
- **Origin Outage (004-E)** showed both mechanisms hold up against a real failure, not just synthetic ones: a warm key succeeded 20/20 times with Origin completely down, a 30-way concurrent burst against a never-cached key collapsed to a single failed dial (not 30), the failure was shared identically by every caller, nothing was left in a stuck state once the burst ended, and the very next request after Origin came back succeeded with no special recovery step.
- **Network Degradation (004-F)** extended the insulation story to a link that's degraded but not dead (added latency, partial loss) — a miss paid the simulated latency almost exactly, a hit didn't, and independent cold-key requests failed near the configured loss rate. Then it surfaced this stage's most important finding: under 30% simulated loss, 50 coalesced bursts were 50/50 all-or-nothing, while 50 independent bursts were 49/50 partial — the aggregate failure rate barely moved (32.0% vs 29.2%), but *who fails together* changed completely.

## 5. What Surprised Us

In order of how much they changed our understanding:

1. **Coalescing's failure-correlation effect (004-F) was the single biggest surprise of the stage.** Every earlier coalescing result (004-C, 004-D, 004-E) was a clean, unqualified win, because in a healthy-origin stampede and a total outage, "everyone shares one outcome" and "everyone gets their own independent outcome" happen to produce the *same* expected result (all succeed, or all fail, respectively). A partially lossy link is the one condition in this stage where those two framings genuinely diverge — coalescing forecloses the possibility that some callers in a burst succeed while others don't, a possibility that's real under independent dialing. We did not go looking for this; it fell out of asking the more general question "what does a degraded link do" rather than stopping at "up or down."
2. **The stampede signal appeared before the stampede experiment did.** 004-A's warmup phase — 30 identical concurrent requests, meant only to warm connection pools — produced 30 misses and 0 hits, a small, unplanned live demonstration of exactly the race Experiment 004-C was designed three steps later to study on purpose.
3. **Origin's peak concurrency scaled with burst size with no measurement noise at all** — 10/10, 30/30, 100/100, exactly, at every level tested in 004-C. Real concurrent goroutine scheduling reliably reproduced the race condition every single time, with none of the flakiness we half-expected from wall-clock-timed concurrent tests.
4. **Origin's own model limited what 004-C and 004-D could show about tail latency.** `OriginServer`'s handler is one goroutine plus `time.Sleep`, which is closer to an infinite-server queue than a real, resource-constrained backend — so a 100-way duplicate stampede barely moved p99 (100ms → 115ms), and coalescing's own p99 win in 004-D was correspondingly modest. This wasn't hidden or treated as a shortfall; it was named explicitly as a property of the current Origin model, not of the phenomena being measured.
5. **The "no abandoned in-flight entry" invariant, built defensively before any real failure existed to test it, needed no changes once a real one did.** `Coalescer`'s panic-safe cleanup and leader/waiter bookkeeping were designed and unit-tested against synthetic errors first; the only failure this stage's real-outage test (`TestEdgeServer_Coalesce_FailureCleansUpAndRecovers`) actually caught was a *test-design* flaw (the coalescing window was too narrow against a connection-refused failure that returns almost instantly), not a bug in the production invariant itself.
6. **`tc netem`'s absence was itself informative.** The TRD assumed Linux-only OS-level network shaping without qualification. Discovering, concretely, that this project's actual environment couldn't run it forced a real design decision (build `internal/netsim` instead) rather than a hypothetical one, and made "state the limitation honestly instead of pretending the substitute is equivalent" a concrete practice rather than an abstract principle.

## 6. Why the Results Happened (Mechanistic Interpretation)

The throughline connecting items 1, 2, and 4 above: **every mechanism this stage built reduces work by converging independent things into one.** A cache converges N future requests for a key into "1 fetch, then N-1 free reads." Coalescing converges N *concurrent* requests for a key into "1 in-flight fetch, N-1 shared waits." Both are, structurally, the same move one level apart in the request lifecycle — and both carry the same consequence: once N callers' fates converge onto one operation, they stop being independent trials and become one shared trial repeated N times in outcome.

That consequence is invisible whenever the shared outcome and the independent-trial outcome would have matched anyway. A healthy origin under a stampede: every independent fetch would have succeeded, so the one shared fetch succeeding changes nothing about who's happy. A total outage: every independent fetch would have failed, so the one shared failure changes nothing either. It only becomes visible — and costly, or at least worth naming — in exactly the condition 004-F introduced on purpose: a link where independent trials would have produced a *mix* of outcomes. That's the one scenario where convergence has a real effect on the distribution of who succeeds, not just on the total amount of work done.

## 7. What Changed in Our Understanding

Going in, "removes duplicate work" and "is strictly beneficial" read as the same claim. Coming out, they're provably not the same claim: removing duplicate work by sharing one outcome across many callers is only free when those callers' independent outcomes would have agreed anyway — and there is no way to know that in advance for a real, imperfect link, only in the two extremes (perfectly healthy, or completely down) this stage checked first. This is a genuinely different category of tradeoff than anything Stage 3 surfaced. Stage 3's policies chose *which* of several truly independent backends serves a request — no Stage 3 mechanism ever made two callers' outcomes causally depend on each other. Stage 4's caching and coalescing both do exactly that, by design, and 004-F is the first experiment in this project with the shape needed to make that dependency visible.

## 8. What This Motivates Next

Two experiments planned at the start of this stage were deliberately not built, and the reason matters as much as the decision: **004-B (cache capacity/LRU)** was skipped because it answers a different question — what happens when cache capacity is constrained relative to the working set — that isn't necessary to establish this stage's architectural lesson, and building it now would add a second state-management mechanism on top of an already-clear causal chain (`cache → stampede → coalescing → failure semantics → network degradation`) without adding new evidence for that chain. **004-G (a combined scenario)** was skipped because, at this point, it would risk becoming a "does everything work together" integration check rather than a clean, individually interpretable experiment — exactly the kind of complexity this project's evidence-first discipline says to add only once it's earned, and a combined scenario is more useful later, once there is deterministic execution machinery to make it reproducible rather than a one-off wall-clock run.

That last phrase is the real throughline to Stage 5. Getting clean results this stage required a steadily growing pile of one-off concessions to real wall-clock time and real OS networking: `clock.MockClock` to force deterministic TTL expiry, a pre-reserved fixed local port so Origin could be stopped and restarted deterministically, an artificial edge-side delay whose *only* purpose was widening a race window so a burst would reliably coalesce instead of racing past the leader registration, and — the largest one — an entire from-scratch network simulator built specifically because the tool this stage's own design document assumed (`tc netem`) doesn't exist on this project's host OS at all. Each fix was reasonable in isolation. Stacked together, they are the concrete, evidenced cost of continuing to run correctness and degradation experiments against real time and a real network: every new experiment in this space is going to need its own bespoke workaround to stay controllable and reproducible. That is precisely the problem this project's dual-engine architecture vision anticipated, and precisely the case for building it now rather than later:

```text
Stage 3: Routing alone is insufficient.
        ↓
Stage 4: Edge-local state fundamentally changes system behavior.
        ↓
Caching reduces upstream work, but expiration creates coordinated misses.
        ↓
Coalescing removes duplicate work, but changes failure correlation.
        ↓
Network degradation exposes behavior application-level delay can't fully represent.
        ↓
Real-time experiments become increasingly difficult to control and reproduce precisely.
        ↓
Stage 5: Deterministic Virtual-Time Engine
```
