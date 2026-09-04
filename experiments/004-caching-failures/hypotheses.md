# Hypotheses: Experiment 004 — Edge Caching, Coalescing, Failures & Network Degradation

## H1 — No-Cache Baseline Characterization
Before any cache exists, the edge forwards every request to Origin regardless of how many times the same object has already been requested. Under a hot/cold key-access workload (one key receiving roughly half of all traffic, the remainder split across several cold keys), a No-Cache edge should show:

- *Prediction 1*: Upstream request count to Origin equals the number of successful client requests exactly — there is no mechanism by which a request could avoid reaching Origin.
- *Prediction 2*: Client-observed latency percentiles are dominated by Origin's own processing time (its configured artificial delay) plus network/dispatch overhead, and should be largely uniform across hot and cold keys — the No-Cache edge has no way to treat a "hot" key any differently from a "cold" one.
- *Purpose*: this run is not itself a finding — it exists to be the fixed reference point that the eventual TTL-cache run (same workload, same topology, only the edge's cache state differs) is compared against. Without a controlled No-Cache baseline, any later "caching helped" claim would have nothing rigorous to be measured against.

## H2 — TTL Cache Substantially Reduces Upstream Work and Improves Hot-Key Latency
Under the identical hot/cold key workload used for H1, an edge with a TTL cache (TTL configured longer than the whole run, so no mid-run expiry confounds this specific comparison — that is Experiment 004-C's question, not this one) should:

- *Prediction 1*: Reduce upstream (edge→origin) request count dramatically relative to the No-Cache baseline — most requests to any given key, after that key's first request, should be served from the edge without touching Origin at all.
- *Prediction 2*: Reduce p50 substantially, since the majority of requests become cache hits (no 15ms Origin round trip); p99 should be much less improved, since it's disproportionately made up of the unavoidable cache-miss requests (each of which still pays Origin's full processing time).
- *Explicit non-prediction*: this experiment does **not** predict the exact number of misses will equal the number of distinct keys (9). Under real concurrency, multiple requests for the same not-yet-cached key can race through before any of them completes and fills the cache — each of those races is itself a small, unplanned preview of the cache stampede Experiment 004-C studies directly. If observed, this should be reported as data, not treated as contamination to explain away.
