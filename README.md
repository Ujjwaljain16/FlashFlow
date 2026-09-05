# FlashFlow

**Adaptive Edge Networking & Flash-Crowd Resilience Laboratory**

> Dual-engine architecture: a deterministic virtual-time simulator for rigorous, counterfactual-verified policy tuning — combined with a real HTTP emulation engine (Go `net/http`, in-process network-degradation simulation) for validation against actual Linux networking behavior.

---

## What is FlashFlow?

FlashFlow is a programmable Go laboratory for studying distributed edge topologies under extreme load and partial failure. The project is built stage-by-stage from raw TCP fundamentals upward, with every engineering decision grounded in experimental evidence.

```text
                       FlashFlow
                           │
          ┌────────────────┴────────────────┐
          │                                 │
   Virtual-Time Engine              Real Emulation Engine
   (deterministic)                  (net/http + internal/netsim)
          │                                 │
          └────────────────┬────────────────┘
                           │
                  Per-Component Trace/Metrics
                           │
               ┌───────────┴───────────────┐
               ▼                           ▼
       Internal Statistics          Experiment Result JSON
```

**Implementation status, stated plainly**: the two engines share routing/health code but are not
yet unified behind a single `ExperimentEngine` interface; network degradation is a real,
in-process Go simulator (`internal/netsim`) built specifically in place of `tc netem`, which this
project evaluated and did not use (Linux-only, unavailable on the Windows host this was developed
on — see `docs/StageArtifacts/Stage4.md`); metrics are computed via `internal/statistics`
(percentiles, Mann-Whitney U, Cliff's Delta, bootstrap CI), not HdrHistogram/Prometheus; and each
experiment writes its own result JSON rather than a unified manifest ledger. All of this is
tracked as deliberately deferred work — see `docs/StageArtifacts/Stage9.md`'s Limitations section
and `docs/audit/RESOLUTION.md` for the full per-finding disposition.

---

## Build Sequence

FlashFlow is built learning-first — not architecture-first.

| Stage | Ships | Status |
|---|---|---|
| **1** | Raw TCP server/client, connection lifecycle, framing, benchmarks | ✅ Complete |
| **2** | HTTP reverse proxy, 3-edge topology, real emulation engine | ✅ Complete |
| **3** | Round-robin → EWMA routing policies | ✅ Complete |
| **4** | LRU (deferred)+TTL edge cache, `internal/netsim` network degradation | ✅ Complete |
| **5** | Virtual-Time Engine, Clock abstraction, Event Stream | ✅ Complete |
| **6** | Internal statistics (percentile/Mann-Whitney/Cliff's Delta/bootstrap), Little's-Law-based queueing analysis (one-off, per-experiment — a generalized attribution engine is Stage 10 scope) | ✅ Complete |
| **7** | P2C + Four-Signal Adaptive Router (six tunable parameters), Counterfactual Replay | ✅ Complete |
| **8** | Auto-Tuner (Random Search v1), Live Dashboard | ✅ Complete |
| **9** | Post-Stage-8 adversarial audit remediation — every finding fixed or honestly disclosed; no new capability shipped | ✅ Complete |

Stage 10 (building the features Stage 9 disclosed as deferred — traffic generator, SWR cache,
declarative YAML chaos engine, experiment manifest/provenance, a generalized queueing-attribution
engine, HdrHistogram+Prometheus telemetry, LHS/Bayesian tuner tiers, a formal `ExperimentEngine`
interface) is planned but not started — see `docs/audit/RESOLUTION.md`.

---

## Stage 1 — TCP Foundations

**Research question**: What is the performance difference between creating a new TCP connection for every request versus reusing persistent connections?

**Key findings**:
1. TCP is a byte stream. Application-level message boundaries require explicit framing — one `Read()` does not correspond to one message.
2. Even on loopback, `net.Dial()` (3-way handshake) costs ~0.5–1.5ms p99. On real networks this is 50–200ms.
3. At c=100, per-request mode app RTT degrades to ~9.4ms (p50) vs ~0µs for persistent — the penalty is systemic OS pressure, not just dial overhead.
4. TIME_WAIT socket exhaustion contaminates sequential benchmarks. Correct methodology requires fresh server instances per experiment cell.

### Running Stage 1

```bash
# Start the echo server
go run ./cmd/tcp-server --addr 127.0.0.1:9000

# Run benchmark (persistent mode)
go run ./cmd/tcp-client --addr 127.0.0.1:9000 --requests 10000 --concurrency 10 --connection-mode persistent

# Run benchmark (per-request mode)
go run ./cmd/tcp-client --addr 127.0.0.1:9000 --requests 10000 --concurrency 10 --connection-mode per-request

# Run full benchmark matrix (120 measured runs)
go run ./cmd/benchmark-runner

# Run causal decomposition experiment 001-A
go run ./cmd/experiment-001a
```

### Running Tests

```bash
go test ./...
go vet ./...
```

`scripts/final-validation.sh` and `scripts/nginx-reference-benchmark.sh` are bash scripts and
require Git Bash or WSL on Windows (they will not run under a plain `cmd.exe` or PowerShell
prompt); the NGINX benchmark additionally requires Docker.

---

## Specifications

- [PRD v3.1](prd.md) — Product requirements and build sequence authority
- [TRD v3.1](trd.md) — Technical architecture and implementation authority
- [Research](research.md) — Research methodology reference

---

## Experiments

| # | Title | Status |
|---|---|---|
| [001](experiments/001-tcp-connection-lifecycle/) | TCP Connection Lifecycle | ✅ Complete |
| [001-A](experiments/001-tcp-connection-lifecycle/results/) | Connection Cost Decomposition | ✅ Complete |
| [002](experiments/002-http-reverse-proxy/) | HTTP Reverse Proxy | ✅ Complete |
| [003](experiments/003-routing-policies/) | Routing Policies | ✅ Complete |
| [004](experiments/004-caching-failures/) | Caching & Failures | ✅ Complete |
| [005](experiments/005-virtual-time/) | Virtual Time | ✅ Complete |
| [006](experiments/006-statistics-queueing/) | Statistics & Queueing | ✅ Complete |
| [007](experiments/007-adaptive-replay/) | Adaptive Routing & Replay | ✅ Complete |
| [008](experiments/008-tuning-validation/) | Tuning & Final Validation | ✅ Complete |

---

## Resume Line

> **FlashFlow — Adaptive Edge Networking Laboratory**: Built a dual-engine distributed system in Go combining a deterministic virtual-time simulator and a real HTTP emulation engine with an in-process network-degradation simulator (built specifically in place of `tc netem`, which was evaluated and not used); implemented adaptive routing, TTL+coalescing edge caching, and a self-tuning parameter optimizer validated via stateful counterfactual replay and statistical queueing analysis. Stage 1 grounded the system in raw TCP semantics through a controlled connection-lifecycle benchmarking study that discovered TIME_WAIT exhaustion and its effects on sequential benchmark contamination.
