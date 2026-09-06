# Stage 10 Demo — Quick Reference

## WHAT

**Question**: Does Adaptive routing actually beat Round Robin when a target is overloaded and
temporarily fails — and can that be proven, explained, and reproduced?

**Result** (real, from an actual run): Adaptive's mean latency is **28.73ms vs 34.35ms** for Round
Robin — a **16.4% reduction** — in a scenario where edge-c is overloaded (ρ>1) and edge-b crashes
1s-2s. p99 ties at 60.00ms for both (Adaptive didn't fix the tail, only the mean). Proven real via
`FirstDivergence` (traces identical through event #8, then diverge). Reproduced byte-for-byte across
3 separate runs.

## WHEN (timing)

| Time | Step |
|---|---|
| 0:00-0:20 | State the question, show the topology |
| 0:20-1:00 | Generate workload + show chaos YAML |
| 1:00-1:50 | Run comparison, show latency table + divergence |
| 1:50-2:40 | Attribution: ρ=1.99→1.34 explanation |
| 2:40-3:20 | Provenance manifest + reproducibility |
| 3:20-3:50 | Takeaway (not "Adaptive won" — "the comparison is provable and explainable") |

Total: **~4 minutes**. The command itself runs in ~2 seconds; the rest is narration over the printed output.

## WHERE

| What | Where |
|---|---|
| Primary demo | Terminal, repo root, `cmd/demo-stage10/main.go` |
| Evidence artifact | `demo/output/stage10-demo/manifest.json` (regenerated every run) |
| Full script + claims audit | `docs/demo/Stage10Demo.md` |
| Optional visual (secondary) | Browser, `http://127.0.0.1:7070` (dashboard Playground → Compare) — **analogous scenario, not identical**, say so |
| Backup demo (real engine) | Terminal, ports `:8000`/`:8081`/`:9090` |

## HOW

```bash
# Reset + run (from repo root)
scripts/demo-stage10.sh

# Or directly:
go run -buildvcs=true ./cmd/demo-stage10
```

`-buildvcs=true` is required — plain `go run` silently omits the git commit from the manifest.

**Optional dashboard segment**:
```bash
go run ./cmd/dashboard   # http://127.0.0.1:7070
```
Playground tab → Run (round-robin) → Compare (round-robin vs adaptive) → point at the divergence banner.

**Backup demo (real engine + live telemetry)**:
```bash
go run ./cmd/http-origin -addr :8000 -delay-ms 20 &
go run ./cmd/proxy -addr :8081 -targets http://127.0.0.1:8000 -metrics-addr :9090 -check-interval-ms 200 &
curl http://127.0.0.1:9090/metrics
```

## Say this, not that

| Say | Don't say |
|---|---|
| "16.4% lower mean latency in this scenario" | "Adaptive is better" (Stage 8: it wins 62.5-70% of scenarios, not all) |
| "reduces the overload ratio from 1.99 to 1.34" | "fixed the overload" (still >1, just less severe) |
| "the analogous dashboard scenario" | "the same experiment" (different traffic-gen code path) |
