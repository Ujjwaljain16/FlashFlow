# Hypotheses: Experiment 004 — Edge Caching, Coalescing, Failures & Network Degradation

## H1 — No-Cache Baseline Characterization
Before any cache exists, the edge forwards every request to Origin regardless of how many times the same object has already been requested. Under a hot/cold key-access workload (one key receiving roughly half of all traffic, the remainder split across several cold keys), a No-Cache edge should show:

- *Prediction 1*: Upstream request count to Origin equals the number of successful client requests exactly — there is no mechanism by which a request could avoid reaching Origin.
- *Prediction 2*: Client-observed latency percentiles are dominated by Origin's own processing time (its configured artificial delay) plus network/dispatch overhead, and should be largely uniform across hot and cold keys — the No-Cache edge has no way to treat a "hot" key any differently from a "cold" one.
- *Purpose*: this run is not itself a finding — it exists to be the fixed reference point that the eventual TTL-cache run (same workload, same topology, only the edge's cache state differs) is compared against. Without a controlled No-Cache baseline, any later "caching helped" claim would have nothing rigorous to be measured against.
